package rehydrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/rehydrate"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/testpg"
)

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func TestKill9RestartContinuesSession(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -1), now.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	insertEvents(t, pool, 1, 77, 500, now.Add(-10*time.Minute), now.Add(-8*time.Minute), now.Add(-5*time.Minute))
	insertSessionRow(t, pool, 1, 77, 500, now.Add(-10*time.Minute), 1)

	memory := session.NewMemory(4, realClock{})
	restored, err := rehydrate.Run(ctx, pool, memory, now)
	if err != nil || restored != 1 {
		t.Fatalf("restored=%d err=%v", restored, err)
	}

	result := memory.Touch(session.Hit{
		SiteID:    1,
		Retention: db.Ret30,
		TS:        now,
		LocalDay:  salts.LocalDay(now, time.UTC),
		VisitorID: 77,
		Kind:      0,
		PathID:    3,
	})
	if result.New || result.SessionID != 500 {
		t.Fatalf("session split after restart: %+v", result)
	}
	if snapshot := findSnapshot(t, memory.Dirty(10), 500); snapshot.Pageviews != 4 || snapshot.UTMSource != 16 || snapshot.Region != 21 || snapshot.City != 22 ||
		snapshot.BrowserVer != 31 || snapshot.OSVer != 32 || snapshot.Language != 33 || snapshot.Hostname != 34 || snapshot.ScreenBucket != 3 {
		t.Fatalf("rehydrated snapshot=%+v, want first-event dimensions preserved", snapshot)
	}
}

func insertEvents(t *testing.T, pool *pgxpool.Pool, siteID int32, visitorID, sessionID int64, timestamps ...time.Time) {
	t.Helper()
	for index, timestamp := range timestamps {
		_, err := pool.Exec(context.Background(), `
INSERT INTO events (
    site_id, retention_class, local_day, ts, visitor_id, session_id,
    kind, path_id, source_id, utm_source_id, medium_id, campaign_id, country, region_id, city_id,
    browser_id, os_id, device, screen, browser_ver_id, os_ver_id, language_id, hostname_id, engagement_ms
) VALUES ($1, $2, $3, $4, $5, $6, 0, $7, 11, 16, 12, 13, 'US', 21, 22, 14, 15, 2, $8, $9, $10, $11, $12, 0)`,
			siteID, db.Ret30, timestamp.UTC().Truncate(24*time.Hour), timestamp, visitorID, sessionID, int32(index+1),
			int16(1280+index*640), int32(31+index), int32(32+index), int32(33+index), int32(34+index))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func insertSessionRow(t *testing.T, pool *pgxpool.Pool, siteID int32, visitorID, sessionID int64, start time.Time, pageviews int32) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
INSERT INTO sessions (
    site_id, retention_class, session_id, visitor_id, start, last, local_day,
    entry_path, exit_path, source_id, utm_source_id, medium_id, campaign_id, country, region_id, city_id,
    browser_id, os_id, device, pageviews, events, engagement_ms, bounce
) VALUES ($1, $2, $3, $4, $5, $5, $6, 1, 1, 11, 16, 12, 13, 'US', 21, 22, 14, 15, 2, $7, 0, 0, true)`,
		siteID, db.Ret30, sessionID, visitorID, start, start.UTC().Truncate(24*time.Hour), pageviews)
	if err != nil {
		t.Fatal(err)
	}
}

func findSnapshot(t *testing.T, snapshots []session.Snapshot, sessionID int64) session.Snapshot {
	t.Helper()
	for _, snapshot := range snapshots {
		if snapshot.SessionID == sessionID {
			return snapshot
		}
	}
	t.Fatalf("session %d not found in snapshots", sessionID)
	return session.Snapshot{}
}

func migratedPool(t *testing.T, now time.Time) *pgxpool.Pool {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -5), now.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	return pool
}

func insertFlushedSessionRow(t *testing.T, pool *pgxpool.Pool, siteID int32, visitorID, sessionID int64, start, last time.Time, pageviews int32) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
INSERT INTO sessions (
    site_id, retention_class, session_id, visitor_id, start, last, local_day,
    entry_path, exit_path, source_id, utm_source_id, medium_id, campaign_id, country, region_id, city_id,
    browser_id, os_id, device, pageviews, events, engagement_ms, bounce
) VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 1, 11, 16, 12, 13, 'US', 21, 22, 14, 15, 2, $8, 0, 0, false)`,
		siteID, db.Ret30, sessionID, visitorID, start, last, start.UTC().Truncate(24*time.Hour), pageviews)
	if err != nil {
		t.Fatal(err)
	}
}

