package links

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supermetry/internal/clientip"
	"supermetry/internal/enrich"
	"supermetry/internal/ingest"
	"supermetry/internal/sites"
)

const (
	positiveTTL   = 30 * time.Second
	negativeTTL   = 10 * time.Second
	maxCacheItems = 10_000
	lookupTimeout = 3 * time.Second
	maxReferrer   = 100
)

// TransparentGIF is a 1x1 transparent GIF89a image (43 bytes).
var TransparentGIF = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

// Enricher is the part of the shared enrichment service the handlers need.
type Enricher interface {
	BotWithBlockedReferrers(ua string, ip netip.Addr, referrer string, blocked []string) enrich.BotReason
	Country(netip.Addr) string
	City(netip.Addr) enrich.CityInfo
	Client(ua string) enrich.Client
}

// LookupFunc resolves a normalized slug to a link; ErrNotFound when unknown.
type LookupFunc func(ctx context.Context, slug string) (Link, error)

type cacheEntry struct {
	link   Link
	found  bool
	base   int64 // persisted plus buffered clicks when loaded
	served atomic.Int64
	until  time.Time
}

// Handler serves the public short-link redirect and tracking pixel. It reads
// the database only to fill a small in-memory cache; counting is delegated to
// the Recorder and never waits on storage.
type Handler struct {
	Lookup         LookupFunc
	Recorder       *Recorder
	Sites          sites.Registry
	Enricher       Enricher
	Limiter        ingest.RateLimiter // per client, all requests
	MissLimiter    ingest.RateLimiter // per client, cache misses that reach the database
	TrustedProxies []netip.Prefix
	Now            func() time.Time
	// DetectProxy names image proxies (GitHub's, Google's) that fetch on behalf of a reader.
	// Their requests share the proxy's address and user agent, so they skip the per-client
	// limiter and the bot check and use ProxyLimiter instead.
	DetectProxy  func(userAgent string) (string, bool)
	ProxyLimiter interface {
		Allow(key int64, proxy string) bool
	}

	mu    sync.Mutex
	cache map[string]*cacheEntry
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Forget drops cached resolutions so changes apply immediately in this process.
func (h *Handler) Forget(slugs ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, slug := range slugs {
		delete(h.cache, slug)
	}
}

func (h *Handler) clientKey(r *http.Request) (string, netip.Addr) {
	ip, ok := clientip.Resolve(r, h.TrustedProxies)
	if !ok {
		return "unknown", netip.Addr{}
	}
	sum := sha256.Sum256([]byte(ip.String()))
	return hex.EncodeToString(sum[:8]), ip
}

// ClientKey returns the privacy-preserving limiter key and parsed client IP.
func (h *Handler) ClientKey(r *http.Request) (string, netip.Addr) { return h.clientKey(r) }

func plain(w http.ResponseWriter, status int, text string) {
	header := w.Header()
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(text + "\n"))
}

func (h *Handler) resolve(ctx context.Context, slug, key string) (*cacheEntry, int) {
	now := h.now()
	h.mu.Lock()
	entry := h.cache[slug]
	h.mu.Unlock()
	if entry != nil && now.Before(entry.until) {
		return entry, 0
	}
	if h.MissLimiter != nil && !h.MissLimiter.Allow(key) {
		return nil, http.StatusTooManyRequests
	}
	lookupCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	link, err := h.Lookup(lookupCtx, slug)
	fresh := &cacheEntry{}
	switch {
	case errors.Is(err, ErrNotFound):
		fresh.until = now.Add(negativeTTL)
	case err != nil:
		return nil, http.StatusServiceUnavailable
	default:
		fresh.link, fresh.found = link, true
		fresh.base = link.Clicks + h.Recorder.Pending(link.ID)
		fresh.until = now.Add(positiveTTL)
	}
	h.mu.Lock()
	if h.cache == nil || len(h.cache) >= maxCacheItems {
		h.cache = make(map[string]*cacheEntry)
	}
	h.cache[slug] = fresh
	h.mu.Unlock()
	return fresh, 0
}

// ServeRedirect handles GET /l/{slug}. The destination always comes from the
// stored, validated link row; nothing in the request can influence where the
// redirect points, so the endpoint cannot be used as an open redirect.
func (h *Handler) ServeRedirect(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugPattern.MatchString(slug) {
		plain(w, http.StatusNotFound, "Link not found")
		return
	}
	key, ip := h.clientKey(r)
	if h.Limiter != nil && !h.Limiter.Allow("l:"+key) {
		w.Header().Set("Retry-After", "1")
		plain(w, http.StatusTooManyRequests, "Too many requests")
		return
	}
	entry, status := h.resolve(r.Context(), slug, "m:"+key)
	switch status {
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", "5")
		plain(w, status, "Too many requests")
		return
	case http.StatusServiceUnavailable:
		w.Header().Set("Retry-After", "5")
		plain(w, status, "Temporarily unavailable")
		return
	}
	if !entry.found {
		plain(w, http.StatusNotFound, "Link not found")
		return
	}
	link := entry.link
	switch link.Status(h.now(), entry.served.Load()+entry.base-link.Clicks) {
	case StatusDisabled:
		plain(w, http.StatusNotFound, "Link not found")
		return
	case StatusExpired, StatusLimitReached:
		plain(w, http.StatusGone, "This link is no longer available")
		return
	}
	site, ok := h.Sites.Site(link.SiteID)
	if !ok {
		plain(w, http.StatusNotFound, "Link not found")
		return
	}
	if r.Method == http.MethodGet {
		h.countClick(r, entry, site, ip)
	}
	header := w.Header()
	header.Set("Location", link.UTM.Apply(link.Destination))
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusFound)
}

