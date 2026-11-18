package approllup_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/approllup"
)

const activeDay = `select day, visitor_id, release, platform, country, device, events, screens, sessions, engagement_ms from app_active_day order by 1,2`

func recomputeDays(t *testing.T, p *appfixture.Product, days ...string) {
	t.Helper()
	for _, d := range days {
		if err := approllup.RecomputeDay(context.Background(), p.Pool, p.SiteID, day(d)); err != nil {
			t.Fatal(err)
		}
	}
}

func countByDay(t *testing.T, p *appfixture.Product) map[string]int {
	t.Helper()
	rows, err := p.Pool.Query(context.Background(), `select day::text, count(*) from app_active_day group by 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			t.Fatal(err)
		}
		out[d] = n
	}
	return out
}

func TestRollupActiveDayHandComputed(t *testing.T) {
	p := appfixture.NewProduct(t, "UTC")
	p.AddDataset(t, true)
	recomputeDays(t, p, "2026-10-06", "2026-10-07", "2026-10-08", "2026-10-09")

	want := map[string]int{"2026-10-06": 3, "2026-10-07": 2, "2026-10-08": 2, "2026-10-09": 1}
	if got := countByDay(t, p); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows per day = %v, want %v", got, want)
	}
	// A on 10-08 reports release 1.1.0 on ios.
	var release, platform, country string
	var events, screens, sessions int
	if err := p.Pool.QueryRow(context.Background(), `select release, platform, country, events, screens, sessions
from app_active_day where day = '2026-10-08' and visitor_id = $1`, p.Visitor("A")).Scan(&release, &platform, &country, &events, &screens, &sessions); err != nil {
		t.Fatal(err)
	}
	if release != "1.1.0" || platform != "ios" || country != "DE" || events != 0 || screens != 1 || sessions != 1 {
		t.Fatalf("A on 10-08 = %q %q %q ev=%d sc=%d se=%d", release, platform, country, events, screens, sessions)
	}
	// C on 10-07: 2 screens and 1 custom event in one session.
	if err := p.Pool.QueryRow(context.Background(), `select release, platform, events, screens, sessions
from app_active_day where day = '2026-10-07' and visitor_id = $1`, p.Visitor("C")).Scan(&release, &platform, &events, &screens, &sessions); err != nil {
		t.Fatal(err)
	}
	if release != "1.0.0" || platform != "android" || events != 1 || screens != 2 || sessions != 1 {
		t.Fatalf("C on 10-07 = %q %q ev=%d sc=%d se=%d", release, platform, events, screens, sessions)
	}
}

func TestRollupActiveDayTZ(t *testing.T) {
	p := appfixture.NewProduct(t, "Asia/Kathmandu")
	p.Add(t,
		// 18:20Z is 00:05 local on 10-07; 18:10Z is 23:55 local on 10-06.
		appfixture.ProductHit{Install: "A", At: time.Date(2026, 10, 6, 18, 20, 0, 0, time.UTC), Screen: "/home", Platform: "ios"},
		appfixture.ProductHit{Install: "B", At: time.Date(2026, 10, 6, 18, 10, 0, 0, time.UTC), Screen: "/home", Platform: "ios"},
	)
	recomputeDays(t, p, "2026-10-06", "2026-10-07")
	rows, err := p.Pool.Query(context.Background(), `select day::text, visitor_id from app_active_day order by 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int64{}
	for rows.Next() {
		var d string
		var v int64
		if err := rows.Scan(&d, &v); err != nil {
			t.Fatal(err)
		}
		got[d] = v
	}
	want := map[string]int64{"2026-10-06": p.Visitor("B"), "2026-10-07": p.Visitor("A")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("days = %v, want %v", got, want)
	}
}

func TestRollupActiveDayIdempotent(t *testing.T) {
	p := appfixture.NewProduct(t, "UTC")
	p.AddDataset(t, true)
	days := []string{"2026-10-06", "2026-10-07", "2026-10-08", "2026-10-09"}
	recomputeDays(t, p, days...)
	first := dumpProduct(t, p)
	recomputeDays(t, p, days...)
	recomputeDays(t, p, days...)
	if second := dumpProduct(t, p); !reflect.DeepEqual(first, second) || len(first) != 8 {
		t.Fatalf("rows changed after recompute\nfirst  %q\nsecond %q", first, second)
	}
}

func dumpProduct(t *testing.T, p *appfixture.Product) []string {
	t.Helper()
	rows, err := p.Pool.Query(context.Background(), activeDay)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(vals...))
	}
	return out
}

// Review Focus 1: a hit that arrives days late reopens its closed day.
func TestRollupLateHitRecomputed(t *testing.T) {
	p := appfixture.NewProduct(t, "UTC")
	p.AddDataset(t, false)
	recomputeDays(t, p, "2026-10-06")
	if got := countByDay(t, p)["2026-10-06"]; got != 2 {
		t.Fatalf("before the late hit 10-06 has %d rows, want 2", got)
	}
	p.Add(t, appfixture.ProductHit{Install: "D", At: time.Date(2026, 10, 6, 18, 0, 0, 0, time.UTC), Screen: "/home", Platform: "android", Country: "US"})
	recomputeDays(t, p, "2026-10-06")
	if got := countByDay(t, p)["2026-10-06"]; got != 3 {
		t.Fatalf("after the late hit 10-06 has %d rows, want 3", got)
	}
}

// A day in a month nobody pre-created a partition for still rolls up.
func TestRollupCreatesTheMonthPartition(t *testing.T) {
	p := appfixture.NewProduct(t, "UTC")
	if _, err := p.Pool.Exec(context.Background(), `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id)
values (7,90,'2026-09-26','2026-09-26 10:00+00',5,5,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pool.Exec(context.Background(), `drop table app_active_day_202609`); err != nil {
		t.Fatal(err)
	}
	recomputeDays(t, p, "2026-09-26")
	if got := countByDay(t, p)["2026-09-26"]; got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
}
