package imports

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestCSVReadErrorStopsParsingInsteadOfLooping(t *testing.T) {
	failure := errors.New("disk failed")
	input := io.MultiReader(
		strings.NewReader("created_at,session_id,event_type,url_path\n2026-01-01T00:00:00Z,s1,1,/\n"),
		iotest.ErrReader(failure),
	)
	done := make(chan error, 1)
	go func() {
		_, _, err := ParseUmami(input)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatalf("ParseUmami error = %v, want %v", err, failure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ParseUmami did not stop after a read error")
	}
}

func TestParsePlausibleRejectsTooManyEntries(t *testing.T) {
	restore := setLimit(&maxZIPEntries, 3)
	defer restore()
	files := map[string]string{}
	for i := range 4 {
		files[fmt.Sprintf("junk_%d.txt", i)] = "x"
	}
	if _, _, err := ParsePlausible(plausibleZIP(t, files)); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("error = %v, want entry-count error", err)
	}
}

func TestParsePlausibleRejectsTotalUncompressedSizeOverLimit(t *testing.T) {
	restore := setLimit(&maxZIPTotalBytes, uint64(150))
	defer restore()
	body := "date,visitors\n" + strings.Repeat("2026-01-01,1\n", 6)
	path := plausibleZIP(t, map[string]string{
		"imported_visitors_a.csv": body,
		"imported_pages_a.csv":    strings.ReplaceAll(body, "date,visitors", "date,page,visitors"),
	})
	if _, _, err := ParsePlausible(path); err == nil || !strings.Contains(err.Error(), "uncompressed") {
		t.Fatalf("error = %v, want total size error", err)
	}
}

func TestParserRejectsTooManyDistinctDimensionValues(t *testing.T) {
	restore := setLimit(&maxAggregateKeys, 3)
	defer restore()
	var body strings.Builder
	body.WriteString("date,page,visitors\n")
	for i := range 10 {
		fmt.Fprintf(&body, "2026-01-01,/p%d,1\n", i)
	}
	path := plausibleZIP(t, map[string]string{"imported_pages_a.csv": body.String()})
	if _, _, err := ParsePlausible(path); err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("error = %v, want distinct-key limit error", err)
	}
}

func TestParseUmamiRejectsTooManySessions(t *testing.T) {
	restore := setLimit(&maxUmamiSessions, 3)
	defer restore()
	var body strings.Builder
	body.WriteString("created_at,session_id,event_type,url_path\n")
	for i := range 10 {
		fmt.Fprintf(&body, "2026-01-01T00:00:00Z,s%d,1,/\n", i)
	}
	if _, _, err := ParseUmami(bytes.NewBufferString(body.String())); err == nil || !strings.Contains(err.Error(), "sessions") {
		t.Fatalf("error = %v, want session limit error", err)
	}
}

func TestProcessFileStopsWhenContextIsCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.csv")
	contents := "created_at,session_id,event_type,url_path\n" + strings.Repeat("2026-01-01T00:00:00Z,s1,1,/\n", 1000)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := ProcessFile(ctx, SourceUmami, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestManagerRecoversPanicAndMarksImportFailed(t *testing.T) {
	store := &failingStore{failed: make(chan int64, 1)}
	manager := NewManager(store, 1)
	manager.process = func(context.Context, Source, string) (Aggregate, ParseStats, error) {
		panic("boom")
	}
	path := filepath.Join(t.TempDir(), "upload")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = manager.Run(ctx)
	}()
	stop := func() {
		t.Helper()
		cancel()
		select {
		case <-done:
			if !errors.Is(runErr, context.Canceled) {
				t.Fatalf("manager shutdown error = %v, want context.Canceled", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("manager did not stop after cancellation")
		}
	}
	t.Cleanup(stop)
	if err := manager.Queue(Job{ImportID: 9, Source: SourceUmami, Path: path}); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-store.failed:
		if id != 9 {
			t.Fatalf("failed import = %d", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panicking import was not marked failed")
	}
	// Failure is recorded before runJob removes the upload in its defer.
	// Run returning proves that deferred cleanup has completed.
	stop()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file was not removed: %v", err)
	}
}

func TestManagerMarksInterruptedAndQueuedImportsFailedOnShutdown(t *testing.T) {
	store := &failingStore{failed: make(chan int64, 2)}
	manager := NewManager(store, 2)
	started := make(chan struct{})
	manager.process = func(ctx context.Context, _ Source, _ string) (Aggregate, ParseStats, error) {
		close(started)
		<-ctx.Done()
		return Aggregate{}, ParseStats{}, ctx.Err()
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	for _, id := range []int64{1, 2} {
		path := filepath.Join(dir, fmt.Sprint(id))
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := manager.Queue(Job{ImportID: id, Source: SourceUmami, Path: path}); err != nil {
			t.Fatal(err)
		}
	}
	<-started
	cancel()
	<-done
	got := map[int64]bool{}
	for range 2 {
		select {
		case id := <-store.failed:
			got[id] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("failed imports = %v, want 1 and 2", got)
		}
	}
	if store.ctxErr != nil {
		t.Fatalf("SetFailed received a canceled context: %v", store.ctxErr)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("temporary files left: %d", len(entries))
	}
}

type failingStore struct {
	failed chan int64
	ctxErr error
}

func (s *failingStore) SetRunning(context.Context, int64) error { return nil }
func (s *failingStore) SetFailed(ctx context.Context, id int64, _ error) error {
	if ctx.Err() != nil {
		s.ctxErr = ctx.Err()
	}
	s.failed <- id
	return nil
}
func (s *failingStore) Complete(context.Context, int64, Aggregate, ParseStats) error { return nil }

func setLimit[T any](target *T, value T) func() {
	previous := *target
	*target = value
	return func() { *target = previous }
}
