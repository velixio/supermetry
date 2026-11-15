// Package appfixture seeds the hand-computed App Health data set that the
// rollup, query and HTTP tests share. It is only imported by tests.
//
// The site runs in Asia/Kathmandu (+05:45) so day bucketing is exercised: a
// local day D spans (D-1) 18:15 UTC to D 18:15 UTC. "Now" is 2026-11-15 10:00
// UTC (15:45 local). Three installs (A, B, C), two releases (1.9.0, 1.10.0),
// two platforms (linux, macos), three issues and 12 events:
//
//	id  UTC time        local day  kind     rel   plat   inst sess  issue occ
//	e1  11-12 06:00     11-12      crash    1.9.0 linux  A    late  I1    1   (late event, 3 days old)
//	e2  11-14 02:00     11-14      session  1.9.0 linux  A    a1    -     1
//	e3  11-14 02:10     11-14      crash    1.9.0 linux  A    a1    I1    2
//	e4  11-14 03:00     11-14      session  1.9.0 linux  B    b1    -     1
//	e5  11-14 03:05     11-14      error    1.9.0 linux  B    b1    I2    1
//	e6  11-14 04:00     11-14      session  1.10.0 macos C    c1    -     1
//	e7  11-14 18:10     11-14      crash    1.10.0 macos C    c1    I3    1   (23:55 local)
//	e8  11-14 18:20     11-15      session  1.10.0 macos C    c2    -     1   (00:05 local)
//	e9  11-15 08:00     11-15      session  1.10.0 linux  B    b2    -     1
//	e10 11-15 08:30     11-15      error    1.10.0 linux  B    -     I2    3   (environment beta)
//	e11 11-15 09:00     11-15      error    1.10.0 linux  A    a2    I1    1   (level fatal)
//	e12 11-15 08:45     11-15      error    1.10.0 macos C    -     I2    1
//
// Everything else is production. Compatibility values: linux 22.04/x64/cuda
// (e1-e3, e11), linux 24.04/x64/cpu (e4, e5, e9, e10), macos 14.5/arm64/cpu
// (e6-e8, e12).
package appfixture

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

// Fixture is a migrated database holding the data set.
type Fixture struct {
	Pool   *pgxpool.Pool
	SiteID int32
	OrgID  int64
	Now    time.Time
	Loc    *time.Location
	// Issues maps "I1".."I3" to app_issues ids.
	Issues map[string]int64
}

// Hash returns the stand-in install or session hash for a label.
func Hash(label string) []byte {
	sum := sha256.Sum256([]byte("fixture:" + label))
	return sum[:]
}

// Event is one raw row.
type Event struct {
	ID, Kind, Level, Release, Platform, Env string
	OS, Arch, Device                        string
	At                                      time.Time
	Install, Session, Issue                 string
	Occ                                     int
}

// Insert writes one event; Issue is "I1".."I3" or empty.
func (f *Fixture) Insert(t testing.TB, e Event) {
	t.Helper()
	var issue any
	if e.Issue != "" {
		issue = f.Issues[e.Issue]
	}
	var install, session []byte
	if e.Install != "" {
		install = Hash(e.Install)
	}
	if e.Session != "" {
		session = Hash(e.Session)
	}
	if e.Env == "" {
		e.Env = "production"
	}
	if e.Occ == 0 {
		e.Occ = 1
	}
	if e.Level == "" {
		e.Level = "error"
	}
	if _, err := f.Pool.Exec(context.Background(), `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,level,release,environment,
platform,os_version,arch,device_class,runtime,error_type,frames,occurrences,install_hash,session_hash,tags,exit_code)
values($1,$2,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'electron','Err','a.go:1 f',$13,$14,$15,'{"gpu":"x"}',null)`,
		f.SiteID, e.At, e.ID, issue, e.Kind, e.Level, e.Release, e.Env, e.Platform, e.OS, e.Arch, e.Device, e.Occ, install, session); err != nil {
		t.Fatalf("insert event %s: %v", e.ID, err)
	}
}

func utc(month, day, hour, minute int) time.Time {
	return time.Date(2026, time.Month(month), day, hour, minute, 0, 0, time.UTC)
}

