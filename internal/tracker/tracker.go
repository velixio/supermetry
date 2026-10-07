// Package tracker serves Panel's embedded browser tracker.
package tracker

import (
	_ "embed"
	"net/http"
	"strconv"
)

//go:generate go run ./cmd/minify -in tracker.js -out tracker.min.js

//go:embed tracker.min.js
var script []byte

// Handler returns the immutable embedded tracker asset handler.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Content-Length", strconv.Itoa(len(script)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(script)
		}
	})
}
