package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/testpg"
)

func TestClampIngestRatesPreservesOrderingAndWarnsOnce(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	ip, site, global := clampIngestRates(logger, 9000, 8000, 7000)
	if ip != 7000 || site != 7000 || global != 7000 {
		t.Fatalf("effective rates = (%d, %d, %d), want (7000, 7000, 7000)", ip, site, global)
	}
	if got := strings.Count(output.String(), "level=WARN"); got != 1 {
		t.Fatalf("warning count = %d, want 1; output=%q", got, output.String())
	}
	output.Reset()
	ip, site, global = clampIngestRates(logger, 1000, 5000, 10000)
	if ip != 1000 || site != 5000 || global != 10000 || output.Len() != 0 {
		t.Fatalf("valid rates changed or warned: rates=(%d, %d, %d) output=%q", ip, site, global, output.String())
	}
}

func TestLogAdminSetup(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	logAdminSetup(logger, "https://analytics.example/", false)
	if got := output.String(); !strings.Contains(got, `msg="No admin account yet: finish setup at https://analytics.example/app"`) {
		t.Fatalf("setup log = %q", got)
	}

	output.Reset()
	logAdminSetup(logger, "https://analytics.example", true)
	if output.Len() != 0 {
		t.Fatalf("setup log with existing user = %q", output.String())
	}
}

func TestWarnLoopbackProxyRisk(t *testing.T) {
	tests := []struct {
		name    string
		addr    net.Addr
		trusted []netip.Prefix
		warn    bool
	}{
		{name: "IPv4 loopback", addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}, warn: true},
		{name: "IPv6 loopback", addr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 8080}, warn: true},
		{name: "all interfaces", addr: &net.TCPAddr{IP: net.IPv4zero, Port: 8080}},
		{name: "trusted proxy configured", addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}, trusted: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			warnLoopbackProxyRisk(slog.New(slog.NewTextHandler(&output, nil)), tt.addr, tt.trusted)
			if got := strings.Contains(output.String(), "SUPERMETRY_TRUSTED_PROXIES"); got != tt.warn {
				t.Fatalf("warning output = %q, want warning %t", output.String(), tt.warn)
			}
			if tt.warn && !strings.Contains(output.String(), "share one IP") {
				t.Fatalf("warning does not explain impact: %q", output.String())
			}
		})
	}
}

func TestNewHTTPServerAppliesConfiguredTimeouts(t *testing.T) {
	server := newHTTPServer(http.NotFoundHandler(), config.Config{
		HTTPReadTimeout:  17 * time.Second,
		HTTPWriteTimeout: 71 * time.Second,
	})
	if server.ReadTimeout != 17*time.Second || server.WriteTimeout != 71*time.Second {
		t.Fatalf("timeouts = (%s, %s), want (17s, 1m11s)", server.ReadTimeout, server.WriteTimeout)
	}
	if server.ReadHeaderTimeout != 5*time.Second || server.IdleTimeout != time.Minute {
		t.Fatalf("existing timeouts changed: %+v", server)
	}
}

func TestPostgresReady(t *testing.T) {
	pool, _ := testpg.New(t)
	if !postgresReady(pool) {
		t.Fatal("open PostgreSQL pool is not ready")
	}
	pool.Close()
	if postgresReady(pool) {
		t.Fatal("closed PostgreSQL pool is ready")
	}
}

func TestCSPInlineScriptHashMatchesSPA(t *testing.T) {
	source, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	const open, close = "<script>", "</script>"
	start := strings.Index(string(source), open)
	end := strings.Index(string(source), close)
	if start < 0 || end <= start {
		t.Fatal("web/index.html has no inline initializer")
	}
	body := source[start+len(open) : end]
	digest := sha256.Sum256(body)
	want := "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"

	response := httptest.NewRecorder()
	appSecurityHeaders(config.Config{}, http.NotFoundHandler()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app", nil))
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, want) {
		t.Fatalf("CSP %q does not permit the SPA initializer %s", csp, want)
	}
}

