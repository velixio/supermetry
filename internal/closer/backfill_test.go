package closer_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/closer"
	"panel/internal/db"
	"panel/internal/testpg"
)

var (
	legacyRollupDimensions  = []int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	currentRollupDimensions = []int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}
	currentLayout           = closer.NewRollupLayout(currentRollupDimensions, closer.RollupCounterColumns())
	legacyLayout            = closer.NewRollupLayout(legacyRollupDimensions, closer.RollupCounterColumns())
)

func storeRollupLayout(t *testing.T, pool *pgxpool.Pool, layout closer.RollupLayout, done int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		insert into rollup_state(singleton, layout_version, dimension_set, counter_set,
		  days_done, total_days, started_at, finished_at)
		values (true, $1, $2, $3, $4, $4, now(), now())`,
		layout.Version(), layout.Dimensions, layout.Counters, done); err != nil {
		t.Fatal(err)
	}
}

func TestRollupBackfillDimensionChangeRebuildsOnlyNewDimensions(t *testing.T) {
	pool, _ := testpg.New(t)
	closeBackfillFixture(t, pool)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		create table expected_new_rollups as
		select * from rollup_day_dim where dim = any($1::int2[])`, []int16{12, 13, 14, 15, 16, 17, 18}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from rollup_day_dim where dim = any($1::int2[])`, []int16{12, 13, 14, 15, 16, 17, 18}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update rollup_day_dim set visitors=999 where site_id=1 and day='2026-10-07' and dim=2`); err != nil {
		t.Fatal(err)
	}
	storeRollupLayout(t, pool, legacyLayout, 0)

	backfill := closer.NewRollupBackfill(pool, currentLayout, nil)
	progress, err := backfill.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Running || progress.DaysDone != 2 || progress.TotalDays != 2 {
		t.Fatalf("progress = %+v, want finished 2/2", progress)
	}

	var changed, difference int
	if err := pool.QueryRow(ctx, `select visitors from rollup_day_dim where site_id=1 and day='2026-10-07' and dim=2`).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		select count(*) from (
		  (select * from expected_new_rollups except select * from rollup_day_dim where dim=any($1::int2[]))
		  union all
		  (select * from rollup_day_dim where dim=any($1::int2[]) except select * from expected_new_rollups)
		) differences`, []int16{12, 13, 14, 15, 16, 17, 18}).Scan(&difference); err != nil {
		t.Fatal(err)
	}
	if changed != 999 {
		t.Fatalf("existing dimension visitors = %d, want preserved sentinel 999", changed)
	}
	if difference != 0 {
		t.Fatalf("selective backfill differs from full recompute by %d rows", difference)
	}
}

func TestRollupBackfillCounterChangeRunsFullRecompute(t *testing.T) {
	pool, _ := testpg.New(t)
	closeBackfillFixture(t, pool)
	ctx := context.Background()
	tables := []string{"rollup_day", "rollup_day_dim", "rollup_month_dim", "rollup_block_dim"}
	for _, table := range tables {
		if _, err := pool.Exec(ctx, `create table expected_`+table+` as select * from `+table); err != nil {
			t.Fatal(err)
		}
	}
	// Days closed before the engaged_n counter existed keep its default 0;
	// other counters may also be stale, so every row must be rebuilt.
	if _, err := pool.Exec(ctx, `
		update rollup_day_dim set engaged_n=0;
		update rollup_day_dim set visitors=999 where site_id=1 and dim=2;
		update rollup_day set pageviews=999 where site_id=2`); err != nil {
		t.Fatal(err)
	}
	withoutEngaged := closer.NewRollupLayout(currentRollupDimensions,
		slices.DeleteFunc(closer.RollupCounterColumns(), func(c string) bool { return c == "engaged_n" }))
	storeRollupLayout(t, pool, withoutEngaged, 0)

	progress, err := closer.NewRollupBackfill(pool, currentLayout, nil).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Running || progress.DaysDone != 2 || progress.TotalDays != 2 {
		t.Fatalf("progress = %+v, want finished 2/2", progress)
	}
	for _, table := range tables {
		var difference int
		if err := pool.QueryRow(ctx, `
			select count(*) from (
			  (select * from expected_`+table+` except select * from `+table+`)
			  union all
			  (select * from `+table+` except select * from expected_`+table+`)
			) differences`).Scan(&difference); err != nil {
			t.Fatal(err)
		}
		if difference != 0 {
			t.Fatalf("%s differs from a full recompute by %d rows", table, difference)
		}
	}
	// Hand-computed: site 1 viewed paths 1 and 2, site 2 path 3, each with
	// positive engagement, so each path row counts one engaged session.
	var engaged, pathRows int
	if err := pool.QueryRow(ctx, `
		select coalesce(sum(engaged_n),0), count(*) from rollup_day_dim where dim=1`).Scan(&engaged, &pathRows); err != nil {
		t.Fatal(err)
	}
	if engaged != 3 || pathRows != 3 {
		t.Fatalf("path engaged_n=%d rows=%d, want 3 and 3", engaged, pathRows)
	}
	var version string
	if err := pool.QueryRow(ctx, `select layout_version from rollup_state where singleton and pending_layout_version is null`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentLayout.Version() {
		t.Fatalf("stored version = %q, want current %q", version, currentLayout.Version())
	}
}

func TestRollupBackfillResumesAfterInterruption(t *testing.T) {
	pool, _ := testpg.New(t)
	closeBackfillFixture(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `delete from rollup_day_dim where dim >= 12`); err != nil {
		t.Fatal(err)
	}
	storeRollupLayout(t, pool, legacyLayout, 0)

	pauses := 0
	interrupted := closer.NewRollupBackfill(pool, currentLayout, func(context.Context) error {
		pauses++
		return context.Canceled
	})
	progress, err := interrupted.Run(ctx)
	if err != nil {
		t.Fatalf("cancelled backfill: %v", err)
	}
	if progress.DaysDone != 1 || !progress.Running || pauses != 1 {
		t.Fatalf("interrupted progress=%+v pauses=%d, want running 1/2 and one pause", progress, pauses)
	}

	resumed := closer.NewRollupBackfill(pool, currentLayout, nil)
	progress, err = resumed.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Running || progress.DaysDone != 2 || progress.TotalDays != 2 {
		t.Fatalf("resumed progress = %+v, want finished 2/2", progress)
	}
	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from rollup_day_dim where day='2026-10-07' and dim=12`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("browser-version rows after resume = %d, want one per site", rows)
	}
}

