package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (f issuesFixture) list(t *testing.T, actor, query string) (int, map[string]any) {
	t.Helper()
	res := f.request(t, actor, http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/issues?query=%s", f.siteID, url.QueryEscape(query)), ``)
	return res.Code, f.decode(res.Body.Bytes())
}

func (f issuesFixture) decode(b []byte) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func issueIDs(body map[string]any) []int64 {
	var ids []int64
	rows, _ := body["issues"].([]any)
	for _, r := range rows {
		ids = append(ids, int64(r.(map[string]any)["id"].(float64)))
	}
	return ids
}

func TestAppIssueListSearch(t *testing.T) {
	f := newAppIssuesFixture(t)
	for query, want := range map[string][]int64{
		"level:fatal": {f.crashIssue}, "level:error": {f.errorIssue}, "kind:error": {f.errorIssue},
		"!kind:error": {f.crashIssue}, "segfault": {f.crashIssue}, "release:1.0.0 is:unresolved": nil,
	} {
		code, body := f.list(t, "viewer", query)
		if code != http.StatusOK {
			t.Errorf("%q = %d", query, code)
			continue
		}
		got := issueIDs(body)
		if query == "release:1.0.0 is:unresolved" {
			if len(got) != 2 {
				t.Errorf("%q = %v, want both issues", query, got)
			}
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q = %v, want %v", query, got, want)
		}
	}
	// Assigned to a member by email: members may look it up, viewers may not.
	f.json(t, "editor", http.MethodPatch, f.path(""), fmt.Sprintf(`{"assignee":%d}`, f.otherUser), http.StatusOK)
	if _, body := f.list(t, "owner", "assigned:second@example.test"); fmt.Sprint(issueIDs(body)) != fmt.Sprint([]int64{f.crashIssue}) {
		t.Errorf("assigned by email = %v", issueIDs(body))
	}
	if _, body := f.list(t, "viewer", "assigned:second@example.test"); len(issueIDs(body)) != 0 {
		t.Errorf("a viewer's email search must not reveal members: %v", issueIDs(body))
	}
	if _, body := f.list(t, "editor2", "assigned:me"); fmt.Sprint(issueIDs(body)) != fmt.Sprint([]int64{f.crashIssue}) {
		t.Errorf("assigned:me = %v", issueIDs(body))
	}
	// The old parameters keep working and are ANDed.
	res := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/issues?kind=crash&query=%s", f.siteID, url.QueryEscape("level:error")), ``)
	if got := issueIDs(f.decode(res.Body.Bytes())); len(got) != 0 {
		t.Errorf("kind=crash AND level:error = %v", got)
	}
}

// Review focus: SQL metacharacters, unicode and over-long input are literals or
// a 400, never a 500.
func TestAppIssueListSearchHostileInput(t *testing.T) {
	f := newAppIssuesFixture(t)
	for name, c := range map[string]struct {
		query string
		code  int
	}{
		"sql":            {"'; drop table app_issues; --", http.StatusOK},
		"quote":          {`"'); select pg_sleep(5); --"`, http.StatusOK},
		"unicode":        {"日本語 é‮", http.StatusOK},
		"percent":        {"%_\\", http.StatusOK},
		"quoted value":   {`release:"1.0.0' or '1'='1"`, http.StatusOK},
		"unknown key":    {"bogus:1", http.StatusBadRequest},
		"nul":            {"a\x00b", http.StatusBadRequest},
		"invalid utf8":   {"a\xffb", http.StatusBadRequest},
		"too long":       {strings.Repeat("a", 201), http.StatusBadRequest},
		"too many terms": {strings.TrimSpace(strings.Repeat("a ", 21)), http.StatusBadRequest},
		"unterminated":   {`release:"x`, http.StatusBadRequest},
	} {
		code, body := f.list(t, "viewer", c.query)
		if code != c.code {
			t.Errorf("%s = %d %v, want %d", name, code, body, c.code)
		}
		if code == http.StatusBadRequest && body["position"] == nil {
			t.Errorf("%s: 400 without a position: %v", name, body)
		}
	}
	if _, body := f.list(t, "viewer", "bogus:1"); body["position"] != 0.0 || !strings.Contains(fmt.Sprint(body["detail"]), `"bogus"`) {
		t.Errorf("unknown key problem = %v", body)
	}
	// The legacy q parameter cannot smuggle a NUL byte into SQL either.
	if res := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/issues?q=a%%00b", f.siteID), ``); res.Code != http.StatusBadRequest {
		t.Errorf("q with NUL = %d", res.Code)
	}
	var left int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from app_issues`).Scan(&left); err != nil || left < 2 {
		t.Fatalf("issues table damaged: %d %v", left, err)
	}
}

func (f issuesFixture) bulkPath() string {
	return fmt.Sprintf("/api/private/sites/%d/app/issues/bulk", f.siteID)
}

// Review focus: a bulk action over 1,000 ids, some of them another site's of the
// same org, changes only the path site's issues and reports the rest.
func TestAppIssueBulkOnlyChangesThePathSite(t *testing.T) {
	f := newAppIssuesFixture(t)
	ctx := context.Background()
	// 995 more issues on this site and 5 on another site of the same org.
	if _, err := f.pool.Exec(ctx, `insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen)
