package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/approllup"
	"supermetry/internal/db"
)

type healthFixture struct {
	linkFixture
	crashIssue, errorIssue int64
}

// newAppHealthFixture makes the link fixture's site an app project (retention
// 30 days, Asia/Kathmandu) with two installs, two sessions and two issues, all
// on local 2026-10-12.
func newAppHealthFixture(t *testing.T) healthFixture {
	t.Helper()
	f := newAppKeyFixture(t)
	ctx := context.Background()
	if err := db.EnsureAppEventPartitions(ctx, f.pool, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	h := healthFixture{linkFixture: f}
	for _, i := range []struct {
		fp, kind, errType, title string
		out                      *int64
	}{
		{"fp-crash", "crash", "SegFault", "SegFault in a.go f", &h.crashIssue},
		{"fp-error", "error", "ValueError", "ValueError in a.go f", &h.errorIssue},
	} {
		if err := f.pool.QueryRow(ctx, `insert into app_issues(site_id,fingerprint,kind,error_type,title,first_seen,last_seen,first_release,last_release,event_count)
values($1,$2,$3,$4,$5,'2026-10-12 09:00+00','2026-10-12 11:00+00','1.0.0','1.0.0',2) returning id`, f.siteID, i.fp, i.kind, i.errType, i.title).Scan(i.out); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []struct {
		id, kind, level, at, install, session string
		issue                                 int64
		occ                                   int
	}{
		{"p1", "session", "error", "2026-10-12 09:00+00", "A", "s1", 0, 1},
		{"c1", "crash", "fatal", "2026-10-12 10:00+00", "A", "s1", h.crashIssue, 2},
		{"p2", "session", "error", "2026-10-12 11:00+00", "B", "s2", 0, 1},
		{"e1", "error", "error", "2026-10-12 11:05+00", "B", "s2", h.errorIssue, 1},
	} {
		var issue any
		if e.issue != 0 {
			issue = e.issue
		}
		if _, err := f.pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,level,release,platform,os_version,arch,device_class,occurrences,install_hash,session_hash,frames)
values($1,$2,$2,$3,$4,$5,$6,'1.0.0','linux','22.04','x64','cpu',$7,sha256($8::bytea),sha256($9::bytea),'a.go:1 f')`,
			f.siteID, e.at, e.id, issue, e.kind, e.level, e.occ, e.install, e.session); err != nil {
			t.Fatal(err)
		}
	}
	for _, day := range []string{"2026-10-12", "2026-10-13"} {
		d, _ := time.Parse(time.DateOnly, day)
		if err := approllup.RecomputeDay(ctx, f.pool, f.siteID, d); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h healthFixture) get(t *testing.T, actor, suffix string) (int, map[string]any, string) {
	t.Helper()
	res := h.request(t, actor, http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/%s", h.siteID, suffix), ``)
	var body map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	return res.Code, body, res.Body.String()
}

func TestAppHealthReadsAreOpenToEveryRole(t *testing.T) {
	h := newAppHealthFixture(t)
	for _, actor := range []string{"owner", "editor", "viewer"} {
		for suffix, key := range map[string]string{"overview": "series", "issues": "issues", "compatibility": "rows", "releases": "releases", fmt.Sprintf("issues/%d", h.crashIssue): "breakdowns"} {
			code, body, raw := h.get(t, actor, suffix)
			if code != http.StatusOK || body[key] == nil {
				t.Fatalf("%s %s = %d %s", actor, suffix, code, raw)
			}
		}
	}
}

func TestAppHealthOverviewShape(t *testing.T) {
	h := newAppHealthFixture(t)
	code, body, raw := h.get(t, "viewer", "overview")
	if code != http.StatusOK {
		t.Fatal(raw)
	}
	// 3 events (the crash counts twice), 2 crashes; installs A B with A crashed; sessions s1 s2 with s1 crashed.
	if body["events"] != 3.0 || body["crashes"] != 2.0 || body["new_issues"] != 2.0 || body["regressed_issues"] != 0.0 ||
		body["crash_free_users"] != 0.5 || body["crash_free_sessions"] != 0.5 {
		t.Fatalf("overview = %s", raw)
	}
	series := body["series"].([]any)
	if len(series) != 30 { // default range: the 30 days up to today (local 2026-10-13)
		t.Fatalf("series length = %d", len(series))
	}
	last := series[len(series)-2].(map[string]any)
	if last["day"] != "2026-10-12" || last["events"] != 3.0 || last["crashes"] != 2.0 || last["crash_free_users"] != 0.5 {
		t.Fatalf("2026-10-12 = %v", last)
	}
	if series[len(series)-1].(map[string]any)["crash_free_users"] != nil {
		t.Fatalf("a day without events has a null rate: %v", series[len(series)-1])
	}
}

func TestAppHealthRangeAndFilterValidation(t *testing.T) {
	h := newAppHealthFixture(t)
	for _, q := range []string{
		"overview?from=nope", "overview?to=2026-13-40", "overview?from=2026-10-12&to=2026-10-01",
		"overview?from=2025-01-01&to=2026-10-12", "issues?limit=101", "issues?sort=bogus", "issues?status=bogus",
		"issues?release=" + strings.Repeat("x", 65), "compatibility?platform=" + strings.Repeat("x", 33),
	} {
		if code, _, raw := h.get(t, "owner", q); code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", q, code, raw)
		}
	}
	code, body, raw := h.get(t, "owner", "issues?release=2.0.0&from=2026-10-01&to=2026-10-12")
	if code != http.StatusOK || len(body["issues"].([]any)) != 0 {
		t.Fatalf("filtered issues = %d %s", code, raw)
	}
}

func TestAppHealthIssueDetailNeverReturnsHashes(t *testing.T) {
	h := newAppHealthFixture(t)
	code, body, raw := h.get(t, "viewer", fmt.Sprintf("issues/%d", h.crashIssue))
	if code != http.StatusOK || body["title"] != "SegFault in a.go f" {
		t.Fatalf("detail = %d %s", code, raw)
	}
	for _, banned := range []string{"hash", "install_", "session_"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("detail mentions %q: %s", banned, raw)
		}
	}
	if len(body["latest_events"].([]any)) != 1 {
		t.Fatalf("latest = %s", raw)
	}
	if code, _, _ := h.get(t, "owner", "issues/999999"); code != http.StatusNotFound {
		t.Fatalf("unknown issue = %d", code)
	}
	if code, _, _ := h.get(t, "owner", "issues/abc"); code != http.StatusNotFound {
		t.Fatalf("non-numeric issue = %d", code)
	}
}

func TestAppHealthAuthorizationAndTenantBoundaries(t *testing.T) {
	h := newAppHealthFixture(t)
	if code, _, _ := h.get(t, "foreign-owner", "overview"); code != http.StatusNotFound {
		t.Fatalf("another org read = %d, want 404", code)
	}
	path := fmt.Sprintf("/api/private/sites/%d/app/issues/%d", h.siteID, h.crashIssue)
	if res := h.request(t, "foreign-owner", http.MethodPatch, path, `{"status":"ignored"}`); res.Code != http.StatusNotFound {
		t.Fatalf("another org patch = %d, want 404", res.Code)
	}
	if res := h.request(t, "viewer", http.MethodPatch, path, `{"status":"ignored"}`); res.Code != http.StatusForbidden {
		t.Fatalf("viewer patch = %d, want 403", res.Code)
	}
	if res := h.request(t, "", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/app/overview", h.siteID), ``); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", res.Code)
	}
	var status string
	if err := h.pool.QueryRow(context.Background(), `select status from app_issues where id=$1`, h.crashIssue).Scan(&status); err != nil || status != "open" {
		t.Fatalf("a refused patch changed the issue: %q %v", status, err)
	}
	// An issue id of another site is not reachable through this site's path.
	var foreign int64
	if err := h.pool.QueryRow(context.Background(), `insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen) values($1,'x','crash',now(),now()) returning id`, h.otherSite).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	if res := h.request(t, "owner", http.MethodPatch, fmt.Sprintf("/api/private/sites/%d/app/issues/%d", h.siteID, foreign), `{"status":"ignored"}`); res.Code != http.StatusNotFound {
		t.Fatalf("issue of another site = %d", res.Code)
	}
	// Web projects have no app health.
	if _, err := h.pool.Exec(context.Background(), `update sites set kind='web' where id=$1`, h.siteID); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := h.get(t, "owner", "overview"); code != http.StatusNotFound {
		t.Fatalf("web project = %d", code)
	}
}

func TestAppHealthStatusChangeAuditsAndResolvesNext(t *testing.T) {
	h := newAppHealthFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/app/issues/%d", h.siteID, h.crashIssue)
	res := h.request(t, "editor", http.MethodPatch, path, `{"status":"resolved","resolved_in_release":"next"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", res.Code, res.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "resolved" || out["resolved_in_release"] != "1.0.0" || out["resolved_next"] != true {
		t.Fatalf("response = %s", res.Body)
	}
	var audits int
	var meta string
	if err := h.pool.QueryRow(context.Background(), `select count(*), coalesce(max(metadata::text),'') from audit_log where action='app.issue_status_changed' and target_id=$1`, h.crashIssue).Scan(&audits, &meta); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d %v", audits, err)
	}
	for _, want := range []string{`"from": "open"`, `"to": "resolved"`, `"resolved_in_release": "1.0.0"`} {
		if !strings.Contains(strings.ReplaceAll(meta, `":"`, `": "`), want) {
			t.Fatalf("audit metadata %s lacks %s", meta, want)
		}
	}
	if code, body, _ := h.get(t, "viewer", fmt.Sprintf("issues/%d", h.crashIssue)); code != http.StatusOK || body["status"] != "resolved" || body["resolved_next"] != true {
		t.Fatalf("detail after patch = %v", body)
	}
	for _, body := range []string{`{"status":"regressed"}`, `{"status":"open","resolved_in_release":"1.0"}`, `{}`, `{"status":"open","extra":1}`, `not json`} {
		if res := h.request(t, "owner", http.MethodPatch, path, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", body, res.Code, res.Body)
		}
	}
}
