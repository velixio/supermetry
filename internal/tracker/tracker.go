// Package tracker serves Supermetry's embedded browser tracker.
package tracker

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"
)

//go:generate go run ./cmd/minify -in tracker.js -out tracker.min.js
//go:generate go run ./cmd/minify -in tracker.js -full -append tracker.extensions.js -out tracker.full.min.js

//go:embed tracker.min.js
var script []byte

//go:embed tracker.full.min.js
var fullScript []byte

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
