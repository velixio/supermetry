package query_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	panelimports "supermetry/internal/imports"
	"supermetry/internal/query"
)

func TestEngineCachesOnlyEntirelyClosedRanges(t *testing.T) {
	pool, engine := setupTinyDims(t)
	engine.SetCache(query.NewCache(16))
	closedRequest := queryRequestRange("2026-10-06", "2026-10-07", nil, nil)

	first := mustQuery(t, engine, closedRequest)
	second := mustQuery(t, engine, closedRequest)
	if first.Meta.Cached {
		t.Fatal("first closed-range response was marked cached")
	}
	if !second.Meta.Cached {
		t.Fatal("second closed-range response was not served from cache")
	}
	second.Meta.Cached = false
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("cached response differs\nfirst:  %+v\nsecond: %+v", first, second)
	}

	openRequest := queryRequestRange("2026-10-07", "2026-10-08", nil, nil)
	openFirst := mustQuery(t, engine, openRequest)
	if _, err := pool.Exec(context.Background(), `update rollup_day set pageviews=pageviews+10 where site_id=1 and day='2026-10-07'`); err != nil {
		t.Fatal(err)
	}
	openSecond := mustQuery(t, engine, openRequest)
	if openFirst.Meta.Cached || openSecond.Meta.Cached {
		t.Fatal("range touching an open day was cached")
	}
	if reflect.DeepEqual(openSecond.Totals, openFirst.Totals) {
		t.Fatal("open-range result did not observe changed storage")
	}
}

func TestEngineDoesNotCacheComparisonTouchingOpenDay(t *testing.T) {
	_, engine := setupTinyDims(t)
	engine.SetCache(query.NewCache(16))
	compare := api.PreviousPeriod
	req := queryRequestRange("2026-10-06", "2026-10-07", nil, nil)
	req.Compare = &compare

	first := mustQuery(t, engine, req)
	second := mustQuery(t, engine, req)
	if first.Meta.Cached || second.Meta.Cached {
		t.Fatal("response with an open comparison period was cached")
	}
}

func TestEngineCacheMissesAfterCloseVersionChanges(t *testing.T) {
	pool, engine := setupTinyDims(t)
	engine.SetCache(query.NewCache(16))
	req := queryRequestRange("2026-10-06", "2026-10-07", nil, nil)
	_ = mustQuery(t, engine, req)
	if got := mustQuery(t, engine, req); !got.Meta.Cached {
		t.Fatal("primed response did not hit cache")
	}

	if _, err := closer.CloseDay(context.Background(), pool, []int32{1}, day("2026-10-07")); err != nil {
		t.Fatal(err)
	}
	engine.InvalidateSite(1)
	if got := mustQuery(t, engine, req); got.Meta.Cached {
		t.Fatal("re-closing a day did not miss the old cache entry")
	}
}