select $1, 'bulk-' || g, 'error', now(), now() from generate_series(1, 995) g`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen)
select $1, 'other-' || g, 'error', now(), now() from generate_series(1, 5) g`, f.otherSite); err != nil {
		t.Fatal(err)
	}
	rows, err := f.pool.Query(ctx, `select id, site_id from app_issues where site_id in ($1,$2) and fingerprint like 'bulk-%' or fingerprint like 'other-%' order by id`, f.siteID, f.otherSite)
	if err != nil {
		t.Fatal(err)
	}
	var ids, foreign []string
	for rows.Next() {
		var id int64
		var site int32
		if err := rows.Scan(&id, &site); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, fmt.Sprint(id))
		if site != f.siteID {
			foreign = append(foreign, fmt.Sprint(id))
		}
	}
	rows.Close()
	if len(ids) != 1000 || len(foreign) != 5 {
		t.Fatalf("fixture: %d ids, %d foreign", len(ids), len(foreign))
	}
	out := f.json(t, "editor", http.MethodPost, f.bulkPath(), `{"ids":[`+strings.Join(ids, ",")+`],"change":{"priority":"high"}}`, http.StatusOK)
	if u, s := out["updated"].([]any), out["skipped"].([]any); len(u) != 995 || len(s) != 5 {
		t.Fatalf("updated %d skipped %d, want 995 and 5", len(u), len(s))
	}
	for _, id := range out["skipped"].([]any) {
		if !strings.Contains(strings.Join(foreign, ","), fmt.Sprint(int64(id.(float64)))) {
			t.Errorf("skipped %v is not one of the other site's issues", id)
		}
	}
	var changedElsewhere, changedHere int
	if err := f.pool.QueryRow(ctx, `select count(*) filter (where site_id <> $1), count(*) filter (where site_id = $1) from app_issues where priority = 'high'`, f.siteID).Scan(&changedElsewhere, &changedHere); err != nil {
		t.Fatal(err)
	}
	if changedElsewhere != 0 || changedHere != 995 {
		t.Fatalf("high priority: %d elsewhere, %d here", changedElsewhere, changedHere)
	}
	var activity int
	if err := f.pool.QueryRow(ctx, `select count(*) from app_issue_activity where kind = 'priority'`).Scan(&activity); err != nil || activity != 995 {
		t.Fatalf("priority activity rows = %d %v, want one per changed issue", activity, err)
	}
	var audits int
	if err := f.pool.QueryRow(ctx, `select count(*) from audit_log where action = 'app.issues_bulk_updated'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("bulk audit rows = %d %v", audits, err)
	}
	// 1,001 ids is refused outright.
	many := strings.Repeat("1,", 1000) + "1"
	if res := f.request(t, "editor", http.MethodPost, f.bulkPath(), `{"ids":[`+many+`],"change":{"priority":"low"}}`); res.Code != http.StatusBadRequest {
		t.Errorf("1001 ids = %d", res.Code)
	}
}

func TestAppIssueBulkByQuery(t *testing.T) {
	f := newAppIssuesFixture(t)
	ctx := context.Background()
	out := f.json(t, "editor", http.MethodPost, f.bulkPath(), `{"query":"level:fatal","change":{"priority":"high"}}`, http.StatusOK)
	if fmt.Sprint(out["updated"]) != fmt.Sprint([]any{float64(f.crashIssue)}) || len(out["skipped"].([]any)) != 0 {
		t.Fatalf("by query = %v", out)
	}
	var high int
	if err := f.pool.QueryRow(ctx, `select count(*) from app_issues where priority='high' and id=$1`, f.crashIssue).Scan(&high); err != nil || high != 1 {
		t.Fatalf("crash issue not changed: %d %v", high, err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from app_issues where priority='high' and id=$1`, f.errorIssue).Scan(&high); err != nil || high != 0 {
		t.Fatalf("the non-matching issue changed: %d %v", high, err)
	}
	// Nothing matches: 200 with empty lists. The status filter ANDs in.
	if out := f.json(t, "editor", http.MethodPost, f.bulkPath(), `{"query":"segfault","change":{"priority":"low"}}`, http.StatusOK); len(out["updated"].([]any)) != 1 {
		t.Fatalf("segfault = %v", out)
	}
	if out := f.json(t, "editor", http.MethodPost, f.bulkPath(), `{"query":"nomatchatall","change":{"priority":"low"}}`, http.StatusOK); len(out["updated"].([]any)) != 0 {
		t.Fatalf("no match = %v", out)
	}
	for name, body := range map[string]string{
		"both":         `{"ids":[1],"query":"x","change":{"priority":"low"}}`,
		"neither":      `{"change":{"priority":"low"}}`,
		"empty ids":    `{"ids":[],"change":{"priority":"low"}}`,
		"bad id":       `{"ids":[0],"change":{"priority":"low"}}`,
		"empty change": `{"query":"level:fatal","change":{}}`,
		"bad query":    `{"query":"bogus:1","change":{"priority":"low"}}`,
		"bad change":   `{"query":"level:fatal","change":{"priority":"urgent"}}`,
	} {
		if res := f.request(t, "editor", http.MethodPost, f.bulkPath(), body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Code, res.Body)
		}
	}
}

