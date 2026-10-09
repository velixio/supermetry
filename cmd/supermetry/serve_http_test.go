package main

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"supermetry/internal/config"
)

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
