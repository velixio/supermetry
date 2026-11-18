package appquery_test

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/approllup"
)

// TestReleasesV1PingsOnly pins today's release numbers for a site that only
// sends v1 pings (the VoiceStudio shape: a session ping per launch, a crash
// event carrying the session id, no Sentry session lifecycle). Phase 2a must
// leave these numbers unchanged for a site without session-table data.
//
// Release voicestudio@1.4.0 on 2026-11-14, four launches on three installs:
//
//	s1 (i1) clean, s2 (i2) crashes, s3 (i3) crashes twice (occurrences 2),
//	s4 (i3) clean, plus a handled error in s1 that must not count as a crash.
//
// Sessions 4, crashed sessions 2 -> crash-free sessions 0.5.
// Installs 3 (i1, i2, i3), crashed installs 2 (i2, i3) -> crash-free users 1/3.
func TestReleasesV1PingsOnly(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rel := "voicestudio@1.4.0"
	at := func(h, m int) time.Time { return time.Date(2026, 11, 14, h, m, 0, 0, time.UTC) }
	for _, ev := range []appfixture.Event{
		{ID: "vs-p1", Kind: "session", Release: rel, Platform: "windows", At: at(8, 0), Session: "vs-s1", Install: "vs-i1"},
		{ID: "vs-p2", Kind: "session", Release: rel, Platform: "windows", At: at(8, 5), Session: "vs-s2", Install: "vs-i2"},
		{ID: "vs-p3", Kind: "session", Release: rel, Platform: "windows", At: at(8, 10), Session: "vs-s3", Install: "vs-i3"},
		{ID: "vs-p4", Kind: "session", Release: rel, Platform: "windows", At: at(9, 0), Session: "vs-s4", Install: "vs-i3"},
		{ID: "vs-c2", Kind: "crash", Level: "fatal", Release: rel, Platform: "windows", At: at(8, 6), Session: "vs-s2", Install: "vs-i2"},
		{ID: "vs-c3", Kind: "crash", Level: "fatal", Release: rel, Platform: "windows", At: at(8, 11), Session: "vs-s3", Install: "vs-i3", Occ: 2},
		{ID: "vs-h1", Kind: "error", Release: rel, Platform: "windows", At: at(8, 3), Session: "vs-s1", Install: "vs-i1"},
	} {
		e.f.Insert(t, ev)
	}
	if err := approllup.RecomputeDay(ctx, e.f.Pool, e.f.SiteID, d("2026-11-14")); err != nil {
		t.Fatal(err)
	}
	f := full()
	f.Release = rel
	rows, err := e.q.Releases(ctx, e.scope, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Release != rel {
		t.Fatalf("releases = %+v", rows)
	}
	r := rows[0]
	if r.Installs != 3 {
		t.Errorf("installs = %d, want 3", r.Installs)
	}
	near(t, "crash-free sessions", r.CrashFreeSessions, 0.5)
	near(t, "crash-free users", r.CrashFreeUsers, 1.0/3)
	ov, err := e.q.Overview(ctx, e.scope, f)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "overview crash-free sessions", ov.CrashFreeSessions, 0.5)
	near(t, "overview crash-free users", ov.CrashFreeUsers, 1.0/3)

	var sessions, crashedSessions, installs, crashedInstalls int64
	if err := e.f.Pool.QueryRow(ctx, `select sum(sessions), sum(crashed_sessions), sum(installs), sum(crashed_installs)
from app_release_daily where release = $1`, rel).Scan(&sessions, &crashedSessions, &installs, &crashedInstalls); err != nil {
		t.Fatal(err)
	}
	if sessions != 4 || crashedSessions != 2 || installs != 3 || crashedInstalls != 2 {
		t.Errorf("rollup sessions/crashed/installs/crashed installs = %d/%d/%d/%d, want 4/2/3/2", sessions, crashedSessions, installs, crashedInstalls)
	}
}
