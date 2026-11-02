package api

import (
	"net/http"
	"strconv"
	"strings"

	"supermetry/internal/ingest"
	"supermetry/internal/replay"
)

// Neutral intake paths give every site its own URLs under /e/{site}, so ad
// blocker rules written for well-known analytics paths (/api/.../events,
// /replay.js, /api/replay/...) do not match. Each one is an alias that
// rewrites the request to the legacy path and hands it to the legacy handler,
// which also rejects a request whose body or query names another site.
//
//	POST /e/{site}             events
//	GET  /e/{site}/r.js        replay recorder
//	POST /e/{site}/s           replay start
//	POST /e/{site}/c?s=...     replay chunk
//	PUT  /e/{site}/k/{hash}    replay stylesheet
const neutralPrefix = "/e/"

// NeutralPathKind reports whether p is a neutral intake path (events or
// replay uploads) or the neutral replay recorder script.
func NeutralPathKind(p string) (intake, recorder bool) {
	_, rest, ok := splitNeutralPath(p)
	if !ok {
		return false, false
	}
	switch {
	case rest == "", rest == "s", rest == "c":
		return true, false
	case rest == "r.js":
		return false, true
	case strings.HasPrefix(rest, "k/") && validNeutralSegment(rest[2:]):
		return true, false
	}
	return false, false
}

// splitNeutralPath splits /e/{site}[/{rest}] and validates the site segment.
func splitNeutralPath(p string) (site, rest string, ok bool) {
	tail, found := strings.CutPrefix(p, neutralPrefix)
	if !found {
		return "", "", false
	}
	site, rest, hasRest := strings.Cut(tail, "/")
	if !validNeutralSegment(site) || hasRest && rest == "" {
		return "", "", false
	}
	return site, rest, true
}

func validNeutralSegment(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

type neutralDeps struct {
	ingest        http.Handler // already wrapped in trackerCORS; nil when ingest is off
	eventPath     string
	replay        http.Handler // start and chunk; nil when replay is off
	replayCSS     http.Handler
	recorder      http.Handler
	replayEnabled bool
}

func neutralHandler(deps neutralDeps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site, rest, ok := splitNeutralPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		replayOn := deps.replayEnabled && deps.replay != nil
		switch {
		case rest == "":
			if deps.ingest == nil {
				http.NotFound(w, r)
				return
			}
			if r.Method != http.MethodPost && r.Method != http.MethodOptions {
				w.Header().Set("Allow", "POST, OPTIONS")
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			deps.ingest.ServeHTTP(w, neutralRequest(r, site, deps.eventPath))
		case rest == "s" && replayOn:
			deps.replay.ServeHTTP(w, neutralRequest(r, site, "/api/replay/start"))
		case rest == "c" && replayOn:
			if !neutralQuerySite(w, r, site) {
				return
			}
			deps.replay.ServeHTTP(w, neutralRequest(r, site, "/api/replay/chunk"))
		case strings.HasPrefix(rest, "k/") && validNeutralSegment(rest[2:]) && deps.replayEnabled && deps.replayCSS != nil:
			if !neutralQuerySite(w, r, site) {
				return
			}
			deps.replayCSS.ServeHTTP(w, neutralRequest(r, site, "/api/replay/css/"+rest[2:]))
		case rest == "r.js" && deps.replayEnabled:
			deps.recorder.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// neutralQuerySite checks that a replay upload's ?s= names the path's site.
// Preflights carry no query worth checking.
func neutralQuerySite(w http.ResponseWriter, r *http.Request, site string) bool {
	if r.Method == http.MethodOptions || r.URL.Query().Get("s") == site {
		return true
	}
	http.Error(w, "site does not match path", http.StatusBadRequest)
	return false
}

func neutralRequest(r *http.Request, site, legacyPath string) *http.Request {
	clone := r.Clone(ingest.WithPathSite(r.Context(), site))
	clone.URL.Path = legacyPath
	clone.URL.RawPath = ""
	return clone
}

// recorderHandler serves the replay recorder script (/replay.js and its
// neutral alias).
func recorderHandler(enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !enabled {
			http.NotFound(w, request)
			return
		}
		body, etag := replay.ReplayJS()
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("ETag", etag)
		if request.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if request.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
}
