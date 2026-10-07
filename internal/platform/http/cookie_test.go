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
	for _, httpsBase := range []bool{false, true} {
		h := &handler{deps: Deps{SecureCookies: httpsBase}}
		for _, c := range cases {
			r := httptest.NewRequest(http.MethodPost, "/api/private/auth/login", nil)
			c.setup(r)
			w := httptest.NewRecorder()
			h.setCookie(w, r, "panel_session", "v", time.Hour)
			cookies := w.Result().Cookies()
			// An https base URL always forces Secure, even when the proxy omits X-Forwarded-Proto.
			want := c.secure || httpsBase
			if len(cookies) != 1 || cookies[0].Secure != want || !cookies[0].HttpOnly {
				t.Errorf("httpsBase=%v %s: cookie %+v, want Secure=%v HttpOnly=true", httpsBase, c.name, cookies, want)
			}
		}
	}
}
