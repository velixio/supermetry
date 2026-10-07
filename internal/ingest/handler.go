package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"panel/internal/dict"
	"panel/internal/enrich"
	"panel/internal/health"
	"panel/internal/salts"
	"panel/internal/session"
	"panel/internal/sites"
	"panel/internal/writer"
)

const previousSaltWindow = 30 * time.Minute

// Bots counts filtered requests by enrich.BotReason. Index zero is unused.
// Invalid counts rejected payloads without retaining request details.
var (
	Bots    [5]atomic.Int64
	Invalid atomic.Int64
)

// Clock supplies the server timestamp used for accepted events.
type Clock interface {
	Now() time.Time
}

// Handler implements the event intake endpoint. Its dependencies are caches
// or in-memory stores on the normal path; the bounded Writer applies the final
// nonblocking backpressure decision.
type Handler struct {
	Sites          sites.Registry
	Enricher       *enrich.Enricher
	Salts          *salts.Store
	Dict           *dict.Dict
	Sessions       session.Store
	Writer         *writer.Writer
	Limiter        RateLimiter
	TrustedProxies []netip.Prefix
	Clock          Clock
	Diag           bool // log how each client address was resolved (PANEL_INGEST_DIAG=1)
}

// ServeHTTP validates, enriches and queues one analytics event.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.invalid(w)
		return
	}
	payload, err := ParsePayload(body)
	if err != nil || payload.Site == "" || payload.Name == "" || payload.URL == "" {
		h.invalid(w)
		return
	}
	if h.Sites == nil || h.Enricher == nil || h.Salts == nil || h.Dict == nil || h.Sessions == nil || h.Writer == nil {
		h.unavailable(w)
		return
	}

	site, ok := h.Sites.ByPublicID(payload.Site)
	if !ok || site.TZ == nil {
		h.invalid(w)
		return
	}
	pageURL, err := parsePageURL(payload.URL)
	if err != nil || !site.Allows(strings.ToLower(pageURL.Hostname())) {
		h.invalid(w)
		return
	}
	ip, ok := h.clientIP(r)
	if !ok {
		h.invalid(w)
		return
	}
	if h.Diag {
		h.logDiag(r, ip)
	}
	if h.Limiter != nil && (!h.Limiter.Allow("site:"+strconv.FormatInt(int64(site.ID), 10)) || !h.Limiter.Allow(ipRateKey(ip))) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	ua := r.UserAgent()
	if reason := h.Enricher.Bot(ua, ip, payload.Referrer); reason != enrich.BotNone {
		Bots[reason].Add(1)
		health.BotsFiltered[reason].Add(1)
		if h.Diag {
			h.logBotDrop(reason, ip)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	now := time.Now()
	if h.Clock != nil {
		now = h.Clock.Now()
	}
	localDay := salts.LocalDay(now, site.TZ)
	salt, ok := h.Salts.For(localDay)
	if !ok {
		h.unavailable(w)
		return
	}
	visitorID := salts.VisitorID(salt, site.ID, ip.String(), ua)
	var previousVisitorID int64
	localNow := now.In(site.TZ)
	localMidnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, site.TZ)
	if localNow.Sub(localMidnight) <= previousSaltWindow {
		if previousSalt, found := h.Salts.For(localDay.AddDate(0, 0, -1)); found {
			previousVisitorID = salts.VisitorID(previousSalt, site.ID, ip.String(), ua)
		}
	}

	event, hit, err := h.buildEvent(r.Context(), payload, pageURL, site, ip, ua, now, localDay, visitorID, previousVisitorID)
	if err != nil {
		h.unavailable(w)
		return
	}
	result := h.Sessions.Touch(hit)
	event.Hit = hit
	event.VisitorID = result.VisitorID
	event.SessionID = result.SessionID
	if !h.Writer.Enqueue(event) {
		health.IngestDropped.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	health.IngestAccepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) buildEvent(
	ctx context.Context,
	payload Payload,
	pageURL *url.URL,
	site sites.Site,
	ip netip.Addr,
	ua string,
	now, localDay time.Time,
	visitorID, previousVisitorID int64,
) (writer.Event, session.Hit, error) {
	path := pageURL.Path
	if path == "" {
		path = "/"
	}
	utm := enrich.ParseUTM(payload.URL)
	source, medium := h.Enricher.Source(payload.Referrer, payload.URL, utm)
	client := h.Enricher.Client(ua)
	country := h.Enricher.Country(ip)

	pathID, err := h.dimensionID(ctx, dict.Path, path)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	referrerID, err := h.dimensionID(ctx, dict.Referrer, payload.Referrer)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	sourceID, err := h.dimensionID(ctx, dict.Source, source)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	mediumID, err := h.dimensionID(ctx, dict.Medium, medium)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	campaignID, err := h.dimensionID(ctx, dict.Campaign, utm.Campaign)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	browserID, err := h.dimensionID(ctx, dict.Browser, client.Browser)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	osID, err := h.dimensionID(ctx, dict.OS, client.OS)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}

	kind := int16(1)
	var nameID int32
	switch payload.Name {
	case "pageview":
		kind = 0
	case "engagement":
		kind = 2
	default:
		nameID, err = h.dimensionID(ctx, dict.EventName, payload.Name)
		if err != nil {
			return writer.Event{}, session.Hit{}, err
		}
	}
	hit := session.Hit{
		SiteID: site.ID, Retention: site.Retention, TS: now, LocalDay: localDay,
		VisitorID: visitorID, PrevVisitorID: previousVisitorID, Kind: kind,
		PathID: pathID, NameID: nameID, Source: sourceID, Medium: mediumID,
		Campaign: campaignID, Country: country, Browser: browserID, OS: osID,
		Device: client.Device, EngagementMS: int32(payload.EngagementMS), ScrollPct: int16(payload.ScrollPct),
	}
	props := payload.Props
	if kind != 1 {
		props = nil
	}
	return writer.Event{
		Props: props, Screen: int16(payload.ScreenWidth), ReferrerID: referrerID,
	}, hit, nil
}

