package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAppSettingsRoundTrip(t *testing.T) {
	f := newAppKeyFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/app-settings", f.siteID)
	res := f.request(t, "owner", http.MethodGet, path, ``)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"max_report_age_days":7`) || !strings.Contains(res.Body.String(), `"tag_allowlist":[]`) {
		t.Fatalf("defaults = %d %s", res.Code, res.Body)
	}
	put := f.request(t, "owner", http.MethodPut, path, `{"tag_allowlist":["Source","source"],"max_report_age_days":14,"environments":["Beta","beta"]}`)
	want := `{"tag_allowlist":["source"],"max_report_age_days":14,"environments":["beta"]}`
	if put.Code != http.StatusOK || strings.TrimSpace(put.Body.String()) != want {
		t.Fatalf("put = %d %s", put.Code, put.Body)
	}
	if get := f.request(t, "owner", http.MethodGet, path, ``); strings.TrimSpace(get.Body.String()) != want {
		t.Fatalf("get = %s", get.Body)
	}
	site, ok := f.registry.Site(f.siteID)
	if !ok || site.App == nil || site.App.MaxReportAgeDays != 14 || len(site.App.Environments) != 1 {
		t.Fatalf("registry app = %+v", site.App)
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='site.app_settings_changed'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
}

func TestAppSettingsGuards(t *testing.T) {
	f := newAppKeyFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/app-settings", f.siteID)
	if res := f.request(t, "viewer", http.MethodPut, path, `{"max_report_age_days":7}`); res.Code != http.StatusForbidden {
		t.Fatalf("viewer put = %d", res.Code)
	}
	if res := f.request(t, "viewer", http.MethodGet, path, ``); res.Code != http.StatusForbidden {
		t.Fatalf("viewer get = %d", res.Code)
	}
	for _, body := range []string{`{"max_report_age_days":31}`, `{"tag_allowlist":["9x"]}`, `{"bogus":1}`} {
		if res := f.request(t, "owner", http.MethodPut, path, body); res.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", body, res.Code, res.Body)
		}
	}
	if res := f.request(t, "owner", http.MethodPut, path, `{"max_report_age_days":31}`); !strings.Contains(res.Body.String(), "max_report_age_days") {
		t.Fatalf("error message = %s", res.Body)
	}
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='web' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		if res := f.request(t, "owner", m, path, `{}`); res.Code != http.StatusNotFound {
			t.Fatalf("web %s = %d", m, res.Code)
		}
	}
}
