package invites_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/mail"
	"supermetry/web"
)

// Exercise the issued URL through the real UI handler before accepting its token.
func TestIssuedLinkRedirectPreservesAcceptanceToken(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	link, err := env.service.Create(ctx, env.owner, "link@example.test", authz.RoleViewer, nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	web.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, link, nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("issued link status = %d", rr.Code)
	}
	location, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.IsAbs() || !strings.HasPrefix(location.Path, "/invite/") || location.RawQuery != "" {
		t.Fatalf("unexpected invitation route: %s", location)
	}
	token := strings.TrimPrefix(location.Path, "/invite/")
	if token != tokenFromLink(t, link) {
		t.Fatal("redirect changed invitation token")
	}
	page := httptest.NewRecorder()
	web.Handler().ServeHTTP(page, httptest.NewRequest(http.MethodGet, location.String(), nil))
	if page.Code != http.StatusOK {
		t.Fatalf("canonical page status = %d", page.Code)
	}
	if _, err := env.service.Accept(ctx, token, 0, "Link User", "password-1234"); err != nil {
		t.Fatalf("accept redirected token: %v", err)
	}
}
