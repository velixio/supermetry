package tracker

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"panel/internal/config"
	"panel/internal/replay"
	"panel/internal/sites"
)

const maxSiteScriptCacheEntries = 10_000

// Script is a generated tracker asset and its strong validator.
type Script struct {
	Body []byte
	ETag string
}

// BuildScript prepends a site's resolved settings to the appropriate embedded
// tracker. Snippet data attributes take precedence because the prelude uses
// nullish assignment.
func BuildScript(publicID string, settings sites.TrackerSettings, replaySettings replay.SiteSettings) Script {
	prefix := prelude(publicID, settings, replaySettings)
	source := script
	if settings.AnyExtension() || replaySettings.Enabled {
		source = fullScript
	}
	body := make([]byte, 0, len(prefix)+len(source))
	body = append(body, prefix...)
	body = append(body, source...)
	digest := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(digest[:])[:16] + `"`
	return Script{Body: body, ETag: etag}
}

const defaultEventPath = "/api/public/v1/events"

var eventPath atomic.Value // string

// SetEventPath sets the ingest path the generated scripts post to
// (PANEL_EVENT_PATH). An empty path restores the default. Call it before
// serving scripts; already cached scripts keep the old path.
func SetEventPath(p string) {
	if p == "" {
		p = defaultEventPath
	}
	eventPath.Store(p)
}

func currentEventPath() string {
	if p, ok := eventPath.Load().(string); ok && p != "" {
		return p
	}
	return defaultEventPath
}

func prelude(publicID string, settings sites.TrackerSettings, replaySettings replay.SiteSettings) string {
	var builder strings.Builder
	builder.WriteString(`(d=>{d.site??="`)
	builder.WriteString(publicID)
	builder.WriteString(`";`)
	// The snippet is a bare <script src>: send events to the origin the script
	// came from (the Panel host, or the site's own proxy), never to a path on
	// the page's origin. An explicit data-api still wins.
	builder.WriteString(`d.api??=new URL(document.currentScript.src).origin+`)
	builder.WriteString(strconv.Quote(currentEventPath()))
	builder.WriteString(`;`)
	off := func(attribute string, enabled bool) {
		if !enabled {
			builder.WriteString(`d`)
			builder.WriteString(attribute)
			builder.WriteString(`??="false";`)
		}
	}
	off(".outbound", settings.Outbound)
	off(".downloads", settings.Downloads)
	off(".forms", settings.Forms)
	off(".vitals", settings.Vitals)
	off("[404]", settings.NotFound)
	// Heatmap capture is opt-in in the tracker, so it is switched on here.
	if settings.Clicks {
		builder.WriteString(`d.heatmap??="true";`)
	}
	if settings.Hash {
		builder.WriteString(`d.hash??="true";`)
	}
	if replaySettings.Enabled {
		builder.WriteString(`d.replay??="`)
		builder.WriteString(replaySettings.Consent)
		builder.WriteString(`";`)
	}
	builder.WriteString(`})(document.currentScript.dataset);` + "\n")
	return builder.String()
}

var siteScriptBuilder = BuildScript

type siteScriptCacheKey struct {
	publicID      string
	settings      sites.TrackerSettings
	replayEnabled bool
	replayConsent string
}

// SiteHandler serves generated tracker scripts for sites in the in-memory
// registry. The mount prefix is deliberately not trusted when extracting the
// final path component.
func SiteHandler(registry sites.Registry, replayEnvs ...config.ReplayEnv) http.Handler {
	var replayEnv config.ReplayEnv
	if len(replayEnvs) != 0 {
		replayEnv = replayEnvs[0]
	}
	var cache sync.Map
	var cacheEntries atomic.Int64
	var buildMu sync.Mutex

	load := func(key siteScriptCacheKey, replaySettings replay.SiteSettings) Script {
		if cached, ok := cache.Load(key); ok {
			return cached.(Script)
		}
		buildMu.Lock()
		defer buildMu.Unlock()
		if cached, ok := cache.Load(key); ok {
			return cached.(Script)
		}
		built := siteScriptBuilder(key.publicID, key.settings, replaySettings)
		if cacheEntries.Load() >= maxSiteScriptCacheEntries {
			cache.Clear()
			cacheEntries.Store(0)
		}
		cache.Store(key, built)
		cacheEntries.Add(1)
		return built
	}

	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(w, request)
			return
		}
		escapedPath := strings.ToLower(request.URL.EscapedPath())
		if strings.Contains(request.URL.Path, "..") || strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") {
			http.NotFound(w, request)
			return
		}
		filename := path.Base(request.URL.Path)
		if !strings.HasSuffix(filename, ".js") {
			http.NotFound(w, request)
			return
		}
		publicID := strings.TrimSuffix(filename, ".js")
		if !validPublicID(publicID) {
			http.NotFound(w, request)
			return
		}
		site, ok := registry.ByPublicID(publicID)
		if !ok {
			http.NotFound(w, request)
			return
		}
		settings := site.Tracker.Resolve(site.HeatmapsEnabled)
		replaySettings := replay.ResolveSite(replayEnv, site.Replay)
		generated := load(siteScriptCacheKey{
			publicID: publicID, settings: settings,
			replayEnabled: replaySettings.Enabled, replayConsent: replaySettings.Consent,
		}, replaySettings)

		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("ETag", generated.ETag)
		if request.Header.Get("If-None-Match") == generated.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(generated.Body)))
		if request.Method != http.MethodHead {
			_, _ = w.Write(generated.Body)
		}
	})
}

func validPublicID(publicID string) bool {
	if len(publicID) != 12 {
		return false
	}
	for _, character := range publicID {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

// SitePath derives the per-site generated tracker path from the configured
// static tracker path.
func SitePath(trackerPath, publicID string) string {
	return path.Dir(trackerPath) + "/" + publicID + ".js"
}
