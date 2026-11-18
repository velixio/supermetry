package siteexport_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
)

func TestAppSiteExportsLogs(t *testing.T) {
	e, ctx, pool := newAppExportPool(t)
	e.SetClock(func() time.Time { return time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC) })
	if err := db.EnsureDailyPartitions(ctx, pool, "logs", day("2026-09-20"), day("2026-10-04")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into logs(site_id,ts,rid,observed_at,severity,service,environment,release,trace_id,span_id,user_hash,body,attributes,source) values
 (7,'2026-10-02T10:00:00Z',1,'2026-10-02T10:00:01Z',17,'api','prod','1.4.2','\x5b8efff798038103d269b633813fc60c','\x0102030405060708','\xcafebabe','payment failed','{"http.route":"/pay"}','otlp'),
 (7,'2026-10-01T09:00:00Z',2,'2026-10-01T09:00:00Z',9,'api','prod','1.4.2',null,null,null,'older but inside window','{}','sentry'),
 (7,'2026-09-25T09:00:00Z',3,'2026-09-25T09:00:00Z',9,'api','prod','1.4.2',null,null,null,'outside the window','{}','otlp'),
 (9,'2026-10-02T10:00:00Z',4,'2026-10-02T10:00:00Z',9,'api','prod','',null,null,null,'other site','{}','otlp')`); err != nil {
		t.Fatal(err)
	}
	files, _, raw := exportFiles(t, e, ctx, 7)
	want := [][]string{
		{"timestamp", "severity", "service", "environment", "release", "trace_id", "body", "attributes"},
		{"2026-10-01T09:00:00.000Z", "9", "api", "prod", "1.4.2", "", "older but inside window", "{}"},
		{"2026-10-02T10:00:00.000Z", "17", "api", "prod", "1.4.2", "5b8efff798038103d269b633813fc60c", "payment failed", `{"http.route": "/pay"}`},
	}
	if got := files["logs.csv"]; !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("logs.csv = %q\nwant %q", got, want)
	}
	if strings.Contains(raw, "cafebabe") || strings.Contains(raw, "0102030405060708") || strings.Contains(raw, "other site") {
		t.Fatalf("export leaks a hash, span id or another site's log:\n%s", raw)
	}
}

func TestAppSiteExportHidesLogsOlderThanSiteRetention(t *testing.T) {
	e, ctx, pool := newAppExportPool(t)
	e.SetClock(func() time.Time { return time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC) })
	if _, err := pool.Exec(ctx, `update sites set settings='{"app":{"log_retention_days":1}}' where id=7`); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureDailyPartitions(ctx, pool, "logs", day("2026-10-01"), day("2026-10-04")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into logs(site_id,ts,rid,observed_at,severity,body,source) values
 (7,'2026-10-02T10:00:00Z',1,'2026-10-02T10:00:00Z',9,'too old for a 1-day site','otlp')`); err != nil {
		t.Fatal(err)
	}
	files, _, _ := exportFiles(t, e, ctx, 7)
	if got := files["logs.csv"]; len(got) != 1 {
		t.Fatalf("logs.csv = %q, want header only", got)
	}
}
