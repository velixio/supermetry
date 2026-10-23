package ghstars

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type fixedLimiter struct{ allow bool }

func (l fixedLimiter) Allow(string) bool { return l.allow }

type countingLimiter struct{ left int }

func (l *countingLimiter) Allow(string) bool {
	l.left--
	return l.left >= 0
}

func publicRequest(h http.Handler, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("GET /api/github/stars/{owner}/{name}", h)
	mux.Handle("OPTIONS /api/github/stars/{owner}/{name}", h)
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return out
}

func TestPublicCountServesOnlyTheStarCount(t *testing.T) {
	var githubCalls atomic.Int32
	svc, server := newTestService(t)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { githubCalls.Add(1); w.WriteHeader(500) })
	ctx := context.Background()
	repo, _, err := svc.Create(ctx, CreateInput{Repo: "Velixio/Supermetry"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	githubCalls.Store(0)
	store, _, _ := svc.StoreFor(ctx, repo.ID)
	if err := store.SaveState(ctx, Counts{Stars: 82412, Forks: 9, Watchers: 8, OpenIssues: 7}, at("2026-10-10T12:00:00Z")); err != nil {
		t.Fatal(err)
	}
	h := &PublicCount{Service: svc}

	rec := publicRequest(h, http.MethodGet, "/api/github/stars/VELIXIO/SuperMetry")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	body := decodeMap(t, rec)
	if len(body) != 1 || body["stars"] != float64(82412) {
		t.Fatalf("body = %v", body)
	}
	for key, want := range map[string]string{
		"Content-Type": "application/json", "Access-Control-Allow-Origin": "*", "Cache-Control": "public, max-age=60",
	} {
		if got := rec.Header().Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// The count follows what the webhook and reconcile keep current.
	if err := store.SaveState(ctx, Counts{Stars: 82413}, at("2026-10-10T12:01:00Z")); err != nil {
		t.Fatal(err)
	}
	if got := decodeMap(t, publicRequest(h, http.MethodGet, "/api/github/stars/velixio/supermetry"))["stars"]; got != float64(82413) {
		t.Fatalf("stars after update = %v", got)
	}
	if githubCalls.Load() != 0 {
		t.Fatalf("GitHub was called %d times", githubCalls.Load())
	}
}

func TestPublicCountTracksWebhookUpdates(t *testing.T) {
	env, _ := newHook(t)
	h := &PublicCount{Service: env.svc}
	if rec := publicRequest(h, http.MethodGet, "/api/github/stars/velixio/supermetry"); rec.Code != http.StatusNotFound {
		t.Fatalf("before first sync = %d", rec.Code)
	}
	if rec := env.do(env.signed("star", "pc1", starBody("created", testRepo, 77, "2026-10-10T11:00:00Z"))); rec.Code != http.StatusOK {
		t.Fatalf("webhook = %d", rec.Code)
	}
	if got := decodeMap(t, publicRequest(h, http.MethodGet, "/api/github/stars/velixio/supermetry"))["stars"]; got != float64(77) {
		t.Fatalf("stars = %v", got)
	}
}

func TestPublicCountNotFoundLooksTheSameEverywhere(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	synced, _, _ := svc.Create(ctx, CreateInput{Repo: "a/synced"}, 0)
	off, _, _ := svc.Create(ctx, CreateInput{Repo: "a/off"}, 0)
	_, _, _ = svc.Create(ctx, CreateInput{Repo: "a/fresh"}, 0)
	for _, r := range []Repo{synced, off} {
		store, _, _ := svc.StoreFor(ctx, r.ID)
		_ = store.SaveState(ctx, Counts{Stars: 5}, at("2026-10-10T12:00:00Z"))
	}
	disabled := false
	if _, err := svc.Update(ctx, off.ID, UpdateInput{Enabled: &disabled}, 0); err != nil {
		t.Fatal(err)
	}
	h := &PublicCount{Service: svc}
	if rec := publicRequest(h, http.MethodGet, "/api/github/stars/a/synced"); rec.Code != http.StatusOK {
		t.Fatalf("synced = %d", rec.Code)
	}
	var first string
	for _, path := range []string{"/api/github/stars/a/unknown", "/api/github/stars/a/off", "/api/github/stars/a/fresh", "/api/github/stars/bad%20owner/x"} {
		rec := publicRequest(h, http.MethodGet, path)
		if rec.Code != http.StatusNotFound || rec.Body.String() != "{\"error\":\"not found\"}\n" || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%s = %d %q", path, rec.Code, rec.Body)
		}
		if first == "" {
			first = rec.Body.String()
		}
		if rec.Body.String() != first {
			t.Fatalf("%s body differs", path)
		}
	}
}

func TestPublicCountHeadAndPreflight(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	repo, _, _ := svc.Create(ctx, CreateInput{Repo: "a/b"}, 0)
	store, _, _ := svc.StoreFor(ctx, repo.ID)
	_ = store.SaveState(ctx, Counts{Stars: 3}, at("2026-10-10T12:00:00Z"))
	h := &PublicCount{Service: svc}

	head := publicRequest(h, http.MethodHead, "/api/github/stars/a/b")
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Access-Control-Allow-Origin") != "*" || head.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("head = %d %q %v", head.Code, head.Body, head.Header())
	}
	pre := publicRequest(h, http.MethodOptions, "/api/github/stars/a/b")
	if pre.Code != http.StatusNoContent || pre.Header().Get("Access-Control-Allow-Origin") != "*" || pre.Header().Get("Access-Control-Allow-Methods") != "GET, HEAD, OPTIONS" || pre.Body.Len() != 0 {
		t.Fatalf("preflight = %d %v", pre.Code, pre.Header())
	}
}

func TestPublicCountRateLimit(t *testing.T) {
	svc, _ := newTestService(t)
	repo, _, _ := svc.Create(context.Background(), CreateInput{Repo: "a/b"}, 0)
	store, _, _ := svc.StoreFor(context.Background(), repo.ID)
	_ = store.SaveState(context.Background(), Counts{Stars: 3}, at("2026-10-10T12:00:00Z"))
	h := &PublicCount{Service: svc, Limiter: &countingLimiter{left: 2}}
	for i := 0; i < 2; i++ {
		if rec := publicRequest(h, http.MethodGet, "/api/github/stars/a/b"); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d", i, rec.Code)
		}
	}
	rec := publicRequest(h, http.MethodGet, "/api/github/stars/a/b")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("limited = %d %v", rec.Code, rec.Header())
	}
	if body := decodeMap(t, rec); len(body) != 1 || body["error"] == nil {
		t.Fatalf("body = %v", body)
	}
	if rec := publicRequest(&PublicCount{Service: svc, Limiter: fixedLimiter{false}}, http.MethodOptions, "/api/github/stars/a/b"); rec.Code != http.StatusNoContent {
		t.Fatalf("preflight should not be limited, got %d", rec.Code)
	}
}
