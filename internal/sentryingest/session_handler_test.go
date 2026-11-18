package sentryingest

import (
	"fmt"
	"sync"
	"testing"

	"supermetry/internal/appingest"
	"supermetry/internal/appsessions"
)

type sessionSink struct {
	mu    sync.Mutex
	items []appsessions.Update
}

func (s *sessionSink) Enqueue(u appsessions.Update) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, u)
	return true
}

func (s *sessionSink) n() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func sessionEnvelope(payload string) string {
	return `{"event_id":"11111111111111111111111111111111"}` + "\n" +
		fmt.Sprintf(`{"type":"session","length":%d}`, len(payload)) + "\n" + payload + "\n"
}

func sessionEnv(t *testing.T) (*env, *sessionSink) {
	t.Helper()
	e := newEnv(t)
	e.reg.sites[fdSite].App.HashSecret = testSecret
	sink := &sessionSink{}
	e.h.WithSessions(sink)
	return e, sink
}

func TestHandlerSessionUpdateNotInitIsQueued(t *testing.T) {
	e, sink := sessionEnv(t)
	body := sessionEnvelope(`{"sid":"s1","init":false,"started":"2026-11-17T13:50:00Z","status":"crashed","errors":1}`)
	if w := e.do(post(fdPath, fdQuery, body, nil)); w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if sink.n() != 1 || e.queued() != 0 {
		t.Fatalf("updates=%d", sink.n())
	}
	if sink.items[0].Status != "crashed" || sink.items[0].SiteID != fdSite {
		t.Fatalf("%+v", sink.items[0])
	}
}

func TestHandlerSessionInitEmitsEventAndUpdate(t *testing.T) {
	e, sink := sessionEnv(t)
	body := sessionEnvelope(`{"sid":"s1","init":true,"started":"2026-11-17T13:50:00Z","status":"ok"}`)
	e.do(post(fdPath, fdQuery, body, nil))
	if sink.n() != 1 || e.queued() != 1 {
		t.Fatalf("updates=%d", sink.n())
	}
	// A retried envelope is a duplicate event but its update still goes through.
	e.do(post(fdPath, fdQuery, body, nil))
	if sink.n() != 2 || e.queued() != 0 {
		t.Fatalf("after retry updates=%d", sink.n())
	}
}

func TestHandlerSessionAggregateQueuesNoUpdate(t *testing.T) {
	e, sink := sessionEnv(t)
	body := sessionEnvelope(`{"aggregates":[{"started":"2026-11-17T13:50:00Z","exited":2}]}`)
	e.do(post(fdPath, fdQuery, body, nil))
	if sink.n() != 0 || e.queued() != 1 {
		t.Fatalf("updates=%d", sink.n())
	}
}

func TestHandlerSessionUpdateRespectsBudget(t *testing.T) {
	e, sink := sessionEnv(t)
	e.intake = appingest.NewIntake(appingest.Config{KeyBudgetPerMinute: 2})
	e.h.intake = e.intake
	for i := 0; i < 4; i++ {
		body := sessionEnvelope(fmt.Sprintf(`{"sid":"s%d","init":false,"started":"2026-11-17T13:50:00Z","status":"exited"}`, i))
		e.do(post(fdPath, fdQuery, body, nil))
	}
	if sink.n() != 2 {
		t.Fatalf("updates = %d, want 2 (budget 2)", sink.n())
	}
}
