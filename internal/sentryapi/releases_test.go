package sentryapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const relBase = "/api/0/organizations/acme/releases/"

func (e *env) mustExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.deps.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// list GETs a release list and returns the status, rows and Link header.
func (e *env) list(t *testing.T, path, token string) (int, []map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out []map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out, resp.Header.Get("Link")
}

func versions(rows []map[string]any) string {
	var out []string
	for _, r := range rows {
		out = append(out, r["version"].(string))
	}
	return strings.Join(out, ",")
}

func TestListReleasesOrgAndProject(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	for _, r := range []map[string]any{
		{"version": "1.0.0", "projects": []string{"appsite00010"}},
		{"version": "1.1.0", "projects": []string{"appsite00020"}},
		{"version": "2.0.0"},
	} {
		if s, _ := e.json(t, "POST", relBase, token, r); s != 201 {
			t.Fatalf("create %v = %d", r, s)
		}
	}
	_, rows, _ := e.list(t, relBase, token)
	if got := versions(rows); got != "2.0.0,1.1.0,1.0.0" {
		t.Fatalf("org list = %s", got)
	}
	_, rows, _ = e.list(t, "/api/0/projects/acme/appsite00010/releases/", token)
	if got := versions(rows); got != "2.0.0,1.0.0" {
		t.Fatalf("project list = %s", got)
	}
	_, rows, _ = e.list(t, relBase+"?query="+url.QueryEscape("1."), token)
	if got := versions(rows); got != "1.1.0,1.0.0" {
		t.Fatalf("query list = %s", got)
	}
	// A key restricted to site 10 sees only releases bound to its sites.
	narrow := e.svc(t, e.owner, []string{Scope}, []int32{10})
	_, rows, _ = e.list(t, relBase, narrow)
	if got := versions(rows); got != "1.0.0" {
		t.Fatalf("restricted list = %s", got)
	}
	if s, _, _ := e.list(t, "/api/0/projects/acme/nope/releases/", token); s != 404 {
		t.Fatalf("unknown project = %d", s)
	}
}

func TestListReleasesPaging(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.mustExec(t, `insert into app_releases(org_id, version) select $1, 'v' || g from generate_series(1,150) g`, e.owner.OrgID)
	s, rows, link := e.list(t, relBase+"?per_page=100", token)
	const marker = `rel="next"; results="true"; cursor="`
	i := strings.Index(link, marker)
	if s != 200 || len(rows) != 100 || i < 0 {
		t.Fatalf("page 1 = %d %d %q", s, len(rows), link)
	}
	cursor := strings.SplitN(link[i+len(marker):], `"`, 2)[0]
	_, rows2, link2 := e.list(t, relBase+"?per_page=100&cursor="+url.QueryEscape(cursor), token)
	if len(rows2) != 50 || !strings.Contains(link2, `rel="next"; results="false"`) {
		t.Fatalf("page 2 = %d %q", len(rows2), link2)
	}
	if rows[99]["version"] == rows2[0]["version"] {
		t.Fatal("pages overlap")
	}
	if s, _, _ := e.list(t, relBase+"?cursor=bogus", token); s != 400 {
		t.Fatalf("bad cursor = %d", s)
	}
}

func TestDeleteRelease(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.json(t, "POST", relBase, token, map[string]any{"version": "1.0.0"})
	e.json(t, "POST", relBase+"1.0.0/deploys/", token, map[string]any{"environment": "production"})
	narrow := e.svc(t, e.owner, []string{Scope}, []int32{10})
	if s, _ := e.json(t, "DELETE", relBase+"1.0.0/", narrow, nil); s != 404 {
		t.Fatalf("restricted delete of org-wide release = %d", s)
	}
	if s, _ := e.json(t, "GET", relBase+"1.0.0/", token, nil); s != 200 {
		t.Fatalf("release removed by restricted key = %d", s)
	}
	if s, _ := e.json(t, "DELETE", relBase+"1.0.0/", token, nil); s != 204 {
		t.Fatalf("delete = %d", s)
	}
	if s, _ := e.json(t, "GET", relBase+"1.0.0/", token, nil); s != 404 {
		t.Fatalf("get after delete = %d", s)
	}
	if s, _ := e.json(t, "DELETE", relBase+"1.0.0/", token, nil); s != 404 {
		t.Fatalf("second delete = %d", s)
	}
	var n int
	_ = e.deps.Pool.QueryRow(context.Background(), `select count(*) from app_deploys`).Scan(&n)
	if n != 0 {
		t.Fatalf("deploys left = %d", n)
	}
}

func TestUpdateReleaseStatusRefDateStarted(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.json(t, "POST", relBase, token, map[string]any{"version": "1.0.0"})
	s, rel := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"status": "archived", "ref": "abc123", "dateStarted": "2026-11-01T10:00:00Z"})
	if s != 200 || rel["status"] != "archived" || rel["ref"] != "abc123" || rel["dateStarted"] != "2026-11-01T10:00:00Z" {
		t.Fatalf("put = %d %v", s, rel)
	}
	s, rel = e.json(t, "GET", relBase+"1.0.0/", token, nil)
	if s != 200 || rel["status"] != "archived" || rel["ref"] != "abc123" {
		t.Fatalf("get = %d %v", s, rel)
	}
	if s, _ := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"status": "bogus"}); s != 400 {
		t.Fatalf("bogus status = %d", s)
	}
	// A partial update keeps the other fields.
	s, rel = e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"status": "open"})
	if s != 200 || rel["status"] != "open" || rel["ref"] != "abc123" {
		t.Fatalf("partial put = %d %v", s, rel)
	}
	// Create accepts the same fields.
	s, rel = e.json(t, "POST", relBase, token, map[string]any{"version": "2.0.0", "ref": "def", "dateStarted": "2026-11-02T00:00:00Z"})
	if s != 201 || rel["ref"] != "def" || rel["dateStarted"] != "2026-11-02T00:00:00Z" {
		t.Fatalf("create = %d %v", s, rel)
	}
}

