package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
)

const obsTraceHex = "5b8efff798038103d269b633813fc60c"

// newObsLogFixture seeds an app site (now = 2026-10-12T20:30Z) with four logs:
// two of one trace and one 5 days old, all with a user hash that must never
// be returned.
func newObsLogFixture(t *testing.T) linkFixture {
	t.Helper()
	f := newAppKeyFixture(t)
	ctx := context.Background()
	f.authenticator["key-noscope"] = authz.Principal{KeyID: 12, OrgID: f.authenticator["key"].OrgID, Role: authz.RoleKey, Scopes: []string{"sites:write"}}
	if err := db.EnsureDailyPartitions(ctx, f.pool, "logs", f.now.AddDate(0, 0, -8), f.now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		rid  int
		ts   string
		sev  int
		body string
		tr   string
	}{
		{1, "2026-10-12T18:00:00Z", 9, "checkout started", obsTraceHex},
		{2, "2026-10-12T18:00:01Z", 17, "checkout failed", obsTraceHex},
		{3, "2026-10-12T19:00:00Z", 9, "health ok", ""},
		{4, "2026-10-07T12:00:00Z", 17, "checkout old failure", ""},
	} {
		var trace any
		if r.tr != "" {
			trace = fmt.Sprintf("\\x%s", r.tr)
		}
		if _, err := f.pool.Exec(ctx, `insert into logs(site_id,ts,rid,observed_at,severity,service,environment,release,trace_id,user_hash,body,attributes,source)
			values($1,$2,$3,$2,$4,'api','prod','1.0',$5::text::bytea,decode('abcdef0123456789','hex'),$6,'{"http.route":"/pay"}','otlp')`, f.siteID, r.ts, r.rid, r.sev, trace, r.body); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f linkFixture) obsGet(t *testing.T, actor, path string) (int, map[string]any, string) {
	t.Helper()
	res := f.request(t, actor, http.MethodGet, path, ``)
	var body map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	return res.Code, body, res.Body.String()
}

func TestObsLogSearchReadsAndShape(t *testing.T) {
	f := newObsLogFixture(t)
	base := fmt.Sprintf("/api/private/sites/%d/obs/logs", f.siteID)
	for _, actor := range []string{"owner", "editor", "viewer"} {
		code, body, raw := f.obsGet(t, actor, base+"?q=checkout")
		rows, _ := body["data"].([]any)
		if code != http.StatusOK || len(rows) != 2 {
			t.Fatalf("%s: %d %s", actor, code, raw)
		}
		// Newest first, and no identity of any kind.
		if rows[0].(map[string]any)["body"] != "checkout failed" || rows[0].(map[string]any)["severity_text"] != "error" {
			t.Fatalf("%s rows = %s", actor, raw)
		}
		for _, banned := range []string{"user_hash", "abcdef0123", "user"} {
			if strings.Contains(raw, banned) {
				t.Fatalf("response contains %q: %s", banned, raw)
			}
		}
	}
	// Paging.
	code, body, raw := f.obsGet(t, "viewer", base+"?q=checkout&limit=1")
	cursor, _ := body["next_cursor"].(string)
	if code != http.StatusOK || cursor == "" || len(body["data"].([]any)) != 1 {
		t.Fatalf("page 1 = %d %s", code, raw)
	}
	code, body, raw = f.obsGet(t, "viewer", base+"?q=checkout&limit=1&cursor="+cursor)
	if code != http.StatusOK || body["next_cursor"] != nil || body["data"].([]any)[0].(map[string]any)["body"] != "checkout started" {
		t.Fatalf("page 2 = %d %s", code, raw)
	}
}

func TestObsLogSearchValidation(t *testing.T) {
	f := newObsLogFixture(t)
	base := fmt.Sprintf("/api/private/sites/%d/obs/logs", f.siteID)
	for q, want := range map[string]string{
		"?q=ab":                        "at least 3 characters",
		"?q=foo:bar":                   "position 0",
		"?limit=201":                   "limit must be",
		"?from=yesterday":              "RFC 3339",
		"?cursor=!!":                   "cursor is invalid",
		"?from=2026-09-01T00:00:00Z":   "at most 30 days",
		"?q=severity:loud":             "severity must be",
		"?q=trace:" + obsTraceHex[:30]: "32 hex",
	} {
		code, _, raw := f.obsGet(t, "owner", base+q)
		if code != http.StatusBadRequest || !strings.Contains(raw, want) {
			t.Errorf("%s = %d %s, want 400 with %q", q, code, raw, want)
		}
	}
}

// Review focus 3: a site that keeps 3 days does not return a 5-day-old row.
func TestObsLogSearchHidesRowsBeyondRetention(t *testing.T) {
	f := newObsLogFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/obs/logs?q=old+failure&from=2026-10-05T00:00:00Z&to=2026-10-12T20:00:00Z", f.siteID)
	code, body, raw := f.obsGet(t, "owner", path)
	if code != http.StatusOK || len(body["data"].([]any)) != 1 {
		t.Fatalf("7-day default: %d %s", code, raw)
	}
	if _, err := f.pool.Exec(context.Background(), `update sites set settings='{"app":{"log_retention_days":3}}' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	code, body, raw = f.obsGet(t, "owner", path)
	if code != http.StatusOK || len(body["data"].([]any)) != 0 {
		t.Fatalf("3-day retention: %d %s", code, raw)
	}
}

func TestObsTraceLogs(t *testing.T) {
	f := newObsLogFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/obs/traces/%s/logs", f.siteID, obsTraceHex)
	code, body, raw := f.obsGet(t, "viewer", path)
	rows, _ := body["data"].([]any)
	if code != http.StatusOK || len(rows) != 2 || rows[0].(map[string]any)["body"] != "checkout started" {
		t.Fatalf("trace logs = %d %s", code, raw)
	}
	if code, _, _ := f.obsGet(t, "viewer", fmt.Sprintf("/api/private/sites/%d/obs/traces/abc/logs", f.siteID)); code != http.StatusBadRequest {
		t.Fatalf("short id = %d", code)
	}
	if code, _, _ := f.obsGet(t, "foreign-owner", path); code != http.StatusNotFound {
		t.Fatalf("other org trace logs = %d, want 404", code)
	}
}

func TestObsLogAuthorizationAndTenantBoundaries(t *testing.T) {
	f := newObsLogFixture(t)
	private := fmt.Sprintf("/api/private/sites/%d/obs/logs", f.siteID)
	public := fmt.Sprintf("/api/public/v1/sites/%d/obs/logs", f.siteID)
	for _, c := range []struct {
		name, actor, path string
		want              int
	}{
		{"other org owner", "foreign-owner", private, http.StatusNotFound},
		{"anonymous", "", private, http.StatusUnauthorized},
		{"viewer limited to another site", "other-editor", private, http.StatusNotFound},
		{"key with stats:read", "key", public, http.StatusOK},
		{"key without stats:read", "key-noscope", public, http.StatusForbidden},
		{"session on the public surface", "owner", public, http.StatusForbidden},
		{"key on the private surface", "key", private, http.StatusForbidden},
		{"share link", "share", private, http.StatusForbidden},
		{"other site of the same org is not an app", "owner", fmt.Sprintf("/api/private/sites/%d/obs/logs", f.otherSite), http.StatusNotFound},
	} {
		if code, _, raw := f.obsGet(t, c.actor, c.path); code != c.want {
			t.Errorf("%s = %d %s, want %d", c.name, code, raw, c.want)
		}
	}
}

func TestObsLogRoutesRejectWebSites(t *testing.T) {
	f := newObsLogFixture(t)
	if _, err := f.pool.Exec(context.Background(), `update sites set kind='web' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.obsGet(t, "owner", fmt.Sprintf("/api/private/sites/%d/obs/logs", f.siteID)); code != http.StatusNotFound {
		t.Fatalf("web site = %d", code)
	}
}

func TestObsIngestHealthSevenDays(t *testing.T) {
	f := newObsLogFixture(t)
	ctx := context.Background()
	today := f.now.UTC().Truncate(24 * time.Hour)
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format("2006-01-02") }
	for _, r := range []struct {
		offset             int
		class              string
		accepted, rejected int64
	}{
		{0, "logs", 120, 5}, {0, "metrics", 900, 0},
		{-3, "logs", 40, 0},
		{-6, "metrics", 70, 7},
		{-7, "logs", 999, 999}, // outside the window
	} {
		if _, err := f.pool.Exec(ctx, `insert into obs_ingest_daily(site_id, day, class, accepted, rejected, bytes) values($1,$2::date,$3,$4,$5,0)`,
			f.siteID, day(r.offset), r.class, r.accepted, r.rejected); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/api/private/sites/%d/obs/ingest", f.siteID)
	for _, actor := range []string{"owner", "viewer"} {
		code, body, raw := f.obsGet(t, actor, path)
		days, _ := body["days"].([]any)
		if code != http.StatusOK || len(days) != 7 {
			t.Fatalf("%s: %d %s", actor, code, raw)
		}
		want := map[int][4]float64{6: {120, 5, 900, 0}, 3: {40, 0, 0, 0}, 0: {0, 0, 70, 7}}
		for i, d := range days {
			row := d.(map[string]any)
			if row["day"] != day(i-6) {
				t.Fatalf("%s day %d = %v, want %s", actor, i, row["day"], day(i-6))
			}
			got := [4]float64{row["logs_accepted"].(float64), row["logs_rejected"].(float64), row["metrics_accepted"].(float64), row["metrics_rejected"].(float64)}
			if got != want[i] {
				t.Fatalf("%s %s = %v, want %v", actor, row["day"], got, want[i])
			}
		}
	}
	if code, _, _ := f.obsGet(t, "key-noscope", path); code != http.StatusForbidden {
		t.Fatalf("no scope = %d", code)
	}
}
