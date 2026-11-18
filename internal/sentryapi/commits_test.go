package sentryapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func commitPayload(sha, msg string, files ...[2]string) map[string]any {
	c := map[string]any{
		"id": sha, "repository": "acme/app", "message": msg,
		"author_name": "Dev One", "author_email": "dev1@example.com", "timestamp": "2026-11-01T10:00:00Z",
	}
	if len(files) > 0 {
		var ps []map[string]string
		for _, f := range files {
			ps = append(ps, map[string]string{"path": f[0], "type": f[1]})
		}
		c["patch_set"] = ps
	}
	return c
}

func (e *env) createRel(t *testing.T, token, version string, body map[string]any) {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["version"] = version
	if s, _ := e.json(t, "POST", relBase, token, body); s != 201 {
		t.Fatalf("create %s = %d", version, s)
	}
}

func (e *env) commitsOf(t *testing.T, token, version string) []map[string]any {
	t.Helper()
	s, rows, _ := e.list(t, relBase+version+"/commits/", token)
	if s != 200 {
		t.Fatalf("commits %s = %d", version, s)
	}
	return rows
}

func (e *env) countOf(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.deps.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSetCommitsStoresPatchSet(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", nil)
	s, out := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{
		commitPayload("aaaa0003", "newest", [2]string{"src/pay.ts", "M"}, [2]string{"src/new.ts", "A"}),
		commitPayload("aaaa0002", "middle"),
		commitPayload("aaaa0001", "oldest"),
	}})
	if s != 200 {
		t.Fatalf("put = %d %v", s, out)
	}
	if out["commitCount"].(float64) != 3 {
		t.Fatalf("commitCount = %v", out["commitCount"])
	}
	last := out["lastCommit"].(map[string]any)
	if last["id"] != "aaaa0003" || last["message"] != "newest" {
		t.Fatalf("lastCommit = %v", last)
	}
	rows := e.commitsOf(t, token, "1.0.0")
	if len(rows) != 3 || rows[0]["id"] != "aaaa0003" || rows[2]["id"] != "aaaa0001" {
		t.Fatalf("commits = %v", rows)
	}
	if rows[0]["author"].(map[string]any)["email"] != "dev1@example.com" || rows[0]["repository"].(map[string]any)["name"] != "acme/app" {
		t.Fatalf("commit shape = %v", rows[0])
	}
	if n := e.countOf(t, `select count(*) from app_commit_files`); n != 2 {
		t.Fatalf("files = %d", n)
	}
	if n := e.countOf(t, `select count(*) from app_commit_files where path='src/new.ts' and change='A'`); n != 1 {
		t.Fatal("added file not stored with type A")
	}
}

func TestSetCommitsReplaces(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", nil)
	three := []any{commitPayload("bbbb0003", "c"), commitPayload("bbbb0002", "b"), commitPayload("bbbb0001", "a")}
	for i := 0; i < 2; i++ { // CI retry
		if s, out := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": three}); s != 200 || out["commitCount"].(float64) != 3 {
			t.Fatalf("retry %d = %d %v", i, s, out["commitCount"])
		}
	}
	if n := e.countOf(t, `select count(*) from app_release_commits`); n != 3 {
		t.Fatalf("links after retry = %d", n)
	}
	s, out := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{commitPayload("bbbb0003", "c"), commitPayload("bbbb0002", "b")}})
	if s != 200 || out["commitCount"].(float64) != 2 {
		t.Fatalf("second payload = %d %v", s, out["commitCount"])
	}
	if n := e.countOf(t, `select count(*) from app_release_commits`); n != 2 {
		t.Fatalf("links = %d", n)
	}
	if n := e.countOf(t, `select count(*) from app_commits`); n != 3 {
		t.Fatalf("shared commit rows = %d, want 3 kept", n)
	}
	// An empty list clears the links.
	if s, out := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{}}); s != 200 || out["commitCount"].(float64) != 0 {
		t.Fatalf("clear = %d %v", s, out["commitCount"])
	}
	// A PUT without commits leaves them alone.
	e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": three})
	if s, out := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"url": "https://example.com/r"}); s != 200 || out["commitCount"].(float64) != 3 {
		t.Fatalf("no-commits put = %d %v", s, out["commitCount"])
	}
}

