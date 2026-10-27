package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdminReplayRetentionCap(t *testing.T) {
	e := newStarsEnv(t, false)
	ctx := context.Background()
	path := "/api/private/admin/organizations/" + jsonNumber(e.org) + "/replay-retention-cap"
	var got struct {
		Days   *int   `json:"days"`
		Source string `json:"source"`
	}
	call := func(method, body string) (int, string) {
		resp := starsRequest(e.handler, method, path, "root", body)
		got.Days, got.Source = nil, ""
		_ = json.Unmarshal(resp.Body.Bytes(), &got)
		return resp.Code, resp.Body.String()
	}
	if code, body := call(http.MethodGet, ""); code != http.StatusOK || got.Days != nil || got.Source != "none" || !strings.Contains(body, `"days":null`) {
		t.Fatalf("get default = %d %s", code, body)
	}
	if code, body := call(http.MethodPut, `{"days":30}`); code != http.StatusOK || got.Days == nil || *got.Days != 30 || got.Source != "org" {
		t.Fatalf("put 30 = %d %s", code, body)
	}
	if code, body := call(http.MethodGet, ""); code != http.StatusOK || got.Days == nil || *got.Days != 30 {
		t.Fatalf("get after put = %d %s", code, body)
	}
	// The other organization is untouched.
	var other *int
	if err := e.pool.QueryRow(ctx, `select replay_retention_cap_days from orgs where id=$1`, e.otherOrg).Scan(&other); err != nil || other != nil {
		t.Fatalf("other org cap = %v, %v", other, err)
	}
	for _, edge := range []string{`{"days":1}`, `{"days":365}`} {
		if code, body := call(http.MethodPut, edge); code != http.StatusOK {
			t.Fatalf("put %s = %d %s", edge, code, body)
		}
	}
	if code, body := call(http.MethodPut, `{"days":null}`); code != http.StatusOK || got.Days != nil || got.Source != "none" {
		t.Fatalf("put null = %d %s", code, body)
	}
	call(http.MethodPut, `{"days":45}`)
	if code, body := call(http.MethodDelete, ""); code != http.StatusOK || got.Days != nil || got.Source != "none" {
		t.Fatalf("delete = %d %s", code, body)
	}
	for _, bad := range []string{`{}`, `{"days":0}`, `{"days":366}`, `{"days":-1}`, `{"days":"30"}`, `{"days":1.5}`, `{"days":30,"x":1}`} {
		if code, _ := call(http.MethodPut, bad); code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", bad, code)
		}
	}
	// Rejected values change nothing.
	if code, _ := call(http.MethodGet, ""); code != http.StatusOK || got.Days != nil {
		t.Fatalf("invalid PUT changed the cap: %v", got.Days)
	}
	// Only instance admins; the org's own owner is refused too.
	for _, actor := range []string{"owner", "admin", "viewer", "owner2"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			if resp := starsRequest(e.handler, method, path, actor, `{"days":5}`); resp.Code != http.StatusForbidden {
				t.Errorf("%s %s = %d, want 403", actor, method, resp.Code)
			}
		}
	}
	var stored *int
	if err := e.pool.QueryRow(ctx, `select replay_retention_cap_days from orgs where id=$1`, e.org).Scan(&stored); err != nil || stored != nil {
		t.Fatalf("forbidden calls changed the cap: %v %v", stored, err)
	}
	unknown := "/api/private/admin/organizations/99999/replay-retention-cap"
	for method, body := range map[string]string{http.MethodGet: "", http.MethodPut: `{"days":5}`, http.MethodDelete: ""} {
		if resp := starsRequest(e.handler, method, unknown, "root", body); resp.Code != http.StatusNotFound {
			t.Errorf("unknown org %s = %d, want 404", method, resp.Code)
		}
	}
}

func TestAdminReplayRetentionCapIsAuditedWithoutSecrets(t *testing.T) {
	e := newStarsEnv(t, false)
	ctx := context.Background()
	path := "/api/private/admin/organizations/" + jsonNumber(e.org) + "/replay-retention-cap"
	request := func(method, body string) {
		if resp := starsRequest(e.handler, method, path, "root", body); resp.Code != http.StatusOK {
			t.Fatalf("%s %s = %d %s", method, body, resp.Code, resp.Body)
		}
	}
	request(http.MethodPut, `{"days":30}`)
	request(http.MethodDelete, "")
	rows, err := e.pool.Query(ctx, `
		select action, target_type, target_id, actor_user_id is not null, metadata::text
		from audit_log where org_id=$1 and action='replay.retention_cap_changed' order by id`, e.org)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var metas []string
	for rows.Next() {
		var action, targetType, meta string
		var targetID int64
		var hasActor bool
		if err := rows.Scan(&action, &targetType, &targetID, &hasActor, &meta); err != nil {
			t.Fatal(err)
		}
		if targetType != "organization" || targetID != e.org || !hasActor {
			t.Fatalf("audit row = %s %s %d actor=%v", action, targetType, targetID, hasActor)
		}
		metas = append(metas, meta)
	}
	if len(metas) != 2 || metas[0] != `{"days": 30}` || metas[1] != `{"days": null}` {
		t.Fatalf("audit metadata = %v", metas)
	}
}
