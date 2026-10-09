package store_test

import (
	"context"
	"testing"
	"time"

	"supermetry/bench/proto/internal/core"
	"supermetry/bench/proto/internal/testdb"
)

func TestLoadOpenSessionsRebuildsAndReconcilesFromDurableEvents(t *testing.T) {
	st := testdb.Open(t)
	ctx := context.Background()
	if err := st.UpsertSites(ctx, []core.Site{{ID: 1, Host: "a.test", TZ: "UTC", Loc: time.UTC}}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if err := st.EnsureSessionDays(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureEventPartitions(ctx, base); err != nil {
		t.Fatal(err)
	}
	want := core.RestoreSession(core.SessionState{
		SiteID: 1, VisitorID: 11, Day: core.DayOf(base, time.UTC), Start: base.Add(-time.Minute).UnixMicro(),
	})
	_, err := st.Pool.Exec(ctx, `INSERT INTO events
		(site_id, local_date, ts, visitor_id, session_id, name, path, source, country, browser, os, device)
		VALUES
		(1, '2026-07-01', $1, 11, $4, 'pageview', '/a', 'google', 'US', 'Chrome', 'Linux', 'desktop'),
		(1, '2026-07-01', $2, 11, $4, 'signup', '/a', 'google', 'US', 'Chrome', 'Linux', 'desktop'),
		(1, '2026-07-01', $3, 11, $4, 'pageview', '/b', 'google', 'US', 'Chrome', 'Linux', 'desktop')`,
		base, base.Add(10*time.Minute), base.Add(25*time.Minute), want.ID())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Pool.Exec(ctx, `INSERT INTO sessions
		(site_id, local_date, start_ts, session_id, visitor_id, duration_ms, pageviews, custom_events,
		 entry_path, exit_path, source, country, browser, os, device)
		VALUES
		(1, '2026-07-01', $1, $2, 11, 0, 1, 0, '/a', '/a', 'google', 'US', 'Chrome', 'Linux', 'desktop'),
		(1, '2026-07-01', $3, 102, 12, 0, 1, 0, '/lost', '/lost', '', 'US', 'Chrome', 'Linux', 'desktop')`,
		base.Add(-time.Minute), want.ID(), base.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadOpenSessions(ctx, base.Add(26*time.Minute), 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID() != want.ID() || got[0].Start != base.UnixMicro() || got[0].Last != base.Add(25*time.Minute).UnixMicro() ||
		got[0].Pageviews != 2 || got[0].Custom != 1 || got[0].Entry() != "/a" || got[0].Exit() != "/b" {
		t.Fatalf("open sessions = %+v", got)
	}
	var rows, pageviews, custom int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(pageviews), 0), coalesce(sum(custom_events), 0) FROM sessions`).Scan(&rows, &pageviews, &custom); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || pageviews != 2 || custom != 1 {
		t.Fatalf("reconciled sessions = rows %d pageviews %d custom %d", rows, pageviews, custom)
	}
}
