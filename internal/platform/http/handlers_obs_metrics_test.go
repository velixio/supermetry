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

// obsFixture is the app-project link fixture with metric series: two counters
// of "http.requests" on the app site (api 30 + 12, web 5, all within one
// five-minute bucket before f.now) and a same-named series on another site.
type obsFixture struct{ linkFixture }

func newObsFixture(t *testing.T) obsFixture {
	t.Helper()
	f := newAppKeyFixture(t)
	ctx := context.Background()
	if err := db.EnsureDailyPartitions(ctx, f.pool, "metric_points", f.now.AddDate(0, 0, -2), f.now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	f.authenticator["key-no-stats"] = authz.Principal{KeyID: 14, OrgID: f.authenticator["owner"].OrgID, Role: authz.RoleKey, Scopes: []string{"sites:write"}}
	add := func(site int32, attrs string, vals ...float64) {
		var id int64
		if err := f.pool.QueryRow(ctx, `insert into metric_series(site_id,name,kind,attrs,attrs_hash,first_seen,last_seen)
values($1,'http.requests','counter',$2::jsonb,sha256(convert_to(gen_random_uuid()::text,'UTF8')),now(),now()) returning id`, site, attrs).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			ts := f.now.Add(-time.Duration(30+i) * time.Second)
			if _, err := f.pool.Exec(ctx, `insert into metric_points(site_id,series_id,ts,value) values($1,$2,$3,$4)`, site, id, ts, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	add(f.siteID, `{"service":"api"}`, 30, 12)
	add(f.siteID, `{"service":"web"}`, 5)
	add(f.otherSite, `{"service":"foreign"}`, 9999)
	return obsFixture{f}
}

func (f obsFixture) query(t *testing.T, actor, path string) (int, map[string]any) {
	t.Helper()
	from := f.now.Add(-time.Hour).UTC().Format(time.RFC3339)
	to := f.now.Add(time.Minute).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"name":"http.requests","agg":"sum","group_by":["service"],"from":%q,"to":%q,"interval":"5m"}`, from, to)
	res := f.request(t, actor, http.MethodPost, path, body)
	var out map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return res.Code, out
}

func sumsByService(t *testing.T, body map[string]any) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	series, _ := body["series"].([]any)
	for _, s := range series {
		m := s.(map[string]any)
		group := m["group"].(map[string]any)
		for _, p := range m["points"].([]any) {
			if v, ok := p.(map[string]any)["value"].(float64); ok {
				out[group["service"].(string)] += v
			}
		}
	}
	return out
}

func TestObsMetricsReadsAreOpenToEveryRole(t *testing.T) {
	f := newObsFixture(t)
	base := fmt.Sprintf("/api/private/sites/%d/obs/metrics", f.siteID)
	for _, actor := range []string{"owner", "editor", "viewer"} {
		res := f.request(t, actor, http.MethodGet, base, ``)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"http.requests"`) || !strings.Contains(res.Body.String(), `"service"`) {
			t.Fatalf("%s list = %d %s", actor, res.Code, res.Body)
		}
		code, body := f.query(t, actor, base+"/query")
		sums := sumsByService(t, body)
		if code != http.StatusOK || sums["api"] != 42 || sums["web"] != 5 || len(sums) != 2 {
			t.Fatalf("%s query = %d %v (the foreign site's 9999 must not appear)", actor, code, body)
		}
	}
}

func TestObsMetricsTenantAndSiteBoundaries(t *testing.T) {
	f := newObsFixture(t)
	for _, suffix := range []string{"", "/query"} {
		path := fmt.Sprintf("/api/private/sites/%d/obs/metrics%s", f.siteID, suffix)
		method := http.MethodGet
		if suffix != "" {
			method = http.MethodPost
		}
		if res := f.request(t, "foreign-owner", method, path, `{}`); res.Code != http.StatusNotFound {
			t.Fatalf("another org %s%s = %d, want 404", method, suffix, res.Code)
		}
		if res := f.request(t, "", method, path, `{}`); res.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s%s = %d, want 401", method, suffix, res.Code)
		}
		// A web project in the caller's own org holds no app metrics.
		web := fmt.Sprintf("/api/private/sites/%d/obs/metrics%s", f.otherSite, suffix)
		if res := f.request(t, "owner", method, web, `{}`); res.Code != http.StatusNotFound {
			t.Fatalf("web project %s%s = %d, want 404", method, suffix, res.Code)
		}
	}
	// A member scoped to the other site cannot read this one.
	if res := f.request(t, "other-editor", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/obs/metrics", f.siteID), ``); res.Code != http.StatusNotFound {
		t.Fatalf("member of another site = %d, want 404", res.Code)
	}
}

func TestObsMetricsPublicAPIRequiresStatsRead(t *testing.T) {
	f := newObsFixture(t)
	base := fmt.Sprintf("/api/public/v1/sites/%d/obs/metrics", f.siteID)
	if res := f.request(t, "key", http.MethodGet, base, ``); res.Code != http.StatusOK {
		t.Fatalf("key with stats:read = %d %s", res.Code, res.Body)
	}
	code, body := f.query(t, "key", base+"/query")
	if sums := sumsByService(t, body); code != http.StatusOK || sums["api"] != 42 {
		t.Fatalf("public query = %d %v", code, body)
	}
	for _, actor := range []string{"key-no-stats", "viewer", "share"} {
		if res := f.request(t, actor, http.MethodGet, base, ``); res.Code != http.StatusForbidden && res.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 403 (or 404)", actor, res.Code)
		}
	}
	if res := f.request(t, "key-no-stats", http.MethodGet, base, ``); res.Code != http.StatusForbidden {
		t.Fatalf("key without stats:read = %d, want 403", res.Code)
	}
	if res := f.request(t, "", http.MethodGet, base, ``); res.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", res.Code)
	}
	// A key of another org cannot see this site.
	if res := f.request(t, "foreign-owner", http.MethodGet, base, ``); res.Code != http.StatusNotFound && res.Code != http.StatusForbidden {
		t.Fatalf("foreign principal = %d", res.Code)
	}
}

func TestObsMetricsQueryValidation(t *testing.T) {
	f := newObsFixture(t)
	path := fmt.Sprintf("/api/private/sites/%d/obs/metrics/query", f.siteID)
	from := f.now.Add(-time.Hour).UTC().Format(time.RFC3339)
	to := f.now.UTC().Format(time.RFC3339)
	for name, body := range map[string]string{
		"unknown field": `{"nope":1}`,
		"bad agg":       fmt.Sprintf(`{"name":"m","agg":"median","from":%q,"to":%q,"interval":"1h"}`, from, to),
		"reversed":      fmt.Sprintf(`{"name":"m","agg":"sum","from":%q,"to":%q,"interval":"1h"}`, to, from),
		"three groups":  fmt.Sprintf(`{"name":"m","agg":"sum","group_by":["a","b","c"],"from":%q,"to":%q,"interval":"1h"}`, from, to),
	} {
		if res := f.request(t, "viewer", http.MethodPost, path, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Code, res.Body)
		}
	}
	// An unknown metric is an empty result, not an error.
	res := f.request(t, "viewer", http.MethodPost, path, fmt.Sprintf(`{"name":"nope","agg":"sum","from":%q,"to":%q,"interval":"1h"}`, from, to))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"series":[]`) {
		t.Fatalf("unknown metric = %d %s", res.Code, res.Body)
	}
}
