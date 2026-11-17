package sentryingest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/clientip"
	"supermetry/internal/dropstats"
	"supermetry/internal/sites"
)

// EventNormaliser turns the JSON of one Sentry event item into the narrow
// appingest.Event. The handler never looks inside an event; PII scrubbing and
// grouping live behind this interface.
type EventNormaliser interface {
	Normalise(raw []byte, hdr EnvelopeHeader, site sites.Site, req RequestMeta, now time.Time) (appingest.Event, error)
}

// BodyQueue takes the scrubbed event bodies for storage. The handler passes a
// RawBody value; Enqueue reports false when the queue is full (the event is
// already accepted, so the handler only counts the loss).
type BodyQueue interface {
	Enqueue(any) bool
}

// RawBody is what the handler enqueues for an accepted event item. Payload is
// a private copy of the raw item and still holds unscrubbed data: the queue
// consumer scrubs it before anything is stored.
type RawBody struct {
	SiteID     int32
	EventID    string
	ReceivedAt time.Time
	Header     EnvelopeHeader
	Request    RequestMeta
	Event      appingest.Event
	Payload    []byte
}

// GeoLookup resolves a country code for an address; the address is not kept.
type GeoLookup interface {
	Country(netip.Addr) string
}

// Handler serves POST /api/{id}/envelope/, /store/ and /security/.
type Handler struct {
	sites      sites.DSNRegistry
	intake     *appingest.Intake
	bodies     BodyQueue
	geo        GeoLookup
	trusted    []netip.Prefix
	now        func() time.Time
	normaliser EventNormaliser
	spike      *spikeGuard
	stats      *Stats
}

// NewHandler builds the front door. bodies, geo and now may be nil.
func NewHandler(reg sites.DSNRegistry, intake *appingest.Intake, bodies BodyQueue, geo GeoLookup, trusted []netip.Prefix, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{sites: reg, intake: intake, bodies: bodies, geo: geo, trusted: trusted, now: now,
		spike: newSpikeGuard(DefaultSpikePerMinute), stats: &Stats{}}
}

// WithNormaliser sets the event normaliser; without one, event items are
// counted and dropped.
func (h *Handler) WithNormaliser(n EventNormaliser) *Handler { h.normaliser = n; return h }

// WithSpikeLimit sets the per-site events-per-minute cap.
func (h *Handler) WithSpikeLimit(perMinute int) *Handler {
	h.spike = newSpikeGuard(perMinute)
	return h
}

// Stats returns the in-memory drop and ignore counters.
func (h *Handler) Stats() *Stats { return h.stats }

type route struct {
	siteID int32
	kind   string // "envelope", "store" or "security"
}

// parseRoute matches /api/{numeric id}/{envelope|store|security}[/].
func parseRoute(path string) (route, bool) {
	rest, ok := strings.CutPrefix(path, "/api/")
	if !ok {
		return route{}, false
	}
	idPart, kind, ok := strings.Cut(rest, "/")
	if !ok {
		return route{}, false
	}
	kind = strings.TrimSuffix(kind, "/")
	if kind != "envelope" && kind != "store" && kind != "security" {
		return route{}, false
	}
	id, err := strconv.ParseInt(idPart, 10, 32)
	if err != nil || id <= 0 || idPart != strconv.FormatInt(id, 10) {
		return route{}, false
	}
	return route{siteID: int32(id), kind: kind}, true
}

// Matches reports whether the request is for this handler. The router checks
// it before its mux, because /api/{id}/... cannot be registered next to the
// fixed /api/private/ subtree.
func (h *Handler) Matches(r *http.Request) bool {
	_, ok := parseRoute(r.URL.Path)
	return ok
}

const corsAllowHeaders = "x-sentry-auth, content-type, content-encoding, authorization, x-requested-with, origin, referer, accept, authentication, transfer-encoding"