func TestAppSearchWriteEndpointsGuardRoleAndTenant(t *testing.T) {
	f := newAppIssuesFixture(t)
	view := f.json(t, "editor", http.MethodPost, fmt.Sprintf("/api/private/sites/%d/app/views", f.siteID), `{"name":"Seed","query":"is:unresolved","shared":true}`, http.StatusCreated)
	viewPath := fmt.Sprintf("/api/private/sites/%d/app/views/%v", f.siteID, view["id"])
	for _, w := range []struct{ method, path, body string }{
		{http.MethodPost, f.bulkPath(), fmt.Sprintf(`{"ids":[%d],"change":{"priority":"low"}}`, f.crashIssue)},
		{http.MethodPost, f.bulkPath(), `{"query":"level:fatal","change":{"priority":"low"}}`},
		{http.MethodPost, fmt.Sprintf("/api/private/sites/%d/app/views", f.siteID), `{"name":"x","query":""}`},
		{http.MethodPatch, viewPath, `{"name":"y"}`},
		{http.MethodDelete, viewPath, ``},
	} {
		if res := f.request(t, "viewer", w.method, w.path, w.body); res.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d, want 403", w.method, w.path, res.Code)
		}
		if res := f.request(t, "foreign-owner", w.method, w.path, w.body); res.Code != http.StatusNotFound {
			t.Errorf("foreign %s %s = %d, want 404", w.method, w.path, res.Code)
		}
		if res := f.request(t, "key", w.method, w.path, w.body); res.Code != http.StatusForbidden {
			t.Errorf("api key %s %s = %d, want 403", w.method, w.path, res.Code)
		}
	}
	if res := f.request(t, "foreign-owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/views", f.siteID), ``); res.Code != http.StatusNotFound {
		t.Errorf("foreign GET views = %d", res.Code)
	}
	var prio string
	if err := f.pool.QueryRow(context.Background(), `select priority from app_issues where id=$1`, f.crashIssue).Scan(&prio); err != nil || prio != "medium" {
		t.Fatalf("a refused bulk change was applied: %q %v", prio, err)
	}
}