func TestSetCommitsSharedAcrossReleases(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", nil)
	e.createRel(t, token, "1.1.0", nil)
	e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{commitPayload("abc1", "x")}})
	e.json(t, "PUT", relBase+"1.1.0/", token, map[string]any{"commits": []any{commitPayload("abc1", "x"), commitPayload("abc2", "y")}})
	if n := e.countOf(t, `select count(*) from app_commits where sha='abc1'`); n != 1 {
		t.Fatalf("commit rows = %d", n)
	}
	if n := e.countOf(t, `select count(*) from app_release_commits`); n != 3 {
		t.Fatalf("links = %d", n)
	}
	e.mustExec(t, `delete from app_releases where version='1.0.0'`)
	if n := e.countOf(t, `select count(*) from app_release_commits`); n != 2 {
		t.Fatalf("links after delete = %d", n)
	}
}

func TestSetCommitsLimits(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", nil)
	many := make([]any, 5001)
	for i := range many {
		many[i] = map[string]any{"id": fmt.Sprintf("%08x", i+1), "repository": "r"}
	}
	s, out := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": many})
	if s != 400 || out["detail"] != "too many commits" {
		t.Fatalf("5001 commits = %d %v", s, out)
	}
	paths := make([]map[string]string, 1001)
	for i := range paths {
		paths[i] = map[string]string{"path": fmt.Sprintf("f%d.ts", i), "type": "M"}
	}
	c := commitPayload("abcd", "x")
	c["patch_set"] = paths
	if s, _ := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{c}}); s != 400 {
		t.Fatalf("1001 paths = %d", s)
	}
	long := commitPayload("abce", strings.Repeat("m", 3000), [2]string{strings.Repeat("p", 1025), "M"})
	if s, _ := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{long}}); s != 400 {
		t.Fatalf("long path = %d", s)
	}
	long = commitPayload("abce", strings.Repeat("m", 3000))
	if s, _ := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{long}}); s != 200 {
		t.Fatalf("long message = %d", s)
	}
	if n := e.countOf(t, `select max(char_length(message)) from app_commits`); n != 2000 {
		t.Fatalf("message length = %d", n)
	}
	if s, _ := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{commitPayload("xyz", "x")}}); s != 400 {
		t.Fatalf("invalid sha = %d", s)
	}
	if s, _ := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{map[string]any{"repository": "r"}}}); s != 400 {
		t.Fatalf("missing sha = %d", s)
	}
	if n := e.countOf(t, `select count(*) from app_release_commits`); n != 1 {
		t.Fatalf("rejected payloads changed links: %d", n)
	}
}

func TestSetCommitsCannotRewriteExistingCommit(t *testing.T) {
	e := newEnv(t, nil)
	owner := e.svc(t, e.owner, []string{Scope}, nil)
	narrow := e.svc(t, e.owner, []string{Scope}, []int32{10})
	e.createRel(t, owner, "1.0.0", map[string]any{"projects": []string{"appsite00020"}})
	e.createRel(t, narrow, "2.0.0", map[string]any{"projects": []string{"appsite00010"}})
	orig := commitPayload("9999aaaa", "original", [2]string{"a.ts", "M"})
	e.json(t, "PUT", relBase+"1.0.0/", owner, map[string]any{"commits": []any{orig}})
	evil := commitPayload("9999aaaa", "rewritten", [2]string{"a.ts", "D"})
	evil["author_name"], evil["author_email"] = "Mallory", "m@example.com"
	if s, _ := e.json(t, "PUT", relBase+"2.0.0/", narrow, map[string]any{"commits": []any{evil}}); s != 200 {
		t.Fatalf("put = %d", s)
	}
	rows := e.commitsOf(t, owner, "1.0.0")
	if len(rows) != 1 || rows[0]["message"] != "original" || rows[0]["author"].(map[string]any)["name"] != "Dev One" {
		t.Fatalf("commit rewritten: %v", rows)
	}
	if n := e.countOf(t, `select count(*) from app_commit_files where change='M'`); n != 1 {
		t.Fatal("file change rewritten")
	}
}

