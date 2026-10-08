package replay

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func TestSpillUploadsOnSessionClose(t *testing.T) {
	t.Parallel()
	fake := newFakeS3(t)
	store := newTestS3Store(t, fake)
	key := SessionKey(7, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), 99)

	if _, err := store.Append(context.Background(), key, bytes.NewBufferString("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(context.Background(), key, bytes.NewBufferString("second")); err != nil {
		t.Fatal(err)
	}
	if got := fake.object("panel/" + key); got != nil {
		t.Fatalf("object uploaded before close: %q", got)
	}
	if err := store.CloseSession(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if got := string(fake.object("panel/" + key)); got != "firstsecond" {
		t.Fatalf("uploaded object = %q, want firstsecond", got)
	}
}

func TestSpillClosesOnlyIdleSessions(t *testing.T) {
	t.Parallel()
	fake := newFakeS3(t)
	store := newTestS3Store(t, fake)
	now := time.Now()
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	idleKey := SessionKey(7, day, 1)
	activeKey := SessionKey(7, day, 2)
	for _, key := range []string{idleKey, activeKey} {
		if _, err := store.Append(context.Background(), key, bytes.NewBufferString(key)); err != nil {
			t.Fatal(err)
		}
	}
	idle, _ := store.spills.get(idleKey)
	if err := os.Chtimes(idle.path, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := store.CloseIdleSessions(context.Background(), now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := fake.object("panel/" + idleKey); got == nil {
		t.Fatal("idle spill was not uploaded")
	}
	if got := fake.object("panel/" + activeKey); got != nil {
		t.Fatal("active spill was uploaded")
	}
}

func TestSpillUploadsOnShutdown(t *testing.T) {
	t.Parallel()
	fake := newFakeS3(t)
	store := newTestS3Store(t, fake)
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	keys := []string{SessionKey(7, day, 1), SessionKey(7, day, 2)}
	for i, key := range keys {
		if _, err := store.Append(context.Background(), key, bytes.NewBufferString(string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, key := range keys {
		if got := string(fake.object("panel/" + key)); got != string(rune('a'+i)) {
			t.Errorf("object %q = %q", key, got)
		}
	}
	if _, err := store.Append(context.Background(), keys[0], bytes.NewBufferString("late")); err == nil {
		t.Fatal("Append after Close succeeded")
	}
}

func newTestS3Store(t *testing.T, fake *fakeS3) *S3Store {
	t.Helper()
	store, err := NewS3Store(S3Settings{
		Endpoint: fake.URL(), Bucket: "replays", Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret", Prefix: "panel",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	return store
}