// New builds the database, the site and the data set.
func New(t testing.TB) *Fixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{Pool: pool, SiteID: 7, OrgID: 1, Now: utc(11, 15, 10, 0), Loc: loc, Issues: map[string]int64{}}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appfix000001','App','Asia/Kathmandu',90,'app')`); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAppEventPartitions(ctx, pool, utc(10, 1, 0, 0), utc(12, 1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	type issue struct {
		key, fp, kind, errType, title, status, resolvedIn, firstRel, lastRel string
		first, last                                                          time.Time
		count                                                                int
	}
	for _, i := range []issue{
		{"I1", "fp1", "crash", "SegFault", "SegFault in a.go f", "regressed", "", "1.9.0", "1.10.0", utc(11, 12, 6, 0), utc(11, 15, 9, 0), 4},
		{"I2", "fp2", "error", "ValueError", "ValueError in a.go f", "open", "", "1.9.0", "1.10.0", utc(11, 14, 3, 5), utc(11, 15, 8, 45), 5},
		{"I3", "fp3", "crash", "Panic", "Panic in a.go f", "resolved", "1.9.0", "1.10.0", "1.10.0", utc(11, 14, 18, 10), utc(11, 14, 18, 10), 1},
	} {
		var resolved any
		if i.resolvedIn != "" {
			resolved = i.resolvedIn
		}
		var id int64
		if err := pool.QueryRow(ctx, `insert into app_issues(site_id,fingerprint,kind,error_type,title,status,resolved_in_release,first_release,last_release,first_seen,last_seen,event_count)
values(7,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) returning id`, i.fp, i.kind, i.errType, i.title, i.status, resolved, i.firstRel, i.lastRel, i.first, i.last, i.count).Scan(&id); err != nil {
			t.Fatal(err)
		}
		f.Issues[i.key] = id
	}
	lin := func(e Event) Event { e.Platform, e.OS, e.Arch, e.Device = "linux", "22.04", "x64", "cuda"; return e }
	lin24 := func(e Event) Event { e.Platform, e.OS, e.Arch, e.Device = "linux", "24.04", "x64", "cpu"; return e }
	mac := func(e Event) Event { e.Platform, e.OS, e.Arch, e.Device = "macos", "14.5", "arm64", "cpu"; return e }
	for _, e := range []Event{
		lin(Event{ID: "e1", Kind: "crash", Level: "fatal", Release: "1.9.0", At: utc(11, 12, 6, 0), Install: "A", Session: "late", Issue: "I1"}),
		lin(Event{ID: "e2", Kind: "session", Release: "1.9.0", At: utc(11, 14, 2, 0), Install: "A", Session: "a1"}),
		lin(Event{ID: "e3", Kind: "crash", Level: "fatal", Release: "1.9.0", At: utc(11, 14, 2, 10), Install: "A", Session: "a1", Issue: "I1", Occ: 2}),
		lin24(Event{ID: "e4", Kind: "session", Release: "1.9.0", At: utc(11, 14, 3, 0), Install: "B", Session: "b1"}),
		lin24(Event{ID: "e5", Kind: "error", Release: "1.9.0", At: utc(11, 14, 3, 5), Install: "B", Session: "b1", Issue: "I2"}),
		mac(Event{ID: "e6", Kind: "session", Release: "1.10.0", At: utc(11, 14, 4, 0), Install: "C", Session: "c1"}),
		mac(Event{ID: "e7", Kind: "crash", Level: "fatal", Release: "1.10.0", At: utc(11, 14, 18, 10), Install: "C", Session: "c1", Issue: "I3"}),
		mac(Event{ID: "e8", Kind: "session", Release: "1.10.0", At: utc(11, 14, 18, 20), Install: "C", Session: "c2"}),
		lin24(Event{ID: "e9", Kind: "session", Release: "1.10.0", At: utc(11, 15, 8, 0), Install: "B", Session: "b2"}),
		lin24(Event{ID: "e10", Kind: "error", Release: "1.10.0", At: utc(11, 15, 8, 30), Install: "B", Issue: "I2", Occ: 3, Env: "beta"}),
		lin(Event{ID: "e11", Kind: "error", Level: "fatal", Release: "1.10.0", At: utc(11, 15, 9, 0), Install: "A", Session: "a2", Issue: "I1"}),
		mac(Event{ID: "e12", Kind: "error", Release: "1.10.0", At: utc(11, 15, 8, 45), Install: "C", Issue: "I2"}),
	} {
		f.Insert(t, e)
	}
	return f
}
