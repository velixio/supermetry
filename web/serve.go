package web

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const immutableCache = "public, max-age=31536000, immutable"

// Brand files keep fixed names, so a short cache lets a new logo reach browsers.
const brandCache = "public, max-age=86400"

var rootIcons = map[string]bool{
	"/favicon.ico":          true,
	"/favicon-32x32.png":    true,
	"/apple-touch-icon.png": true,
	"/email-logo.png":       true, // linked from transactional emails; must be public
}

// Handler serves the embedded dashboard and its static assets.
func Handler() http.Handler {
	return wrapUI(newHandler(builtUI.files()))
}

func newHandler(files fs.FS) http.Handler {
	server := http.FileServer(http.FS(files))
	index, indexErr := fs.ReadFile(files, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		path := r.URL.Path
		// Issued invitations use a query token; the SPA accepts a path token.
		// Keep already-issued links working without caching their credentials.
		if path == "/invite" {
			if token := r.URL.Query().Get("token"); token != "" {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, "/invite/"+url.PathEscape(token), http.StatusFound)
				return
			}
		}
		// Older reset emails linked to /password-reset?token=; the SPA route is
		// /reset/{token}. The token is a credential: never cache or refer it.
		if path == "/password-reset" {
			if token := r.URL.Query().Get("token"); token != "" {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Referrer-Policy", "no-referrer")
				http.Redirect(w, r, "/reset/"+url.PathEscape(token), http.StatusFound)
				return
			}
		}
		if path == "/api" || strings.HasPrefix(path, "/api/") {
			http.NotFound(w, r)
			return
		}

		cache := ""
		switch {
		case strings.HasPrefix(path, "/assets/") || strings.HasPrefix(path, "/flags/") || strings.HasPrefix(path, "/icons/"):
			cache = immutableCache
		case strings.HasPrefix(path, "/brand/") || rootIcons[path]:
			cache = brandCache
		}
		if cache != "" {
			name := strings.TrimPrefix(path, "/")
			if info, err := fs.Stat(files, name); err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", cache)
			server.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		if indexErr != nil {
			http.Error(w, "UI index is unavailable", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
