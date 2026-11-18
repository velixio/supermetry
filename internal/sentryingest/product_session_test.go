package sentryingest

import (
	"context"
	"sync"
	"testing"

	"supermetry/internal/appproduct"
	"supermetry/internal/sites"
)

type productSink struct {
	mu    sync.Mutex
	items []appproduct.SessionStart
}

func (p *productSink) AcceptSession(_ context.Context, _ sites.Site, s appproduct.SessionStart) appproduct.Outcome {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items = append(p.items, s)
	return appproduct.Outcome{}
}

func productEnv(t *testing.T) (*env, *sessionSink, *productSink) {
	t.Helper()
	e, sessions := sessionEnv(t)
	p := &productSink{}
	e.h.WithProductSessions(p)
	return e, sessions, p
}

func TestSessionInitBecomesProductStart(t *testing.T) {
	e, _, p := productEnv(t)
	body := sessionEnvelope(`{"sid":"s1","did":"inst-1","init":true,"started":"2026-11-17T13:50:00Z","status":"ok","attrs":{"release":"1.2.3","environment":"prod","user_agent":"Dalvik/2.1.0 (Linux; U; Android 14; Pixel 8)"}}`)
	if w := e.do(post(fdPath, fdQuery, body, nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if len(p.items) != 1 {
		t.Fatalf("starts = %d", len(p.items))
	}
	got := p.items[0]
	if got.InstallID != "inst-1" || got.Release != "1.2.3" || got.Environment != "prod" || got.Country != "DE" || got.Started.IsZero() {
		t.Fatalf("%+v", got)
	}
}

func TestSessionUpdateNoProductStart(t *testing.T) {
	e, _, p := productEnv(t)
	e.do(post(fdPath, fdQuery, sessionEnvelope(`{"sid":"s1","did":"inst-1","init":false,"started":"2026-11-17T13:50:00Z","status":"crashed"}`), nil))
	if len(p.items) != 0 {
		t.Fatalf("starts = %d", len(p.items))
	}
}

func TestSessionNoDidCountedNotProduct(t *testing.T) {
	e, sessions, p := productEnv(t)
	e.do(post(fdPath, fdQuery, sessionEnvelope(`{"sid":"s1","init":true,"started":"2026-11-17T13:50:00Z","status":"ok"}`), nil))
	if len(p.items) != 0 {
		t.Fatalf("starts = %d", len(p.items))
	}
	if got := e.h.Stats().Snapshot()["session_no_install"]; got != 1 {
		t.Fatalf("session_no_install = %d", got)
	}
	// The session-state row and the v1 init row are unaffected.
	if sessions.n() != 1 || e.queued() != 1 {
		t.Fatalf("updates=%d", sessions.n())
	}
}

func TestSessionAggregateNoProductStart(t *testing.T) {
	e, _, p := productEnv(t)
	e.do(post(fdPath, fdQuery, sessionEnvelope(`{"aggregates":[{"started":"2026-11-17T13:50:00Z","exited":2}]}`), nil))
	if len(p.items) != 0 || e.h.Stats().Snapshot()["session_no_install"] != 0 {
		t.Fatalf("starts = %d", len(p.items))
	}
}

func TestSessionRetryNoDoubleProductStart(t *testing.T) {
	e, _, p := productEnv(t)
	body := sessionEnvelope(`{"sid":"s1","did":"inst-1","init":true,"started":"2026-11-17T13:50:00Z","status":"ok"}`)
	e.do(post(fdPath, fdQuery, body, nil))
	e.do(post(fdPath, fdQuery, body, nil))
	if len(p.items) != 1 {
		t.Fatalf("starts = %d, want 1", len(p.items))
	}
}
