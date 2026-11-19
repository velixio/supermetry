package main

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"supermetry/internal/config"
)

func TestApplyCSPExtra(t *testing.T) {
	base := "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'"
	got := applyCSPExtra(base, "img-src https://cdn.example; connect-src https://api.example; script-src https://evil.example; frame-ancestors *")
	want := "default-src 'self'; img-src 'self' data: https://cdn.example; frame-ancestors 'none'; connect-src https://api.example"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if applyCSPExtra(base, "") != base || applyCSPExtra("", "img-src x") != "" {
		t.Fatal("empty inputs must be a no-op")
	}
}

func TestCSPModes(t *testing.T) {
	for mode, hdr := range map[string]string{"enforce": "Content-Security-Policy", "": "Content-Security-Policy", "report-only": "Content-Security-Policy-Report-Only"} {
		h := appSecurityHeaders(config.Config{CSPMode: mode}, http.NotFoundHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Header().Get(hdr) == "" {
			t.Errorf("mode %q: %s missing", mode, hdr)
		}
		if mode == "report-only" && rec.Header().Get("Content-Security-Policy") != "" {
			t.Error("report-only must not enforce")
		}
	}
	rec := httptest.NewRecorder()
	appSecurityHeaders(config.Config{CSPMode: "off"}, http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Content-Security-Policy") != "" || rec.Header().Get("X-Content-Type-Options") == "" {
		t.Error("off drops CSP only")
	}
}

// Every inline script in the SPA shell must be allowed by hash, and nothing
// else: no inline script may slip in without the policy being updated.
func TestAppCSPAllowsExactlyTheInlineScriptsInIndexHTML(t *testing.T) {
	for _, file := range []string{"../../web/index.html", "../../web/dist/index.html"} {
		page, err := os.ReadFile(file)
		if err != nil {
			if strings.Contains(file, "dist") {
				continue // not built in this checkout
			}
			t.Fatal(err)
		}
		inline := regexp.MustCompile(`(?s)<script(?:\s[^>]*)?>(.*?)</script>`).FindAllStringSubmatch(string(page), -1)
		hashes := 0
		for _, m := range inline {
			if strings.TrimSpace(m[0][:strings.Index(m[0], ">")]) != "<script" && strings.Contains(m[0][:strings.Index(m[0], ">")], "src=") {
				continue // external script, covered by 'self'
			}
			sum := sha256.Sum256([]byte(m[1]))
			want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
			if !strings.Contains(appContentSecurityPolicy, want) {
				t.Errorf("%s: inline script hash %s missing from appContentSecurityPolicy", file, want)
			}
			hashes++
		}
		if hashes != 1 {
			t.Errorf("%s: %d inline scripts, policy allows exactly 1", file, hashes)
		}
		if strings.Contains(string(page), "style=\"") || strings.Contains(appContentSecurityPolicy, "unsafe-eval") {
			t.Errorf("%s: unexpected inline style or unsafe-eval", file)
		}
	}
}
