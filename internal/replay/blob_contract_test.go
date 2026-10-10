package replay

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDiskStoreContract(t *testing.T) {
	root := t.TempDir()
	store, err := NewDiskStore(root)
	if err != nil {
		t.Fatal(err)
	}
	testBlobStoreContract(t, store)

	key := SessionKey(42, time.Date(2026, 10, 8, 0, 0, 0, 0, time.FixedZone("site", 5*60*60+30*60)), 99)
	wantKey := "replay/42/2026-10-08/99.gz"
	if key != wantKey {
		t.Fatalf("SessionKey() = %q, want %q", key, wantKey)
	}
	if got := CSSKey(42, "abcdef"); got != "replay/42/css/abcdef.gz" {
		t.Fatalf("CSSKey() = %q", got)
	}
	if _, err := store.Append(context.Background(), key, bytes.NewBufferString("mode")); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(key)))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("file mode = %o, want 640", info.Mode().Perm())
	}
	parent, err := os.Stat(filepath.Dir(filepath.Join(root, filepath.FromSlash(key))))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && parent.Mode().Perm() != 0o750 {
		t.Fatalf("directory mode = %o, want 750", parent.Mode().Perm())
	}
}

func testBlobStoreContract(t *testing.T, store BlobStore) {
	t.Helper()
	ctx := context.Background()
	key := SessionKey(42, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), 99)
	for _, part := range []string{"one", "two", "three"} {
		written, err := store.Append(ctx, key, bytes.NewBufferString(part))
		if err != nil {
			t.Fatalf("Append(%q): %v", part, err)
		}
		if written != int64(len(part)) {
			t.Fatalf("Append(%q) wrote %d bytes, want %d", part, written, len(part))
		}
	}
	reader, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "onetwothree" {
		t.Fatalf("Open() = %q, want %q", got, "onetwothree")
	}

	cssKey := CSSKey(42, "abcdef")
	stored, err := store.PutIfAbsent(ctx, cssKey, bytes.NewBufferString("css"))
	if err != nil || !stored {
		t.Fatalf("first PutIfAbsent() = (%v, %v), want (true, nil)", stored, err)
	}
	stored, err = store.PutIfAbsent(ctx, cssKey, bytes.NewBufferString("replacement"))
	if err != nil || stored {
		t.Fatalf("second PutIfAbsent() = (%v, %v), want (false, nil)", stored, err)
	}
	blobs, err := store.List(ctx, "replay/42")
	if err != nil {
		t.Fatal(err)
	}
	wantSizes := map[string]int64{key: int64(len("onetwothree")), cssKey: int64(len("css"))}
	for _, blob := range blobs {
		if want, exists := wantSizes[blob.Key]; exists {
			if blob.Size != want {
				t.Fatalf("List() size for %q = %d, want %d", blob.Key, blob.Size, want)
			}
			delete(wantSizes, blob.Key)
		}
	}
	if len(wantSizes) != 0 {
		t.Fatalf("List() omitted blobs: %v", wantSizes)
	}
	deleteKey := "replay/42/delete.gz"
	if _, err := store.Append(ctx, deleteKey, bytes.NewBufferString("delete")); err != nil {
		t.Fatal(err)
	}
	freed, err := store.Delete(ctx, deleteKey)
	if err != nil || freed != int64(len("delete")) {
		t.Fatalf("Delete() = (%d, %v), want (6, nil)", freed, err)
	}
	freed, err = store.Delete(ctx, deleteKey)
	if err != nil || freed != 0 {
		t.Fatalf("second Delete() = (%d, %v), want (0, nil)", freed, err)
	}

	for _, badKey := range []string{"../escape", "replay/../escape", "/absolute"} {
		if _, err := store.Append(ctx, badKey, bytes.NewBufferString("bad")); err == nil {
			t.Errorf("Append(%q) succeeded, want rejection", badKey)
		}
	}

	freed, err = store.DeletePrefix(ctx, "replay/42")
	if err != nil {
		t.Fatal(err)
	}
	if freed != int64(len("onetwothree")+len("css")) {
		t.Fatalf("DeletePrefix() freed %d bytes, want %d", freed, len("onetwothree")+len("css"))
	}
}

func TestDiskStoreRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	store, err := NewDiskStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(context.Background(), "escape/file", bytes.NewBufferString("bad")); err == nil {
		t.Fatal("Append through escaping symlink succeeded")
	}
}

func TestAppendedGzipMembersAreOneStream(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := SessionKey(1, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), 7)
	for _, value := range []string{"a", "b", "c"} {
		if _, err := store.Append(ctx, key, bytes.NewReader(gzipMember(t, value))); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	zr, err := gzip.NewReader(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != "abc" {
		t.Fatalf("gzip stream = %q, want abc", got)
	}
}

func gzipMember(t *testing.T, value string) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := io.WriteString(zw, value); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
