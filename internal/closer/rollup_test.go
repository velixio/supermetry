package closer_test

import (
	"context"
	"testing"

	"supermetry/internal/closer"
	"supermetry/internal/testpg"
)

func TestRecomputeRollupsBackfillsEngagedSessionsAndAggregates(t *testing.T) {
	pool, _ := testpg.New(t)
	loadFixture(t, pool)
	ctx := context.Background()
	for _, day := range fixtureDays() {
		if _, err := closer.CloseDay(ctx, pool, []int32{1}, day); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
update rollup_day_dim set engaged_n=0;
update rollup_month_dim set engaged_n=0;
update rollup_block_dim set engaged_n=0;
insert into rollup_month_done(site_id,month) values (1,'2026-10-01')
on conflict do nothing`); err != nil {
		t.Fatal(err)
	}

	var before int64
	if err := pool.QueryRow(ctx, `
select engaged_n from rollup_day_dim
where site_id=1 and day='2026-10-07' and dim=1 and value_id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("engaged_n before recompute = %d, want migration default 0", before)
	}

	days, err := closer.RecomputeRollups(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if days != 1 {
		t.Fatalf("recomputed days = %d, want 1 retained closed day", days)
	}

	var daily int64
	if err := pool.QueryRow(ctx, `
select engaged_n from rollup_day_dim
where site_id=1 and day='2026-10-07' and dim=1 and value_id=1`).Scan(&daily); err != nil {
		t.Fatal(err)
	}
	if daily != 1 {
		t.Fatalf("day engaged_n = %d, want 1 distinct engaged session", daily)
	}
	assertEqualQuery(t, pool,
		`select coalesce(sum(engaged_n),0) from rollup_month_dim where site_id=1 and month='2026-10-01' and dim=1 and value_id=1`,
		`select coalesce(sum(engaged_n),0) from rollup_day_dim where site_id=1 and day between '2026-10-01' and '2026-10-31' and dim=1 and value_id=1`)
	assertEqualQuery(t, pool,
		`select coalesce(sum(engaged_n),0) from rollup_block_dim where site_id=1 and k=1 and start_day='2026-10-06' and dim=1 and value_id=1`,
		`select coalesce(sum(engaged_n),0) from rollup_day_dim where site_id=1 and day between '2026-10-06' and '2026-10-07' and dim=1 and value_id=1`)
}
