//go:build !embedui

package web

import (
	"html"
	"net/http"
	"net/url"
	"strings"
)

// DevUIEnv names the variable `make dev` sets to the Vite dev server's address.
const DevUIEnv = "SUPERMETRY_DEV_UI_URL"

// devProxyHeader is set by the Vite dev proxy on every request it forwards here
// (web/dev-proxy.ts defines the same name). Such a request already came from the
// UI host, so redirecting it back would loop for paths this server does not own.
const devProxyHeader = "X-Supermetry-Dev-Proxy"

// devUIOrigin returns scheme://host of raw when it is an absolute http(s) URL
// with a host, and "" otherwise. Only this origin is ever used as a redirect
// target, so a request can never choose the host.
func devUIOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// devUI wraps the placeholder UI handler: with a valid dev UI URL, GET and HEAD
// requests (other than /api) are redirected to the same path and query there.
// Without one, next is returned unchanged.
func devUI(next http.Handler, raw string) http.Handler {
	origin := devUIOrigin(raw)
	if origin == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") ||
			r.Header.Get(devProxyHeader) != "" ||
			// The legacy /invite?token= link is rewritten by the UI handler itself;
			// its target /invite/<token> is redirected to Vite on the next hop.
			(r.URL.Path == "/invite" && r.URL.Query().Get("token") != "") {
			next.ServeHTTP(w, r)
			return
		}
		// Keep the original escaped path (so /a%2fb stays as sent) and drop extra
		// leading slashes. The scheme and host always come from the configuration.
		target := origin + "/" + strings.TrimLeft(r.URL.EscapedPath(), "/")
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusFound)
	})
}

// stubIndex is the placeholder page of a build without the embedded UI.
func stubIndex(devURL string) []byte {
	note := "UI not built — run <code>make web</code>."
	if origin := devUIOrigin(devURL); origin != "" {
		note = "In make dev the UI is at <a href=\"" + html.EscapeString(origin) + "\">" + html.EscapeString(origin) + "</a>."
	}
	return []byte("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>Supermetry</title></head><body><main><p>" + note + "</p></main></body></html>")
}
