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
		name    string
		setup   func(*http.Request)
		trusted []netip.Prefix
		secure  bool
	}{
		{name: "plain http", setup: func(*http.Request) {}},
		{name: "direct TLS", setup: func(r *http.Request) { r.TLS = &tls.ConnectionState{} }, secure: true},
		{name: "trusted TLS-terminating proxy", setup: func(r *http.Request) {
			r.RemoteAddr = "192.0.2.10:1234"
			r.Header.Set("X-Forwarded-Proto", "https")
		}, trusted: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, secure: true},
		{name: "untrusted spoofed forwarded proto", setup: func(r *http.Request) {
			r.RemoteAddr = "198.51.100.10:1234"
			r.Header.Set("X-Forwarded-Proto", "https")
		}, trusted: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}},
	}
	for _, httpsBase := range []bool{false, true} {
		for _, c := range cases {
			h := &handler{deps: Deps{SecureCookies: httpsBase, TrustedProxies: c.trusted}}
			r := httptest.NewRequest(http.MethodPost, "/api/private/auth/login", nil)
			c.setup(r)
			w := httptest.NewRecorder()
			h.setCookie(w, r, "supermetry_session", "v", time.Hour)
			cookies := w.Result().Cookies()
			// An https base URL always forces Secure, even when the proxy omits X-Forwarded-Proto.
			want := c.secure || httpsBase
			if len(cookies) != 1 || cookies[0].Secure != want || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].MaxAge != 3600 || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Errorf("httpsBase=%v %s: cookie %+v, want Secure=%v HttpOnly=true Path=/ MaxAge=3600 SameSite=Lax", httpsBase, c.name, cookies, want)
			}
		}
	}
}

func TestAuthCookiesHaveMatchedLifetimeAndAttributes(t *testing.T) {
	h := &handler{}
	request := httptest.NewRequest(http.MethodPost, "/api/private/auth/login", nil)
	response := httptest.NewRecorder()
	h.setAuthCookies(response, request, "session", "csrf")

	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d, want 2", len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.Path != "/" || cookie.MaxAge != 90*24*60*60 || cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie %s attributes = %+v", cookie.Name, cookie)
		}
		wantHTTPOnly := cookie.Name == platformauth.CookieName
		if cookie.HttpOnly != wantHTTPOnly {
			t.Errorf("cookie %s HttpOnly = %t, want %t", cookie.Name, cookie.HttpOnly, wantHTTPOnly)
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
