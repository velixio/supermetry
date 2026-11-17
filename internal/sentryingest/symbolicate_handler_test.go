package sentryingest

import (
	"net/http/httptest"
	"strings"

	"fmt"
	"net/http"
	"supermetry/internal/sites"
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

func TestHandlerBudgetExhaustedIs429EvenWhenSymbolicated(t *testing.T) {
	v := loadVite(t)
	run := func(withSymbolicator bool) []int {
		reg := newRegistry(appSite(fdSite, sitesKey(fdKey)))
		intake := appingest.NewIntake(appingest.Config{KeyBudgetPerMinute: 2})
		h := NewHandler(reg, intake, nil, nil, nil, func() time.Time { return fdNow }).WithIPLimit(-1)
		if withSymbolicator {
			s := NewSymbolicator(newFakeArtifacts(), newMemBlobs(), SymbolicatorConfig{Stats: &Stats{}})
			defer s.Close()
			h.WithSymbolicator(s)
		}
		var codes []int
		for i := 0; i < 6; i++ {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, post(fdPath, fdQuery, jsEnvelope(v, fmt.Sprintf("%032d", i), "1.0.0"), nil))
			codes = append(codes, w.Code)
			if w.Code == http.StatusTooManyRequests && (w.Header().Get("Retry-After") == "" || !strings.HasPrefix(w.Header().Get("X-Sentry-Rate-Limits"), "60:")) {
				t.Errorf("429 headers = %v", w.Header())
			}
			time.Sleep(20 * time.Millisecond) // let the worker take its token
		}
		return codes
	}
	without, with := run(false), run(true)
	if fmt.Sprint(without) != fmt.Sprint(with) || with[len(with)-1] != http.StatusTooManyRequests {
		t.Fatalf("without symbolicator %v, with %v", without, with)
	}
}

func sitesKey(k string) sites.DSNKey { return sites.DSNKey{PublicKey: k} }
