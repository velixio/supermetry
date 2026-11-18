package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"supermetry/internal/platform/authz"
)

type issuesFixture struct {
	healthFixture
	otherUser int64
}

// newAppIssuesFixture adds org memberships for the fixture's user (owner) and a
// second editor user, so assignment and comment authorship can be checked.
func newAppIssuesFixture(t *testing.T) issuesFixture {
	t.Helper()
	h := newAppHealthFixture(t)
	ctx := context.Background()
	var orgID, other int64
	if err := h.pool.QueryRow(ctx, `select org_id from sites where id=$1`, h.siteID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('second@example.test','Second','h') returning id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		user int64
		role string
	}{{h.userID, "owner"}, {other, "editor"}} {
		if _, err := h.pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,$3)`, orgID, q.user, q.role); err != nil {
			t.Fatal(err)
		}
	}
	h.authenticator["editor2"] = authz.Principal{UserID: other, OrgID: orgID, Role: authz.RoleEditor}
	return issuesFixture{healthFixture: h, otherUser: other}
}

func (f issuesFixture) path(suffix string) string {
	return fmt.Sprintf("/api/private/sites/%d/app/issues/%d%s", f.siteID, f.crashIssue, suffix)
}

func (f issuesFixture) json(t *testing.T, actor, method, path, body string, want int) map[string]any {
	t.Helper()
	res := f.request(t, actor, method, path, body)
	if res.Code != want {
		t.Fatalf("%s %s as %s = %d, want %d: %s", method, path, actor, res.Code, want, res.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return out
}

func TestAppIssueChangeUpdatesFieldsAndDetail(t *testing.T) {
	f := newAppIssuesFixture(t)
	out := f.json(t, "editor", http.MethodPatch, f.path(""), fmt.Sprintf(`{"priority":"high","assignee":%d,"github_url":"https://github.com/acme/app/issues/7"}`, f.otherUser), http.StatusOK)
	if out["status"] != "open" || fmt.Sprint(out["updated"]) != fmt.Sprint([]any{float64(f.crashIssue)}) {
		t.Fatalf("response = %v", out)
	}
	detail := f.json(t, "viewer", http.MethodGet, f.path(""), ``, http.StatusOK)
	if detail["priority"] != "high" || detail["assignee_user_id"] != float64(f.otherUser) || detail["assignee_name"] != "Second" || detail["github_url"] != "https://github.com/acme/app/issues/7" {
		t.Fatalf("detail = %v", detail)
	}
	list := f.json(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/issues", f.siteID), ``, http.StatusOK)
	for _, row := range list["issues"].([]any) {
		if r := row.(map[string]any); r["id"] == float64(f.crashIssue) && (r["priority"] != "high" || r["assignee_name"] != "Second") {
			t.Fatalf("row = %v", r)
		}
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action='app.issue_updated' and target_id=$1`, f.crashIssue).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d %v", audits, err)
	}
	// Archive and snooze through the same endpoint.
	f.json(t, "editor", http.MethodPatch, f.path(""), `{"archive":true}`, http.StatusOK)
	f.json(t, "editor", http.MethodPatch, f.path(""), `{"snooze":{"until":"2026-10-20T00:00:00Z","users":50}}`, http.StatusOK)
	detail = f.json(t, "viewer", http.MethodGet, f.path(""), ``, http.StatusOK)
	if detail["status"] != "ignored" || detail["snooze_users"] != 50.0 || detail["archived_until_escalating"] != false {
		t.Fatalf("snoozed detail = %v", detail)
	}
}

func TestAppIssueChangeRejectsBadInput(t *testing.T) {
	f := newAppIssuesFixture(t)
	for name, body := range map[string]string{
		"empty":          `{}`,
		"unknown field":  `{"nope":1}`,
		"bad priority":   `{"priority":"urgent"}`,
		"bad github":     `{"github_url":"https://example.com/x"}`,
		"past snooze":    `{"snooze":{"until":"2020-01-01T00:00:00Z"}}`,
		"outsider":       fmt.Sprintf(`{"assignee":%d}`, 999999),
		"status+archive": `{"status":"open","archive":true}`,
		"bad status":     `{"status":"regressed"}`,
	} {
		if res := f.request(t, "owner", http.MethodPatch, f.path(""), body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Code, res.Body)
		}
	}
}

