package otlpingest

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/platform/appkeys"
	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

const (
	// DedupTTL is how long a request body digest is remembered.
	DedupTTL = 10 * time.Minute
	// DedupEntries bounds the digest set.
	DedupEntries = 200_000
	// maxInflight bounds concurrent body reads and decodes, so a burst of
	// large requests cannot hold more than this many decoded bodies at once.
	maxInflight = 8
	// inflightWait is how long a request waits for a free slot before it is
	// answered 503: a stall of a few milliseconds (garbage collection, a busy
	// CPU) must not turn a burst of exporters into rejected batches.
	inflightWait = 250 * time.Millisecond

	ctProto = "application/x-protobuf"
	ctJSON  = "application/json"
)

// Sink takes converted spans; spans.Writer implements it. Enqueue is all or
// nothing and reports false when the queue is full.
type Sink interface {
	Enqueue(batch []spans.Span) bool
}

// Handler serves POST /api/{project_id}/otlp/v1/traces.
type Handler struct {
	sites    sites.DSNRegistry
	intake   *appingest.Intake
	sink     Sink
	budget   *spans.Budget
	stats    spans.Stats
	now      func() time.Time
	dedup    *digestSet
	inflight chan struct{}
	metrics  *metricsFront
	logs     *logsFront
}

// NewHandler builds the handler. intake, budget and stats may be nil (no
// exception events, no span budget, no counters).
func NewHandler(reg sites.DSNRegistry, intake *appingest.Intake, sink Sink, budget *spans.Budget, stats spans.Stats, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{
		sites: reg, intake: intake, sink: sink, budget: budget, stats: stats, now: now,
		dedup: newDigestSet(DedupEntries, DedupTTL), inflight: make(chan struct{}, maxInflight),
	}
}

type routeKind int

const (
	routeTraces routeKind = iota
	routeMetrics
	routeLogs
)

// parseRoute extracts the project id from the traces, metrics and logs routes
// under /api/{id}/otlp/v1/, and which of the three it was.
func parseRoute(path string) (id int32, kind routeKind, ok bool) {
	rest, found := strings.CutPrefix(path, "/api/")
	if !found {
		return 0, 0, false
	}
	idPart, tail, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	switch strings.TrimSuffix(tail, "/") {
	case "otlp/v1/traces":
		kind = routeTraces
	case "otlp/v1/metrics":
		kind = routeMetrics
	case "otlp/v1/logs":
		kind = routeLogs
	default:
		return 0, 0, false
	}
	n, err := strconv.ParseInt(idPart, 10, 32)
	if err != nil || n <= 0 || idPart != strconv.FormatInt(n, 10) {
		return 0, 0, false
	}
	return int32(n), kind, true
}

// enabled reports whether the route's front door is wired in.
func (h *Handler) enabled(kind routeKind) bool {
	switch kind {
	case routeMetrics:
		return h.metrics != nil
	case routeLogs:
		return h.logs != nil
	}
	return true
}

// Matches reports whether the request is for this handler.
func (h *Handler) Matches(r *http.Request) bool {
	_, kind, ok := parseRoute(r.URL.Path)
	return ok && h.enabled(kind)
}

// acquire takes an in-flight slot, waiting up to inflightWait for one. It
// reports false when none came free or the client went away.
func (h *Handler) acquire(r *http.Request) bool {
	select {
	case h.inflight <- struct{}{}:
		return true
	default:
	}
	t := time.NewTimer(inflightWait)
	defer t.Stop()
	select {
	case h.inflight <- struct{}{}:
		return true
	case <-t.C:
		return false
	case <-r.Context().Done():
		return false
	}
}

func (h *Handler) count(name string, n int) {
	if h.stats != nil && n > 0 {
		h.stats.Add(name, n)
	}
}

func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}

// bearerKey returns the DSN public key from x-supermetry-key or a Bearer
// Authorization header. Two different keys are ambiguous and yield "".
func bearerKey(r *http.Request) string {
	k1 := strings.TrimSpace(r.Header.Get("X-Supermetry-Key"))
	k2 := ""
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
		k2 = strings.TrimSpace(auth[7:])
	}
	switch {
	case k1 != "" && k2 != "" && k1 != k2:
		return ""
	case k1 != "":
		return k1
	}
	return k2
}

// authenticate resolves the key to the project named in the path. A key that
// belongs to another project, an expired key and an unknown key all look the
// same to the caller.
func (h *Handler) authenticate(r *http.Request, pathID int32, now time.Time) (sites.Site, sites.DSNKey, bool) {
	k := bearerKey(r)
	if k == "" || !appkeys.ValidDSNKey(k) {
		return sites.Site{}, sites.DSNKey{}, false
	}
	site, key, ok := h.sites.ByDSNKey(k)
	if !ok || key.Expired(now) || site.ID != pathID || site.Kind != sites.KindApp || site.App == nil {
		return sites.Site{}, sites.DSNKey{}, false
	}
	return site, key, true
}

