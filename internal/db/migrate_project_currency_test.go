package db

import (
	"context"
	"testing"
	"time"

	"panel/internal/testpg"
)

func TestProjectCurrencyMigrationClassifiesLegacySitesWithoutRewritingRevenue(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.version < 30 {
			if err := applyMigration(ctx, conn, migration); err != nil {
				conn.Release()
				t.Fatal(err)
			}
		}
	}
	conn.Release()
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if err := EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	for i, publicID := range []string{"currenc00001", "currenc00002", "currenc00003", "currenc00004", "currenc00005", "currenc00006", "currenc00007", "currenc00008", "currenc00009"} {
		if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class) values(1,$1,$2,array['example.test'],'UTC',30)`, publicID, publicID); err != nil {
			t.Fatalf("site %d: %v", i+1, err)
		}
	}
	insertEvent := func(siteID int, currency string, visitor int64) {
		t.Helper()
		if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,revenue_micros,revenue_currency) values($1,30,$2::date,$2::date,$3,$3,1,1000000,$4)`, siteID, day, visitor, currency); err != nil {
			t.Fatal(err)
		}
	}
	insertClosed := func(siteID int, currency string, visitor int64) {
		t.Helper()
		if _, err := pool.Exec(ctx, `insert into custom_closed(site_id,day,visitor_id,name_id,path_id,revenue_micros,revenue_currency) values($1,$2,$3,1,1,2000000,$4)`, siteID, day, visitor, currency); err != nil {
			t.Fatal(err)
		}
	}
	insertEvent(1, "USD", 1)
	insertEvent(2, "USD", 2)
	insertClosed(2, "USD", 2)
	insertEvent(3, "USD", 3)
	insertClosed(3, "EUR", 3)
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,revenue_micros,revenue_currency) values(7,30,$1::date,$1::date,7,7,1,1000000,null)`, day); err != nil {
		t.Fatal(err)
	}
	insertEvent(8, "usd", 8)
	if _, err := pool.Exec(ctx, `insert into goals(site_id,kind,match,value,name,currency) values
		(4,'event','exact','buy','Buy','GBP'),
		(6,'event','exact','buy','Buy','USD'),
		(6,'event','exact','renew','Renew','EUR'),
		(9,'event','exact','buy','Buy','usd')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	want := []struct{ currency, state string }{{"USD", "configured"}, {"USD", "configured"}, {"", "conflict"}, {"GBP", "configured"}, {"", "unconfigured"}, {"", "conflict"}, {"", "conflict"}, {"", "conflict"}, {"", "conflict"}}
	for id, expected := range want {
		var currency, state string
		if err := pool.QueryRow(ctx, `select coalesce(revenue_currency::text,''),revenue_currency_state from sites where id=$1`, id+1).Scan(&currency, &state); err != nil {
			t.Fatal(err)
		}
		if currency != expected.currency || state != expected.state {
			t.Errorf("site %d = %q/%q, want %q/%q", id+1, currency, state, expected.currency, expected.state)
		}
	}
	var currencies []string
	if err := pool.QueryRow(ctx, `select array_agg(currency order by currency) from (select distinct revenue_currency::text currency from events where site_id=3 union select distinct revenue_currency::text from custom_closed where site_id=3) c`).Scan(&currencies); err != nil {
		t.Fatal(err)
	}
	if len(currencies) != 2 || currencies[0] != "EUR" || currencies[1] != "USD" {
		t.Fatalf("mixed history changed: %v", currencies)
	}
}

func TestProjectCurrencyMigrationMakesSelectionImmutable(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org'); insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,revenue_currency,revenue_currency_state) values(1,'currenc00001','Site',array['example.test'],'UTC',30,'USD','configured')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update sites set revenue_currency='EUR' where id=1`); err == nil {
		t.Fatal("configured currency was mutable")
	}
}
