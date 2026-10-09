package replay

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"supermetry/internal/clientip"
	"supermetry/internal/config"
	"supermetry/internal/health"
	"supermetry/internal/ingest"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
)

const (
	maxChunkBytes      = 256 << 10
	maxStartBodyBytes  = 4 << 10
	previousSaltWindow = 30 * time.Minute
	defaultUploads     = 64
)

type saltSource interface {
	For(time.Time) ([]byte, bool)
}

type indexAdder interface {
	Add(IndexUpdate)
}

type handlerClock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type HandlerDeps struct {
	Sites          sites.Registry
	Salts          saltSource
	Sessions       session.Joiner
	Blobs          BlobStore
	Index          indexAdder
	Replay         config.ReplayEnv
	Limiter        ingest.RateLimiter
	TrustedProxies []netip.Prefix
}

type Handler struct {
	deps      HandlerDeps
	clock     handlerClock
	diskGuard func() bool
	uploads   chan struct{}
	statesMu  sync.Mutex
	states    map[uploadKey]*uploadState
	stateUses uint64
}

type uploadKey struct {
	siteID    int32
	sessionID int64
}

type uploadState struct {
	busy         chan struct{}
	nextSequence uint64
	hasPage      bool
	expires      time.Time
}

type HandlerOption func(*Handler)

func WithClock(clock handlerClock) HandlerOption {
	return func(handler *Handler) {
		if clock != nil {
			handler.clock = clock
		}
	}
}

func WithDiskGuard(tripped func() bool) HandlerOption {
	return func(handler *Handler) {
		if tripped != nil {
			handler.diskGuard = tripped
		}
	}
}

func WithUploadConcurrency(limit int) HandlerOption {
	return func(handler *Handler) {
		if limit > 0 {
			handler.uploads = make(chan struct{}, limit)
		}
	}
}

func NewHandler(deps HandlerDeps, options ...HandlerOption) *Handler {
	handler := &Handler{
		deps: deps, clock: wallClock{}, diskGuard: func() bool { return false },
		uploads: make(chan struct{}, defaultUploads),
		states:  make(map[uploadKey]*uploadState),
	}
	for _, option := range options {
		option(handler)
	}
	return handler
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/replay/start":
		h.Start(w, r)
	case "/api/replay/chunk":
		h.Chunk(w, r)
	default:
		http.NotFound(w, r)
	}
}

type startRequest struct {
	Site    string `json:"s"`
	URL     string `json:"u"`
	Consent bool   `json:"c"`
}

type startResponse struct {
	Mode     string           `json:"m"`
	Mask     bool             `json:"mask"`
	Version  string           `json:"v"`
	Triggers responseTriggers `json:"t"`
}