func TestAppIssueWriteEndpointsGuardRoleAndTenant(t *testing.T) {
	f := newAppIssuesFixture(t)
	c := f.json(t, "editor", http.MethodPost, f.path("/comments"), `{"body":"seed"}`, http.StatusCreated)
	comment := fmt.Sprintf("/comments/%v", c["id"])
	for _, w := range []struct{ method, suffix, body string }{
		{http.MethodPatch, "", `{"priority":"low"}`},
		{http.MethodPost, "/comments", `{"body":"x"}`},
		{http.MethodPatch, comment, `{"body":"x"}`},
		{http.MethodDelete, comment, ``},
	} {
		if res := f.request(t, "viewer", w.method, f.path(w.suffix), w.body); res.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d, want 403", w.method, w.suffix, res.Code)
		}
		if res := f.request(t, "foreign-owner", w.method, f.path(w.suffix), w.body); res.Code != http.StatusNotFound {
			t.Errorf("foreign %s %s = %d, want 404", w.method, w.suffix, res.Code)
		}
		if res := f.request(t, "key", w.method, f.path(w.suffix), w.body); res.Code != http.StatusForbidden {
			t.Errorf("api key %s %s = %d, want 403", w.method, w.suffix, res.Code)
		}
	}
	// Reads of comments, activity and assignees: any member, but not another org.
	for _, suffix := range []string{"/comments", "/activity"} {
		if res := f.request(t, "viewer", http.MethodGet, f.path(suffix), ``); res.Code != http.StatusOK {
			t.Errorf("viewer GET %s = %d", suffix, res.Code)
		}
		if res := f.request(t, "foreign-owner", http.MethodGet, f.path(suffix), ``); res.Code != http.StatusNotFound {
			t.Errorf("foreign GET %s = %d, want 404", suffix, res.Code)
		}
	}
	// An issue of another site is not reachable through this site's path.
	var foreign int64
	if err := f.pool.QueryRow(context.Background(), `insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen) values($1,'zz','crash',now(),now()) returning id`, f.otherSite).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/private/sites/%d/app/issues/%d", f.siteID, foreign)
	for _, r := range []struct{ method, suffix, body string }{
		{http.MethodPatch, "", `{"priority":"low"}`}, {http.MethodPost, "/comments", `{"body":"x"}`},
		{http.MethodGet, "/comments", ``}, {http.MethodGet, "/activity", ``},
	} {
		if res := f.request(t, "owner", r.method, base+r.suffix, r.body); res.Code != http.StatusNotFound {
			t.Errorf("other site's issue %s %s = %d, want 404", r.method, r.suffix, res.Code)
		}
	}
	var prio string
	if err := f.pool.QueryRow(context.Background(), `select priority from app_issues where id=$1`, f.crashIssue).Scan(&prio); err != nil || prio != "medium" {
		t.Fatalf("a refused change was applied: %q %v", prio, err)
	}
}