func TestAppSecurityHeaders(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*http.Request)
		trusted   []netip.Prefix
		wantHSTS  bool
	}{
		{name: "direct HTTPS enables HSTS", configure: func(r *http.Request) { r.TLS = &tls.ConnectionState{} }, wantHSTS: true},
		{name: "plain HTTP omits HSTS"},
		{name: "trusted forwarded HTTPS enables HSTS", configure: func(r *http.Request) {
			r.RemoteAddr = "192.0.2.10:1234"
			r.Header.Set("X-Forwarded-Proto", "https")
		}, trusted: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, wantHSTS: true},
		{name: "untrusted forwarded HTTPS omits HSTS", configure: func(r *http.Request) {
			r.RemoteAddr = "198.51.100.10:1234"
			r.Header.Set("X-Forwarded-Proto", "https")
		}, trusted: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := appSecurityHeaders(config.Config{TrustedProxies: tt.trusted}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/app", nil)
			if tt.configure != nil {
				tt.configure(request)
			}
			handler.ServeHTTP(response, request)

			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}
			if got := response.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
				t.Errorf("Referrer-Policy = %q", got)
			}
			if got := response.Header().Get("Permissions-Policy"); got == "" {
				t.Error("Permissions-Policy is absent")
			}
			csp := response.Header().Get("Content-Security-Policy")
			for _, directive := range []string{"default-src 'self'", "script-src 'self' 'sha256-SuOncodWHu+gLg1OlZCyogivDxh1qcH4Utmkganh+Pg='", "style-src 'self' 'unsafe-inline'", "frame-ancestors 'none'", "object-src 'none'"} {
				if !strings.Contains(csp, directive) {
					t.Errorf("CSP %q missing %q", csp, directive)
				}
			}
			if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
				t.Errorf("CSP permits arbitrary inline scripts: %q", csp)
			}
			if got := response.Header().Get("Strict-Transport-Security"); (got != "") != tt.wantHSTS {
				t.Errorf("Strict-Transport-Security = %q, want present %t", got, tt.wantHSTS)
			}
		})
	}
}

func TestAppSecurityHeadersScopesReplayFontSourcesToViewerHTML(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantFont string
	}{
		{name: "replay viewer", path: "/app/acme/example/replays/123", wantFont: "font-src 'self' data: blob: https:"},
		{name: "replay list overlay", path: "/app/acme/example/replays/", wantFont: "font-src 'self' data: blob: https:"},
		{name: "sessions overlay", path: "/app/acme/example/sessions", wantFont: "font-src 'self' data: blob: https:"},
		{name: "events overlay", path: "/app/acme/example/events", wantFont: "font-src 'self' data: blob: https:"},
		{name: "profile detail overlay", path: "/app/acme/example/profiles/123", wantFont: "font-src 'self' data: blob: https:"},
		{name: "dashboard HTML", path: "/app/acme/example", wantFont: "font-src 'self'"},
		{name: "profiles index", path: "/app/acme/example/profiles", wantFont: "font-src 'self'"},
		{name: "replay settings", path: "/app/acme/example/settings/replay", wantFont: "font-src 'self'"},
		{name: "similarly named non-viewer", path: "/app/acme/example/replays-archive/123", wantFont: "font-src 'self'"},
		{name: "replay API", path: "/api/private/sites/42/replays/123/data", wantFont: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			appSecurityHeaders(config.Config{}, http.NotFoundHandler()).ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, tt.path, nil),
			)

			var gotFont string
			for directive := range strings.SplitSeq(response.Header().Get("Content-Security-Policy"), ";") {
				directive = strings.TrimSpace(directive)
				if strings.HasPrefix(directive, "font-src ") {
					gotFont = directive
					break
				}
			}
			if gotFont != tt.wantFont {
				t.Errorf("font CSP for %s = %q, want %q", tt.path, gotFont, tt.wantFont)
			}
		})
	}
}

