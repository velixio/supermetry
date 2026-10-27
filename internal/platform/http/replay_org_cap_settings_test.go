package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/replay"
)

func TestReplaySettingsReflectOrgRetentionCap(t *testing.T) {
	instance := 60
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true, Retention: &instance})
	ctx := context.Background()
	path := fmt.Sprintf("/api/private/sites/%d/replay-settings", f.siteID)
	read := func() replay.SiteSettings {
		t.Helper()
		response := f.request(t, "owner", http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("GET = %d %s", response.Code, response.Body)
		}
		var settings replay.SiteSettings
		if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil {
			t.Fatal(err)
		}
		return settings
	}
	setCap := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	has := func(settings replay.SiteSettings) bool {
		for _, restriction := range settings.Restrictions {
			if restriction == replay.OrgRetentionCapped {
				return true
			}
		}
		return false
	}

	if got := read(); got.RetentionDays.V != 30 || has(got) || got.RetentionDays.Restriction != "" {
		t.Fatalf("no cap: %+v", got)
	}
	setCap(`update orgs set replay_retention_cap_days=7 where id=(select org_id from sites where id=$1)`, f.siteID)
	if got := read(); got.RetentionDays.V != 7 || !has(got) || got.RetentionDays.Restriction != replay.OrgRetentionCapped ||
		got.RetentionDays.Maximum == nil || *got.RetentionDays.Maximum != 7 {
		t.Fatalf("cap 7: %+v", got)
	}
	// Saving above the organization cap is refused, like the instance cap.
	if response := f.request(t, "owner", http.MethodPut, path, `{"retention_days":8}`); response.Code != http.StatusConflict {
		t.Fatalf("PUT 8 over cap 7 = %d %s", response.Code, response.Body)
	}
	response := f.request(t, "owner", http.MethodPut, path, `{"retention_days":5}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT 5 = %d %s", response.Code, response.Body)
	}
	var saved replay.SiteSettings
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.RetentionDays.V != 5 || has(saved) {
		t.Fatalf("project value below the cap: %+v", saved)
	}
	// The cap is not written into the site's stored settings.
	var stored string
	if err := f.pool.Pool.QueryRow(ctx, `select settings::text from sites where id=$1`, f.siteID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "cap") || strings.Contains(stored, `"retention_days": 7`) {
		t.Fatalf("org cap leaked into stored settings: %s", stored)
	}
}
