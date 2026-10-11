// Package tracker serves Supermetry's embedded browser tracker.
package tracker

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"
)

//go:generate go run ./cmd/minify -in tracker.js -out tracker.min.js
//go:generate go run ./cmd/minify -in tracker.js -full -out tracker.runtime.min.js
//go:generate go run ./cmd/minify -in tracker.js -full -append tracker.extensions.js -append tracker.replay.js -out tracker.full.min.js
//go:generate go run ./cmd/minify -in tracker.outbound.js -out tracker.outbound.min.js
//go:generate go run ./cmd/minify -in tracker.downloads.js -out tracker.downloads.min.js
//go:generate go run ./cmd/minify -in tracker.clicks.js -out tracker.clicks.min.js
//go:generate go run ./cmd/minify -in tracker.forms.js -out tracker.forms.min.js
//go:generate go run ./cmd/minify -in tracker.notfound.js -out tracker.notfound.min.js
//go:generate go run ./cmd/minify -in tracker.vitals.js -out tracker.vitals.min.js
//go:generate go run ./cmd/minify -in tracker.heatmap.js -out tracker.heatmap.min.js
//go:generate go run ./cmd/minify -in tracker.replay.js -out tracker.replay.min.js

//go:embed tracker.min.js
var script []byte

//go:embed tracker.runtime.min.js
var runtimeScript []byte

//go:embed tracker.full.min.js
var fullScript []byte

//go:embed tracker.outbound.min.js
var outboundScript []byte

//go:embed tracker.downloads.min.js
var downloadsScript []byte

//go:embed tracker.clicks.min.js
var clicksScript []byte

//go:embed tracker.forms.min.js
var formsScript []byte

//go:embed tracker.notfound.min.js
var notFoundScript []byte

//go:embed tracker.vitals.min.js
var vitalsScript []byte

//go:embed tracker.heatmap.min.js
var heatmapScript []byte

//go:embed tracker.replay.min.js
var replayScript []byte

// Handler returns the immutable embedded tracker asset handler.
func Handler() http.Handler {
	return handler(script)
}

// FullHandler returns the core tracker plus automatic event extensions.
func FullHandler() http.Handler {
	return handler(fullScript)
}

// FullPath derives the automatic-tracking asset path from the core path.
func FullPath(path string) string {
	return strings.TrimSuffix(path, ".js") + ".full.js"
}

func handler(source []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Content-Length", strconv.Itoa(len(source)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(source)
		}
	})
}
