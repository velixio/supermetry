package appquery_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/appquery"
	"supermetry/internal/approllup"
)

// v2Env builds a UTC app site (id 8) next to the fixture's v1-only site. Now is
// 2026-11-15 10:00 UTC and every session starts on 2026-11-15.
//
// Release 1.0.0 has 10 session rows over installs i1..i4: ok 4 (i3, i3, i4,
// i4), crashed 2 (i1), abnormal 1 (i2), errored 3 (i2, i3, i4); plus
// aggregates exited 20 and crashed 2. Totals: sessions 32, crashed 4,
// abnormal 1, errored 3, users 4, crashed users 1, so crash-free sessions are
// 28/32 = 0.875 and crash-free users 0.75.
//
// Release 2.0.0 has 8 healthy sessions on installs i1 and i5.
//
// Events: 1.0.0 has 5 handled and 2 unhandled occurrences, 2.0.0 one handled.
func v2Env(t *testing.T) (env, *appfixture.Fixture) {
	t.Helper()
	f := appfixture.New(t)
	ctx := context.Background()
	if _, err := f.Pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(8,1,'appfix000002','App 2','UTC',90,'app')`); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC)
	n := 0
	session := func(release, install, status string) {
		n++
		if _, err := f.Pool.Exec(ctx, `insert into app_sessions(site_id,started,session_hash,install_hash,release,environment,platform,status,updated_at)
values(8,$1,$2,$3,$4,'production','linux',$5,$1)`, start.Add(time.Duration(n)*time.Minute), appfixture.Hash(fmt.Sprintf("s%d", n)), appfixture.Hash(install), release, status); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range [][2]string{{"i3", "ok"}, {"i3", "ok"}, {"i4", "ok"}, {"i4", "ok"}, {"i1", "crashed"}, {"i1", "crashed"},
		{"i2", "abnormal"}, {"i2", "errored"}, {"i3", "errored"}, {"i4", "errored"}} {
		session("1.0.0", s[0], s[1])
	}
	for i := 0; i < 8; i++ {
		session("2.0.0", []string{"i1", "i5"}[i%2], "ok")
	}
	for _, a := range []struct {
		id, exit string
		occ      int
	}{{"agg1", "exited", 20}, {"agg2", "crashed", 2}} {
		if _, err := f.Pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind,level,release,environment,platform,exit_reason,occurrences)
values(8,$1,$1,$2,'session','info','1.0.0','production','linux',$3,$4)`, start, a.id, a.exit, a.occ); err != nil {
			t.Fatal(err)
		}
	}
	for i, h := range []struct {
		release string
		handled bool
		occ     int
	}{{"1.0.0", true, 5}, {"1.0.0", false, 2}, {"2.0.0", true, 1}} {
		if _, err := f.Pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind,level,release,environment,platform,handled,occurrences)
