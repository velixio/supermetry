package sentryingest

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"supermetry/internal/appingest"
)

func jsEnvelope(v viteFixture, id, release string) string {
	payload := fmt.Sprintf(`{"event_id":%q,"timestamp":%d,"platform":"javascript","release":%q,"exception":{"values":[{"type":"Error","value":"boom","stacktrace":{"frames":[{"abs_path":"https://shop.example.com/assets/app.js","lineno":1,"colno":%d,"in_app":true}]}}]}}`,
		id, fdNow.Unix(), release, v.col+1)
	return `{"event_id":"` + id + `"}` + "\n" + `{"type":"event"}` + "\n" + payload + "\n"
}

func recvQueued(t testing.TB, in *appingest.Intake) appingest.Queued {
	t.Helper()
	select {
	case q := <-in.Queue():
		return q
	case <-time.After(5 * time.Second):
		t.Fatal("event never reached the intake")
		return appingest.Queued{}
	}
}

func TestHandlerNeverWaitsOnSymbolication(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["k"] = v.mapBytes
	store.addURL(fdSite, "1.0.0", "", "~/assets/app.js", "k")
	release := make(chan struct{})
	gate := &gatedStore{ArtifactStore: store, gate: release}
	e := newEnv(t)
	s := NewSymbolicator(gate, blobs, SymbolicatorConfig{Workers: 1, QueueSize: 4, Stats: &Stats{}})
	t.Cleanup(s.Close)
	e.h.WithSymbolicator(s)

	done := make(chan int, 1)
	go func() { done <- e.do(post(fdPath, fdQuery, jsEnvelope(v, fmt.Sprintf("%032d", 1), "1.0.0"), nil)).Code }()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the HTTP handler waited for symbolication")
	}
	if e.queued() != 0 {
		t.Fatal("the event reached the intake before symbolication finished")
	}
	close(release)
	q := recvQueued(t, e.intake)
	if q.Event.Symbolication != "ok" || q.SiteID != fdSite {
		t.Fatalf("queued event: %+v", q.Event)
	}
}

func TestHandlerFullSymbolicationQueueProceedsUnsymbolicated(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	release := make(chan struct{})
	defer close(release)
	gate := &gatedStore{ArtifactStore: store, gate: release}
	e := newEnv(t)
	stats := &Stats{}
	s := NewSymbolicator(gate, blobs, SymbolicatorConfig{Workers: 1, QueueSize: 1, Stats: stats})
	t.Cleanup(s.Close)
	e.h.WithSymbolicator(s)
	send := func(n int) int {
		return e.do(post(fdPath, fdQuery, jsEnvelope(v, fmt.Sprintf("%032d", n), "1.0.0"), nil)).Code
	}
	send(1) // held by the worker
	gate.waitEntered(t)
	send(2) // fills the queue
	if code := send(3); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	q := recvQueued(t, e.intake)
	if q.Event.Symbolication != "" || q.Event.ID != fmt.Sprintf("%032d", 3) {
		t.Fatalf("skipped event: %+v", q.Event)
	}
	if got := stats.Snapshot()["symbolication_skipped"]; got != 1 {
		t.Fatalf("symbolication_skipped = %d", got)
	}
}

func TestHandlerEventsWithoutScriptFramesBypassTheQueue(t *testing.T) {
	e := newEnv(t)
	s := NewSymbolicator(newFakeArtifacts(), newMemBlobs(), SymbolicatorConfig{Stats: &Stats{}})
	t.Cleanup(s.Close)
	e.h.WithSymbolicator(s)
	if w := e.do(post(fdPath, fdQuery, eventEnvelope(fmt.Sprintf("%032d", 9)), nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if e.queued() != 1 {
		t.Fatal("a message event must reach the intake synchronously")
	}
}