type responseTriggers struct {
	Error       bool     `json:"e"`
	RageClick   bool     `json:"r"`
	URLPatterns []string `json:"u"`
	Manual      bool     `json:"x"`
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	setReplayCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request startRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxStartBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Site == "" || request.URL == "" {
		h.reject(w, http.StatusBadRequest)
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		h.reject(w, http.StatusBadRequest)
		return
	}
	site, ok := h.site(request.Site)
	if !ok || !siteAllowsURL(site, request.URL) || !originAllowed(site, r.Header.Get("Origin")) {
		h.reject(w, http.StatusBadRequest)
		return
	}
	settings := ResolveSite(h.deps.Replay, site.Replay)
	response := startResponse{Mode: "off", Mask: settings.Mask.V, Version: policyVersion(settings), Triggers: wireTriggers(settings.Triggers)}
	if !settings.Enabled || !collectionAllowed(r, settings, request.Consent) || h.diskGuard() {
		writeStart(w, response)
		return
	}
	ip, ok := clientip.Resolve(r, h.deps.TrustedProxies)
	if !ok {
		h.reject(w, http.StatusBadRequest)
		return
	}
	if h.rateLimited(site.ID, ip) {
		h.reject(w, http.StatusTooManyRequests)
		return
	}
	current, previous, ok := h.visitorIDs(site, ip, r.UserAgent(), h.clock.Now())
	if !ok {
		health.ReplayDropped.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if Sampled(current, settings.SampleRate.V) || previous != 0 && Sampled(previous, settings.SampleRate.V) {
		response.Mode = "full"
	}
	writeStart(w, response)
}

func (h *Handler) Chunk(w http.ResponseWriter, r *http.Request) {
	setReplayCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	sequence, sequenceErr := strconv.ParseUint(query.Get("q"), 10, 64)
	page := query.Get("p")
	trigger := query.Get("t")
	if query.Get("s") == "" || sequenceErr != nil || (page != "0" && page != "1") || len(trigger) > 64 {
		h.reject(w, http.StatusBadRequest)
		return
	}
	site, ok := h.site(query.Get("s"))
	settings := ResolveSite(h.deps.Replay, site.Replay)
	if !ok || !settings.Enabled || !originAllowed(site, r.Header.Get("Origin")) {
		h.reject(w, http.StatusBadRequest)
		return
	}
	if !collectionAllowed(r, settings, query.Get("c") == "1") || query.Get("v") != policyVersion(settings) {
		h.reject(w, http.StatusForbidden)
		return
	}
	if h.diskGuard() {
		h.drop(w)
		return
	}
	ip, ok := clientip.Resolve(r, h.deps.TrustedProxies)
	if !ok {
		h.reject(w, http.StatusBadRequest)
		return
	}
	if h.rateLimited(site.ID, ip) {
		h.reject(w, http.StatusTooManyRequests)
		return
	}
	current, previous, ok := h.visitorIDs(site, ip, r.UserAgent(), h.clock.Now())
	if !ok {
		h.drop(w)
		return
	}
	select {
	case h.uploads <- struct{}{}:
		defer func() { <-h.uploads }()
	default:
		h.drop(w)
		return
	}
	body, status := prepareChunk(w, r)
	if status != 0 {
		h.reject(w, status)
		return
	}
	now := h.clock.Now()
	day := salts.LocalDay(now, site.TZ)
	result := h.deps.Sessions.Join(session.Hit{
		SiteID: site.ID, Retention: site.Retention, TS: now, LocalDay: day,
		VisitorID: current, PrevVisitorID: previous, JoinOnly: true,
	})
	if result.SessionID == 0 {
		health.ReplayNoSession.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if profiles, ok := h.deps.Sessions.(session.ProfileBlockChecker); ok && profiles.ProfileBlocked(site.ID, result.SessionID) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if previous != 0 && result.VisitorID == previous {
		day = day.AddDate(0, 0, -1)
	}
	state, acquired := h.acquireUploadState(uploadKey{siteID: site.ID, sessionID: result.SessionID}, now)
	if acquired {
		defer func() { <-state.busy }()
	} else {
		h.drop(w)
		return
	}
	sampled := Sampled(current, settings.SampleRate.V) || previous != 0 && Sampled(previous, settings.SampleRate.V)
	if !sampled {
		h.reject(w, http.StatusBadRequest)
		return
	}
	// A new page starts at any number: a full load restarts the browser's
	// counter at 0, while a client-side navigation keeps counting. Within a
	// page the number must grow; gaps (dropped chunks) are fine, repeats and
	// reordering are not.
	newPage := page == "1"
	if !newPage && (!state.hasPage || sequence < state.nextSequence) {
		h.reject(w, http.StatusBadRequest)
		return
	}
	// Body reads and session admission may have waited. Recheck the cached
	// policy immediately before committing the upload.
	currentSite, currentOK := h.site(query.Get("s"))
	currentSettings := ResolveSite(h.deps.Replay, currentSite.Replay)
	if !currentOK || !currentSettings.Enabled || query.Get("v") != policyVersion(currentSettings) || !collectionAllowed(r, currentSettings, query.Get("c") == "1") {
		h.reject(w, http.StatusForbidden)
		return
	}
	written, err := h.deps.Blobs.Append(r.Context(), SessionKey(site.ID, day, result.SessionID), body)
	if err != nil {
		h.drop(w)
		return
	}
	state.hasPage = true
	state.nextSequence = sequence + 1
	health.ReplayBytes.Add(uint64(written))
	h.deps.Index.Add(IndexUpdate{
		SiteID: site.ID, SessionID: result.SessionID, Day: day, At: now,
		Bytes: written, Page: newPage, Trigger: trigger,
	})
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) site(publicID string) (sites.Site, bool) {
	if h == nil || h.deps.Sites == nil || h.deps.Salts == nil || h.deps.Sessions == nil || h.deps.Blobs == nil || h.deps.Index == nil {
		return sites.Site{}, false
	}
	site, ok := h.deps.Sites.ByPublicID(publicID)
	return site, ok && site.TZ != nil
}

func (h *Handler) visitorIDs(site sites.Site, ip netip.Addr, userAgent string, now time.Time) (int64, int64, bool) {
	day := salts.LocalDay(now, site.TZ)
	salt, ok := h.deps.Salts.For(day)
	if !ok {
		return 0, 0, false
	}
	current := salts.VisitorID(salt, site.ID, ip.String(), userAgent)
	localNow := now.In(site.TZ)
	localMidnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, site.TZ)
	if localNow.Sub(localMidnight) > previousSaltWindow {
		return current, 0, true
	}
	previousSalt, ok := h.deps.Salts.For(day.AddDate(0, 0, -1))
	if !ok {
		return current, 0, true
	}
	return current, salts.VisitorID(previousSalt, site.ID, ip.String(), userAgent), true
}

func (h *Handler) rateLimited(siteID int32, ip netip.Addr) bool {
	if h.deps.Limiter == nil {
		return false
	}
	sum := sha256.Sum256([]byte(ip.String()))
	key := "replay:" + strconv.FormatInt(int64(siteID), 10) + ":" + hex.EncodeToString(sum[:16])
	return !h.deps.Limiter.Allow(key)
}

func prepareChunk(w http.ResponseWriter, r *http.Request) (io.Reader, int) {
	if r.ContentLength > maxChunkBytes {
		return nil, http.StatusRequestEntityTooLarge
	}
	if r.ContentLength >= 0 {
		body := bufio.NewReaderSize(http.MaxBytesReader(w, r.Body, maxChunkBytes), 2)
		magic, err := body.Peek(2)
		if err != nil {
			return nil, http.StatusBadRequest
		}
		if magic[0] != 0x1f || magic[1] != 0x8b {
			return nil, http.StatusBadRequest
		}
		return body, 0
	}
	var buffer bytes.Buffer
	buffer.Grow(maxChunkBytes + 1)
	if _, err := io.CopyN(&buffer, r.Body, maxChunkBytes+1); err != nil && !errors.Is(err, io.EOF) {
		return nil, http.StatusBadRequest
	}
	if buffer.Len() > maxChunkBytes {
		return nil, http.StatusRequestEntityTooLarge
	}
	body := buffer.Bytes()
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return nil, http.StatusBadRequest
	}
	return bytes.NewReader(body), 0
}