func TestEngineCacheMissesAfterImportCompletes(t *testing.T) {
	pool, engine := setupTinyDims(t)
	engine.SetCache(query.NewCache(16))
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into day_close(site_id,day,status,version) values (1,'2026-10-05','done',1)`); err != nil {
		t.Fatal(err)
	}
	req := queryRequestRange("2026-10-05", "2026-10-05", nil, nil)
	if got := mustQuery(t, engine, req); got.Totals["visitors"] != 0 {
		t.Fatalf("initial visitors=%v, want 0", got.Totals["visitors"])
	}
	if got := mustQuery(t, engine, req); !got.Meta.Cached {
		t.Fatal("primed imported response did not hit cache")
	}

	importID := insertImport(t, pool, "running")
	day := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	store := panelimports.NewStore(pool, engine.InvalidateSite)
	aggregate := panelimports.Aggregate{
		Days:       map[time.Time]panelimports.Metrics{day: {Visitors: 9, Visits: 10, Pageviews: 11}},
		Dimensions: map[panelimports.DimensionKey]panelimports.Metrics{},
	}
	if err := store.Complete(ctx, importID, aggregate, panelimports.ParseStats{Rows: 1}); err != nil {
		t.Fatal(err)
	}
	got := mustQuery(t, engine, req)
	if got.Meta.Cached {
		t.Fatal("query after import completion was served from stale cache")
	}
	if got.Totals["visitors"] != 9 || got.Totals["visits"] != 10 || got.Totals["pageviews"] != 11 {
		t.Fatalf("totals after import=%v, want imported totals", got.Totals)
	}
}

func TestEngineSingleflightCallerCancellationIsIsolated(t *testing.T) {
	pool, engine := setupTinyDims(t)
	engine.SetCache(query.NewCache(16))
	req := queryRequestRange("2026-10-06", "2026-10-07", nil, nil)

	lock, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err := lock.Exec(context.Background(), `lock table rollup_day in access exclusive mode`); err != nil {
		t.Fatal(err)
	}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()
	type result struct {
		response api.QueryResponse
		err      error
	}
	results := make(chan result, 2)
	var started sync.WaitGroup
	started.Add(1)
	go func() {
		started.Done()
		response, err := engine.Query(leaderCtx, req)
		results <- result{response: response, err: err}
	}()
	started.Wait()
	time.Sleep(50 * time.Millisecond)
	go func() {
		response, err := engine.Query(context.Background(), req)
		results <- result{response: response, err: err}
	}()
	time.Sleep(50 * time.Millisecond)
	cancelLeader()
	if err := lock.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	var canceled, succeeded bool
	for range 2 {
		got := <-results
		if got.err == context.Canceled {
			canceled = true
			continue
		}
		if got.err != nil {
			t.Fatalf("query error=%v, want one cancellation and one success", got.err)
		}
		succeeded = true
	}
	if !canceled || !succeeded {
		t.Fatalf("canceled=%t succeeded=%t, want both true", canceled, succeeded)
	}
}

func TestEngineDoesNotCacheErrors(t *testing.T) {
	_, engine := setupTinyDims(t)
	engine.SetCache(query.NewCache(16))
	req := queryRequestRange("2026-10-06", "2026-10-07", nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Query(ctx, req); err == nil {
		t.Fatal("Query() with canceled context succeeded")
	}
	if got := mustQuery(t, engine, req); got.Meta.Cached {
		t.Fatal("response after an errored query was served from cache")
	}
	if got := mustQuery(t, engine, req); !got.Meta.Cached {
		t.Fatal("successful response was not cached")
	}
}

func TestRecomputeRollupsAdvancesCloseVersion(t *testing.T) {
	pool, _ := setupTinyDims(t)
	ctx := context.Background()
	var before int64
	if err := pool.QueryRow(ctx, `select sum(version) from day_close where site_id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := closer.RecomputeRollups(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := pool.QueryRow(ctx, `select sum(version) from day_close where site_id=1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Fatalf("close version sum after recompute=%d, want > %d", after, before)
	}
}

func TestRollupBackfillAdvancesVersionAndInvalidatesSite(t *testing.T) {
	pool, _ := setupTinyDims(t)
	ctx := context.Background()
	var before int64
	if err := pool.QueryRow(ctx, `select sum(version) from day_close where site_id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	invalidated := make(chan int32, 4)
	backfill := closer.NewRollupBackfill(pool, query.CurrentRollupLayout(), nil, func(siteID int32) {
		invalidated <- siteID
	})
	if _, err := backfill.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := pool.QueryRow(ctx, `select sum(version) from day_close where site_id=1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Fatalf("close version sum after backfill=%d, want > %d", after, before)
	}
	select {
	case siteID := <-invalidated:
		if siteID != 1 {
			t.Fatalf("invalidated site=%d, want 1", siteID)
		}
	default:
		t.Fatal("backfill did not invalidate the recomputed site")
	}
}

func TestEngineDoesNotCacheToday(t *testing.T) {
	_, engine := setupTinyDims(t)
	engine.SetCache(query.NewCache(16))
	today := time.Now().UTC().Format(time.DateOnly)
	req := queryRequestRange(today, today, nil, nil)
	if first, second := mustQuery(t, engine, req), mustQuery(t, engine, req); first.Meta.Cached || second.Meta.Cached {
		t.Fatal("today's response was cached")
	}
}

func TestEngineDoesNotCacheHotTierForClosedDates(t *testing.T) {
	_, engine := setupTinyDims(t)
	engine.SetCache(query.NewCache(16))
	req := queryRequestRange("2026-10-06", "2026-10-07", []string{"hour"}, nil)
	if first, second := mustQuery(t, engine, req), mustQuery(t, engine, req); first.Meta.Cached || second.Meta.Cached {
		t.Fatal("hot-tier response for closed dates was cached")
	}
}
