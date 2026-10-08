package replay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/testpg"
)

func TestMigrateCopiesThenDeletes(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	prepareMigrationTables(t, pool)
	from := newMigrationDiskStore(t)
	to := newMigrationDiskStore(t)
	putMigrationBlob(t, ctx, pool, from, "replay/1/2026-10-08/1.gz", "one")
	putMigrationBlob(t, ctx, pool, from, "replay/1/2026-10-08/2.gz", "two")

	if err := RunMigration(ctx, pool, from, to); err != nil {
		t.Fatal(err)
	}
	assertBlob(t, ctx, to, "replay/1/2026-10-08/1.gz", "one")
	assertBlob(t, ctx, to, "replay/1/2026-10-08/2.gz", "two")
	assertBlobMissing(t, ctx, from, "replay/1/2026-10-08/1.gz")
	assertBlobMissing(t, ctx, from, "replay/1/2026-10-08/2.gz")

	var complete bool
	if err := pool.QueryRow(ctx, `select coalesce((settings->'replay_migration'->>'complete')::boolean, false) from instance_settings where id`).Scan(&complete); err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("migration checkpoint is not complete")
	}
}

func TestMigrateResumesAfterRestart(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	prepareMigrationTables(t, pool)
	from := newMigrationDiskStore(t)
	to := newMigrationDiskStore(t)
	first := "replay/1/2026-10-08/1.gz"
	second := "replay/1/2026-10-08/2.gz"
	putMigrationBlob(t, ctx, pool, from, first, "one")
	putMigrationBlob(t, ctx, pool, from, second, "two")

	failing := &failAfterStore{BlobStore: to, remaining: 1}
	if err := RunMigration(ctx, pool, from, failing); err == nil {
		t.Fatal("RunMigration succeeded despite injected destination failure")
	}
	assertBlob(t, ctx, to, first, "one")
	assertBlobMissing(t, ctx, from, first)
	assertBlob(t, ctx, from, second, "two")

	if err := RunMigration(ctx, pool, from, to); err != nil {
		t.Fatal(err)
	}
	assertBlob(t, ctx, to, second, "two")
	assertBlobMissing(t, ctx, from, second)
}

func prepareMigrationTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
create table instance_settings (
    id boolean primary key default true check (id),
    settings jsonb not null default '{}'::jsonb
);
insert into instance_settings default values;
create table replay_sessions (blob_key text not null);
`)
	if err != nil {
		t.Fatal(err)
	}
}

func newMigrationDiskStore(t *testing.T) *DiskStore {
	t.Helper()
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func putMigrationBlob(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store BlobStore, key, value string) {
	t.Helper()
	if _, err := store.Append(ctx, key, bytes.NewBufferString(value)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into replay_sessions(blob_key) values ($1)`, key); err != nil {
		t.Fatal(err)
	}
}

func assertBlob(t *testing.T, ctx context.Context, store BlobStore, key, want string) {
	t.Helper()
	reader, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("blob %q = %q, want %q", key, got, want)
	}
}

func assertBlobMissing(t *testing.T, ctx context.Context, store BlobStore, key string) {
	t.Helper()
	reader, err := store.Open(ctx, key)
	if err == nil {
		reader.Close()
		t.Fatalf("blob %q still exists", key)
	}
}

type failAfterStore struct {
	BlobStore
	remaining int
}

func (s *failAfterStore) PutIfAbsent(ctx context.Context, key string, reader io.Reader) (bool, error) {
	if s.remaining == 0 {
		return false, errors.New("injected destination failure")
	}
	s.remaining--
	return s.BlobStore.PutIfAbsent(ctx, key, reader)
}
