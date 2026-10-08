package replay

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/config"
	"panel/internal/db"
	"panel/internal/testpg"
)

func TestRetentionDeletesDaysOlderThanSiteSetting(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 30, 1<<20)
	oldKey := addReplaySession(t, pool, store, siteID, 1, now.AddDate(0, 0, -31), 10, nil)
	keptKey := addReplaySession(t, pool, store, siteID, 2, now.AddDate(0, 0, -29), 10, nil)

	if err := RunRetention(context.Background(), pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertBlobMissing(t, store, oldKey)
	assertBlobPresent(t, store, keptKey)
	assertReplaySessions(t, pool, siteID, []int64{2})
}

func TestQuotaDeletesOldestDaysFirst(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 30, 25)
	oldestKey := addReplaySession(t, pool, store, siteID, 1, now.AddDate(0, 0, -3), 10, nil)
	middleKey := addReplaySession(t, pool, store, siteID, 2, now.AddDate(0, 0, -2), 10, nil)
	newestKey := addReplaySession(t, pool, store, siteID, 3, now.AddDate(0, 0, -1), 10, nil)

	if err := RunRetention(context.Background(), pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertBlobMissing(t, store, oldestKey)
	assertBlobPresent(t, store, middleKey)
	assertBlobPresent(t, store, newestKey)
	assertReplaySessions(t, pool, siteID, []int64{2, 3})
}

func TestQuotaDeletesOldestSessionsWithinDay(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 30, 15)
	day := now.AddDate(0, 0, -1)
	oldestKey := addReplaySession(t, pool, store, siteID, 1, day.Add(time.Hour), 10, nil)
	middleKey := addReplaySession(t, pool, store, siteID, 2, day.Add(2*time.Hour), 10, nil)
	newestKey := addReplaySession(t, pool, store, siteID, 3, day.Add(3*time.Hour), 10, nil)

	if err := RunRetention(context.Background(), pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertBlobMissing(t, store, oldestKey)
	assertBlobMissing(t, store, middleKey)
	assertBlobPresent(t, store, newestKey)
	assertReplaySessions(t, pool, siteID, []int64{3})
}

func TestQuotaKeepsActiveSession(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 30, 5)
	key := addReplaySession(t, pool, store, siteID, 1, now.Add(-10*time.Minute), 10, nil)

	if err := RunRetention(context.Background(), pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertBlobPresent(t, store, key)
	assertReplaySessions(t, pool, siteID, []int64{1})
}

func TestRetentionEnvironmentOverridesSiteSetting(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 90, 1<<20)
	key := addReplaySession(t, pool, store, siteID, 1, now.AddDate(0, 0, -31), 10, nil)
	days := 30

	if err := RunRetentionConfigured(context.Background(), pool, store, now, config.ReplayEnv{Retention: &days}); err != nil {
		t.Fatal(err)
	}
	assertBlobMissing(t, store, key)
	assertReplaySessions(t, pool, siteID, nil)
}

func TestRetentionRetriesProfileDelete(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 30, 1<<20)
	deletedProfileID := int64(404)
	key := addReplaySession(t, pool, store, siteID, 1, now.AddDate(0, 0, -1), 10, &deletedProfileID)

	if err := RunRetention(context.Background(), pool, store, now); err != nil {
		t.Fatal(err)
	}
	assertBlobMissing(t, store, key)
	assertReplaySessions(t, pool, siteID, nil)
}

func TestEraseSiteRemovesFilesAndRows(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 30, 1<<20)
	firstKey := addReplaySession(t, pool, store, siteID, 1, now.AddDate(0, 0, -2), 10, nil)
	secondKey := addReplaySession(t, pool, store, siteID, 2, now.AddDate(0, 0, -1), 10, nil)

	if err := EraseSite(context.Background(), pool, store, siteID); err != nil {
		t.Fatal(err)
	}
	assertBlobMissing(t, store, firstKey)
	assertBlobMissing(t, store, secondKey)
	assertReplaySessions(t, pool, siteID, nil)
}

