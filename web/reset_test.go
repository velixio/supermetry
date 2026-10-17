package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssuedPasswordResetRedirect(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, location string
		status                       int
	}{
		{"issued link", "GET", "/password-reset?token=abc_DEF-123", "/reset/abc_DEF-123", http.StatusFound},
		{"head", "HEAD", "/password-reset?token=abc", "/reset/abc", http.StatusFound},
		{"escaped token", "GET", "/password-reset?token=a%2Fb%2Bc%3Fd", "/reset/a%2Fb+c%3Fd", http.StatusFound},
		{"untrusted destination", "GET", "/password-reset?token=abc&next=https://evil.example", "/reset/abc", http.StatusFound},
		{"canonical route", "GET", "/reset/abc", "", http.StatusOK},
		{"empty token", "GET", "/password-reset?token=", "", http.StatusOK},
		{"post", "POST", "/password-reset?token=abc", "", http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			Handler().ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, nil))
			if rr.Code != tt.status || rr.Header().Get("Location") != tt.location {
				t.Fatalf("got status %d location %q; want %d %q", rr.Code, rr.Header().Get("Location"), tt.status, tt.location)
			}
			if tt.location != "" && (rr.Header().Get("Cache-Control") != "no-store" || rr.Header().Get("Referrer-Policy") != "no-referrer") {
				t.Fatal("password reset redirects must not be cached or leak the token in Referer")
			}
		})
	}
}