func TestAppSavedViewsOverHTTP(t *testing.T) {
	f := newAppIssuesFixture(t)
	base := fmt.Sprintf("/api/private/sites/%d/app/views", f.siteID)
	private := f.json(t, "editor2", http.MethodPost, base, `{"name":"Mine","query":"assigned:me is:unresolved"}`, http.StatusCreated)
	shared := f.json(t, "editor2", http.MethodPost, base, `{"name":"Fatal","query":"level:fatal","shared":true}`, http.StatusCreated)
	if private["mine"] != true || private["shared"] != false || shared["shared"] != true {
		t.Fatalf("created = %v %v", private, shared)
	}
	names := func(actor string) string {
		var out []string
		for _, v := range f.json(t, actor, http.MethodGet, base, ``, http.StatusOK)["views"].([]any) {
			out = append(out, v.(map[string]any)["name"].(string))
		}
		return strings.Join(out, ",")
	}
	if got := names("editor2"); got != "Fatal,Mine" {
		t.Errorf("owner sees %q", got)
	}
	for _, actor := range []string{"viewer", "owner"} {
		if got := names(actor); got != "Fatal" {
			t.Errorf("%s sees %q, want only the shared view", actor, got)
		}
	}
	if res := f.request(t, "editor", http.MethodPost, base, `{"name":"Mine","query":""}`); res.Code != http.StatusCreated {
		t.Errorf("same name for another owner = %d", res.Code)
	}
	if res := f.request(t, "editor2", http.MethodPost, base, `{"name":"Mine","query":""}`); res.Code != http.StatusConflict {
		t.Errorf("duplicate = %d", res.Code)
	}
	for name, body := range map[string]string{"empty": `{"name":"","query":""}`, "bad query": `{"name":"n","query":"bogus:1"}`, "long": `{"name":"n","query":"` + strings.Repeat("a", 201) + `"}`} {
		if res := f.request(t, "editor2", http.MethodPost, base, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, res.Code)
		}
	}
	// Another editor cannot see or touch the private view; the owner (admin) can change the shared one.
	privatePath := fmt.Sprintf("%s/%v", base, private["id"])
	if res := f.request(t, "editor", http.MethodDelete, privatePath, ``); res.Code != http.StatusNotFound {
		t.Errorf("delete of another's private view = %d", res.Code)
	}
	sharedPath := fmt.Sprintf("%s/%v", base, shared["id"])
	if res := f.request(t, "editor", http.MethodPatch, sharedPath, `{"name":"x"}`); res.Code != http.StatusForbidden {
		t.Errorf("non-admin edit of another's shared view = %d", res.Code)
	}
	if out := f.json(t, "owner", http.MethodPatch, sharedPath, `{"name":"Fatal2"}`, http.StatusOK); out["name"] != "Fatal2" {
		t.Errorf("admin edit = %v", out)
	}
	if res := f.request(t, "editor2", http.MethodDelete, privatePath, ``); res.Code != http.StatusNoContent {
		t.Errorf("owner delete = %d", res.Code)
	}
	if res := f.request(t, "editor2", http.MethodDelete, base+"/abc", ``); res.Code != http.StatusNotFound {
		t.Errorf("bad id = %d", res.Code)
	}
}
