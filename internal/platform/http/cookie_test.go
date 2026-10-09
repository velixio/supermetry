package platformhttp

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	platformauth "supermetry/internal/platform/auth"
)

// Cookies are Secure only over HTTPS, so plain-http LAN and dev logins work.
func TestCookiesSecureOnlyOverHTTPS(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*http.Request)
		secure bool
	}{
		{"plain http", func(*http.Request) {}, false},
		{"direct TLS", func(r *http.Request) { r.TLS = &tls.ConnectionState{} }, true},
		{"TLS-terminating proxy", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }, true},
	}
	for _, httpsBase := range []bool{false, true} {
		h := &handler{deps: Deps{SecureCookies: httpsBase}}
		for _, c := range cases {
			r := httptest.NewRequest(http.MethodPost, "/api/private/auth/login", nil)
			c.setup(r)
			w := httptest.NewRecorder()
			h.setCookie(w, r, "supermetry_session", "v", time.Hour)
			cookies := w.Result().Cookies()
			// An https base URL always forces Secure, even when the proxy omits X-Forwarded-Proto.
			want := c.secure || httpsBase
			if len(cookies) != 1 || cookies[0].Secure != want || !cookies[0].HttpOnly {
				t.Errorf("httpsBase=%v %s: cookie %+v, want Secure=%v HttpOnly=true", httpsBase, c.name, cookies, want)
			}
		}
	}
}

func TestSignupRateLimitUsesForwardedClientFromTrustedProxy(t *testing.T) {
	handler := New(Deps{
		Auth:           platformauth.New(nil, nil, nil, ""),
		Signup:         "open",
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
	})
	request := func(forwarded string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/private/auth/signup", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", forwarded)
		r.RemoteAddr = "192.0.2.10:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for range 20 {
		if response := request("203.0.113.10"); response.Code != http.StatusBadRequest {
			t.Fatalf("initial client status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if response := request("203.0.113.11"); response.Code != http.StatusBadRequest {
		t.Fatalf("second client status=%d body=%s", response.Code, response.Body.String())
	}
}
