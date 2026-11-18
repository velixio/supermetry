package platformhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/exportschedule"
	"supermetry/internal/orgbundle"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/testpg"
)

type nopExporter struct{}

func (nopExporter) Write(_ context.Context, w io.Writer, _ int32, _, _ time.Time) error {
	_, err := w.Write([]byte("zip"))
	return err
}

func newExportEnv(t *testing.T) (http.Handler, int64, int64) {
	t.Helper()
	return newExportEnvStepUp(t, freshSession{})
}

func newExportEnvStepUp(t *testing.T, stepUp platformhttp.StepUpChecker) (http.Handler, int64, int64) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var org1, org2 int64
	for slug, p := range map[string]*int64{"one": &org1, "two": &org2} {
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ($1,$1) returning id`, slug).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]int64{}
	for _, n := range []string{"owner", "admin", "editor", "viewer", "owner2"} {
		var id int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ($1,'U','hash') returning id`, n+"@example.test").Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[n] = id
	}
	for n, m := range map[string]struct {
		org  int64
		role string
	}{"owner": {org1, "owner"}, "admin": {org1, "admin"}, "editor": {org1, "editor"}, "viewer": {org1, "viewer"}, "owner2": {org2, "owner"}} {
		if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,$3)`, m.org, ids[n], m.role); err != nil {
			t.Fatal(err)
		}
	}
	box, _ := platformcrypto.NewSecretBox(make([]byte, 32), "test/exports")
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, time.Hour)
	t.Cleanup(registry.Close)
	now := func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }
	member := func(n string, r authz.Role, org int64) authz.Principal {
		return authz.Principal{UserID: ids[n], OrgID: org, Role: r}
	}
	handler := platformhttp.New(platformhttp.Deps{
		StepUp: stepUp,
		Authenticator: actorAuthenticator{
			"owner": member("owner", authz.RoleOwner, org1), "admin": member("admin", authz.RoleAdmin, org1),
			"editor": member("editor", authz.RoleEditor, org1), "viewer": member("viewer", authz.RoleViewer, org1),
			"owner2": member("owner2", authz.RoleOwner, org2),
		},
		Store:           data,
		Now:             now,
		ExportSchedules: exportschedule.New(pool, box, nopExporter{}, exportschedule.Options{Now: now}),
		OrgBundle:       orgbundle.New(pool, sitesvc.New(data, registry, privacy.Identified), nil, now),
	})
	return handler, org1, org2
}

func TestOrgExportEndpoints(t *testing.T) {
	h, org1, _ := newExportEnv(t)
	base := "/api/private/orgs/" + jsonNumber(org1)
	routes := [][2]string{
		{http.MethodGet, base + "/export-schedules"}, {http.MethodGet, base + "/settings-export"},
		{http.MethodPost, base + "/settings-import?dry_run=true"},
	}
	for _, r := range routes {
		for _, actor := range []string{"editor", "viewer"} {
			if got := starsRequest(h, r[0], r[1], actor, `{}`); got.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s = %d, want 403", r[0], r[1], actor, got.Code)
			}
		}
		if got := starsRequest(h, r[0], r[1], "owner2", `{}`); got.Code != http.StatusNotFound {
			t.Errorf("%s %s from another org = %d, want 404", r[0], r[1], got.Code)
		}
	}
	// Private endpoint without the operator opt-in is refused.
	body := `{"name":"n","frequency":"daily","endpoint":"https://127.0.0.1:9000","region":"us-east-1","bucket":"b","access_key_id":"k","secret":"s"}`
	if got := starsRequest(h, http.MethodPost, base+"/export-schedules", "owner", body); got.Code != http.StatusBadRequest {
		t.Fatalf("private endpoint = %d %s", got.Code, got.Body)
	}
	body = strings.Replace(body, "https://127.0.0.1:9000", "https://s3.example.com", 1)
	created := starsRequest(h, http.MethodPost, base+"/export-schedules", "admin", body)
	if created.Code != http.StatusCreated || bytes.Contains(created.Body.Bytes(), []byte(`"secret":`)) || !bytes.Contains(created.Body.Bytes(), []byte(`"secret_set":true`)) {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	var sched struct{ ID int64 }
	_ = json.Unmarshal(created.Body.Bytes(), &sched)
	one := base + "/export-schedules/" + jsonNumber(sched.ID)
	if got := starsRequest(h, http.MethodPatch, one, "owner", `{"enabled":false}`); got.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", got.Code, got.Body)
	}
	if got := starsRequest(h, http.MethodPost, one+"/run", "owner", ``); got.Code != http.StatusNotFound { // disabled schedules cannot be queued
		t.Fatalf("run disabled = %d", got.Code)
	}
	if got := starsRequest(h, http.MethodGet, one+"/runs", "owner", ``); got.Code != http.StatusOK {
		t.Fatalf("runs = %d", got.Code)
	}
	if got := starsRequest(h, http.MethodGet, "/api/private/orgs/"+jsonNumber(org1+1)+"/export-schedules/"+jsonNumber(sched.ID)+"/runs", "owner2", ``); got.Code != http.StatusNotFound {
		t.Fatalf("cross-org runs = %d", got.Code)
	}
	if got := starsRequest(h, http.MethodDelete, one, "owner", ``); got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", got.Code)
	}
	exp := starsRequest(h, http.MethodGet, base+"/settings-export", "owner", ``)
	if exp.Code != http.StatusOK || !strings.Contains(exp.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("settings-export = %d %v", exp.Code, exp.Header())
	}
	if got := starsRequest(h, http.MethodPost, base+"/settings-import?dry_run=true", "owner", exp.Body.String()); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"dry_run":true`) {
		t.Fatalf("dry-run import = %d %s", got.Code, got.Body)
	}
	if got := starsRequest(h, http.MethodPost, base+"/settings-import", "owner", `{"kind":"nope"}`); got.Code != http.StatusBadRequest {
		t.Fatalf("bad bundle = %d", got.Code)
	}
}
