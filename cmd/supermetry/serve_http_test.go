package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

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
	appSecurityHeaders("https://analytics.example", http.NotFoundHandler()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app", nil))
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, want) {
		t.Fatalf("CSP %q does not permit the SPA initializer %s", csp, want)
	}
}

func TestAppSecurityHeaders(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		wantHSTS bool
	}{
		{name: "HTTPS enables HSTS", baseURL: "https://analytics.example", wantHSTS: true},
		{name: "HTTP omits HSTS", baseURL: "http://analytics.example", wantHSTS: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := appSecurityHeaders(tt.baseURL, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app", nil))

			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}
			if got := response.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
				t.Errorf("Referrer-Policy = %q", got)
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
