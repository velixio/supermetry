package approllup_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/appfixture"
	"supermetry/internal/approllup"
)

const sessionDaily = `select to_char(day,'YYYY-MM-DD'), release, environment, platform, sessions, errored, crashed, abnormal, users, crashed_users, errored_users from app_session_daily order by 1,2,3,4`

type sess struct {
	id, install, status string
	at                  time.Time
}

func insertSessions(t *testing.T, f *appfixture.Fixture, release string, rows []sess) {
	t.Helper()
	for _, s := range rows {
		var install any
		if s.install != "" {
			install = appfixture.Hash(s.install)
		}
		if _, err := f.Pool.Exec(context.Background(), `insert into app_sessions(site_id,started,session_hash,install_hash,release,environment,platform,status,errors,updated_at)
values($1,$2,$3,$4,$5,'production','linux',$6,0,$2)`, f.SiteID, s.at, appfixture.Hash(s.id), install, release, s.status); err != nil {
			t.Fatalf("insert session %s: %v", s.id, err)
		}
	}
}

func TestRollupSessionsHandComputed(t *testing.T) {
	f := appfixture.New(t)
	at := time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC) // local 11-14 10:45
	// 10 sessions over 4 installs: s1-s2 crashed (install A), s3 abnormal (B),
	// s4-s6 errored (B, C, C), s7-s10 healthy (D, D, A, B).
	insertSessions(t, f, "1.0.0", []sess{
		{"s1", "A", "crashed", at}, {"s2", "A", "crashed", at},
		{"s3", "B", "abnormal", at},
		{"s4", "B", "errored", at}, {"s5", "C", "errored", at}, {"s6", "C", "errored", at},
		{"s7", "D", "exited", at}, {"s8", "D", "ok", at}, {"s9", "A", "exited", at}, {"s10", "B", "exited", at},
	})
	// An aggregate: exited=20, crashed=2 (hash-less session rows).
	f.Insert(t, appfixture.Event{ID: "agg1", Kind: "session", Release: "1.0.0", Platform: "linux", At: at, Occ: 20})
	f.Insert(t, appfixture.Event{ID: "agg2", Kind: "session", Release: "1.0.0", Platform: "linux", At: at, Occ: 2})
	if _, err := f.Pool.Exec(context.Background(), `update app_events set exit_reason='crashed' where event_id='agg2'`); err != nil {
		t.Fatal(err)
	}
	// A v1 init ping with a hash must not be counted again.
	f.Insert(t, appfixture.Event{ID: "ping", Kind: "session", Release: "1.0.0", Platform: "linux", At: at, Install: "A", Session: "s1"})

	if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, day("2026-11-14")); err != nil {
		t.Fatal(err)
	}
	// sessions 32, errored 3, crashed 4, abnormal 1, users 4, crashed_users 1, errored_users 2
	want := []string{"2026-11-14 1.0.0 production linux 32 3 4 1 4 1 2"}
	if got := dump(t, f, sessionDaily); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	var crashFree float64
	if err := f.Pool.QueryRow(context.Background(), `select 1 - crashed::float8 / sessions from app_session_daily`).Scan(&crashFree); err != nil || crashFree != 0.875 {
		t.Fatalf("crash-free sessions = %v %v, want 0.875", crashFree, err)
	}
}

func TestRollupSessionTZ(t *testing.T) {
	f := appfixture.New(t)
	if _, err := f.Pool.Exec(context.Background(), `update sites set timezone='Asia/Kolkata' where id=$1`, f.SiteID); err != nil {
		t.Fatal(err)
	}
	// 19:00Z is 00:30 IST on the 18th; it counts there and not on the 17th.
	insertSessions(t, f, "2.0.0", []sess{{"m1", "A", "exited", time.Date(2026, 11, 17, 19, 0, 0, 0, time.UTC)}})
	for _, d := range []string{"2026-11-17", "2026-11-18"} {
		if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, day(d)); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"2026-11-18 2.0.0 production linux 1 0 0 0 1 0 0"}
	if got := dump(t, f, sessionDaily); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRollupSessionsIdempotent(t *testing.T) {
	f := appfixture.New(t)
	at := time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC)
	insertSessions(t, f, "1.0.0", []sess{{"i1", "A", "crashed", at}, {"i2", "B", "exited", at}})
	for range 2 {
		if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, day("2026-11-14")); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"2026-11-14 1.0.0 production linux 2 0 1 0 2 1 0"}
	if got := dump(t, f, sessionDaily); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRollupSessionsAggregateOnly(t *testing.T) {
	f := appfixture.New(t)
	f.Insert(t, appfixture.Event{ID: "agg", Kind: "session", Release: "3.0.0", Platform: "linux", At: time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC), Occ: 5})
	if err := approllup.RecomputeDay(context.Background(), f.Pool, f.SiteID, day("2026-11-14")); err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-11-14 3.0.0 production linux 5 0 0 0 0 0 0"}
	if got := dump(t, f, sessionDaily); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}