func TestRollupBackfillNoOpWhenDimensionSetIsUnchanged(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	storeRollupLayout(t, pool, currentLayout, 7)
	backfill := closer.NewRollupBackfill(pool, currentLayout, func(context.Context) error {
		t.Fatal("unchanged dimension set performed work")
		return nil
	})

	progress, err := backfill.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Running || progress.DaysDone != 7 || progress.TotalDays != 7 {
		t.Fatalf("progress = %+v, want preserved finished 7/7", progress)
	}
}

func TestRollupBackfillFreshInstallStoresDimensionSetWithoutWork(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	backfill := closer.NewRollupBackfill(pool, currentLayout, nil)

	progress, err := backfill.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if progress.Running || progress.DaysDone != 0 || progress.TotalDays != 0 {
		t.Fatalf("progress = %+v, want finished 0/0", progress)
	}
	var stored []int16
	var counters []string
	var version string
	if err := pool.QueryRow(context.Background(), `
		select dimension_set, counter_set, layout_version from rollup_state where singleton`).Scan(
		&stored, &counters, &version); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, currentRollupDimensions) || !reflect.DeepEqual(counters, currentLayout.Counters) {
		t.Fatalf("stored layout = %v %v, want %+v", stored, counters, currentLayout)
	}
	if version != currentLayout.Version() {
		t.Fatalf("stored layout version = %q, want %q", version, currentLayout.Version())
	}
}

func TestRollupBackfillShutdownCancelsPauseCleanly(t *testing.T) {
	pool, _ := testpg.New(t)
	closeBackfillFixture(t, pool)
	ctx, cancel := context.WithCancel(context.Background())
	backfill := closer.NewRollupBackfill(pool, currentLayout, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})

	progress, err := backfill.Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown error = %v, want nil or context cancellation", err)
	}
	if progress.DaysDone != 1 || !progress.Running {
		t.Fatalf("shutdown progress = %+v, want resumable 1/2", progress)
	}
}

func TestRollupBackfillJobRunsOnStartup(t *testing.T) {
	job := closer.NewRollupBackfill(nil, currentLayout, nil).Job()
	if job.Name != "rollup-backfill" || !job.RunOnStart || job.Every != 5*time.Minute {
		t.Fatalf("job = %+v, want startup rollup-backfill every five minutes", job)
	}
}

func closeBackfillFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		create table sessions_closed_202610 partition of sessions_closed
		  for values from ('2026-10-01') to ('2026-11-01');
		create table rollup_day_202610 partition of rollup_day
		  for values from ('2026-10-01') to ('2026-11-01');
		create table rollup_day_dim_202610 partition of rollup_day_dim
		  for values from ('2026-10-01') to ('2026-11-01');
		insert into sessions_closed (
		  site_id, day, visitor_id, session_id, started_at,
		  source_id, utm_source_id, medium_id, campaign_id, country,
		  region_id, city_id, browser_id, os_id, browser_ver_id, os_ver_id,
		  language_id, hostname_id, screen_bucket, device, entry_path, exit_path,
		  pageviews, custom, duration_ms, engagement_ms, vd_n, flags,
		  seq, seq_engage, seq_scroll
		) values
		  (1,'2026-10-07',101,1001,'2026-10-07 09:00Z',
		   10,11,12,13,'IN',61,71,20,30,21,31,41,51,2,1,1,2,
		   2,0,1000,500,1,255,'{7,11}','{100,200}','{25,80}'),
		  (2,'2026-10-07',201,2001,'2026-10-07 10:00Z',
		   110,111,112,113,'US',161,171,120,130,121,131,141,151,3,2,3,3,
		   1,0,2000,600,1,255,'{15}','{300}','{50}');
		insert into day_close(site_id,day,status,version,closed_at,rows,duration_ms)
		values (1,'2026-10-07','done',1,now(),1,1),
		       (2,'2026-10-07','done',1,now(),1,1)`); err != nil {
		t.Fatal(err)
	}
	if days, err := closer.RecomputeRollups(ctx, pool); err != nil {
		t.Fatal(err)
	} else if days != 1 {
		t.Fatalf("fixture full recompute days = %d, want 1", days)
	}
}
