package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"supermetry/internal/api"
	"supermetry/internal/config"
)

func TestMetricsAccessPolicy(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		remoteAddr string
		header     string
		forwarded  string
		auth       string
		trusted    []netip.Prefix
		want       int
	}{
		{name: "configured token accepted from remote peer", token: "metrics-secret", remoteAddr: "198.51.100.8:1234", auth: "Bearer metrics-secret", want: http.StatusOK},
		{name: "configured token accepts forwarding header", token: "metrics-secret", remoteAddr: "127.0.0.1:1234", header: "X-Forwarded-For", forwarded: "198.51.100.9", auth: "Bearer metrics-secret", want: http.StatusOK},
		{name: "configured token required", token: "metrics-secret", remoteAddr: "127.0.0.1:1234", want: http.StatusUnauthorized},
		{name: "configured token rejects wrong value", token: "metrics-secret", remoteAddr: "127.0.0.1:1234", auth: "Bearer wrong", want: http.StatusUnauthorized},
		{name: "direct loopback accepted without token", remoteAddr: "127.0.0.1:1234", want: http.StatusOK},
		{name: "IPv6 loopback accepted without token", remoteAddr: "[::1]:1234", want: http.StatusOK},
		{name: "remote peer rejected without token", remoteAddr: "198.51.100.8:1234", want: http.StatusUnauthorized},
		{name: "loopback proxy rejected without token", remoteAddr: "127.0.0.1:1234", header: "X-Forwarded-For", forwarded: "198.51.100.9", want: http.StatusUnauthorized},
		{name: "empty forwarding header rejected without token", remoteAddr: "127.0.0.1:1234", header: "X-Forwarded-For", want: http.StatusUnauthorized},
		{name: "forwarded header rejected without token", remoteAddr: "127.0.0.1:1234", header: "Forwarded", forwarded: "for=198.51.100.9", want: http.StatusUnauthorized},
		{name: "real IP header rejected without token", remoteAddr: "127.0.0.1:1234", header: "X-Real-IP", forwarded: "198.51.100.9", want: http.StatusUnauthorized},
		{name: "Cloudflare header rejected without token", remoteAddr: "127.0.0.1:1234", header: "CF-Connecting-IP", forwarded: "198.51.100.9", want: http.StatusUnauthorized},
		{name: "true client header rejected without token", remoteAddr: "127.0.0.1:1234", header: "True-Client-IP", forwarded: "198.51.100.9", want: http.StatusUnauthorized},
		{
			name:       "trusted proxy loopback client accepted",
			remoteAddr: "192.0.2.8:1234",
			forwarded:  "127.0.0.1",
			trusted:    []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
			want:       http.StatusUnauthorized,
		},
		{
			name:       "trusted proxy remote client concealed",
			remoteAddr: "192.0.2.8:1234",
			forwarded:  "198.51.100.9",
			trusted:    []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
			want:       http.StatusUnauthorized,
		},
		{
			name:       "private client behind trusted proxy cannot spoof loopback",
			remoteAddr: "192.0.2.8:1234",
			forwarded:  "127.0.0.1, 10.1.2.3",
			trusted:    []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
			want:       http.StatusUnauthorized,
		},
		{
			name:       "private client behind trusted loopback proxy concealed",
			remoteAddr: "127.0.0.1:1234",
			forwarded:  "192.168.1.9",
			trusted:    []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			want:       http.StatusUnauthorized,
		},
		{
			name:       "malformed forwarded chain concealed",
			remoteAddr: "127.0.0.1:1234",
			forwarded:  "not-an-ip",
			trusted:    []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			want:       http.StatusUnauthorized,
		},
		{
			name:       "direct request from trusted loopback proxy host accepted",
			remoteAddr: "127.0.0.1:1234",
			trusted:    []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			want:       http.StatusOK,
		},
		{name: "token without bearer scheme rejected", token: "metrics-secret", remoteAddr: "127.0.0.1:1234", auth: "metrics-secret", want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := identifiedRouter(api.Deps{Config: config.Config{
				MetricsToken:   tt.token,
				TrustedProxies: tt.trusted,
			}})
			request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			request.RemoteAddr = tt.remoteAddr
			if tt.header != "" || tt.forwarded != "" {
				header := tt.header
				if header == "" {
					header = "X-Forwarded-For"
				}
				request.Header.Set(header, tt.forwarded)
			}
			request.Header.Set("Authorization", tt.auth)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}