values(8,$1,$1,$2,'error','error',$3,'production','linux',$4,$5)`, start, fmt.Sprintf("ev%d", i), h.release, h.handled, h.occ); err != nil {
			t.Fatal(err)
		}
	}
	if err := approllup.RecomputeDay(ctx, f.Pool, 8, d("2026-11-15")); err != nil {
		t.Fatal(err)
	}
	return env{f: f, q: appquery.New(f.Pool, func() time.Time { return f.Now }), scope: appquery.Scope{SiteID: 8, RetentionDays: 90}}, f
}

func releaseByName(t *testing.T, rows []appquery.ReleaseRow, name string) appquery.ReleaseRow {
	t.Helper()
	for _, r := range rows {
		if r.Release == name {
			return r
		}
	}
	t.Fatalf("release %s missing in %+v", name, rows)
	return appquery.ReleaseRow{}
}

func TestReleasesV2HandComputed(t *testing.T) {
	e, _ := v2Env(t)
	rows, err := e.q.Releases(context.Background(), e.scope, appquery.Filter{From: d("2026-11-15"), To: d("2026-11-15")})
	if err != nil {
		t.Fatal(err)
	}
	r := releaseByName(t, rows, "1.0.0")
	if r.HealthSource != "sessions" || r.Sessions != 32 || r.Users != 4 || r.AbnormalSessions != 1 || r.ErroredSessions != 3 {
		t.Fatalf("1.0.0 = %+v", r)
	}
	near(t, "cfs", r.CrashFreeSessions, 0.875) // abnormal is not a crash
	near(t, "cfu", r.CrashFreeUsers, 0.75)
	if r.HandledErrors != 5 || r.UnhandledErrors != 2 {
		t.Errorf("handled/unhandled = %d/%d, want 5/2", r.HandledErrors, r.UnhandledErrors)
	}
	// Review Focus 3: sessions without any crash are 100 %, not null.
	r2 := releaseByName(t, rows, "2.0.0")
	near(t, "2.0.0 cfs", r2.CrashFreeSessions, 1)
	near(t, "2.0.0 cfu", r2.CrashFreeUsers, 1)
	if r2.Sessions != 8 || r2.Users != 2 || r2.HandledErrors != 1 || r2.UnhandledErrors != 0 {
		t.Errorf("2.0.0 = %+v", r2)
	}
	// Adoption: 32 and 8 of the 40 sessions of the last 24 h.
	near(t, "adoption 1.0.0", r.Adoption, 0.8)
	near(t, "adoption 2.0.0", r2.Adoption, 0.2)
}

func TestOverviewV2(t *testing.T) {
	e, _ := v2Env(t)
	ov, err := e.q.Overview(context.Background(), e.scope, appquery.Filter{From: d("2026-11-15"), To: d("2026-11-15")})
	if err != nil {
		t.Fatal(err)
	}
	if ov.HealthSource != "sessions" || ov.Sessions != 40 || ov.AbnormalSessions != 1 || ov.ErroredSessions != 3 || ov.Users != 5 {
		t.Fatalf("overview = %+v", ov)
	}
	near(t, "cfs", ov.CrashFreeSessions, 1-4.0/40)
	near(t, "cfu", ov.CrashFreeUsers, 1-1.0/5)
	if ov.HandledErrors != 6 || ov.UnhandledErrors != 2 {
		t.Errorf("handled/unhandled = %d/%d", ov.HandledErrors, ov.UnhandledErrors)
	}
	near(t, "series cfs", ov.Series[len(ov.Series)-1].CrashFreeSessions, 0.9)
}

// Review Focus 4: a site with only v1 pings keeps today's numbers.
func TestReleasesV1Fallback(t *testing.T) {
	e := setup(t)
	rows, err := e.q.Releases(context.Background(), e.scope, full())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.HealthSource != "events" {
			t.Errorf("%s source = %q", r.Release, r.HealthSource)
		}
	}
	n := releaseByName(t, rows, "1.10.0")
	near(t, "1.10.0 cfu", n.CrashFreeUsers, 1.0/3)
	near(t, "1.10.0 cfs", n.CrashFreeSessions, 2.0/3)
	if n.Installs != 3 || n.AbnormalSessions != 0 {
		t.Errorf("1.10.0 = %+v", n)
	}
	ov, err := e.q.Overview(context.Background(), e.scope, full())
	if err != nil {
		t.Fatal(err)
	}
	if ov.HealthSource != "events" {
		t.Errorf("overview source = %q", ov.HealthSource)
	}
}

// A release with only v1 pings on an earlier day and session data on a later
// one counts the earlier day from pings and the later day from the rollup.
func TestReleasesMixedDays(t *testing.T) {
	e, f := v2Env(t)
	ctx := context.Background()
	// 2026-11-14: two v1 pings of 1.0.0, one of the sessions also saw a crash.
	for i, s := range []struct{ id, kind string }{{"p1", "session"}, {"p2", "session"}, {"p2", "crash"}} {
		if _, err := f.Pool.Exec(ctx, `insert into app_events(site_id,ts,received_at,event_id,kind,level,release,environment,platform,session_hash,install_hash)
values(8,'2026-11-14 12:00+00','2026-11-14 12:00+00',$1,$2,'error','1.0.0','production','linux',$3,$4)`, fmt.Sprintf("m%d", i), s.kind, appfixture.Hash(s.id), appfixture.Hash("v1")); err != nil {
			t.Fatal(err)
		}
	}
	if err := approllup.RecomputeDay(ctx, f.Pool, 8, d("2026-11-14")); err != nil {
		t.Fatal(err)
	}
	rows, err := e.q.Releases(ctx, e.scope, appquery.Filter{From: d("2026-11-14"), To: d("2026-11-15")})
	if err != nil {
		t.Fatal(err)
	}
	r := releaseByName(t, rows, "1.0.0")
	if r.HealthSource != "sessions" || r.Sessions != 34 {
		t.Fatalf("1.0.0 = %+v", r)
	}
	near(t, "cfs", r.CrashFreeSessions, 1-5.0/34)
}