func applyOrigin(w http.ResponseWriter, r *http.Request, key sites.DSNKey) bool {
	if len(key.AllowedOrigins) == 0 {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	w.Header().Add("Vary", "Origin")
	for _, allowed := range key.AllowedOrigins {
		if strings.EqualFold(allowed, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			return true
		}
	}
	return false
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pathID, kind, ok := parseRoute(r.URL.Path)
	if !ok || !h.enabled(kind) {
		http.NotFound(w, r)
		return
	}
	hdr := w.Header()
	hdr.Set("Access-Control-Allow-Origin", "*")
	hdr.Set("Access-Control-Expose-Headers", "retry-after")
	if r.Method == http.MethodOptions {
		hdr.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		hdr.Set("Access-Control-Allow-Headers", "authorization, content-type, content-encoding, x-supermetry-key")
		hdr.Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		hdr.Set("Allow", "POST, OPTIONS")
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	now := h.now()

	// Authentication comes first, so an unauthenticated sender costs no body
	// read, no decompression and no decode.
	site, key, ok := h.authenticate(r, pathID, now)
	if !ok {
		hdr.Set("WWW-Authenticate", "Bearer")
		fail(w, http.StatusUnauthorized, "invalid or missing key")
		return
	}
	if !applyOrigin(w, r, key) {
		fail(w, http.StatusForbidden, "origin not allowed")
		return
	}
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (ct != ctProto && ct != ctJSON) {
		fail(w, http.StatusUnsupportedMediaType, "content type must be application/x-protobuf or application/json")
		return
	}
	switch kind {
	case routeMetrics:
		h.serveMetrics(w, r, site, ct, now)
		return
	case routeLogs:
		h.serveLogs(w, r, site, key, ct, now)
		return
	}
	if site.App.PerformanceEnabled != nil && !*site.App.PerformanceEnabled {
		h.respond(w, ct, 0, "")
		return
	}

	if !h.acquire(r) {
		hdr.Set("Retry-After", "5")
		fail(w, http.StatusServiceUnavailable, "too many requests in flight")
		return
	}
	released := false
	release := func() {
		if !released {
			released = true
			<-h.inflight
		}
	}
	defer release()

	body, err := readBody(r.Body, r.Header.Get("Content-Encoding"), MaxCompressedBytes, MaxDecompressedBytes)
	if err != nil {
		switch {
		case errors.Is(err, ErrTooLarge):
			fail(w, http.StatusRequestEntityTooLarge, "request too large")
		case errors.Is(err, ErrEncoding):
			fail(w, http.StatusUnsupportedMediaType, "unsupported content encoding")
		default:
			fail(w, http.StatusBadRequest, "malformed request body")
		}
		return
	}
	// The digest covers the project, so one project's body never suppresses
	// another's.
	hs := sha256.New()
	var id [4]byte
	binary.BigEndian.PutUint32(id[:], uint32(site.ID))
	hs.Write(id[:])
	hs.Write(body)
	var digest [32]byte
	hs.Sum(digest[:0])
	if h.dedup.seen(digest, now) {
		h.count("span_duplicate_request", 1)
		h.respond(w, ct, 0, "")
		return
	}
	var req *Request
	if ct == ctProto {
		req, err = DecodeProto(body)
	} else {
		req, err = DecodeJSON(body)
	}
	body = nil
	release()
	if err != nil {
		h.dedup.release(digest)
		fail(w, http.StatusBadRequest, "malformed request")
		return
	}

	batch, events, rejected := Convert(req, site, now)
	req = nil
	h.count("span_invalid", rejected)

	if n := len(batch); n > 0 && h.budget != nil {
		fit := h.budget.Take(site.ID, n, now)
		if fit < n {
			h.count("span_rate_limited", n-fit)
			rejected += n - fit
			batch = batch[:fit]
		}
		if fit == 0 {
			h.dedup.release(digest)
			hdr.Set("Retry-After", "60")
			fail(w, http.StatusTooManyRequests, "span rate limit exceeded")
			return
		}
	}
	if len(batch) > 0 && !h.sink.Enqueue(batch) {
		h.dedup.release(digest)
		hdr.Set("Retry-After", "5")
		fail(w, http.StatusServiceUnavailable, "ingest queue full")
		return
	}
	if h.intake != nil {
		budgetKey := "dsn:" + key.PublicKey
		for _, ev := range events {
			h.intake.Accept(site, budgetKey, now, ev)
		}
	}
	msg := ""
	if rejected > 0 {
		msg = "some spans were rejected (invalid, too old, over a limit or over budget)"
	}
	h.respond(w, ct, rejected, msg)
}

// respond writes the ExportTraceServiceResponse in the request's encoding.
func (h *Handler) respond(w http.ResponseWriter, ct string, rejected int, msg string) {
	out := encodeResponse(ct == ctProto, rejected, msg)
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// digestSet remembers request body digests for a TTL. When full it sweeps
// expired entries (at most once a second) and otherwise stops remembering new
// ones, which only means a duplicate body may be stored twice.
type digestSet struct {
	mu        sync.Mutex
	max       int
	ttl       time.Duration
	entries   map[[32]byte]time.Time
	lastSweep time.Time
}

func newDigestSet(max int, ttl time.Duration) *digestSet {
	return &digestSet{max: max, ttl: ttl, entries: map[[32]byte]time.Time{}}
}

// seen reports whether the digest is already remembered and remembers it if not.
func (s *digestSet) seen(d [32]byte, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at, ok := s.entries[d]; ok {
		if now.Sub(at) < s.ttl {
			return true
		}
	}
	if len(s.entries) >= s.max && now.Sub(s.lastSweep) >= time.Second {
		s.lastSweep = now
		for k, at := range s.entries {
			if now.Sub(at) >= s.ttl {
				delete(s.entries, k)
			}
		}
	}
	if len(s.entries) < s.max {
		s.entries[d] = now
	}
	return false
}

func (s *digestSet) release(d [32]byte) {
	s.mu.Lock()
	delete(s.entries, d)
	s.mu.Unlock()
}
