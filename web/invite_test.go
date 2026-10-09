package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssuedInvitationRedirect(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, location string
		status                       int
	}{
		{"issued link", "GET", "/invite?token=abc_DEF-123", "/invite/abc_DEF-123", http.StatusFound},
		{"head", "HEAD", "/invite?token=abc", "/invite/abc", http.StatusFound},
		{"escaped token", "GET", "/invite?token=a%2Fb%2Bc%3Fd", "/invite/a%2Fb+c%3Fd", http.StatusFound},
		{"untrusted destination", "GET", "/invite?token=abc&next=https://evil.example", "/invite/abc", http.StatusFound},
		{"canonical route", "GET", "/invite/abc", "", http.StatusOK},
		{"missing token", "GET", "/invite", "", http.StatusOK},
		{"empty token", "GET", "/invite?token=", "", http.StatusOK},
		{"post", "POST", "/invite?token=abc", "", http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			Handler().ServeHTTP(rr, httptest.NewRequest(tt.method, tt.path, nil))
			if rr.Code != tt.status || rr.Header().Get("Location") != tt.location {
				t.Fatalf("got status %d location %q; want %d %q", rr.Code, rr.Header().Get("Location"), tt.status, tt.location)
			}
			if tt.location != "" && rr.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("invitation redirects must not be cached")
			}
		})
	}
}
