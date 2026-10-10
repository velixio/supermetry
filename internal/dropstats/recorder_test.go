package dropstats_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"supermetry/internal/db"
	"supermetry/internal/dropstats"
	"supermetry/internal/testpg"
)

func TestRecorderFlushPersistsCountsForANewReader(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	insertSite(t, pool, 7)
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	recorder := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	recorder.Record(7, day, dropstats.BotUserAgent, "Googlebot\\/")
	recorder.Record(7, day, dropstats.BotUserAgent, "Googlebot\\/")
	recorder.Record(7, day, dropstats.RateLimitedSite, "")

	if err := recorder.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	reader := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	rows, err := reader.Read(t.Context(), []dropstats.SiteDay{{SiteID: 7, Today: day}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Today[dropstats.BotUserAgent] != 2 || rows[0].Today[dropstats.RateLimitedSite] != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if len(rows[0].TopBotRules) != 1 || rows[0].TopBotRules[0].Rule != "Googlebot\\/" || rows[0].TopBotRules[0].Count != 2 {
		t.Fatalf("top bot rules = %+v", rows[0].TopBotRules)
	}
}

func TestRecorderSkipsDeletedSitesInsteadOfBlockingOthers(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	insertSite(t, pool, 7)
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	recorder := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	recorder.Record(404, day, dropstats.BotUserAgent, "Gone\\/")
	recorder.Record(7, day, dropstats.RateLimitedSite, "")

	if err := recorder.Flush(t.Context()); err != nil {
		t.Fatalf("a site missing from the sites table must not fail the flush: %v", err)
	}
	if err := recorder.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := recorder.Read(t.Context(), []dropstats.SiteDay{{SiteID: 7, Today: day}, {SiteID: 404, Today: day}})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range rows {
		if row.SiteID == 404 && (row.Today[dropstats.BotUserAgent] != 0 || len(row.TopBotRules) != 0) {
			t.Fatalf("deleted site was persisted: %+v", row)
		}
		if row.SiteID == 7 && row.Today[dropstats.RateLimitedSite] == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the existing site's count was lost: %+v", rows)
	}
}

func TestRecorderRunFlushesOnShutdown(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	insertSite(t, pool, 9)
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	recorder := dropstats.New(pool, dropstats.Options{MaxSites: 10, FlushEvery: time.Hour})
	recorder.Record(9, day, dropstats.PrivacySignal, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- recorder.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rows, err := recorder.Read(t.Context(), []dropstats.SiteDay{{SiteID: 9, Today: day}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Today[dropstats.PrivacySignal] != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

func insertSite(t *testing.T, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, siteID int32) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `insert into orgs(id,name,slug) overriding system value values(1,'Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values($1,1,$2,'Site','UTC',30)`, siteID, fmt.Sprintf("site%08d", siteID)); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderBoundsTrackedSites(t *testing.T) {
	recorder := dropstats.New(nil, dropstats.Options{MaxSites: 2})
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if !recorder.Record(1, day, dropstats.ClockSkew, "") || !recorder.Record(2, day, dropstats.ClockSkew, "") {
		t.Fatal("first two sites were not recorded")
	}
	if recorder.Record(3, day, dropstats.ClockSkew, "") {
		t.Fatal("third site exceeded the configured bound")
	}
	if got := recorder.TrackedSites(); got != 2 {
		t.Fatalf("TrackedSites() = %d, want 2", got)
	}
}

func TestRecorderSteadyStateDoesNotAllocate(t *testing.T) {
	recorder := dropstats.New(nil, dropstats.Options{MaxSites: 2})
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	recorder.Record(1, day, dropstats.PrivacySignal, "")
	if allocations := testing.AllocsPerRun(1000, func() {
		recorder.Record(1, day, dropstats.PrivacySignal, "")
	}); allocations != 0 {
		t.Fatalf("steady-state Record allocations = %v, want 0", allocations)
	}
}

func TestRecorderCountsConcurrentDropsForOneSite(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	insertSite(t, pool, 7)
	recorder := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	const goroutines, records = 32, 1_000
	var workers sync.WaitGroup
	for range goroutines {
		workers.Go(func() {
			for range records {
				if !recorder.Record(7, day, dropstats.PrivacySignal, "") {
					t.Error("Record rejected a tracked site and day")
					return
				}
			}
		})
	}
	workers.Wait()
	if err := recorder.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := pool.QueryRow(t.Context(), `select count from site_drop_daily where site_id=7 and day=$1 and reason='privacy_signal'`, day).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != goroutines*records {
		t.Fatalf("count=%d, want %d", count, goroutines*records)
	}
}

func TestRecorderBoundsBotRulesIncludingOverflow(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	insertSite(t, pool, 7)
	recorder := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for index := range 25 {
		recorder.Record(7, day, dropstats.BotUserAgent, fmt.Sprintf("rule-%d", index))
	}
	if err := recorder.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var rows int
	var overflow int64
	if err := pool.QueryRow(t.Context(), `select count(*),coalesce(sum(count) filter(where rule='other'),0) from site_bot_rule_daily`).Scan(&rows, &overflow); err != nil {
		t.Fatal(err)
	}
	if rows != 20 || overflow != 6 {
		t.Fatalf("rules=%d overflow=%d, want 20/6", rows, overflow)
	}
}

func TestRecorderPromotesFrequentBotRuleIntoBoundedTopRules(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	insertSite(t, pool, 7)
	recorder := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for index := range 19 {
		recorder.Record(7, day, dropstats.BotUserAgent, fmt.Sprintf("one-off-%d", index))
	}
	for range 50 {
		recorder.Record(7, day, dropstats.BotUserAgent, "dominant-rule")
	}
	if err := recorder.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := pool.QueryRow(t.Context(), `select count from site_bot_rule_daily where site_id=7 and rule='dominant-rule'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 50 {
		t.Fatalf("dominant rule count=%d, want 50", count)
	}
}

func TestRecorderBoundsBotRulesAcrossFlushes(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	insertSite(t, pool, 7)
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	first := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	for index := range 19 {
		first.Record(7, day, dropstats.BotUserAgent, fmt.Sprintf("first-%d", index))
	}
	if err := first.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	second := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	second.Record(7, day, dropstats.BotUserAgent, "after-restart-1")
	second.Record(7, day, dropstats.BotUserAgent, "after-restart-2")
	if err := second.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var rows int
	var overflow int64
	if err := pool.QueryRow(t.Context(), `select count(*),coalesce(sum(count) filter(where rule='other'),0) from site_bot_rule_daily`).Scan(&rows, &overflow); err != nil {
		t.Fatal(err)
	}
	if rows != 20 || overflow != 2 {
		t.Fatalf("rules=%d overflow=%d, want 20/2", rows, overflow)
	}
}

func TestRecorderPromotesFrequentBotRuleAcrossRestart(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	insertSite(t, pool, 7)
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	first := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	for index := range 19 {
		first.Record(7, day, dropstats.BotUserAgent, fmt.Sprintf("one-off-%d", index))
	}
	if err := first.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	second := dropstats.New(pool, dropstats.Options{MaxSites: 10})
	for range 50 {
		second.Record(7, day, dropstats.BotUserAgent, "dominant-after-restart")
	}
	if err := second.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var rows int
	var dominant, overflow int64
	if err := pool.QueryRow(t.Context(), `select count(*),coalesce(sum(count) filter(where rule='dominant-after-restart'),0),coalesce(sum(count) filter(where rule='other'),0) from site_bot_rule_daily`).Scan(&rows, &dominant, &overflow); err != nil {
		t.Fatal(err)
	}
	if rows != 20 || dominant != 50 || overflow != 1 {
		t.Fatalf("rules=%d dominant=%d overflow=%d, want 20/50/1", rows, dominant, overflow)
	}
}

func TestSpike(t *testing.T) {
	tests := []struct {
		name     string
		today    int64
		previous int64
		want     bool
	}{
		{name: "minimum and five times average", today: 100, previous: 140, want: true},
		{name: "below minimum", today: 99, previous: 0, want: false},
		{name: "below multiplier", today: 100, previous: 141, want: false},
		{name: "zero baseline", today: 100, previous: 0, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dropstats.Spike(tt.today, tt.previous); got != tt.want {
				t.Fatalf("Spike(%d, %d) = %v, want %v", tt.today, tt.previous, got, tt.want)
			}
		})
	}
}