func touch(memory *session.Memory, visitorID int64, now time.Time) session.Result {
	return memory.Touch(session.Hit{
		SiteID: 1, Retention: db.Ret30, TS: now, LocalDay: salts.LocalDay(now, time.UTC),
		VisitorID: visitorID, Kind: 0, PathID: 3,
	})
}

// A session ends only after 30 idle minutes, so a live one can be days old. Restoring it must
// read back to where it began, not just the last half hour.
func TestLongRunningSessionIsRestoredFromItsFirstEvent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := migratedPool(t, now)
	started := now.Add(-72 * time.Hour)
	insertEvents(t, pool, 1, 91, 900, started, now.Add(-48*time.Hour), now.Add(-24*time.Hour), now.Add(-10*time.Minute))
	insertFlushedSessionRow(t, pool, 1, 91, 900, started, now.Add(-10*time.Minute), 4)

	memory := session.NewMemory(4, realClock{})
	restored, err := rehydrate.Run(context.Background(), pool, memory, now)
	if err != nil || restored != 1 {
		t.Fatalf("restored=%d err=%v", restored, err)
	}
	if result := touch(memory, 91, now); result.New || result.SessionID != 900 {
		t.Fatalf("long session split after restart: %+v", result)
	}
	snapshot := findSnapshot(t, memory.Dirty(10), 900)
	if !snapshot.Start.Equal(started) || snapshot.Pageviews != 5 {
		t.Fatalf("long session restored as start=%s pageviews=%d, want start=%s pageviews=5 (4 stored + this hit)", snapshot.Start, snapshot.Pageviews, started)
	}
}

// A session row flushed a little before the cutoff, with events after it that were never
// flushed, is still live and must keep its original start.
func TestSessionFlushedJustBeforeTheCutoffKeepsItsStart(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := migratedPool(t, now)
	started := now.Add(-3 * time.Hour)
	insertEvents(t, pool, 1, 92, 901, started, now.Add(-35*time.Minute), now.Add(-2*time.Minute))
	insertFlushedSessionRow(t, pool, 1, 92, 901, started, now.Add(-35*time.Minute), 2)

	memory := session.NewMemory(4, realClock{})
	restored, err := rehydrate.Run(context.Background(), pool, memory, now)
	if err != nil || restored != 1 {
		t.Fatalf("restored=%d err=%v", restored, err)
	}
	if result := touch(memory, 92, now); result.New || result.SessionID != 901 {
		t.Fatalf("session split after restart: %+v", result)
	}
	snapshot := findSnapshot(t, memory.Dirty(10), 901)
	if !snapshot.Start.Equal(started) || snapshot.Pageviews != 4 {
		t.Fatalf("restored start=%s pageviews=%d, want start=%s pageviews=4 (3 stored + this hit)", snapshot.Start, snapshot.Pageviews, started)
	}
}

// Sessions that went idle long ago are history, not live sessions, however old their events.
func TestFinishedSessionsAreNotRestored(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := migratedPool(t, now)
	insertEvents(t, pool, 1, 93, 902, now.Add(-50*time.Hour), now.Add(-49*time.Hour))
	insertFlushedSessionRow(t, pool, 1, 93, 902, now.Add(-50*time.Hour), now.Add(-49*time.Hour), 2)
	insertEvents(t, pool, 1, 94, 903, now.Add(-4*time.Minute))
	insertFlushedSessionRow(t, pool, 1, 94, 903, now.Add(-4*time.Minute), now.Add(-4*time.Minute), 1)

	memory := session.NewMemory(4, realClock{})
	restored, err := rehydrate.Run(context.Background(), pool, memory, now)
	if err != nil || restored != 1 {
		t.Fatalf("restored=%d err=%v, want exactly the live session", restored, err)
	}
	if result := touch(memory, 94, now); result.New || result.SessionID != 903 {
		t.Fatalf("live session not continued: %+v", result)
	}
	if result := touch(memory, 93, now); !result.New {
		t.Fatalf("finished session was resurrected: %+v", result)
	}
}