func (h *Handler) fail(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("X-Sentry-Error", detail)
	writeJSON(w, status, `{"detail":`+strconv.Quote(detail)+`}`)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	hdr := w.Header()
	hdr.Set("Access-Control-Allow-Origin", "*")
	hdr.Set("Access-Control-Expose-Headers", "x-sentry-error, x-sentry-rate-limits, retry-after")
	now := h.now()

	if r.Method == http.MethodOptions {
		// A preflight carries no credentials; only a key in the query can
		// narrow the allowed origin.
		if _, key, err := authenticate(authParamsFromQuery(r), rt.siteID, h.sites, now); err == nil {
			_ = h.applyOrigin(w, r, key)
		}
		hdr.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		hdr.Set("Access-Control-Allow-Headers", corsAllowHeaders)
		hdr.Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		hdr.Set("Allow", "POST, OPTIONS")
		h.fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Authenticate early when a header or query parameter carries a key (or a
	// bad version), so unauthenticated senders cost no body read. A request
	// whose only carrier is the envelope header's dsn is checked after parsing.
	if p := collectAuth(r, EnvelopeHeader{}); len(p.keys) > 0 || p.version != "" {
		if _, _, err := authenticate(p, rt.siteID, h.sites, now); err != nil && (len(p.keys) > 0 || errors.Is(err, ErrVersion)) {
			h.authFailure(w, err)
			return
		}
	}

	body, err := decodeBody(r, MaxEnvelopeBytes)
	if err != nil {
		h.bodyFailure(w, err)
		return
	}

	var env Envelope
	switch rt.kind {
	case "envelope":
		if env, err = parseEnvelopeBytes(body, Limits{MaxItems: MaxItems}); err != nil {
			h.bodyFailure(w, err)
			return
		}
	case "store":
		var probe struct {
			EventID string `json:"event_id"`
		}
		_ = json.Unmarshal(body, &probe)
		env = Envelope{Header: EnvelopeHeader{EventID: probe.EventID}, Items: []Item{{Type: "event", ContentType: "application/json", Payload: body}}}
	case "security":
		env = Envelope{Items: []Item{{Type: "csp", Payload: body}}}
	}

	// Re-check with the envelope header's dsn: a second, different key is an error.
	site, key, err := Authenticate(r, env.Header, rt.siteID, h.sites, now)
	if err != nil {
		h.authFailure(w, err)
		return
	}
	if err := h.applyOrigin(w, r, key); err != nil {
		h.fail(w, http.StatusForbidden, "origin not allowed")
		return
	}

	req := RequestMeta{UserAgent: r.UserAgent(), Origin: r.Header.Get("Origin"), SDKClient: collectAuth(r, EnvelopeHeader{}).client}
	if ip, ok := clientip.Resolve(r, h.trusted); ok {
		req.ClientIP = ip
		if h.geo != nil {
			req.Country = h.geo.Country(ip)
		}
	}

	events := 0
	for _, it := range env.Items {
		if it.Type == "event" {
			events++
		}
	}
	var cspEvents []appingest.Event
	if rt.kind == "security" {
		cspEvents = cspReportEvents(body, now)
		events = len(cspEvents)
		if events == 0 {
			h.fail(w, http.StatusBadRequest, "invalid CSP report")
			return
		}
	}
	if !h.spike.take(site.ID, events, now) {
		for range events {
			h.intake.Drop(site, now, dropstats.AppRateLimited)
		}
		h.stats.Add("spike_protection", uint64(events))
		hdr.Set("Retry-After", "60")
		hdr.Set("X-Sentry-Rate-Limits", spikeLimits)
		h.fail(w, http.StatusTooManyRequests, "spike protection")
		return
	}

	out := h.process(site, key, env, cspEvents, req, now)
	switch {
	case out.accepted == 0 && out.queueFull > 0:
		hdr.Set("Retry-After", "5")
		h.fail(w, http.StatusServiceUnavailable, "ingest queue full")
	case out.accepted == 0 && out.rateLimited > 0:
		hdr.Set("Retry-After", "60")
		hdr.Set("X-Sentry-Rate-Limits", budgetLimits)
		h.fail(w, http.StatusTooManyRequests, "rate limited")
	default:
		hdr.Set("X-Sentry-Rate-Limits", rejectUnsupported)
		if out.eventID != "" {
			writeJSON(w, http.StatusOK, `{"id":`+strconv.Quote(out.eventID)+`}`)
		} else {
			writeJSON(w, http.StatusOK, `{}`)
		}
	}
}

type outcome struct {
	accepted, rateLimited, queueFull int
	eventID                          string
}

func (h *Handler) process(site sites.Site, key sites.DSNKey, env Envelope, csp []appingest.Event, req RequestMeta, now time.Time) outcome {
	var out outcome
	budgetKey := "dsn:" + key.PublicKey
	accept := func(ev appingest.Event, it Item) {
		res := h.intake.Accept(site, budgetKey, now, ev)
		switch {
		case res.QueueFull:
			out.queueFull++
		case res.Reason == appingest.ReasonRateLimited:
			out.rateLimited++
		case res.Reason == "":
			out.accepted++
			if h.bodies != nil && it.Type == "event" {
				payload := append([]byte(nil), it.Payload...)
				if !h.bodies.Enqueue(RawBody{SiteID: site.ID, EventID: ev.ID, ReceivedAt: now, Header: env.Header, Request: req, Event: ev, Payload: payload}) {
					h.stats.Add("body_queue_full", 1)
				}
			}
		}
	}
	for _, ev := range csp {
		accept(ev, Item{Type: "csp"})
		if out.eventID == "" {
			out.eventID = ev.ID
		}
	}
	for _, it := range env.Items {
		switch it.Type {
		case "event":
			if out.eventID == "" {
				out.eventID = env.Header.EventID
			}
			if len(it.Payload) > MaxEventBytes {
				h.stats.Add("too_large", 1)
				h.intake.Drop(site, now, dropstats.AppInvalid)
				continue
			}
			if h.normaliser == nil {
				h.stats.Add("event_unsupported", 1)
				continue
			}
			ev, err := h.normaliser.Normalise(it.Payload, env.Header, site, req, now)
			if err != nil {
				h.stats.Add("event_invalid", 1)
				h.intake.Drop(site, now, dropstats.AppInvalid)
				continue
			}
			if out.eventID == "" {
				out.eventID = ev.ID
			}
			accept(ev, it)
		case "attachment":
			switch {
			case len(it.Payload) > MaxAttachment:
				h.stats.Add("attachment_too_large", 1)
			case it.AttachmentType == "event.minidump":
				h.stats.Add("minidump_deferred", 1)
			default:
				h.stats.Add("item:attachment", 1)
			}
		case "csp":
			// Already turned into events above.
		default:
			h.stats.Add("item:"+safeCategory(it.Type), 1)
		}
	}
	return out
}

// safeCategory keeps a hostile item type from minting unbounded categories.
func safeCategory(t string) string {
	if t == "" || len(t) > 32 {
		return "unknown"
	}
	for _, c := range []byte(t) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.') {
			return "unknown"
		}
	}
	return t
}

