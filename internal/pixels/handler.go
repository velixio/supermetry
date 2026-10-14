package pixels

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/enrich"
	"supermetry/internal/ingest"
	"supermetry/internal/links"
	"supermetry/internal/sites"
)

type Handler struct {
	Pixels       *Registry
	Recorder     *Recorder
	Sites        sites.Registry
	Enricher     links.Enricher
	IPLimiter    ingest.RateLimiter
	ProxyLimiter *ProxyLimiter
	Fallback     http.HandlerFunc
	ClientIP     func(*http.Request) (string, netip.Addr)
	Branded      bool
	Now          func() time.Time
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	id, ext, _ := strings.Cut(file, ".")
	pixel, known := h.Pixels.ByPublicID(id)
	if !known && ext == "gif" && h.Fallback != nil {
		h.Fallback(w, r)
		return
	}
	if known && (ext == "gif" || ext == "svg") {
		h.count(r, pixel)
	}
	if ext == "svg" {
		label, total := "views", int64(0)
		if known {
			label, total = pixel.BadgeLabel, h.Pixels.Totals().Get(pixel.ID)
		}
		style, color := ParseBadgeQuery(r.URL.Query())
		write(w, r, "image/svg+xml", RenderBadge(BadgeOptions{Label: label, Count: total, Style: style, Color: color, Branded: h.Branded}))
		return
	}
	write(w, r, "image/gif", links.TransparentGIF)
}

func write(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", "no-store, max-age=0")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (h *Handler) count(r *http.Request, pixel Pixel) {
	if r.Method != http.MethodGet || !pixel.Enabled {
		return
	}
	site, ok := h.Sites.Site(pixel.SiteID)
	if !ok || site.Kind != sites.KindPixel {
		return
	}
	day := h.Now().In(location(site)).Format(time.DateOnly)
	if proxy, ok := DetectProxy(r.UserAgent()); ok {
		if h.ProxyLimiter.Allow(pixel.ID, proxy) {
			h.Recorder.View(ViewKey{PixelID: pixel.ID, Day: day, Via: "proxy"})
		}
		return
	}
	key, ip := h.ClientIP(r)
	if h.IPLimiter != nil && !h.IPLimiter.Allow("pxv:"+key) {
		return
	}
	if h.Enricher.BotWithBlockedReferrers(r.UserAgent(), ip, "", site.BlockedReferrers) != enrich.BotNone {
		return
	}
	if site.RespectPrivacySignals && (r.Header.Get("DNT") == "1" || r.Header.Get("Sec-GPC") == "1") {
		return
	}
	h.Recorder.View(ViewKey{PixelID: pixel.ID, Day: day, Country: h.Enricher.Country(ip), Via: "direct"})
}

func location(site sites.Site) *time.Location {
	if site.TZ == nil {
		return time.UTC
	}
	return site.TZ
}
