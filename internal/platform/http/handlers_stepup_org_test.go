package platformhttp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func isReauthRequired(code int, body string) bool {
	return code == http.StatusForbidden && strings.Contains(body, `"code":"reauth_required"`)
}

func TestStepUpGuardsExportSchedulesAndSettingsBundle(t *testing.T) {
	stale := false
	h, org1, _ := newExportEnvStepUp(t, toggleSession{stale: &stale})
	base := "/api/private/orgs/" + jsonNumber(org1)
	body := `{"name":"n","frequency":"daily","endpoint":"https://s3.example.com","region":"us-east-1","bucket":"b","access_key_id":"k","secret":"s"}`
	created := starsRequest(h, http.MethodPost, base+"/export-schedules", "owner", body)
	if created.Code != http.StatusCreated {
		t.Fatalf("fresh create = %d %s", created.Code, created.Body)
	}
	var sched struct{ ID int64 }
	_ = json.Unmarshal(created.Body.Bytes(), &sched)
	one := base + "/export-schedules/" + jsonNumber(sched.ID)

	calls := []struct{ name, method, path, body string }{
		{"create schedule", http.MethodPost, base + "/export-schedules", body},
		{"update schedule", http.MethodPatch, one, `{"name":"renamed"}`},
		{"run schedule", http.MethodPost, one + "/run", ``},
		{"settings export", http.MethodGet, base + "/settings-export", ``},
		{"settings import", http.MethodPost, base + "/settings-import", `{}`},
		{"delete schedule", http.MethodDelete, one, ``},
	}
	for _, c := range calls {
		stale = true
		if got := starsRequest(h, c.method, c.path, "owner", c.body); !isReauthRequired(got.Code, got.Body.String()) {
			t.Errorf("%s stale = %d %s, want 403 reauth_required", c.name, got.Code, got.Body)
		}
		stale = false
		if got := starsRequest(h, c.method, c.path, "owner", c.body); got.Code == http.StatusForbidden || got.Code >= 500 {
			t.Errorf("%s fresh = %d %s, want it to pass the guard", c.name, got.Code, got.Body)
		}
	}
	// A stale delete changed nothing: the schedule was still there for the fresh delete above (204).
	// A dry run changes nothing, so it needs no step-up.
	stale = true
	if got := starsRequest(h, http.MethodPost, base+"/settings-import?dry_run=true", "owner", `{}`); isReauthRequired(got.Code, got.Body.String()) {
		t.Errorf("dry-run import must not need step-up: %d %s", got.Code, got.Body)
	}
}