func authParamsFromQuery(r *http.Request) authParams {
	var p authParams
	q := r.URL.Query()
	p.addKey(q.Get("sentry_key"))
	p.version = q.Get("sentry_version")
	return p
}

// applyOrigin enforces a key's allowed origins for browser requests. Requests
// without an Origin header (SDKs outside browsers) are not restricted.
func (h *Handler) applyOrigin(w http.ResponseWriter, r *http.Request, key sites.DSNKey) error {
	if len(key.AllowedOrigins) == 0 {
		return nil
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	for _, allowed := range key.AllowedOrigins {
		if strings.EqualFold(allowed, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			return nil
		}
	}
	w.Header().Add("Vary", "Origin")
	return errors.New("origin not allowed")
}

func (h *Handler) authFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrVersion):
		h.fail(w, http.StatusBadRequest, "unsupported sentry_version")
	case errors.Is(err, ErrAuthMultiple):
		h.fail(w, http.StatusUnauthorized, "multiple DSN keys")
	case errors.Is(err, ErrAuthMissing):
		h.fail(w, http.StatusUnauthorized, "missing DSN key")
	default:
		h.fail(w, http.StatusUnauthorized, "invalid DSN key")
	}
}

func (h *Handler) bodyFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrTooLarge) {
		h.fail(w, http.StatusRequestEntityTooLarge, "envelope too large")
		return
	}
	h.fail(w, http.StatusBadRequest, "malformed envelope")
}
