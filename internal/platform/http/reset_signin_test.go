package platformhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

// A reset that asks to sign in returns session cookies; one that does not
// keeps answering 204 with no cookies, as before.
func TestPasswordResetConfirmSignsInOnlyWhenAsked(t *testing.T) {
	for _, tt := range []struct {
		name       string
		extra      string
		wantStatus int
		wantCookie bool
	}{
		{"default stays 204", "", http.StatusNoContent, false},
		{"sign_in false stays 204", `,"sign_in":false`, http.StatusNoContent, false},
		{"sign_in true signs in", `,"sign_in":true`, http.StatusOK, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool, _ := testpg.New(t)
			ctx := context.Background()
			if err := db.Migrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			data := &store.Store{Pool: pool}
			box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
			if err != nil {
				t.Fatal(err)
			}
			mailer := &mail.Fake{}
			authService := platformauth.New(data, mailer, box, "https://supermetry.example")
			if _, err := users.New(data).Create(ctx, "owner@example.test", "Owner", "old-password"); err != nil {
				t.Fatal(err)
			}
			if _, err := authService.StartReset(platformauth.WithClientKey(ctx, "signin"), "owner@example.test"); err != nil {
				t.Fatal(err)
			}
			_, link, _ := strings.Cut(mailer.Sent[0].Text, "\n\n")
			token := queryToken(t, strings.TrimSpace(link))
			handler := platformhttp.New(platformhttp.Deps{Auth: authService, Signup: "open"})

			body := `{"token":"` + token + `","password":"new-password-1"` + tt.extra + `}`
			request := httptest.NewRequest(http.MethodPost, "/api/private/auth/password-reset/confirm", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.RemoteAddr = "192.0.2.10:12345"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s, want %d", response.Code, response.Body.String(), tt.wantStatus)
			}
			cookies := map[string]bool{}
			for _, c := range response.Result().Cookies() {
				cookies[c.Name] = c.Value != ""
			}
			if got := cookies[platformauth.CookieName] && cookies["supermetry_csrf"]; got != tt.wantCookie {
				t.Fatalf("session cookies set = %v, want %v (%v)", got, tt.wantCookie, cookies)
			}
			if tt.wantCookie && !strings.Contains(response.Body.String(), `"authenticated":true`) {
				t.Fatalf("body = %s", response.Body.String())
			}
		})
	}
}