func (e *env) seedIssue(t *testing.T, site int32, fp, firstRelease, firstSeen, lastSeen string) {
	t.Helper()
	e.mustExec(t, `insert into app_issues(site_id, fingerprint, kind, first_seen, last_seen, first_release, last_release) values ($1,$2,'error',$3,$4,$5,$5)`, site, fp, firstSeen, lastSeen, firstRelease)
}

func TestReleaseCountersReal(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.json(t, "POST", relBase, token, map[string]any{"version": "1.0.0", "projects": []string{"appsite00010"}})
	e.seedIssue(t, 10, "a", "1.0.0", "2026-11-01T10:00:00Z", "2026-11-03T10:00:00Z")
	e.seedIssue(t, 10, "b", "1.0.0", "2026-11-02T10:00:00Z", "2026-11-05T10:00:00Z")
	e.seedIssue(t, 20, "c", "1.0.0", "2026-11-04T10:00:00Z", "2026-11-09T10:00:00Z") // not a site of the release
	e.seedIssue(t, 10, "d", "0.9.0", "2026-10-01T10:00:00Z", "2026-11-09T10:00:00Z")
	e.json(t, "POST", relBase+"1.0.0/deploys/", token, map[string]any{"environment": "production", "name": "ci"})
	s, rel := e.json(t, "GET", relBase+"1.0.0/", token, nil)
	if s != 200 || rel["newGroups"] != float64(2) || rel["deployCount"] != float64(1) {
		t.Fatalf("counters = %d %v", s, rel)
	}
	if rel["firstEvent"] != "2026-11-01T10:00:00Z" || rel["lastEvent"] != "2026-11-05T10:00:00Z" {
		t.Fatalf("first/last event = %v %v", rel["firstEvent"], rel["lastEvent"])
	}
	ld, _ := rel["lastDeploy"].(map[string]any)
	if ld == nil || ld["environment"] != "production" {
		t.Fatalf("lastDeploy = %v", rel["lastDeploy"])
	}
	if rel["commitCount"] != float64(0) {
		t.Fatalf("commitCount = %v", rel["commitCount"])
	}
	// An org-wide release counts every app site of the org, and no other org's.
	e.json(t, "POST", relBase, token, map[string]any{"version": "1.0.0-all"})
	e.seedIssue(t, 10, "e", "1.0.0-all", "2026-11-01T00:00:00Z", "2026-11-01T00:00:00Z")
	e.seedIssue(t, 20, "f", "1.0.0-all", "2026-11-01T00:00:00Z", "2026-11-01T00:00:00Z")
	e.seedIssue(t, 30, "g", "1.0.0-all", "2026-11-01T00:00:00Z", "2026-11-01T00:00:00Z")
	_, all := e.json(t, "GET", relBase+"1.0.0-all/", token, nil)
	if all["newGroups"] != float64(2) || all["lastDeploy"] != nil || all["firstEvent"] == nil {
		t.Fatalf("org-wide = %v", all)
	}
	// No issues: null first/last event.
	e.json(t, "POST", relBase, token, map[string]any{"version": "3.0.0"})
	_, none := e.json(t, "GET", relBase+"3.0.0/", token, nil)
	if none["newGroups"] != float64(0) || none["firstEvent"] != nil || none["lastEvent"] != nil {
		t.Fatalf("empty = %v", none)
	}
	// A restricted key only counts its own sites.
	narrow := e.svc(t, e.owner, []string{Scope}, []int32{10})
	_, mine := e.json(t, "GET", relBase+"1.0.0/", narrow, nil)
	if mine["newGroups"] != float64(2) {
		t.Fatalf("restricted = %v", mine)
	}
}

func TestReleaseOrgIsolation(t *testing.T) {
	e := newEnv(t, nil)
	x := e.svc(t, e.owner, []string{Scope}, nil)
	y := e.svc(t, e.other, []string{Scope}, nil)
	const otherBase = "/api/0/organizations/other/releases/"
	e.json(t, "POST", relBase, x, map[string]any{"version": "1.0.0", "ref": "x-ref"})
	e.json(t, "POST", otherBase, y, map[string]any{"version": "1.0.0", "ref": "y-ref"})
	_, rows, _ := e.list(t, relBase, x)
	if len(rows) != 1 || rows[0]["ref"] != "x-ref" {
		t.Fatalf("x list = %v", rows)
	}
	if s, _ := e.json(t, "PUT", relBase+"1.0.0/", x, map[string]any{"ref": "x2"}); s != 200 {
		t.Fatalf("x put = %d", s)
	}
	if _, got := e.json(t, "GET", otherBase+"1.0.0/", y, nil); got["ref"] != "y-ref" {
		t.Fatalf("y release changed: %v", got)
	}
	if s, _ := e.json(t, "DELETE", relBase+"1.0.0/", x, nil); s != 204 {
		t.Fatalf("x delete = %d", s)
	}
	if s, got := e.json(t, "GET", otherBase+"1.0.0/", y, nil); s != 200 || got["ref"] != "y-ref" {
		t.Fatalf("y release after x delete = %d %v", s, got)
	}
	if s, _ := e.json(t, "DELETE", otherBase+"1.0.0/", x, nil); s != 404 {
		t.Fatalf("x deleting in org y = %d", s)
	}
}
