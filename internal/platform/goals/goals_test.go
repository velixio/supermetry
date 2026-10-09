package goals

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestGoalsAndFunnelReadCachedDefinitions(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Test', 'test') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id, public_id, name, allowed_hosts, timezone, retention_class)
		values ($1, 'abcdefghijkl', 'Blog', array['example.com'], 'UTC', 30)
		returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into goals(site_id, kind, match, value, prop_key, prop_value, name, currency)
		values ($1, 'event', 'exact', 'signup', 'plan', 'pro', 'Paid signup', 'USD')`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into goals(site_id, kind, match, value, name, scroll_threshold)
		values ($1, 'scroll', 'wildcard', '/docs/*', 'Read docs', 75)`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into funnels(site_id, name, steps)
		values ($1, 'Signup', '[{"kind":"page","match":"exact","value":"/pricing"},{"kind":"event","match":"exact","value":"signup"}]')`, siteID); err != nil {
		t.Fatal(err)
	}

	service := New(&store.Store{Pool: pool}, time.Hour)
	t.Cleanup(service.Close)
	got := service.Goals(siteID)
	if len(got) != 2 || got[0].Name != "Paid signup" || got[0].PropKey != "plan" || got[0].Currency != "USD" || got[1].ScrollThreshold != 75 {
		t.Fatalf("Goals() = %+v", got)
	}
	funnel, ok := service.Funnel(siteID, 1)
	if !ok || funnel.Name != "Signup" || len(funnel.Steps) != 2 || funnel.Steps[1].Value != "signup" {
		t.Fatalf("Funnel() = (%+v, %t)", funnel, ok)
	}

	got[0].Name = "mutated"
	funnel.Steps[0].Value = "mutated"
	if service.Goals(siteID)[0].Name != "Paid signup" {
		t.Fatal("Goals returned mutable cache storage")
	}
	again, _ := service.Funnel(siteID, funnel.ID)
	if again.Steps[0].Value != "/pricing" {
		t.Fatal("Funnel returned mutable cache storage")
	}
}

func TestInvalidateReloadsGoalCacheAndHidesDeletedSites(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Test', 'test') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id, public_id, name, allowed_hosts, timezone, retention_class)
		values ($1, 'abcdefghijkl', 'Blog', array['example.com'], 'UTC', 30)
		returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	service := New(&store.Store{Pool: pool}, time.Hour)
	t.Cleanup(service.Close)
	if len(service.Goals(siteID)) != 0 {
		t.Fatal("new site unexpectedly has goals")
	}
	if _, err := pool.Exec(ctx, `insert into goals(site_id, kind, match, value, name) values ($1, 'page', 'prefix', '/docs', 'Docs')`, siteID); err != nil {
		t.Fatal(err)
	}
	if err := service.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if got := service.Goals(siteID); len(got) != 1 || got[0].Value != "/docs" {
		t.Fatalf("Goals after invalidation = %+v", got)
	}
	if _, err := pool.Exec(ctx, `update sites set deleted_at = now() where id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	if err := service.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if got := service.Goals(siteID); len(got) != 0 {
		t.Fatalf("deleted site's goals = %+v", got)
	}
}