func TestEraseSessionsRemovesOnlyRequestedFilesAndRows(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 30, 1<<20)
	deletedKey := addReplaySession(t, pool, store, siteID, 1, now.AddDate(0, 0, -1), 10, nil)
	keptKey := addReplaySession(t, pool, store, siteID, 2, now.AddDate(0, 0, -1), 10, nil)

	if err := EraseSessions(context.Background(), pool, store, siteID, []int64{1}); err != nil {
		t.Fatal(err)
	}
	assertBlobMissing(t, store, deletedKey)
	assertBlobPresent(t, store, keptKey)
	assertReplaySessions(t, pool, siteID, []int64{2})
}

func TestFailedBlobDeleteLeavesIndexRowForRetry(t *testing.T) {
	pool, store, siteID, now := newRetentionFixture(t, 30, 1<<20)
	addReplaySession(t, pool, store, siteID, 1, now.AddDate(0, 0, -31), 10, nil)
	wantErr := errors.New("delete unavailable")
	failing := &deleteFailureStore{BlobStore: store, err: wantErr}

	err := RunRetention(context.Background(), pool, failing, now)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunRetention() error = %v, want %v", err, wantErr)
	}
	assertReplaySessions(t, pool, siteID, []int64{1})
}

type deleteFailureStore struct {
	BlobStore
	err error
}

func (s *deleteFailureStore) DeletePrefix(context.Context, string) (int64, error) {
	return 0, s.err
}

func newRetentionFixture(t *testing.T, retentionDays int, quotaBytes int64) (*pgxpool.Pool, BlobStore, int32, time.Time) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, statement := range []string{
		`create table replay_sessions_202609 partition of replay_sessions for values from ('2026-09-01') to ('2026-10-01')`,
		`create table replay_sessions_202610 partition of replay_sessions for values from ('2026-10-01') to ('2026-11-01')`,
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Replay retention','replay-retention') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,settings)
		values ($1,'replayretain','Replay retention','UTC',30,
		        jsonb_build_object('replay', jsonb_build_object('enabled',true,'retention_days',$2::int,'quota_bytes',$3::bigint)))
		returning id`, orgID, retentionDays, quotaBytes).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return pool, store, siteID, now
}

func addReplaySession(t *testing.T, pool *pgxpool.Pool, store BlobStore, siteID int32, sessionID int64, startedAt time.Time, size int, profileID *int64) string {
	t.Helper()
	day := time.Date(startedAt.Year(), startedAt.Month(), startedAt.Day(), 0, 0, 0, 0, time.UTC)
	key := SessionKey(siteID, day, sessionID)
	if _, err := store.Append(context.Background(), key, strings.NewReader(strings.Repeat("x", size))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,profile_id,blob_key)
		values ($1,$2,$3,$3,$4,$5,1,$6,$7)`, siteID, sessionID, startedAt, day, size, profileID, key); err != nil {
		t.Fatal(err)
	}
	return key
}

func assertReplaySessions(t *testing.T, pool *pgxpool.Pool, siteID int32, want []int64) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `select session_id from replay_sessions where site_id=$1 order by started_at,session_id`, siteID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var sessionID int64
		if err := rows.Scan(&sessionID); err != nil {
			t.Fatal(err)
		}
		got = append(got, sessionID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("replay sessions = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("replay sessions = %v, want %v", got, want)
		}
	}
}

func assertBlobPresent(t *testing.T, store BlobStore, key string) {
	t.Helper()
	reader, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open(%q): %v", key, err)
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertBlobMissing(t *testing.T, store BlobStore, key string) {
	t.Helper()
	reader, err := store.Open(context.Background(), key)
	if err == nil {
		_ = reader.Close()
		t.Fatalf("Open(%q) succeeded, want missing blob", key)
	}
}
