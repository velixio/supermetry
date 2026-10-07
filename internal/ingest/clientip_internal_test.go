package ingest

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPTrustsMappedIPv4Proxy(t *testing.T) {
	h := &Handler{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.168.0.1/32")}}
	cases := []struct{ remote, xff, want string }{
		{"[::ffff:192.168.0.1]:51234", "8.8.8.8", "8.8.8.8"},       // dual-stack listener, trusted proxy
		{"192.168.0.1:51234", "49.36.0.1", "49.36.0.1"},            // plain IPv4, trusted proxy
		{"[::ffff:203.0.113.9]:51234", "8.8.8.8", "203.0.113.9"},   // untrusted peer: header ignored
		{"[::ffff:192.168.0.1]:51234", "not-an-ip", "192.168.0.1"}, // bad header: fall back to peer
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/api/public/v1/events", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		got, ok := h.clientIP(r)
		if !ok || got.String() != c.want {
			t.Errorf("remote=%s xff=%s: got %v (ok=%v), want %s", c.remote, c.xff, got, ok, c.want)
		}
	}
}
