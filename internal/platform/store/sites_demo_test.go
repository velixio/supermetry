package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestCreateDemoSiteOnePerOrg(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	orgID, userID := createDemoTestOrg(t, pool)
	created := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := store.CreateDemoSite(ctx, tx, store.Site{
		OrgID: orgID, Name: "Demo project", Timezone: "UTC", RetentionClass: 90,
		AllowedHosts: []string{"www.demo.example", "docs.demo.example"},
	}, created)
	if err != nil || !s1.IsDemo {
		t.Fatalf("create: %v %+v", err, s1)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var demoStartedAt time.Time
	if err := pool.QueryRow(ctx, `select demo_started_at from sites where id=$1`, s1.ID).Scan(&demoStartedAt); err != nil {
		t.Fatal(err)
	}
	if demoStartedAt.IsZero() {
		t.Fatal("demo_started_at was not set")
	}

	var profilesEnabled, heatmapsEnabled, replayEnabled bool
	var replayRate int
	var currency, currencyState string
	if err := pool.QueryRow(ctx, `
		select settings->'profiles_enabled' = 'true'::jsonb,
		       settings->'heatmaps_enabled' = 'true'::jsonb,
		       settings->'replay'->'enabled' = 'true'::jsonb,
		       (settings->'replay'->>'sample_rate')::integer,
		       revenue_currency::text, revenue_currency_state
		from sites where id=$1`, s1.ID).Scan(
		&profilesEnabled, &heatmapsEnabled, &replayEnabled, &replayRate, &currency, &currencyState,
	); err != nil {
		t.Fatal(err)
	}
	if !profilesEnabled || !heatmapsEnabled || !replayEnabled || replayRate != 100 || currency != "USD" || currencyState != "configured" {
		t.Fatalf("settings profiles=%v heatmaps=%v replay=%v/%d currency=%s/%s", profilesEnabled, heatmapsEnabled, replayEnabled, replayRate, currency, currencyState)
	}

	data := &store.Store{Pool: pool}
	active, err := store.ActiveSites(ctx, data)
	if err != nil || len(active) != 1 || !active[0].IsDemo {
		t.Fatalf("active sites: %v %+v", err, active)
	}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	registered, ok := registry.ByPublicID(s1.PublicID)
	if !ok || !registered.Demo {
		t.Fatalf("registered demo: ok=%v site=%+v", ok, registered)
	}
	got, ok, err := store.DemoSiteForOrg(ctx, pool, orgID)
	if err != nil || !ok || got.ID != s1.ID {
		t.Fatalf("lookup: %v %v %+v", err, ok, got)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateDemoSite(ctx, tx, store.Site{OrgID: orgID, Name: "Demo project", Timezone: "UTC", RetentionClass: 90}, created)
	if !errors.Is(err, store.ErrDemoExists) {
		t.Fatalf("second demo: got %v, want ErrDemoExists", err)
	}
	_ = tx.Rollback(ctx)

	dismissedAt := created.Add(time.Hour)
	if err := store.DismissDemoPrompt(ctx, pool, userID, dismissedAt); err != nil {
		t.Fatal(err)
	}
	dismissed, err := store.DemoPromptDismissed(ctx, pool, userID)
	if err != nil || !dismissed {
		t.Fatalf("prompt dismissed: %v %v", dismissed, err)
	}

	tx = mustDemoTx(t, pool)
	if err := store.SoftDeleteSite(ctx, tx, orgID, s1.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	if err := pool.QueryRow(ctx, `select deleted_at is not null from sites where id=$1`, s1.ID).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("soft delete: deleted=%v err=%v", deleted, err)
	}
	tx = mustDemoTx(t, pool)
	if _, err := store.CreateDemoSite(ctx, tx, store.Site{OrgID: orgID, Name: "Demo project", Timezone: "UTC", RetentionClass: 90}, created); err != nil {
		t.Fatalf("create after delete: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredDemoSites(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	orgID, _ := createDemoTestOrg(t, pool)
	var otherOrgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Other expiry','other-expiry') returning id`).Scan(&otherOrgID); err != nil {
		t.Fatal(err)
	}
	var legacyOrgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Legacy expiry','legacy-expiry') returning id`).Scan(&legacyOrgID); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	var expiredID, youngID, deletedID, realID int32
	for _, item := range []struct {
		publicID string
		isDemo   bool
		start    time.Time
		deleted  bool
		orgID    int64
		id       *int32
	}{
		{publicID: "expired-demo", isDemo: true, start: started, orgID: orgID, id: &expiredID},
		{publicID: "young-demo01", isDemo: true, start: started.Add(2 * time.Hour), orgID: otherOrgID, id: &youngID},
		{publicID: "deleted-demo", isDemo: true, start: started, deleted: true, orgID: orgID, id: &deletedID},
		{publicID: "real-site001", start: started, orgID: orgID, id: &realID},
	} {
		if err := pool.QueryRow(ctx, `
			insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,demo_started_at,deleted_at)
			values ($1,$2,'Expiry test','UTC',90,$3,$4::timestamptz,case when $5 then $4::timestamptz else null end)
			returning id`, item.orgID, item.publicID, item.isDemo, item.start, item.deleted).Scan(item.id); err != nil {
			t.Fatal(err)
		}
	}
	var legacyID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,created_at,demo_started_at)
		values ($1,'legacy-demo1','Legacy demo','UTC',90,true,$2,null)
		returning id`, legacyOrgID, started).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}

	got, err := store.ExpiredDemoSites(ctx, pool, started.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SiteID != expiredID || got[0].OrgID != orgID || got[1].SiteID != legacyID || got[1].OrgID != legacyOrgID {
		t.Fatalf("expired demos = %+v, want sites %d and %d", got, expiredID, legacyID)
	}
}

func TestDemoStartedAtDefaultsToNow(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	orgID, _ := createDemoTestOrg(t, pool)
	var startedAt *time.Time
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,is_demo)
		values ($1,'default-demo','Demo','UTC',90,true)
		returning demo_started_at`, orgID).Scan(&startedAt); err != nil {
		t.Fatal(err)
	}
	if startedAt == nil {
		t.Fatal("demo_started_at default is null")
	}
}

func TestDemoStateUpdatesAndQueries(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	orgID, _ := createDemoTestOrg(t, pool)
	created := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	tx := mustDemoTx(t, pool)
	site, err := store.CreateDemoSite(ctx, tx, store.Site{OrgID: orgID, Name: "Demo project", Timezone: "UTC", RetentionClass: 90}, created)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	unseeded, err := store.UnseededDemoSites(ctx, pool)
	if err != nil || len(unseeded) != 1 || unseeded[0] != site.ID {
		t.Fatalf("unseeded: %v %+v", err, unseeded)
	}
	seededAt := created.Add(90 * 24 * time.Hour)
	liveThrough := seededAt.Add(10 * time.Minute)
	viewedAt := seededAt.Add(20 * time.Minute)
	if err := store.SetDemoSeeded(ctx, pool, site.ID, seededAt); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDemoLiveThrough(ctx, pool, site.ID, liveThrough); err != nil {
		t.Fatal(err)
	}
	if err := store.TouchDemoViewed(ctx, pool, site.ID, viewedAt); err != nil {
		t.Fatal(err)
	}
	states, err := store.ActiveDemoSites(ctx, pool, viewedAt.Add(-time.Minute))
	if err != nil || len(states) != 1 || states[0].SiteID != site.ID || states[0].SeededAt == nil || !states[0].SeededAt.Equal(seededAt) || states[0].LiveThrough == nil || !states[0].LiveThrough.Equal(liveThrough) {
		t.Fatalf("active demo states: %v %+v", err, states)
	}
	states, err = store.ActiveDemoSites(ctx, pool, viewedAt.Add(time.Minute))
	if err != nil || len(states) != 0 {
		t.Fatalf("inactive demo states: %v %+v", err, states)
	}
	if err := store.SetDemoSeeded(ctx, pool, -1, seededAt); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing demo seed update: got %v, want ErrNoRows", err)
	}
}

func createDemoTestOrg(t *testing.T, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	tx := mustDemoTx(t, pool)
	userID, err := store.CreateUser(ctx, tx, "owner@demo.example", "Owner", "hash", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	orgID, err := store.CreateOrg(ctx, tx, "Demo test", userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return orgID, userID
}

func mustDemoTx(t *testing.T, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
