//go:build !embedui

package web

import (
	"context"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// DevUIEnv names the variable `make dev` sets to the Vite dev server's address.
const DevUIEnv = "SUPERMETRY_DEV_UI_URL"

// devCSP replaces the app's strict policy on proxied pages: Vite injects an
// inline module script (React refresh) and opens a WebSocket for hot reload.
// It only exists in builds without the embedded UI, never in production. The
// rest mirrors the production app/replay-viewer policy in cmd/supermetry/serve.go
// (a different package, so it is copied; keep the two in step).
const devCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data: blob: https:; connect-src 'self' ws: wss:; frame-src 'self' blob: https: http:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// devUIOrigin returns scheme://host of raw when it is an absolute http(s) URL
// with a host and no credentials, and "" otherwise. Only this origin is ever
// the upstream, so a request can never choose where it is forwarded.
func devUIOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// devUI makes the server a reverse proxy for the Vite dev server: GET and HEAD
// requests that reach the UI catch-all (including WebSocket upgrades for hot
// reload) are forwarded to raw, so the browser only ever talks to this server.
// /api, other methods and the legacy /invite?token= link stay with next, which
// rewrites the invite link before it is proxied on the following hop. With no
// valid URL, next is returned unchanged.
func devUI(next http.Handler, raw string) http.Handler {
	origin := devUIOrigin(raw)
	if origin == "" {
		return next
	}
	target, _ := url.Parse(origin)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// SetURL swaps scheme and host for the configured ones; the path and
			// query are forwarded as received. The client's Host is kept so Vite's
			// own allowedHosts check (DNS-rebinding protection) still applies.
			// Vite needs no credentials.
			r.SetURL(target)
			r.Out.Host = r.In.Host
			r.Out.Header.Del("Cookie")
			r.Out.Header.Del("Authorization")
		},
		Transport: &http.Transport{
			Proxy:                 nil, // never through HTTP_PROXY
			DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			MaxIdleConns:          8,
		},
		ModifyResponse: func(resp *http.Response) error {
			// Only the dev policy set below may apply to proxied pages.
			resp.Header.Del("Content-Security-Policy")
			resp.Header.Del("Content-Security-Policy-Report-Only")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			if r.Context().Err() == context.Canceled {
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<!doctype html><meta charset=\"utf-8\"><title>Supermetry dev</title><p>The Vite dev server is not reachable at " +
				html.EscapeString(origin) + "; run <code>make dev</code>.</p>"))
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") ||
			(r.URL.Path == "/invite" && r.URL.Query().Get("token") != "") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", devCSP)
		if r.Header.Get("Upgrade") != "" {
			// A hot-reload socket outlives the server's read and write timeouts.
			rc := http.NewResponseController(w)
			_ = rc.SetReadDeadline(time.Time{})
			_ = rc.SetWriteDeadline(time.Time{})
		}
		proxy.ServeHTTP(w, r)
	})
}
