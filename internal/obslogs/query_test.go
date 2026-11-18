package obslogs_test

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/obslogs"
	"supermetry/internal/obslogs/search"
)

const traceHex = "5b8efff798038103d269b633813fc60c"

// searchFixture inserts the hand-listed rows below (now = 2026-11-18T12:00Z).
// The row number is the rid, so ties on ts order by it.
//
//	 1  11-16 10:00 sev 9   api     "payment ok"
//	 2  11-16 11:00 sev 17  api     "payment declined"
//	 3  11-16 12:00 sev 17  api     "Payment Failed for order 12"  attr http.route=/pay
//	 4  11-17 09:00 sev 13  api     "payment failed retrying"
//	 5  11-17 10:00 sev 17  api     "disk full"
//	 6  11-17 11:00 sev 9   api     "payment failed"
//	 7  11-17 12:00 sev 21  api     "payment failed hard"
//	 8  11-18 08:00 sev 9   web     "login ok"                     trace T
//	 9  11-18 09:00 sev 9   web     "login again"                  trace T
//	10  11-18 09:00 sev 5   worker  "cache 50%_hit"                (same ts as 9)
//	11  11-13 12:00 sev 17  api     "ancient payment failed"       (5 days old)
func searchFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := fixture(t)
	ctx := context.Background()
	if err := db.EnsureDailyPartitions(ctx, pool, "logs", now.AddDate(0, 0, -10), now.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	trace, _ := hex.DecodeString(traceHex)
	rows := []struct {
		rid     int64
		ts      string
		sev     int
		service string
		body    string
		attrs   string
		trace   []byte
	}{
		{1, "2026-11-16T10:00:00Z", 9, "api", "payment ok", `{}`, nil},
		{2, "2026-11-16T11:00:00Z", 17, "api", "payment declined", `{}`, nil},
		{3, "2026-11-16T12:00:00Z", 17, "api", "Payment Failed for order 12", `{"http.route":"/pay"}`, nil},
		{4, "2026-11-17T09:00:00Z", 13, "api", "payment failed retrying", `{}`, nil},
		{5, "2026-11-17T10:00:00Z", 17, "api", "disk full", `{}`, nil},
		{6, "2026-11-17T11:00:00Z", 9, "api", "payment failed", `{}`, nil},
		{7, "2026-11-17T12:00:00Z", 21, "api", "payment failed hard", `{}`, nil},
		{8, "2026-11-18T08:00:00Z", 9, "web", "login ok", `{}`, trace},
		{9, "2026-11-18T09:00:00Z", 9, "web", "login again", `{}`, trace},
		{10, "2026-11-18T09:00:00Z", 5, "worker", "cache 50%_hit", `{}`, nil},
		{11, "2026-11-13T12:00:00Z", 17, "api", "ancient payment failed", `{}`, nil},
	}
	for _, r := range rows {
		if _, err := pool.Exec(ctx, `insert into logs(site_id,ts,rid,observed_at,severity,service,environment,release,trace_id,body,attributes,source)
			values(1,$1,$2,$1,$3,$4,'prod','1.0',$5,$6,$7::jsonb,'otlp')`, r.ts, r.rid, r.sev, r.service, r.trace, r.body, r.attrs); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

func parse(t *testing.T, in string) search.Query {
	t.Helper()
	q, err := search.Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func bodies(p obslogs.Page) string {
	var b []string
	for _, r := range p.Rows {
		b = append(b, r.Body)
	}
	return strings.Join(b, " | ")
}

func window() (time.Time, time.Time) { return now.AddDate(0, 0, -6), now }

func TestSearchPhraseAndSeverity(t *testing.T) {
	pool := searchFixture(t)
	from, to := window()
	// Rows 7 and 3, newest first; row 11 is outside the window's 7-day range
	// only by severity filter, row 4 and 6 by severity, row 1 and 2 by phrase.
	got, err := obslogs.Search(context.Background(), pool, 1, now, parse(t, `"payment failed" severity:>=error`), from, to, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "payment failed hard | Payment Failed for order 12 | ancient payment failed"; bodies(got) != want {
		t.Fatalf("rows = %s", bodies(got))
	}
	// Narrow the window to drop row 11 (11-13): exactly rows 7 then 3.
	got, _ = obslogs.Search(context.Background(), pool, 1, now, parse(t, `"payment failed" severity:>=error`), now.AddDate(0, 0, -3), to, 0, nil)
	if want := "payment failed hard | Payment Failed for order 12"; bodies(got) != want || got.Next != nil {
		t.Fatalf("rows = %s next=%v", bodies(got), got.Next)
	}
}

func TestSearchKeysetPaging(t *testing.T) {
	pool := searchFixture(t)
	q := parse(t, `"payment failed" severity:>=error`)
	from, to := now.AddDate(0, 0, -3), now
	var seen []string
	var cursor *obslogs.Cursor
	for page := 0; page < 5; page++ {
		got, err := obslogs.Search(context.Background(), pool, 1, now, q, from, to, 1, cursor)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, bodies(got))
		if got.Next == nil {
			break
		}
		// The cursor survives the URL round trip.
		if cursor, err = obslogs.ParseCursor(got.Next.Encode()); err != nil {
			t.Fatal(err)
		}
	}
	if want := "payment failed hard,Payment Failed for order 12"; strings.Join(seen, ",") != want {
		t.Fatalf("pages = %v", seen)
	}
}

func TestSearchTiesPageStably(t *testing.T) {
	pool := searchFixture(t)
	// Rows 9 and 10 share a timestamp; rid orders them 10 then 9, and no row repeats.
	q := parse(t, `env:prod`)
	var seen []string
	var cursor *obslogs.Cursor
	for i := 0; i < 20; i++ {
		got, err := obslogs.Search(context.Background(), pool, 1, now, q, now.Add(-12*time.Hour), now, 1, cursor)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, bodies(got))
		if got.Next == nil {
			break
		}
		cursor = got.Next
	}
	if want := "cache 50%_hit,login again,login ok"; strings.Join(seen, ",") != want {
		t.Fatalf("pages = %v", seen)
	}
}

func TestSearchFieldsNegationAndEscaping(t *testing.T) {
	pool := searchFixture(t)
	from, to := window()
	run := func(in string) string {
		t.Helper()
		got, err := obslogs.Search(context.Background(), pool, 1, now, parse(t, in), from, to, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		return bodies(got)
	}
	for in, want := range map[string]string{
		"trace:" + traceHex:         "login again | login ok",
		"attr.http.route:/pay":      "Payment Failed for order 12",
		"service:worker":            "cache 50%_hit",
		"50%_hit":                   "cache 50%_hit",
		"%%%":                       "",
		"!service:api !service:web": "cache 50%_hit",
		"severity:debug":            "cache 50%_hit",
		"severity:>=fatal":          "payment failed hard",
		"PAYMENT DECLINED":          "payment declined",
	} {
		if got := run(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

// Review focus 3: a site that keeps 3 days does not see a 5-day-old row.
func TestSearchHidesRowsBeyondSiteRetention(t *testing.T) {
	pool := searchFixture(t)
	ctx := context.Background()
	q := parse(t, "ancient")
	from, to := now.AddDate(0, 0, -10), now.AddDate(0, 0, -5).Add(24*time.Hour) // 8 days, around the row
	got, err := obslogs.Search(ctx, pool, 1, now, q, from, to, 0, nil)
	if err != nil || len(got.Rows) != 1 {
		t.Fatalf("default retention (7 days): %v %v", got.Rows, err)
	}
	if _, err := pool.Exec(ctx, `update sites set settings='{"app":{"log_retention_days":3}}' where id=1`); err != nil {
		t.Fatal(err)
	}
	got, err = obslogs.Search(ctx, pool, 1, now, q, from, to, 0, nil)
	if err != nil || len(got.Rows) != 0 {
		t.Fatalf("3-day retention: %v %v", got.Rows, err)
	}
	trace, _ := hex.DecodeString(traceHex)
	if _, err := pool.Exec(ctx, `update logs set trace_id=$1 where rid=11`, trace); err != nil {
		t.Fatal(err)
	}
	rows, err := obslogs.TraceLogs(ctx, pool, 1, now, trace, time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC), 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("trace logs beyond retention: %v %v", rows, err)
	}
}

func TestSearchIsSiteScoped(t *testing.T) {
	pool := searchFixture(t)
	if _, err := pool.Exec(context.Background(), `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values(2,1,'applogs00002','Other','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	from, to := window()
	got, err := obslogs.Search(context.Background(), pool, 2, now, parse(t, "payment"), from, to, 0, nil)
	if err != nil || len(got.Rows) != 0 {
		t.Fatalf("site 2 sees %v %v", got.Rows, err)
	}
	trace, _ := hex.DecodeString(traceHex)
	rows, err := obslogs.TraceLogs(context.Background(), pool, 2, now, trace, now, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("site 2 trace logs %v %v", rows, err)
	}
}

func TestTraceLogsOldestFirstAndRowShape(t *testing.T) {
	pool := searchFixture(t)
	trace, _ := hex.DecodeString(traceHex)
	rows, err := obslogs.TraceLogs(context.Background(), pool, 1, now, trace, now.Add(-2*time.Hour), 10)
	if err != nil || len(rows) != 2 || rows[0].Body != "login ok" || rows[1].Body != "login again" {
		t.Fatalf("trace logs = %+v %v", rows, err)
	}
	if rows[0].TraceID != traceHex || rows[0].SeverityText != "info" || rows[0].Attributes == nil {
		t.Fatalf("row = %+v", rows[0])
	}
	// A wrong-size id is rejected, not matched.
	if _, err := obslogs.TraceLogs(context.Background(), pool, 1, now, []byte{1, 2}, now, 10); !errors.Is(err, obslogs.ErrInvalid) {
		t.Fatalf("short trace id: %v", err)
	}
}

func TestSearchLimits(t *testing.T) {
	pool := searchFixture(t)
	from, to := window()
	ctx := context.Background()
	q := parse(t, "")
	for name, run := range map[string]func() error{
		"limit 201": func() error { _, err := obslogs.Search(ctx, pool, 1, now, q, from, to, 201, nil); return err },
		"31 days":   func() error { _, err := obslogs.Search(ctx, pool, 1, now, q, now.AddDate(0, 0, -31), now, 10, nil); return err },
		"inverted":  func() error { _, err := obslogs.Search(ctx, pool, 1, now, q, to, from, 10, nil); return err },
	} {
		if err := run(); !errors.Is(err, obslogs.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := obslogs.ParseCursor("not a cursor!"); !errors.Is(err, obslogs.ErrInvalid) {
		t.Errorf("bad cursor: %v", err)
	}
}