func (h *Handler) dimensionID(ctx context.Context, kind dict.Kind, value string) (int32, error) {
	if value == "" {
		return 0, nil
	}
	ids, err := h.Dict.IDs(ctx, kind, []string{value})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

func parsePageURL(raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errInvalidURL
	}
	return u, nil
}

var errInvalidURL = errors.New("invalid page URL")

func (h *Handler) clientIP(r *http.Request) (netip.Addr, bool) {
	peer, ok := parseRemoteAddr(r.RemoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	// A dual-stack listener reports IPv4 peers as IPv4-mapped IPv6
	// (::ffff:192.168.0.1); unmap before matching IPv4 proxy prefixes.
	peer = peer.Unmap()
	if h.proxyTrusted(peer) {
		// Walk the chain from the nearest hop outward and take the first
		// public address, so private hops and spoofed left-hand entries lose
		// to what our own proxy appended. A chain with no public address
		// (LAN or local testing) falls back to the peer.
		hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				continue
			}
			hop = hop.Unmap()
			if isPublicIP(hop) && !h.proxyTrusted(hop) {
				return hop, true
			}
		}
	}
	return peer.Unmap(), true
}

// isPublicIP reports whether addr is a globally routable unicast address;
// private, loopback, link-local and unique-local ranges are not.
func isPublicIP(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast()
}

func parseRemoteAddr(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	addr, err := netip.ParseAddr(host)
	return addr, err == nil
}

func (h *Handler) proxyTrusted(addr netip.Addr) bool {
	for _, prefix := range h.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func ipRateKey(ip netip.Addr) string {
	sum := sha256.Sum256([]byte(ip.String()))
	return "ip:" + hex.EncodeToString(sum[:16])
}

func (h *Handler) invalid(w http.ResponseWriter) {
	Invalid.Add(1)
	health.IngestInvalid.Add(1)
	w.WriteHeader(http.StatusBadRequest)
}

func (h *Handler) unavailable(w http.ResponseWriter) {
	health.IngestDropped.Add(1)
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
}