func TestRefsOnly(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", nil)
	s, out := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"refs": []any{map[string]any{"repository": "acme/app", "commit": "abc1"}}})
	if s != 200 || out["commitCount"].(float64) != 0 {
		t.Fatalf("refs = %d %v", s, out)
	}
	if n := e.countOf(t, `select count(*) from app_release_refs where repository='acme/app' and commit_sha='abc1'`); n != 1 {
		t.Fatal("ref not stored")
	}
	if s, _ := e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"refs": []any{map[string]any{"repository": "acme/app", "commit": "zz"}}}); s != 400 {
		t.Fatalf("bad ref = %d", s)
	}
}

func TestPreviousWithCommits(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	other := e.svc(t, e.other, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", nil)
	e.createRel(t, token, "1.1.0", nil)
	e.createRel(t, token, "1.2.0", nil)
	if s, _ := e.do(t, "GET", relBase+"1.0.0/previous-with-commits/", token, nil); s != 404 {
		t.Fatalf("first release = %d", s)
	}
	e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{commitPayload("cccc0002", "newer"), commitPayload("cccc0001", "older")}})
	// A release in another org with commits is never returned.
	if s, _ := e.json(t, "POST", "/api/0/organizations/other/releases/", other, map[string]any{"version": "0.9.0"}); s != 201 {
		t.Fatal("other create")
	}
	e.json(t, "PUT", "/api/0/organizations/other/releases/0.9.0/", other, map[string]any{"commits": []any{commitPayload("dddd0001", "x")}})
	s, out := e.json(t, "GET", relBase+"1.2.0/previous-with-commits/", token, nil)
	if s != 200 || out["version"] != "1.0.0" || out["lastCommit"].(map[string]any)["id"] != "cccc0002" {
		t.Fatalf("previous = %d %v", s, out)
	}
	if s, _ := e.do(t, "GET", relBase+"nope/previous-with-commits/", token, nil); s != 404 {
		t.Fatalf("unknown release = %d", s)
	}
}

func TestPreviousWithCommitsSharesASite(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", map[string]any{"projects": []string{"appsite00010"}})
	e.createRel(t, token, "1.1.0", map[string]any{"projects": []string{"appsite00020"}})
	e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{commitPayload("eeee0001", "x")}})
	if s, _ := e.do(t, "GET", relBase+"1.1.0/previous-with-commits/", token, nil); s != 404 {
		t.Fatalf("disjoint sites = %d", s)
	}
	e.createRel(t, token, "1.2.0", map[string]any{"projects": []string{"appsite00010", "appsite00020"}})
	if s, out := e.json(t, "GET", relBase+"1.2.0/previous-with-commits/", token, nil); s != 200 || out["version"] != "1.0.0" {
		t.Fatalf("overlap = %d %v", s, out)
	}
}

func TestReposStaysEmpty(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", nil)
	e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{commitPayload("ffff0001", "x")}})
	s, rows, _ := e.list(t, "/api/0/organizations/acme/repos/", token)
	if s != 200 || len(rows) != 0 {
		t.Fatalf("repos = %d %v", s, rows)
	}
}

func TestLoadReleaseCommits(t *testing.T) {
	e := newEnv(t, nil)
	token := e.svc(t, e.owner, []string{Scope}, nil)
	e.createRel(t, token, "1.0.0", nil)
	e.json(t, "PUT", relBase+"1.0.0/", token, map[string]any{"commits": []any{
		commitPayload("1111aaaa", "fix pay", [2]string{"src/pay.ts", "M"}, [2]string{"src/new.ts", "A"}), commitPayload("2222bbbb", "init"),
	}})
	got, err := LoadReleaseCommits(context.Background(), e.deps.Pool, e.owner.OrgID, "1.0.0", 10)
	if err != nil || len(got) != 2 || got[0].SHA != "1111aaaa" || got[0].AuthorName != "Dev One" || len(got[0].Paths) != 2 || got[0].Repository != "acme/app" || got[0].At == nil {
		t.Fatalf("load = %+v %v", got, err)
	}
	if got, _ := LoadReleaseCommits(context.Background(), e.deps.Pool, e.other.OrgID, "1.0.0", 10); len(got) != 0 {
		t.Fatalf("other org = %+v", got)
	}
	if got, _ := LoadReleaseCommits(context.Background(), e.deps.Pool, e.owner.OrgID, "1.0.0", 1); len(got) != 1 {
		t.Fatalf("limit = %+v", got)
	}
}
