package platformhttp

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestAuthEndpointsRequireSameOrigin(t *testing.T) {
	tests := []struct {
		name    string
		origin  string
		referer string
		want    int
	}{
		{name: "same origin", origin: "https://supermetry.example", want: http.StatusNoContent},
		{name: "same origin referer", referer: "https://supermetry.example/login", want: http.StatusNoContent},
		{name: "missing both", want: http.StatusForbidden},
		{name: "cross origin", origin: "https://attacker.example", want: http.StatusForbidden},
		{name: "origin takes precedence", origin: "https://attacker.example", referer: "https://supermetry.example/login", want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			handler := csrf(next, "", "https://supermetry.example")
			req := httptest.NewRequest(http.MethodPost, "/api/private/auth/login", nil)
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("Referer", tt.referer)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}

func TestLimiterClientAddressUsesNearestUntrustedHop(t *testing.T) {
	h := handler{deps: Deps{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}}
	req := httptest.NewRequest(http.MethodPost, "/api/private/auth/login", nil)
	req.RemoteAddr = "192.0.2.1:443"
	req.Header.Set("X-Forwarded-For", "8.8.8.8, 10.0.0.7")
	if got := h.clientAddress(req); got != "10.0.0.7" {
		t.Fatalf("clientAddress() = %q, want nearest untrusted hop", got)
	}
}
