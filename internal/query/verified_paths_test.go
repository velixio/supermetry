package query_test

import (
	"context"
	"errors"
	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"testing"
)

func TestVerifiedPathsExcludeLegacyImportsAndPreserveAcrossClose(t *testing.T) {
	pool, engine := setupTiny(t)
	ctx := context.Background()
	policy, err := privacy.NewPathPolicy([]string{"/public"})
	if err != nil {
		t.Fatal(err)
	}
	engine.SetTrackingMode(privacy.Anonymous)
	engine.SetPathPolicy(policy)
	engine.SetCache(query.NewCache(16))
	_, err = pool.Exec(ctx, `insert into dict(kind,id,value) values (0,9901,'/public'),(0,9902,'/[redacted]')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,path_sanitizer_version,path_policy_id)
 select 1,30,'2026-10-08','2026-10-08 12:00Z',v,s,0,p,ver,case when ver=1 then $1 else '' end
 from (values (71,701,9901,1),(71,701,9901,1),(71,701,9902,1),(71,702,9901,1),(72,703,9901,1),(99,999,9901,0)) x(v,s,p,ver)`, policy.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's existing unverified raw paths and imported lookalikes must not join this population.
	importID := insertImport(t, pool, "completed")
	insertImportedDay(t, pool, importID, "2026-10-05", 100, 100, 100, 0, 0)
	insertImportedDimension(t, pool, importID, "2026-10-05", 1, "/public", 100, 100, 100, 0, 0, 0)
	req := queryRequestRange("2026-10-05", "2026-10-08", []string{"page"}, nil)
	req.Metrics = []api.Metric{api.Visitors, api.Visits, api.Pageviews}
	check := func(result api.QueryResponse) {
		t.Helper()
		if result.Totals["visitors"] != 2 || result.Totals["visits"] != 3 || result.Totals["pageviews"] != 5 {
			t.Fatalf("totals=%v", result.Totals)
		}
		if result.Meta.Privacy == nil || result.Meta.Privacy.PolicyId != policy.Fingerprint() || result.Meta.ImportedIncluded {
			t.Fatalf("scope=%+v", result.Meta)
		}
		if len(result.Rows) != 2 {
			t.Fatalf("rows=%v", result.Rows)
		}
		for _, r := range result.Rows {
			if r["page"] == "/public" && (r["visitors"] != float64(2) || r["visits"] != float64(3) || r["pageviews"] != float64(4)) {
				t.Fatalf("public=%v", r)
			}
		}
	}
	check(mustQuery(t, engine, req))
	if _, err = closer.CloseDay(ctx, pool, []int32{1}, day("2026-10-08")); err != nil {
		t.Fatal(err)
	}
	engine.InvalidateSite(1)
	check(mustQuery(t, engine, req))
	// Complete the earlier empty date so normal closed-range caching is eligible.
	if _, err = closer.CloseDay(ctx, pool, []int32{1}, day("2026-10-05")); err != nil {
		t.Fatal(err)
	}
	engine.InvalidateSite(1)
	check(mustQuery(t, engine, req))
	cached := mustQuery(t, engine, req)
	if !cached.Meta.Cached {
		t.Fatal("verified closed response not cached")
	}
	check(cached)
	// Metadata is copied at the cache boundary and cannot poison future responses.
	cached.Meta.Privacy.PolicyId = "tampered"
	check(mustQuery(t, engine, req))
	comparison := api.PreviousPeriod
	comparedReq := req
	comparedReq.Compare = &comparison
	compared := mustQuery(t, engine, comparedReq)
	check(compared)
	if compared.Comparison == nil || compared.Comparison.Totals["pageviews"] != 0 || len(compared.Comparison.Rows) != 0 {
		t.Fatalf("comparison includes legacy/imports=%+v", compared.Comparison)
	}
	limitedReq := req
	one := 1
	limitedReq.Limit = &one
	first := mustQuery(t, engine, limitedReq)
	if len(first.Rows) != 1 || first.NextCursor == nil {
		t.Fatalf("pagination=%+v", first)
	}
	limitedReq.Cursor = first.NextCursor
	second := mustQuery(t, engine, limitedReq)
	if len(second.Rows) != 1 || second.Rows[0]["page"] == first.Rows[0]["page"] || second.Totals["pageviews"] != 5 {
		t.Fatalf("second page=%+v", second)
	}
	zone := "Asia/Kolkata"
	wrongZone := req
	wrongZone.Timezone = &zone
	if _, err := engine.Query(ctx, wrongZone); err == nil {
		t.Fatal("site-day provenance answered foreign time zone")
	}

	if _, err = pool.Exec(ctx, `delete from events where local_day='2026-10-08'`); err != nil {
		t.Fatal(err)
	}
	engine.InvalidateSite(1)
	check(mustQuery(t, engine, req))
	// Configuration changes neither expose cached old labels nor relabel old rows.
	engine.SetPathPolicy(privacy.PathPolicy{})
	restricted := mustQuery(t, engine, req)
	if len(restricted.Rows) != 0 || restricted.Totals["pageviews"] != 0 || restricted.Meta.Cached {
		t.Fatalf("old policy leaked=%+v", restricted)
	}
}

func TestVerifiedPathsRejectCorruptDictionaryPairing(t *testing.T) {
	pool, engine := setupTiny(t)
	engine.SetTrackingMode(privacy.Anonymous)
	policy, err := privacy.NewPathPolicy([]string{"/public"})
	if err != nil {
		t.Fatal(err)
	}
	engine.SetPathPolicy(policy)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into dict(kind,id,value) values(0,9903,'/private-person')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,path_sanitizer_version,path_policy_id) values(1,30,'2026-10-08','2026-10-08 12:00Z',1,1,0,9903,1,$1)`, policy.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	req := queryRequestRange("2026-10-08", "2026-10-08", []string{"page"}, nil)
	req.Metrics = []api.Metric{api.Pageviews}
	if _, err := engine.Query(ctx, req); !errors.Is(err, api.ErrAnonymousHistory) {
		t.Fatalf("corrupt marker=%v", err)
	}
}
