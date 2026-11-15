package appingest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"time"

	"supermetry/internal/clientip"
	"supermetry/internal/dropstats"
	"supermetry/internal/platform/appkeys"
	"supermetry/internal/sites"
)

const (
	keyHeader        = "X-Supermetry-Key"
	ipRequestsPerMin = 60
	unauthorizedBody = `{"error":"unauthorized"}` + "\n"
	badRequestBody   = `{"error":"bad_request"}` + "\n"
	rateLimitedBody  = `{"error":"rate_limited"}` + "\n"
	allowedHeaders   = "Content-Type, X-Supermetry-Key"
	preflightMaxAgeS = "86400"
	clientIPUnknown  = "unknown"
)

// Handler serves POST /api/public/v1/app-events. The ingest key is the trust
// signal, so there is no bot filter and no allowed-hosts check, and any origin
// may send.
type Handler struct {
	sites   sites.Registry
	intake  *Intake
	trusted []netip.Prefix
	now     func() time.Time
	ips     *budgetMap
}

// NewHandler builds the endpoint. now may be nil.
func NewHandler(registry sites.Registry, intake *Intake, trustedProxies []netip.Prefix, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{
		sites: registry, intake: intake, trusted: trustedProxies, now: now,
		ips: newBudgetMap(ipRequestsPerMin, time.Minute),
	}
}

type dropped struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

type warning struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
	Tag    string `json:"tag"`
}

type response struct {
	Accepted int       `json:"accepted"`
	Dropped  []dropped `json:"dropped"`
	Warnings []warning `json:"warnings"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
		w.Header().Set("Access-Control-Max-Age", preflightMaxAgeS)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		h.reply(w, http.StatusBadRequest, badRequestBody)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		h.reply(w, http.StatusBadRequest, badRequestBody)
		return
	}
	publicID, events, ok := decodeRequest(body)
	if !ok {
		h.reply(w, http.StatusBadRequest, badRequestBody)
		return
	}
	plain := r.Header.Get(keyHeader)
	if !appkeys.Valid(plain) {
		h.reply(w, http.StatusUnauthorized, unauthorizedBody)
		return
	}
	site, found := h.sites.ByPublicID(publicID)
	hash := appkeys.Hash(plain)
	now := h.now()
	if !found || site.Kind != sites.KindApp || site.App == nil || !site.App.KeyValid(hash, now) {
		h.reply(w, http.StatusUnauthorized, unauthorizedBody)
		return
	}
	if !h.ips.take(h.clientKey(r), now) {
		w.Header().Set("Retry-After", "1")
		h.reply(w, http.StatusTooManyRequests, rateLimitedBody)
		return
	}

	out := response{Dropped: []dropped{}, Warnings: []warning{}}
	budgetKey := string(hash[:])
	for index, raw := range events {
		object, isObject := raw.(map[string]any)
		var event Event
		var err error
		if isObject {
			event, err = ParseEvent(object)
		}
		if !isObject || err != nil {
			h.intake.Drop(site, now, dropstats.AppInvalid)
			out.Dropped = append(out.Dropped, dropped{Index: index, Reason: ReasonInvalid})
			continue
		}
		outcome := h.intake.Accept(site, budgetKey, now, event)
		for _, tag := range outcome.Stripped {
			out.Warnings = append(out.Warnings, warning{Index: index, Reason: ReasonTagNotAllowed, Tag: tag})
		}
		if outcome.Reason != "" {
			out.Dropped = append(out.Dropped, dropped{Index: index, Reason: outcome.Reason})
			continue
		}
		out.Accepted++
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		h.reply(w, http.StatusInternalServerError, badRequestBody)
		return
	}
	h.reply(w, http.StatusAccepted, string(encoded)+"\n")
}

func (h *Handler) reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// clientKey is the in-memory rate-limit key; the address is never stored.
func (h *Handler) clientKey(r *http.Request) string {
	if ip, ok := clientip.Resolve(r, h.trusted); ok {
		return ip.String()
	}
	return clientIPUnknown
}

// decodeRequest parses {"site": string, "events": [1..20 items]}.
func decodeRequest(body []byte) (site string, events []any, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var request struct {
		Site   any `json:"site"`
		Events any `json:"events"`
	}
	if err := decoder.Decode(&request); err != nil || decoder.More() {
		return "", nil, false
	}
	site, isString := request.Site.(string)
	events, isArray := request.Events.([]any)
	if !isString || site == "" || !isArray || len(events) < 1 || len(events) > maxEventsPerRequest {
		return "", nil, false
	}
	return site, events, true
}