func TestAppIssueCommentsAndActivity(t *testing.T) {
	f := newAppIssuesFixture(t)
	if res := f.request(t, "editor", http.MethodPost, f.path("/comments"), `{"body":""}`); res.Code != http.StatusBadRequest {
		t.Fatalf("empty comment = %d", res.Code)
	}
	c := f.json(t, "editor", http.MethodPost, f.path("/comments"), `{"body":"looks like the renderer"}`, http.StatusCreated)
	id := fmt.Sprint(c["id"])
	if c["body"] != "looks like the renderer" || c["author_name"] != "Linker" {
		t.Fatalf("comment = %v", c)
	}
	// editor2 is not the author: 403; the owner is an admin: allowed.
	if res := f.request(t, "editor2", http.MethodPatch, f.path("/comments/"+id), `{"body":"x"}`); res.Code != http.StatusForbidden {
		t.Fatalf("non-author edit = %d", res.Code)
	}
	if res := f.request(t, "editor2", http.MethodDelete, f.path("/comments/"+id), ``); res.Code != http.StatusForbidden {
		t.Fatalf("non-author delete = %d", res.Code)
	}
	if out := f.json(t, "editor", http.MethodPatch, f.path("/comments/"+id), `{"body":"edited"}`, http.StatusOK); out["body"] != "edited" || out["edited_at"] == nil {
		t.Fatalf("edit = %v", out)
	}
	list := f.json(t, "viewer", http.MethodGet, f.path("/comments"), ``, http.StatusOK)
	if len(list["comments"].([]any)) != 1 {
		t.Fatalf("comments = %v", list)
	}
	if res := f.request(t, "owner", http.MethodDelete, f.path("/comments/"+id), ``); res.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d", res.Code)
	}
	if res := f.request(t, "owner", http.MethodDelete, f.path("/comments/"+id), ``); res.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", res.Code)
	}
	if res := f.request(t, "owner", http.MethodDelete, f.path("/comments/abc"), ``); res.Code != http.StatusNotFound {
		t.Fatalf("bad comment id = %d", res.Code)
	}
	// The fixture clock is frozen: move the comment back so the two rows differ in time.
	if _, err := f.pool.Exec(context.Background(), `update app_issue_activity set at = at - interval '1 hour' where kind = 'commented'`); err != nil {
		t.Fatal(err)
	}
	f.json(t, "editor", http.MethodPatch, f.path(""), `{"priority":"high"}`, http.StatusOK)
	act := f.json(t, "viewer", http.MethodGet, f.path("/activity"), ``, http.StatusOK)
	items := act["activity"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["kind"] != "priority" || items[1].(map[string]any)["kind"] != "commented" {
		t.Fatalf("activity = %v", act)
	}
	first := items[0].(map[string]any)
	if first["actor_name"] != "Linker" || !strings.Contains(fmt.Sprint(first["data"]), "high") {
		t.Fatalf("activity row = %v", first)
	}
	// Paging: limit 1, then before the first row's time.
	page := f.json(t, "viewer", http.MethodGet, f.path("/activity?limit=1"), ``, http.StatusOK)
	rows := page["activity"].([]any)
	if len(rows) != 1 {
		t.Fatalf("page = %v", page)
	}
	at := rows[0].(map[string]any)["at"].(string)
	if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
		t.Fatal(err)
	}
	next := f.json(t, "viewer", http.MethodGet, f.path("/activity?limit=1&before="+url.QueryEscape(at)), ``, http.StatusOK)
	if r := next["activity"].([]any); len(r) != 1 || r[0].(map[string]any)["kind"] != "commented" {
		t.Fatalf("next page = %v", next)
	}
	for _, q := range []string{"?limit=0", "?limit=201", "?limit=x", "?before=yesterday"} {
		if res := f.request(t, "viewer", http.MethodGet, f.path("/activity"+q), ``); res.Code != http.StatusBadRequest {
			t.Errorf("activity%s = %d, want 400", q, res.Code)
		}
	}
}

func TestAppAssigneesListsMembersWithSiteAccess(t *testing.T) {
	f := newAppIssuesFixture(t)
	out := f.json(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/assignees", f.siteID), ``, http.StatusOK)
	members := out["members"].([]any)
	if len(members) != 2 || strings.Contains(fmt.Sprint(out), "@") {
		t.Fatalf("members = %v", out)
	}
	if res := f.request(t, "foreign-owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/assignees", f.siteID), ``); res.Code != http.StatusNotFound {
		t.Fatalf("foreign = %d", res.Code)
	}
}

