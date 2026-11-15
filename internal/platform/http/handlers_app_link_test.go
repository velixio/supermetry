package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func newAppLinkFixture(t *testing.T) (linkFixture, string) {
	t.Helper()
	f := newLinkFixture(t)
	ctx := context.Background()
	// otherSite (same org) and foreignSite (another org) become apps; siteID stays a web site.
	if _, err := f.pool.Exec(ctx, `update sites set kind='app' where id in ($1,$2)`, f.otherSite, f.foreignSite); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	return f, fmt.Sprintf("/api/private/sites/%d/app-link", f.siteID)
}

func TestAppLinkRoundTrip(t *testing.T) {
	f, path := newAppLinkFixture(t)
	if res := f.request(t, "owner", http.MethodGet, path, ``); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != `{"app_site_id":null}` {
		t.Fatalf("initial get = %d %s", res.Code, res.Body)
	}
	put := f.request(t, "owner", http.MethodPut, path, fmt.Sprintf(`{"app_site_id":%d}`, f.otherSite))
	want := fmt.Sprintf(`{"app_site_id":%d}`, f.otherSite)
	if put.Code != http.StatusOK || strings.TrimSpace(put.Body.String()) != want {
		t.Fatalf("put = %d %s", put.Code, put.Body)
	}
	if get := f.request(t, "owner", http.MethodGet, path, ``); strings.TrimSpace(get.Body.String()) != want {
		t.Fatalf("get = %s", get.Body)
	}
	site, ok := f.registry.Site(f.siteID)
	if !ok || site.LinkedAppID != f.otherSite {
		t.Fatalf("registry LinkedAppID = %d", site.LinkedAppID)
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='site.app_link_changed'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	clear := f.request(t, "owner", http.MethodPut, path, `{"app_site_id":null}`)
	if clear.Code != http.StatusOK || strings.TrimSpace(clear.Body.String()) != `{"app_site_id":null}` {
		t.Fatalf("clear = %d %s", clear.Code, clear.Body)
	}
	if site, _ := f.registry.Site(f.siteID); site.LinkedAppID != 0 {
		t.Fatalf("link not cleared: %d", site.LinkedAppID)
	}
}

func TestAppLinkRejectsBadTargets(t *testing.T) {
	f, path := newAppLinkFixture(t)
	cases := map[string]string{
		"app in another org":  fmt.Sprintf(`{"app_site_id":%d}`, f.foreignSite),
		"the web site itself": fmt.Sprintf(`{"app_site_id":%d}`, f.siteID),
		"unknown site":        `{"app_site_id":99999}`,
		"missing field":       `{}`,
		"wrong type":          `{"app_site_id":"7"}`,
		"unknown field":       fmt.Sprintf(`{"app_site_id":%d,"x":1}`, f.otherSite),
	}
	for name, body := range cases {
		if res := f.request(t, "owner", http.MethodPut, path, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d %s", name, res.Code, res.Body)
		}
	}
	var linked int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from sites where settings ? 'app_link'`).Scan(&linked); err != nil || linked != 0 {
		t.Fatalf("a rejected link was stored: %v %d", err, linked)
	}
}

func TestAppLinkGuards(t *testing.T) {
	f, path := newAppLinkFixture(t)
	body := fmt.Sprintf(`{"app_site_id":%d}`, f.otherSite)
	for _, actor := range []string{"viewer", "editor"} {
		if res := f.request(t, actor, http.MethodPut, path, body); res.Code != http.StatusForbidden {
			t.Errorf("%s put = %d", actor, res.Code)
		}
		if res := f.request(t, actor, http.MethodGet, path, ``); res.Code != http.StatusForbidden {
			t.Errorf("%s get = %d", actor, res.Code)
		}
	}
	// Only web sites link out: an app project has no link of its own.
	appPath := fmt.Sprintf("/api/private/sites/%d/app-link", f.otherSite)
	if res := f.request(t, "owner", http.MethodPut, appPath, `{"app_site_id":null}`); res.Code != http.StatusNotFound {
		t.Errorf("app project put = %d", res.Code)
	}
}