func (h *Handler) countClick(r *http.Request, entry *cacheEntry, site sites.Site, ip netip.Addr) {
	referrer := r.Referer()
	ua := r.UserAgent()
	if h.Enricher.BotWithBlockedReferrers(ua, ip, referrer, site.BlockedReferrers) != enrich.BotNone {
		return
	}
	if site.RespectPrivacySignals && privacySignal(r) {
		return
	}
	entry.served.Add(1)
	h.Recorder.Click(entry.link.ID, localDay(h.now(), site), h.Enricher.Country(ip), referrerHost(referrer), deviceName(h.Enricher.Client(ua).Device))
}

// ServePixel handles GET /px/{file}, where file is "<public id>.gif". It always
// answers with the same 1x1 image, whether or not the site exists or the hit
// was counted, so the endpoint reveals nothing and an email never shows a
// broken image.
func (h *Handler) ServePixel(w http.ResponseWriter, r *http.Request) {
	publicID, isGIF := strings.CutSuffix(r.PathValue("file"), ".gif")
	header := w.Header()
	header.Set("Content-Type", "image/gif")
	header.Set("Cache-Control", "no-store, max-age=0")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Length", "43")
	defer func() {
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(TransparentGIF)
		}
	}()
	if isGIF {
		h.countPixel(r, publicID)
	}
}

// BadgeStats counts the request like ServePixel and returns one registered tag's
// all-time total and last n days. Unregistered tags never allocate badge state.
func (h *Handler) BadgeStats(r *http.Request, publicID string, n int) (total int64, series []int64, ok bool) {
	if h.Recorder == nil || h.Recorder.Views == nil {
		return 0, nil, false
	}
	site, found := h.countPixel(r, publicID)
	if !found {
		return 0, nil, false
	}
	tag := NormalizeCampaign(r.URL.Query().Get("c"))
	if tag == "" {
		return 0, nil, false
	}
	return h.Recorder.Views.Snapshot(site.ID, tag, localDay(h.now(), site), n)
}

// countPixel records one impression when the request is allowed to count, and reports
// the site the public ID belongs to. Previews, bots, limited clients and privacy signals
// are not counted but still find the site.
func (h *Handler) countPixel(r *http.Request, publicID string) (sites.Site, bool) {
	if len(publicID) > 64 {
		return sites.Site{}, false
	}
	site, ok := h.Sites.ByPublicID(publicID)
	if !ok {
		return sites.Site{}, false
	}
	if r.Method != http.MethodGet || r.URL.Query().Get("preview") == "1" {
		return site, true
	}
	tag := NormalizeCampaign(r.URL.Query().Get("c"))
	if tag == "" || h.Recorder == nil || h.Recorder.Views == nil || !h.Recorder.Views.Registered(site.ID, tag) {
		return site, true
	}
	if h.DetectProxy != nil {
		if proxy, ok := h.DetectProxy(r.UserAgent()); ok {
			if h.ProxyLimiter == nil || h.ProxyLimiter.Allow(int64(site.ID), proxy) {
				source := SourceOther
				if proxy == "camo" {
					source = SourceGitHub
				}
				h.Recorder.Impression(site.ID, localDay(h.now(), site), tag, Place{Source: source})
			}
			return site, true
		}
	}
	key, ip := h.clientKey(r)
	if h.Limiter != nil && !h.Limiter.Allow("px:"+key) {
		return site, true
	}
	if h.Enricher.BotWithBlockedReferrers(r.UserAgent(), ip, "", site.BlockedReferrers) != enrich.BotNone {
		return site, true
	}
	if site.RespectPrivacySignals && privacySignal(r) {
		return site, true
	}
	where := h.Enricher.City(ip)
	h.Recorder.Impression(site.ID, localDay(h.now(), site), tag, Place{Country: where.Country, City: where.City, Source: SourceOther})
	return site, true
}

func privacySignal(r *http.Request) bool {
	return r.Header.Get("DNT") == "1" || r.Header.Get("Sec-GPC") == "1"
}

func localDay(now time.Time, site sites.Site) string {
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	return now.In(location).Format(time.DateOnly)
}

// referrerHost reduces a Referer header to a lowercase host; paths and query
// strings, which can carry identifiers, are never kept.
func referrerHost(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if len(host) > maxReferrer {
		host = host[:maxReferrer]
	}
	// Truncation or hostile input must never produce bytes PostgreSQL rejects,
	// which would fail the whole batch.
	return strings.ReplaceAll(strings.ToValidUTF8(host, ""), "\x00", "")
}

func deviceName(device int16) string {
	switch device {
	case 1:
		return "mobile"
	case 2:
		return "tablet"
	default:
		return "desktop"
	}
}
