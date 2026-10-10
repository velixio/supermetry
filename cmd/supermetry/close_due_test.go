package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

type closeTestRegistry struct{ values []sites.Site }

func (r closeTestRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (r closeTestRegistry) Site(id int32) (sites.Site, bool) {
	for _, site := range r.values {
		if site.ID == id {
			return site, true
		}
	}
	return sites.Site{}, false
}
func (r closeTestRegistry) All() []sites.Site { return append([]sites.Site(nil), r.values...) }

func TestCloseDueDaysCatchesUpOldestFirst(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into orgs(id,name,slug) overriding system value values (1,'Catch up','catch-up');
insert into sites(id,org_id,public_id,name,timezone,retention_class,created_at) overriding system value
values (1,1,'catchupsite1','Catch up','UTC',30,'2026-10-07 00:00Z');
create table close_order(seq bigint generated always as identity, site_id integer, day date);
create function record_close_order() returns trigger language plpgsql as $$
begin
  insert into close_order(site_id,day) values(new.site_id,new.day);
  return new;
end $$;
create trigger record_close_order after insert on day_close
for each row execute function record_close_order()`); err != nil {
		t.Fatal(err)
	}

	closed, err := loadClosedDays(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	reg := closeTestRegistry{values: []sites.Site{{ID: 1, TZ: time.UTC, Retention: db.Ret30}}}
	if err := closeDueDays(ctx, pool, reg, closed, time.Date(2026, 10, 10, 0, 46, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `select day::text from close_order order by seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"2026-10-07", "2026-10-08", "2026-10-09"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("close order=%v, want %v", got, want)
	}
}

func TestCloseDueDaysContinuesAfterSiteDayFailure(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into orgs(id,name,slug) overriding system value values (1,'Continue','continue');
insert into sites(id,org_id,public_id,name,timezone,retention_class,created_at) overriding system value values
  (1,1,'failingsite1','Failing','UTC',30,'2026-10-09 00:00Z'),
  (2,1,'workingsite2','Working','UTC',30,'2026-10-09 00:00Z');
create function fail_first_site_close() returns trigger language plpgsql as $$
begin
  if new.site_id = 1 then raise exception 'expected close failure'; end if;
  return new;
end $$;
create trigger fail_first_site_close before insert on day_close
for each row execute function fail_first_site_close()`); err != nil {
		t.Fatal(err)
	}
	closed, err := loadClosedDays(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	reg := closeTestRegistry{values: []sites.Site{
		{ID: 1, TZ: time.UTC, Retention: db.Ret30},
		{ID: 2, TZ: time.UTC, Retention: db.Ret30},
	}}
	err = closeDueDays(ctx, pool, reg, closed, time.Date(2026, 10, 10, 0, 46, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("closeDueDays returned nil after one site failed")
	}
	if closed.IsClosed(1, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("failed site/day was marked closed")
	}
	if !closed.IsClosed(2, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("later site/day was not closed after earlier failure")
	}
	if _, err := pool.Exec(ctx, `drop trigger fail_first_site_close on day_close`); err != nil {
		t.Fatal(err)
	}
	if err := closeDueDays(ctx, pool, reg, closed, time.Date(2026, 10, 10, 0, 47, 0, 0, time.UTC)); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if !closed.IsClosed(1, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("failed site/day was not retried on the next tick")
	}
}
