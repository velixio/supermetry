package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"supermetry/internal/appmonitors"
)

func (f issuesFixture) monitorsPath() string {
	return fmt.Sprintf("/api/private/sites/%d/app/monitors", f.siteID)
}

// seedMonitor records one ok check-in, which creates the monitor.
func (f issuesFixture) seedMonitor(t *testing.T) int64 {
	t.Helper()
	store := appmonitors.NewStore(f.pool, nil)
	err := store.Record(context.Background(), appmonitors.CheckIn{
		SiteID: f.siteID, Slug: "nightly", Environment: "production", ID: "11111111-1111-4111-8111-111111111111",
		Status: appmonitors.StatusOK, At: time.Now().UTC(),
		Config: &appmonitors.Config{ScheduleType: "interval", Schedule: "1 hour", MarginMinutes: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := f.pool.QueryRow(context.Background(), `select id from app_monitors where site_id=$1`, f.siteID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAppMonitorsListPatchDelete(t *testing.T) {
	f := newAppIssuesFixture(t)
	id := f.seedMonitor(t)
	got := f.json(t, "viewer", http.MethodGet, f.monitorsPath(), ``, http.StatusOK)
	list, _ := got["monitors"].([]any)
	if len(list) != 1 {
		t.Fatalf("monitors = %v", got)
	}
	m := list[0].(map[string]any)
	if m["slug"] != "nightly" || m["status"] != "ok" || m["schedule"] != "1 hour" || m["next_expected_at"] == nil {
		t.Fatalf("monitor = %v", m)
	}
	checkins := f.json(t, "viewer", http.MethodGet, fmt.Sprintf("%s/%d/checkins?limit=5", f.monitorsPath(), id), ``, http.StatusOK)
	if rows, _ := checkins["checkins"].([]any); len(rows) != 1 {
		t.Fatalf("checkins = %v", checkins)
	}
	one := fmt.Sprintf("%s/%d", f.monitorsPath(), id)
	patched := f.json(t, "editor", http.MethodPatch, one, `{"disabled":true,"checkin_margin_minutes":10}`, http.StatusOK)
	if patched["status"] != "disabled" || patched["checkin_margin_minutes"] != float64(10) {
		t.Fatalf("patched = %v", patched)
	}
	if res := f.request(t, "editor", http.MethodPatch, one, `{"checkin_margin_minutes":0}`); res.Code != http.StatusBadRequest {
		t.Errorf("margin 0 = %d, want 400", res.Code)
	}
	if res := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("%s/%d/checkins?limit=500", f.monitorsPath(), id), ``); res.Code != http.StatusBadRequest {
		t.Errorf("limit 500 = %d, want 400", res.Code)
	}
	if res := f.request(t, "editor", http.MethodDelete, one, ``); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", res.Code)
	}
	if res := f.request(t, "editor", http.MethodDelete, one, ``); res.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", res.Code)
	}
}

func TestAppMonitorsGuardRoleAndTenant(t *testing.T) {
	f := newAppIssuesFixture(t)
	id := f.seedMonitor(t)
	one := fmt.Sprintf("%s/%d", f.monitorsPath(), id)
	for name, c := range map[string]struct {
		actor, method, path, body string
		want                      int
	}{
		"viewer patch":   {"viewer", http.MethodPatch, one, `{"disabled":true}`, http.StatusForbidden},
		"viewer delete":  {"viewer", http.MethodDelete, one, ``, http.StatusForbidden},
		"foreign list":   {"foreign-owner", http.MethodGet, f.monitorsPath(), ``, http.StatusNotFound},
		"foreign checks": {"foreign-owner", http.MethodGet, one + "/checkins", ``, http.StatusNotFound},
		"foreign patch":  {"foreign-owner", http.MethodPatch, one, `{"disabled":true}`, http.StatusNotFound},
		"foreign delete": {"foreign-owner", http.MethodDelete, one, ``, http.StatusNotFound},
	} {
		if res := f.request(t, c.actor, c.method, c.path, c.body); res.Code != c.want {
			t.Errorf("%s = %d, want %d", name, res.Code, c.want)
		}
	}
	if res := f.request(t, "editor", http.MethodGet, f.monitorsPath()+"/99999/checkins", ``); res.Code != http.StatusNotFound {
		t.Errorf("unknown monitor checkins = %d, want 404", res.Code)
	}
}
