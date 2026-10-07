package platformhttp

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/api/private/auth/login", nil)
		c.setup(r)
		w := httptest.NewRecorder()
		setCookie(w, r, "panel_session", "v", time.Hour)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Secure != c.secure || !cookies[0].HttpOnly {
			t.Errorf("%s: cookie %+v, want Secure=%v HttpOnly=true", c.name, cookies, c.secure)
		}
	}
}
