package platformhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveCSRFBody(env adminOrgDeleteEnv, method, path, actor, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Test-Actor", actor)
	request.Header.Set("X-CSRF-Token", "t")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "t"})
	response := httptest.NewRecorder()
	env.handler.ServeHTTP(response, request)
	return response
}

func (e adminOrgDeleteEnv) addSecondOrg(t *testing.T) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `insert into orgs(name,slug) values ('Other','other')`); err != nil {
		t.Fatal(err)
	}
}

func (e adminOrgDeleteEnv) erase(actor, body string, t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return serveCSRFBody(e, http.MethodPost, e.path()+"/erase", actor, body)
}

func TestAdminEraseOrgLiveOrgDeletesFirstAndQueuesSites(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	env.addSecondOrg(t)
	got := env.erase("admin", `{"slug":"doomed"}`, t)
	if got.Code != http.StatusAccepted || !strings.Contains(got.Body.String(), `"erasing"`) {
		t.Fatalf("status=%d body=%s, want 202 erasing", got.Code, got.Body.String())
	}
	var deleted, requested bool
	var sites, queued int
	if err := env.pool.QueryRow(context.Background(), `
		select o.deleted_at is not null, o.erase_requested_at is not null,
		       (select count(*) from sites where org_id=o.id),
		       (select count(*) from sites where org_id=o.id and erasure_started_at is not null and deleted_at is not null)
		from orgs o where o.id=$1`, env.orgID).Scan(&deleted, &requested, &sites, &queued); err != nil {
		t.Fatal(err)
	}
	if !deleted || !requested || sites != 1 || queued != 1 {
		t.Fatalf("deleted=%v requested=%v sites=%d queued=%d", deleted, requested, sites, queued)
	}
	var meta string
	if err := env.pool.QueryRow(context.Background(), `select metadata::text from audit_log where action='instance.organization.erase_now' and target_id=$1`, env.orgID).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(meta, "@") || !strings.Contains(meta, `"sites_queued": 1`) {
		t.Fatalf("audit metadata=%s", meta)
	}
}

func TestAdminEraseOrgDeletedOrg(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	if got := serveCSRF(env.handler, http.MethodDelete, env.path(), "admin"); got.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", got.Code)
	}
	// Already deleted, so the only-live-organization refusal does not apply.
	if got := env.erase("admin", `{"slug":"doomed"}`, t); got.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s, want 202", got.Code, got.Body.String())
	}
}

func TestAdminEraseOrgRejections(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSecondOrg(t)
	cases := []struct {
		name, actor, path, body string
		want                    int
	}{
		{"wrong slug", "admin", env.path() + "/erase", `{"slug":"other"}`, http.StatusBadRequest},
		{"empty slug", "admin", env.path() + "/erase", `{}`, http.StatusBadRequest},
		{"unknown org", "admin", "/api/private/admin/organizations/999999/erase", `{"slug":"doomed"}`, http.StatusNotFound},
		{"owner", "owner", env.path() + "/erase", `{"slug":"doomed"}`, http.StatusForbidden},
		{"member", "member", env.path() + "/erase", `{"slug":"doomed"}`, http.StatusForbidden},
	}
	for _, c := range cases {
		if got := serveCSRFBody(env, http.MethodPost, c.path, c.actor, c.body); got.Code != c.want {
			t.Errorf("%s: status=%d body=%s, want %d", c.name, got.Code, got.Body.String(), c.want)
		}
	}
	var deleted bool
	if err := env.pool.QueryRow(context.Background(), `select deleted_at is not null from orgs where id=$1`, env.orgID).Scan(&deleted); err != nil || deleted {
		t.Fatalf("org deleted=%v err=%v after refused requests", deleted, err)
	}
}

func TestAdminEraseOrgRefusesOnlyOrg(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	if got := env.erase("admin", `{"slug":"doomed"}`, t); got.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", got.Code, got.Body.String())
	}
	var deleted bool
	if err := env.pool.QueryRow(context.Background(), `select deleted_at is not null from orgs where id=$1`, env.orgID).Scan(&deleted); err != nil || deleted {
		t.Fatalf("org deleted=%v err=%v", deleted, err)
	}
}

func TestAdminEraseOrgIdempotentAndNotRestorable(t *testing.T) {
	env := newAdminOrgDeleteEnv(t)
	env.addSite(t)
	env.addSecondOrg(t)
	if got := env.erase("admin", `{"slug":"doomed"}`, t); got.Code != http.StatusAccepted {
		t.Fatalf("first status=%d", got.Code)
	}
	if got := env.erase("admin", `{"slug":"doomed"}`, t); got.Code != http.StatusAccepted {
		t.Fatalf("second (pending) status=%d, want 202", got.Code)
	}
	var n int
	if err := env.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='instance.organization.erase_now'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows=%d err=%v, want 1", n, err)
	}
	if got := serveCSRF(env.handler, http.MethodPost, env.restorePath(), "admin"); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "not_restorable") {
		t.Fatalf("restore while erasing status=%d body=%s", got.Code, got.Body.String())
	}
	// The erasure job finished.
	if _, err := env.pool.Exec(context.Background(), `update sites set erased_at=now() where org_id=$1`, env.orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(context.Background(), `update orgs set erased_at=now() where id=$1`, env.orgID); err != nil {
		t.Fatal(err)
	}
	if got := env.erase("admin", `{"slug":"doomed"}`, t); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"erased"`) {
		t.Fatalf("erased status=%d body=%s, want 200 erased", got.Code, got.Body.String())
	}
	if got := serveCSRF(env.handler, http.MethodPost, env.restorePath(), "admin"); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "not_restorable") {
		t.Fatalf("restore after erase status=%d body=%s", got.Code, got.Body.String())
	}
}