func siteAllowsURL(site sites.Site, raw string) bool {
	page, err := url.ParseRequestURI(raw)
	return err == nil && (page.Scheme == "http" || page.Scheme == "https") && page.User == nil && site.Allows(strings.ToLower(page.Hostname()))
}

func originAllowed(site sites.Site, origin string) bool {
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil && parsed.Path == "" &&
		parsed.RawQuery == "" && parsed.Fragment == "" && site.Allows(strings.ToLower(parsed.Hostname()))
}

func wireTriggers(triggers Triggers) responseTriggers {
	patterns := []string{}
	if triggers.URL {
		patterns = append(patterns, triggers.URLPatterns...)
	}
	return responseTriggers{Error: triggers.Error, RageClick: triggers.RageClick, URLPatterns: patterns, Manual: triggers.Manual}
}

func (h *Handler) acquireUploadState(key uploadKey, now time.Time) (*uploadState, bool) {
	h.statesMu.Lock()
	defer h.statesMu.Unlock()
	h.stateUses++
	if h.stateUses%1024 == 0 {
		for candidate, state := range h.states {
			if !state.expires.After(now) && len(state.busy) == 0 {
				delete(h.states, candidate)
			}
		}
	}
	state := h.states[key]
	if state == nil {
		state = &uploadState{busy: make(chan struct{}, 1), expires: now.Add(session.IdleTimeout)}
		h.states[key] = state
	}
	state.expires = now.Add(session.IdleTimeout)
	select {
	case state.busy <- struct{}{}:
		return state, true
	default:
		return state, false
	}
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after replay start request")
	}
	return nil
}

func writeStart(w http.ResponseWriter, response startResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func setReplayCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	} else {
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func (h *Handler) reject(w http.ResponseWriter, status int) {
	health.ReplayRejected.Add(1)
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(status)
}

func (h *Handler) drop(w http.ResponseWriter) {
	health.ReplayDropped.Add(1)
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
}

// Consent is an explicit client assertion, not authenticated proof. GPC and DNT
// always veto collection, including a forged/stale client requesting consent.
func collectionAllowed(r *http.Request, settings SiteSettings, consent bool) bool {
	return r.Header.Get("Sec-GPC") != "1" && r.Header.Get("DNT") != "1" && (settings.Consent != "always" || consent)
}

// CollectionGuard applies the same current cached project policy to auxiliary
// uploads (CSS). Resolve must not perform database work on the request path.
func CollectionGuard(registry sites.Registry, env config.ReplayEnv, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		site, ok := registry.ByPublicID(r.URL.Query().Get("s"))
		settings := ResolveSite(env, site.Replay)
		if !ok || !settings.Enabled || settings.SampleRate.V <= 0 || !originAllowed(site, r.Header.Get("Origin")) || !collectionAllowed(r, settings, r.URL.Query().Get("c") == "1") || r.URL.Query().Get("v") != policyVersion(settings) {
			setCSSCORS(w, r)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// This public fingerprint detects stale recorder settings; it is not a proof
// of consent or masking. Recompute from cached settings, without database I/O.
func policyVersion(settings SiteSettings) string {
	encoded, _ := json.Marshal(settings)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:16])
}
