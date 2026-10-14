package opencounters

import (
	"hash/fnv"
	"net/http"
	"strconv"
	"time"

	"supermetry/internal/links"
	"supermetry/internal/pixels"
)

type Handler struct {
	Recorder    *Recorder
	Proxy       *pixels.ProxyLimiter
	Blocklist   map[string]bool
	Branded     bool
	HomepageURL string
	Now         func() time.Time
	Stats       StatsFunc
}

func Register(mux *http.ServeMux, handler *Handler, enabled bool) {
	if !enabled || handler == nil {
		mux.HandleFunc("GET /c/", http.NotFound)
		return
	}
	mux.Handle("GET /c/{rest...}", handler)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key, ext, ok := ParsePath(r.PathValue("rest"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ext == "" {
		h.servePage(w, r, key)
		return
	}
	blocked := h.Blocklist[key]
	if !blocked && r.Method == http.MethodGet {
		if proxy, ok := pixels.DetectProxy(r.UserAgent()); ok && h.Proxy.Allow(hashKey(key), proxy) {
			h.Recorder.View(key, h.Now().UTC().Format(time.DateOnly))
		}
	}
	if ext == "gif" {
		writeImage(w, r, "image/gif", links.TransparentGIF)
		return
	}
	total := int64(0)
	if !blocked {
		total = h.Recorder.Total(key)
	}
	style, color := pixels.ParseBadgeQuery(r.URL.Query())
	body := pixels.RenderBadge(pixels.BadgeOptions{Label: "views", Count: total, Style: style, Color: color, Branded: h.Branded})
	writeImage(w, r, "image/svg+xml", body)
}

func hashKey(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64() >> 1)
}

func writeImage(w http.ResponseWriter, r *http.Request, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
