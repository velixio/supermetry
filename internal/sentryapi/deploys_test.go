package sentryapi

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func (e *env) deploySites(t *testing.T, id any) []int32 {
	t.Helper()
	var arr []int32
	if err := e.deps.Pool.QueryRow(context.Background(), `select site_ids from app_deploys where id=$1::bigint`, fmt.Sprint(id)).Scan(&arr); err != nil {
		t.Fatal(err)
	}
	slices.Sort(arr)
	return arr
}

func TestDeployProjectsResolved(t *testing.T) {
	e := newEnv(t, nil)
	full := e.svc(t, e.owner, []string{Scope}, nil)
	if s, _ := e.json(t, "POST", relBase, full, map[string]any{"version": "1.0", "projects": []string{"appsite00010", "appsite00020"}}); s != 201 {
		t.Fatalf("create release = %d", s)
	}
	s, body := e.json(t, "POST", relBase+"1.0/deploys/", full, map[string]any{"environment": "production", "projects": []string{"appsite00010"}})
	if s != 201 {
		t.Fatalf("deploy = %d", s)
	}
	if got := e.deploySites(t, body["id"]); !slices.Equal(got, []int32{10}) {
		t.Fatalf("site_ids = %v, want [10]", got)
	}
	var orgID int64
	if err := e.deps.Pool.QueryRow(context.Background(), `select org_id from app_deploys where id=$1::bigint`, fmt.Sprint(body["id"])).Scan(&orgID); err != nil || orgID != e.owner.OrgID {
		t.Fatalf("org_id = %d %v", orgID, err)
	}
	// Without projects an unrestricted key inherits the release's projects.
	_, body = e.json(t, "POST", relBase+"1.0/deploys/", full, map[string]any{"environment": "staging"})
	if got := e.deploySites(t, body["id"]); !slices.Equal(got, []int32{10, 20}) {
		t.Fatalf("inherited site_ids = %v, want [10 20]", got)
	}
}

func TestDeployRestrictedKeyNoProjects(t *testing.T) {
	e := newEnv(t, nil)
	full := e.svc(t, e.owner, []string{Scope}, nil)
	restricted := e.svc(t, e.owner, []string{Scope}, []int32{10})
	if s, _ := e.json(t, "POST", relBase, full, map[string]any{"version": "1.0", "projects": []string{"appsite00010", "appsite00020"}}); s != 201 {
		t.Fatalf("create release = %d", s)
	}
	s, body := e.json(t, "POST", relBase+"1.0/deploys/", restricted, map[string]any{"environment": "production"})
	if s != 201 {
		t.Fatalf("restricted deploy = %d", s)
	}
	if got := e.deploySites(t, body["id"]); !slices.Equal(got, []int32{10}) {
		t.Fatalf("site_ids = %v, want [10]", got)
	}
	if s, _ := e.json(t, "POST", relBase, restricted, map[string]any{"version": "mine"}); s != 201 {
		t.Fatalf("create mine = %d", s)
	}
	s, body = e.json(t, "POST", relBase+"mine/deploys/", restricted, map[string]any{"environment": "production"})
	if s != 201 {
		t.Fatalf("own deploy = %d", s)
	}
	if got := e.deploySites(t, body["id"]); !slices.Equal(got, []int32{10}) {
		t.Fatalf("own site_ids = %v, want [10]", got)
	}
}

func TestListDeploysHidesForeignSites(t *testing.T) {
	e := newEnv(t, nil)
	full := e.svc(t, e.owner, []string{Scope}, nil)
	keyA := e.svc(t, e.owner, []string{Scope}, []int32{10})
	if s, _ := e.json(t, "POST", relBase, full, map[string]any{"version": "1.0", "projects": []string{"appsite00010", "appsite00020"}}); s != 201 {
		t.Fatalf("create release = %d", s)
	}
	for env, proj := range map[string]string{"on-a": "appsite00010", "on-b": "appsite00020"} {
		if s, _ := e.json(t, "POST", relBase+"1.0/deploys/", full, map[string]any{"environment": env, "projects": []string{proj}}); s != 201 {
			t.Fatalf("deploy %s = %d", env, s)
		}
	}
	_, rows, _ := e.list(t, relBase+"1.0/deploys/", keyA)
	if len(rows) != 1 || rows[0]["environment"] != "on-a" {
		t.Fatalf("key A sees %v", rows)
	}
	_, rows, _ = e.list(t, relBase+"1.0/deploys/", full)
	if len(rows) != 2 {
		t.Fatalf("full key sees %v", rows)
	}
}

// A site-restricted key never creates an org-wide deploy: an org-wide release
// stays out of its reach (Review Focus 2).
func TestDeployOfOrgWideReleaseByRestrictedKeyIsRefused(t *testing.T) {
	e := newEnv(t, nil)
	full := e.svc(t, e.owner, []string{Scope}, nil)
	restricted := e.svc(t, e.owner, []string{Scope}, []int32{10})
	if s, _ := e.json(t, "POST", relBase, full, map[string]any{"version": "wide"}); s != 201 {
		t.Fatal(s)
	}
	if s, _ := e.json(t, "POST", relBase+"wide/deploys/", restricted, map[string]any{"environment": "p"}); s != 404 {
		t.Fatalf("restricted deploy on org-wide release = %d, want 404", s)
	}
}
