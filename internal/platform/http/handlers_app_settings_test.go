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
	want := `{"tag_allowlist":["source"],"max_report_age_days":14,"environments":["beta"],"filter_extensions":true,"filter_crawlers":true,"filter_localhost":null,"performance_enabled":true,"span_retention_days":14,"log_retention_days":7,"electron_symbols":true,"log_daily_quota":5000000,"metric_daily_quota":50000000,"filter_localhost_default":false}`
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

func TestAppSettingsFilterToggles(t *testing.T) {
	f := newAppKeyFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/app-settings", f.siteID)
	put := f.request(t, "owner", http.MethodPut, path, `{"filter_extensions":false,"filter_crawlers":false,"filter_localhost":true}`)
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"filter_extensions":false,"filter_crawlers":false,"filter_localhost":true`) {
		t.Fatalf("put = %d %s", put.Code, put.Body)
	}
	site, _ := f.registry.Site(f.siteID)
	if e, c, l := site.App.Filters(false); e || c || !l {
		t.Fatalf("registry filters = %v %v %v", e, c, l)
	}
	// Null returns a switch to its default.
	put = f.request(t, "owner", http.MethodPut, path, `{"filter_extensions":null,"filter_crawlers":null,"filter_localhost":null}`)
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"filter_extensions":true,"filter_crawlers":true,"filter_localhost":null`) {
		t.Fatalf("reset = %d %s", put.Code, put.Body)
	}
	site, _ = f.registry.Site(f.siteID)
	if e, c, l := site.App.Filters(false); !e || !c || l {
		t.Fatalf("registry defaults = %v %v %v", e, c, l)
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
