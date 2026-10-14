package replay

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func cssExists(t *testing.T, store BlobStore, hash string) bool {
	t.Helper()
	reader, err := store.Open(context.Background(), CSSKey(1, hash))
	if err != nil {
		return false
	}
	_ = reader.Close()
	return true
}

// A recording whose tail is damaged (zero padding after an unclean stop, or a
// cut-off last chunk) must not stop CSS garbage collection. Playback already
// reads such a file up to the first unreadable chunk; retention reads it the
// same way, so the CSS referenced by the readable part is kept and unreferenced
// CSS is still collected.
func TestGarbageCollectCSSToleratesDamagedRecordingTail(t *testing.T) {
	kept := strings.Repeat("a", 64)
	unreferenced := strings.Repeat("b", 64)
	good := gzipMember(t, `{"type":2,"data":{"node":{"_cssRef":"`+kept+`"}}}`+"\n")
	cut := gzipMember(t, `{"type":3,"data":{"node":{"_cssRef":"`+strings.Repeat("c", 64)+`"}}}`+"\n")

	cases := []struct {
		name string
		body []byte
	}{
		{"zero padding after valid chunks", append(append([]byte{}, good...), make([]byte, 938)...)},
		{"garbage after valid chunks", append(append([]byte{}, good...), []byte("not gzip at all")...)},
		{"last chunk cut off", append(append([]byte{}, good...), cut[:len(cut)/2]...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewDiskStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			for _, hash := range []string{kept, unreferenced} {
				if _, err := store.PutIfAbsent(ctx, CSSKey(1, hash), bytes.NewBufferString("body{}")); err != nil {
					t.Fatal(err)
				}
			}
			key := SessionKey(1, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), 4688299951020359180)
			if _, err := store.PutIfAbsent(ctx, key, bytes.NewReader(tc.body)); err != nil {
				t.Fatal(err)
			}
			rows := []retainedReplay{{sessionID: 4688299951020359180, blobKey: key}}
			if err := garbageCollectCSS(ctx, store, 1, rows, time.Now().Add(time.Hour)); err != nil {
				t.Fatalf("garbageCollectCSS with a damaged recording tail: %v", err)
			}
			if !cssExists(t, store, kept) {
				t.Fatal("CSS referenced by the readable part of the recording was deleted")
			}
			if cssExists(t, store, unreferenced) {
				t.Fatal("unreferenced CSS was not collected")
			}
		})
	}
}

// A recording that cannot be read at all (not gzip from the first byte) is
// still tolerated: it references nothing readable, and playback shows nothing.
func TestGarbageCollectCSSToleratesUnreadableRecording(t *testing.T) {
	store, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash := strings.Repeat("d", 64)
	if _, err := store.PutIfAbsent(ctx, CSSKey(1, hash), bytes.NewBufferString("body{}")); err != nil {
		t.Fatal(err)
	}
	key := SessionKey(1, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), 7)
	if _, err := store.PutIfAbsent(ctx, key, bytes.NewReader(make([]byte, 64))); err != nil {
		t.Fatal(err)
	}
	rows := []retainedReplay{{sessionID: 7, blobKey: key}}
	if err := garbageCollectCSS(ctx, store, 1, rows, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("garbageCollectCSS with an unreadable recording: %v", err)
	}
}
