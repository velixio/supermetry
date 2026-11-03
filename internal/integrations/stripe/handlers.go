package stripe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"supermetry/internal/clientip"
	"supermetry/internal/privacy"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
)

// MaxWebhookBytes bounds a delivery. Stripe event payloads are well below
// this; larger bodies are rejected before they are buffered.
const MaxWebhookBytes = 256 << 10

// RateLimiter is satisfied by ingest.NewTokenBuckets.
type RateLimiter interface {
	Allow(key string) bool
}

// WebhookHandler receives POST {WebhookPath}{endpoint}.
type WebhookHandler struct {
	Service        *Service
	Limiter        RateLimiter
	TrustedProxies []netip.Prefix
}

type webhookProcessor interface {
	HandleWebhook(ctx context.Context, endpointID, signature string, body []byte) int
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var processor webhookProcessor
	if h != nil && h.Service != nil {
		processor = h.Service
	}
	serveWebhook(w, r, processor, h.limiter(), h.trustedProxies())
}

func (h *WebhookHandler) limiter() RateLimiter {
	if h == nil {
		return nil
	}
	return h.Limiter
}

func (h *WebhookHandler) trustedProxies() []netip.Prefix {
	if h == nil {
		return nil
	}
	return h.TrustedProxies
}

func serveWebhook(w http.ResponseWriter, r *http.Request, processor webhookProcessor, limiter RateLimiter, trusted []netip.Prefix) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeStatus(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if processor == nil {
		writeStatus(w, http.StatusServiceUnavailable, "stripe integration is unavailable")
		return
	}
	if limiter != nil {
		ip, _ := clientip.Resolve(r, trusted)
		if !limiter.Allow("stripe-webhook:" + ip.String()) {
			w.Header().Set("Retry-After", "1")
			writeStatus(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
	}
	if r.ContentLength > MaxWebhookBytes {
		writeStatus(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxWebhookBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeStatus(w, http.StatusRequestEntityTooLarge, "payload too large")
			return
		}
		writeStatus(w, http.StatusBadRequest, "could not read the payload")
		return
	}
	status := processor.HandleWebhook(r.Context(), r.PathValue("endpoint"), r.Header.Get("Stripe-Signature"), body)
	switch status {
	case http.StatusOK:
		writeJSON(w, http.StatusOK, map[string]bool{"received": true})
	case http.StatusBadRequest:
		// Unknown endpoints, pending ones and bad signatures share this answer.
		writeStatus(w, status, "the webhook could not be verified")
	case http.StatusServiceUnavailable:
		w.Header().Set("Retry-After", "5")
		writeStatus(w, status, "temporarily unavailable, retry later")
	default:
		writeStatus(w, status, "the webhook could not be processed")
	}
}

func writeStatus(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": http.StatusText(status), "status": status, "detail": detail,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// SaltSource returns the daily visitor salt.
type SaltSource interface {
	For(day time.Time) ([]byte, bool)
}

// AttributionHandler serves POST /api/public/v1/attribution. It returns a
// token for the caller's live session, derived exactly like event ingestion
// (IP and user agent with the daily salt), or null. No cookie or storage is
// involved and no new identifier is created.
type AttributionHandler struct {
	Sites          sites.Registry
	Salts          SaltSource
	Sessions       session.SnapshotJoiner
	Tokens         *Tokens
	Limiter        RateLimiter
	TrustedProxies []netip.Prefix
	Mode           privacy.Mode
	Now            func() time.Time
}

const (
	maxAttributionBody = 1 << 10
	previousSaltWindow = 30 * time.Minute
)

type attributionResponse struct {
	ID *string `json:"id"`
}

func (h *AttributionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Origin")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		writeStatus(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h == nil || h.Sites == nil || h.Salts == nil || h.Sessions == nil || h.Tokens == nil {
		writeStatus(w, http.StatusServiceUnavailable, "attribution is unavailable")
		return
	}
	ip, ok := clientip.Resolve(r, h.TrustedProxies)
	if !ok {
		writeStatus(w, http.StatusBadRequest, "client address is unavailable")
		return
	}
	if h.Limiter != nil && !h.Limiter.Allow(attributionRateKey(ip)) {
		w.Header().Set("Retry-After", "1")
		writeStatus(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	var request struct {
		Site string `json:"site"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAttributionBody))
	if err := decoder.Decode(&request); err != nil || request.Site == "" || len(request.Site) > 300 {
		writeStatus(w, http.StatusBadRequest, "site is required")
		return
	}
	site, found := h.Sites.ByPublicID(request.Site)
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if found && origin != "" {
		if !originAllowed(site, origin) {
			writeStatus(w, http.StatusForbidden, "origin is not allowed for this site")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	writeJSON(w, http.StatusOK, attributionResponse{ID: h.token(r, site, found, ip)})
}

// token returns nil whenever the site would not keep attribution for an
// ordinary event: unknown or demo site, identity not allowed by the tracking
// mode or the site, a privacy signal the site respects, or no live session.
func (h *AttributionHandler) token(r *http.Request, site sites.Site, found bool, ip netip.Addr) *string {
	if !found || site.Demo || site.TZ == nil || h.Mode != privacy.Identified || !site.ProfilesEnabled {
		return nil
	}
	if site.RespectPrivacySignals && (r.Header.Get("Sec-GPC") == "1" || r.Header.Get("DNT") == "1") {
		return nil
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	day := salts.LocalDay(now, site.TZ)
	salt, ok := h.Salts.For(day)
	if !ok {
		return nil
	}
	ua := r.UserAgent()
	hit := session.Hit{SiteID: site.ID, TS: now, VisitorID: salts.VisitorID(salt, site.ID, ip.String(), ua)}
	localNow := now.In(site.TZ)
	midnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, site.TZ)
	if localNow.Sub(midnight) <= previousSaltWindow {
		if previous, ok := h.Salts.For(day.AddDate(0, 0, -1)); ok {
			hit.PrevVisitorID = salts.VisitorID(previous, site.ID, ip.String(), ua)
		}
	}
	snapshot, ok := h.Sessions.JoinSnapshot(hit)
	if !ok {
		return nil
	}
	token := h.Tokens.Seal(Attribution{
		SiteID: site.ID, SessionID: snapshot.SessionID, VisitorID: snapshot.VisitorID,
		Source: snapshot.Source, UTMSource: snapshot.UTMSource, Medium: snapshot.Medium, Campaign: snapshot.Campaign,
		Region: snapshot.Region, Country: snapshot.Country, IssuedAt: now,
	})
	return &token
}

func attributionRateKey(ip netip.Addr) string {
	sum := sha256.Sum256([]byte(ip.String()))
	return "stripe-attribution:" + hex.EncodeToString(sum[:16])
}

func originAllowed(site sites.Site, origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return site.Allows(strings.ToLower(parsed.Hostname()))
}