func TestSecurityHeadersByRouteClass(t *testing.T) {
	const (
		appPermissions    = "accelerometer=(), autoplay=(), browsing-topics=(), camera=(), clipboard-write=(self), display-capture=(), encrypted-media=(), fullscreen=(self), geolocation=(), gyroscope=(), interest-cohort=(), magnetometer=(), microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), screen-wake-lock=(), serial=(), usb=(), web-share=(), xr-spatial-tracking=()"
		lockedPermissions = "accelerometer=(), autoplay=(), browsing-topics=(), camera=(), clipboard-write=(), display-capture=(), encrypted-media=(), fullscreen=(), geolocation=(), gyroscope=(), interest-cohort=(), magnetometer=(), microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), screen-wake-lock=(), serial=(), usb=(), web-share=(), xr-spatial-tracking=()"
		apiCSP            = "default-src 'none'; frame-ancestors 'none'"
		shareCSP          = "default-src 'self'; script-src 'self' 'sha256-SuOncodWHu+gLg1OlZCyogivDxh1qcH4Utmkganh+Pg='; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self'; frame-src 'self' blob: https: http:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
	)
	appCSP := "default-src 'self'; script-src 'self' 'sha256-SuOncodWHu+gLg1OlZCyogivDxh1qcH4Utmkganh+Pg='; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self'; frame-src 'self' blob: https: http:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
	replayCSP := strings.Replace(appCSP, "font-src 'self'", "font-src 'self' data: blob: https:", 1)

	base := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"Permissions-Policy":     lockedPermissions,
	}
	with := func(values map[string]string) map[string]string {
		result := make(map[string]string, len(base)+len(values))
		for name, value := range base {
			result[name] = value
		}
		for name, value := range values {
			result[name] = value
		}
		return result
	}
	tests := []struct {
		name string
		path string
		want map[string]string
	}{
		{name: "SPA page", path: "/app/acme/site", want: with(map[string]string{"Content-Security-Policy": appCSP, "Referrer-Policy": "strict-origin-when-cross-origin", "Permissions-Policy": appPermissions, "X-Frame-Options": "DENY", "Cross-Origin-Resource-Policy": "same-origin", "Cross-Origin-Opener-Policy": "same-origin"})},
		{name: "replay viewer page", path: "/app/acme/site/replays/123", want: with(map[string]string{"Content-Security-Policy": replayCSP, "Referrer-Policy": "strict-origin-when-cross-origin", "Permissions-Policy": appPermissions, "X-Frame-Options": "DENY", "Cross-Origin-Resource-Policy": "same-origin", "Cross-Origin-Opener-Policy": "same-origin"})},
		{name: "authenticated API", path: "/api/private/me", want: with(map[string]string{"Content-Security-Policy": apiCSP, "X-Frame-Options": "DENY", "Cross-Origin-Resource-Policy": "same-origin"})},
		{name: "public API v1", path: "/api/public/v1/stats/summary", want: with(map[string]string{"Content-Security-Policy": apiCSP, "X-Frame-Options": "DENY", "Cross-Origin-Resource-Policy": "same-origin"})},
		{name: "tracker script", path: "/js/script.js", want: with(map[string]string{"Cross-Origin-Resource-Policy": "cross-origin"})},
		{name: "tracker event", path: "/api/public/v1/events", want: with(map[string]string{"Cross-Origin-Resource-Policy": "cross-origin"})},
		{name: "replay chunk", path: "/api/replay/chunk", want: with(map[string]string{"Cross-Origin-Resource-Policy": "cross-origin"})},
		{name: "replay CSS", path: "/api/replay/css/abc", want: with(map[string]string{"Cross-Origin-Resource-Policy": "cross-origin"})},
		{name: "health", path: "/healthz", want: with(map[string]string{"Content-Security-Policy": apiCSP, "X-Frame-Options": "DENY", "Cross-Origin-Resource-Policy": "same-origin"})},
		{name: "metrics", path: "/metrics", want: with(map[string]string{"Content-Security-Policy": apiCSP, "X-Frame-Options": "DENY", "Cross-Origin-Resource-Policy": "same-origin"})},
		{name: "share-link page", path: "/share/shr_example", want: with(map[string]string{"Content-Security-Policy": shareCSP, "Permissions-Policy": appPermissions, "X-Frame-Options": "DENY", "Cross-Origin-Resource-Policy": "same-origin"})},
	}
	managed := []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy", "X-Frame-Options", "Strict-Transport-Security", "Cross-Origin-Resource-Policy", "Cross-Origin-Opener-Policy"}
	cfg := config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js"}
	handler := appSecurityHeaders(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
			for _, name := range managed {
				values := response.Header().Values(name)
				if len(values) > 1 {
					t.Errorf("%s has duplicate values %q", name, values)
				}
				if got := response.Header().Get(name); got != tt.want[name] {
					t.Errorf("%s = %q, want %q", name, got, tt.want[name])
				}
			}
		})
	}
}

func TestTrackerCrossOriginContract(t *testing.T) {
	cfg := config.Config{EventPath: "/api/public/v1/events", TrackerPath: "/js/script.js"}
	router := api.NewRouter(api.Deps{
		Config: cfg,
		Ingest: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }),
	})
	handler := appSecurityHeaders(cfg, router)
	tests := []struct {
		name        string
		method      string
		path        string
		wantStatus  int
		wantMethods string
	}{
		{name: "tracker script GET", method: http.MethodGet, path: "/js/script.js", wantStatus: http.StatusOK},
		{name: "event preflight", method: http.MethodOptions, path: "/api/public/v1/events", wantStatus: http.StatusNoContent, wantMethods: "POST, OPTIONS"},
		{name: "event POST", method: http.MethodPost, path: "/api/public/v1/events", wantStatus: http.StatusAccepted, wantMethods: "POST, OPTIONS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{}`))
			request.Header.Set("Origin", "https://publisher.example")
			request.Header.Set("Content-Type", "text/plain")
			if tt.method == http.MethodOptions {
				request.Header.Set("Access-Control-Request-Method", http.MethodPost)
				request.Header.Set("Access-Control-Request-Headers", "content-type")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
			}
			if got := response.Header().Get("Cross-Origin-Resource-Policy"); got != "cross-origin" {
				t.Errorf("Cross-Origin-Resource-Policy = %q, want cross-origin", got)
			}
			if got := response.Header().Get("Access-Control-Allow-Methods"); got != tt.wantMethods {
				t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, tt.wantMethods)
			}
			if tt.method == http.MethodOptions && !slices.Contains(strings.Split(response.Header().Get("Access-Control-Allow-Headers"), ", "), "Content-Type") {
				t.Errorf("Access-Control-Allow-Headers = %q, want Content-Type", response.Header().Get("Access-Control-Allow-Headers"))
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/api/public/v1/query", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("authenticated API permits arbitrary cross-origin reads: Access-Control-Allow-Origin=%q", got)
	}
}
