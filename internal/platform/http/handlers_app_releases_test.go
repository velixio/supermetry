package platformhttp_test

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"
)

func TestAppDeploysEndpoint(t *testing.T) {
	h := newAppHealthFixture(t)
	ctx := context.Background()
	var releaseID int64
	if err := h.pool.QueryRow(ctx, `insert into app_releases(org_id,version,site_ids) select org_id,'1.0.0','{}' from sites where id=$1 returning id`, h.siteID).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `insert into app_deploys(release_id,org_id,environment,name,url,date_finished)
select $1,org_id,'production','ship','https://ci.example.test/1','2026-10-12 12:00+00' from sites where id=$2`, releaseID, h.siteID); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"owner", "editor", "viewer"} {
		code, body, raw := h.get(t, actor, "deploys?from=2026-10-01&to=2026-10-12")
		if code != http.StatusOK {
			t.Fatalf("%s = %d %s", actor, code, raw)
		}
		rows, ok := body["deploys"].([]any)
		if !ok || len(rows) != 1 {
			t.Fatalf("%s deploys = %s", actor, raw)
		}
		row := rows[0].(map[string]any)
		var keys []string
		for k := range row {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if got := strings.Join(keys, ","); got != "at,environment,id,name,release,url" {
			t.Fatalf("keys = %s", got)
		}
		if row["release"] != "1.0.0" || row["environment"] != "production" || row["at"] != "2026-10-12T12:00:00Z" {
			t.Fatalf("row = %v", row)
		}
	}
	if code, body, raw := h.get(t, "owner", "deploys?from=2026-10-01&to=2026-10-12&release=2.0.0"); code != http.StatusOK || len(body["deploys"].([]any)) != 0 {
		t.Fatalf("release filter = %d %s", code, raw)
	}
	if code, _, _ := h.get(t, "owner", "deploys?from=2026-10-12&to=2026-10-01"); code != http.StatusBadRequest {
		t.Fatalf("bad range = %d", code)
	}
	if code, _, _ := h.get(t, "foreign-owner", "deploys?from=2026-10-01&to=2026-10-12"); code != http.StatusNotFound {
		t.Fatalf("foreign org = %d, want 404", code)
	}
}
