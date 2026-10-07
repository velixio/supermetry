package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"panel/internal/sites"
)

type fakeRegistry map[int32]sites.Site

func (f fakeRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (f fakeRegistry) Site(id int32) (sites.Site, bool)     { s, ok := f[id]; return s, ok }
func (f fakeRegistry) All() []sites.Site                    { return nil }

func authorize(p Principal, reg sites.Registry, siteID int32) int {
	r := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/summary", nil)
	ctx := context.WithValue(r.Context(), principalContextKey{}, p)
	if reg != nil {
		ctx = context.WithValue(ctx, registryContextKey{}, reg)
	}
	w := httptest.NewRecorder()
	if authorizeRequest(w, r.WithContext(ctx), siteID, "stats:read") {
		return http.StatusOK
	}
	return w.Code
}

func TestSiteAccessIsScopedToTheCallersOrg(t *testing.T) {
	reg := fakeRegistry{1: {ID: 1, OrgID: 10}, 2: {ID: 2, OrgID: 10}, 3: {ID: 3, OrgID: 20}}
	org10 := Principal{OrgID: 10, Scopes: []string{"stats:read"}} // nil SiteIDs = all sites of org 10
	viewer := Principal{OrgID: 10, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}
	admin := Principal{AllSites: true, Scopes: []string{"*"}}
	noOrg := Principal{Scopes: []string{"stats:read"}} // a bug elsewhere must not mean "everything"

	cases := []struct {
		name string
		p    Principal
		reg  sites.Registry
		site int32
		want int
	}{
		{"own org, any site", org10, reg, 2, http.StatusOK},
		{"other org's site is hidden", org10, reg, 3, http.StatusNotFound},
		{"unknown site", org10, reg, 99, http.StatusNotFound},
		{"site-restricted viewer, allowed site", viewer, reg, 1, http.StatusOK},
		{"site-restricted viewer, other site in same org", viewer, reg, 2, http.StatusNotFound},
		{"static admin key", admin, reg, 3, http.StatusOK},
		{"principal without org", noOrg, reg, 1, http.StatusNotFound},
		{"no registry fails closed", org10, nil, 1, http.StatusNotFound},
		{"no registry, admin still allowed", admin, nil, 1, http.StatusOK},
	}
	for _, c := range cases {
		if got := authorize(c.p, c.reg, c.site); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestCSRFRequiresMatchingCookieAndHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := requireCSRF(next)
	cases := []struct {
		name, cookie, header string
		want                 int
	}{
		{"matching", "tok-123", "tok-123", http.StatusNoContent},
		{"header only", "", "tok-123", http.StatusForbidden},
		{"cookie only", "tok-123", "", http.StatusForbidden},
		{"mismatch", "tok-123", "tok-999", http.StatusForbidden},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/api/private/query", nil)
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "panel_csrf", Value: c.cookie})
		}
		if c.header != "" {
			r.Header.Set("X-CSRF-Token", c.header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, w.Code, c.want)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/private/me", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("GET must not need a CSRF token: %d", w.Code)
	}
}
