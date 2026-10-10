package replay

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// A recording file that is already gone must not stop CSS garbage collection.
func TestGarbageCollectCSSIgnoresMissingRecording(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	if _, err := store.PutIfAbsent(ctx, CSSKey(1, hash), bytes.NewBufferString("body{}")); err != nil {
		t.Fatal(err)
	}
	missing := SessionKey(1, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), 424853833437825670)
	rows := []retainedReplay{{sessionID: 424853833437825670, blobKey: missing}}
	if err := garbageCollectCSS(ctx, store, 1, rows, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("garbageCollectCSS with a missing recording: %v", err)
	}
}