func TestAppIssueMergeEndpointsGuardRoleAndTenant(t *testing.T) {
	f := newAppIssuesFixture(t)
	merge := fmt.Sprintf(`{"source_ids":[%d]}`, f.errorIssue)
	unmerge := `{"hashes":["` + strings.Repeat("ab", 32) + `"]}`
	for _, w := range []struct{ method, suffix, body string }{
		{http.MethodPost, "/merge", merge},
		{http.MethodPost, "/unmerge", unmerge},
	} {
		if res := f.request(t, "viewer", w.method, f.path(w.suffix), w.body); res.Code != http.StatusForbidden {
			t.Errorf("viewer %s = %d, want 403", w.suffix, res.Code)
		}
		if res := f.request(t, "foreign-owner", w.method, f.path(w.suffix), w.body); res.Code != http.StatusNotFound {
			t.Errorf("foreign %s = %d, want 404", w.suffix, res.Code)
		}
		if res := f.request(t, "key", w.method, f.path(w.suffix), w.body); res.Code != http.StatusForbidden {
			t.Errorf("api key %s = %d, want 403", w.suffix, res.Code)
		}
	}
	if res := f.request(t, "foreign-owner", http.MethodGet, f.path("/merged"), ``); res.Code != http.StatusNotFound {
		t.Errorf("foreign GET merged = %d, want 404", res.Code)
	}
	// An issue of another site cannot be merged in, or merged into.
	var foreign int64
	if err := f.pool.QueryRow(context.Background(), `insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen) values($1,'zm','crash',now(),now()) returning id`, f.otherSite).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	if res := f.request(t, "owner", http.MethodPost, f.path("/merge"), fmt.Sprintf(`{"source_ids":[%d]}`, foreign)); res.Code != http.StatusNotFound {
		t.Errorf("merge of another site's issue = %d, want 404", res.Code)
	}
	other := fmt.Sprintf("/api/private/sites/%d/app/issues/%d/merge", f.siteID, foreign)
	if res := f.request(t, "owner", http.MethodPost, other, merge); res.Code != http.StatusNotFound {
		t.Errorf("merge into another site's issue = %d, want 404", res.Code)
	}
	var merged *int64
	if err := f.pool.QueryRow(context.Background(), `select merged_into from app_issues where id=$1`, f.errorIssue).Scan(&merged); err != nil || merged != nil {
		t.Fatalf("a refused merge was applied: %v %v", merged, err)
	}
	for name, body := range map[string]string{"empty": `{"source_ids":[]}`, "self": fmt.Sprintf(`{"source_ids":[%d]}`, f.crashIssue)} {
		if res := f.request(t, "editor", http.MethodPost, f.path("/merge"), body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, res.Code)
		}
	}
	if res := f.request(t, "editor", http.MethodPost, f.path("/unmerge"), `{"hashes":["zz"]}`); res.Code != http.StatusBadRequest {
		t.Errorf("bad hash = %d, want 400", res.Code)
	}
}

func TestAppIssueMergeListUnmergeRoundTrip(t *testing.T) {
	f := newAppIssuesFixture(t)
	f.json(t, "editor", http.MethodPost, f.path("/merge"), fmt.Sprintf(`{"source_ids":[%d]}`, f.errorIssue), http.StatusOK)
	// The merged source is gone from the list and points at the target.
	list := f.json(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/issues", f.siteID), ``, http.StatusOK)
	for _, row := range list["issues"].([]any) {
		if row.(map[string]any)["id"] == float64(f.errorIssue) {
			t.Fatal("merged source still listed")
		}
	}
	if d := f.json(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/issues/%d", f.siteID, f.errorIssue), ``, http.StatusOK); d["merged_into"] != float64(f.crashIssue) {
		t.Fatalf("source detail merged_into = %v", d["merged_into"])
	}
	out := f.json(t, "viewer", http.MethodGet, f.path("/merged"), ``, http.StatusOK)
	items := out["merged"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["issue_id"] != float64(f.errorIssue) {
		t.Fatalf("merged = %v", out)
	}
	hash := items[0].(map[string]any)["hash"].(string)
	res := f.json(t, "editor", http.MethodPost, f.path("/unmerge"), `{"hashes":["`+hash+`"]}`, http.StatusOK)
	if res["issue_id"] != float64(f.errorIssue) {
		t.Fatalf("unmerge = %v", res)
	}
	if out := f.json(t, "viewer", http.MethodGet, f.path("/merged"), ``, http.StatusOK); len(out["merged"].([]any)) != 0 {
		t.Fatalf("still merged: %v", out)
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from audit_log where action in ('app.issues_merged','app.issues_unmerged')`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit rows = %d %v", audits, err)
	}
}
