package ghstars

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeGitHub struct {
	repo        map[string]any
	stargazers  []map[string]any
	status      int
	rejectAuth  bool // 401 for any request that carries a token
	repoCalls   atomic.Int32
	starCalls   atomic.Int32
	lastAuth    atomic.Value
	lastAccepts atomic.Value
}

func newFakeGitHub(t *testing.T, stars int, gazers ...[3]string) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	f := &fakeGitHub{repo: map[string]any{
		"stargazers_count": stars, "forks_count": 3, "subscribers_count": 5, "open_issues_count": 2,
	}}
	for _, g := range gazers {
		id, _ := strconv.ParseInt(g[0], 10, 64)
		f.stargazers = append(f.stargazers, map[string]any{"starred_at": g[2], "user": map[string]any{"id": id, "login": g[1]}})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{name}", func(w http.ResponseWriter, r *http.Request) {
		f.repoCalls.Add(1)
		f.lastAuth.Store(r.Header.Get("Authorization"))
		if f.rejectAuth && r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		_ = json.NewEncoder(w).Encode(f.repo)
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/stargazers", func(w http.ResponseWriter, r *http.Request) {
		f.starCalls.Add(1)
		if f.rejectAuth && r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.lastAccepts.Store(r.Header.Get("Accept"))
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		lo, hi := (page-1)*perPage, page*perPage
		if lo > len(f.stargazers) {
			lo = len(f.stargazers)
		}
		if hi > len(f.stargazers) {
			hi = len(f.stargazers)
		}
		_ = json.NewEncoder(w).Encode(f.stargazers[lo:hi])
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return f, server
}

// repoService returns a service pointed at the fake GitHub, one tracked
// repository (token "tok-123", Kathmandu) and its store.
func repoService(t *testing.T, server *httptest.Server) (*Service, Repo, *Store) {
	t.Helper()
	svc, _ := newTestService(t)
	svc.APIBase = server.URL
	svc.HTTP = server.Client()
	svc.Now = func() time.Time { return at("2026-10-10T20:00:00Z") }
	repo, _, err := svc.Create(context.Background(), CreateInput{Repo: testRepo, Token: "tok-123", TimeZone: "Asia/Kathmandu"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	store, _, err := svc.StoreFor(context.Background(), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo, store
}

func TestReconcileBackfillsEmptyHistory(t *testing.T) {
	fake, server := newFakeGitHub(t, 3,
		[3]string{"1", "a", "2026-01-01T00:00:00Z"},
		[3]string{"2", "b", "2026-02-01T00:00:00Z"},
		[3]string{"3", "c", "2026-03-01T00:00:00Z"})
	svc, repo, store := repoService(t, server)
	ctx := context.Background()
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	events, _, _ := store.Events(ctx, "", 10)
	if len(events) != 3 || events[0].Login != "c" || events[0].Source != SourceBackfill || !events[0].OccurredAt.Equal(at("2026-03-01T00:00:00Z")) {
		t.Fatalf("events = %+v", events)
	}
	sum, _ := store.Summary(ctx, at("2026-10-10T20:00:00Z"))
	if sum.Stars != 3 || sum.Forks != 3 || sum.Watchers != 5 || sum.OpenIssues != 2 {
		t.Fatalf("state = %+v", sum)
	}
	// 20:00Z is Oct 11 01:45 in Kathmandu, so the snapshot belongs to Oct 11.
	var day string
	if err := svc.pool.QueryRow(ctx, `select day::text from github_star_snapshots`).Scan(&day); err != nil || day != "2026-10-11" {
		t.Fatalf("snapshot day = %q, %v", day, err)
	}
	if got := fake.lastAuth.Load(); got != "Bearer tok-123" {
		t.Errorf("authorization = %v", got)
	}
	if got := fake.lastAccepts.Load(); got != "application/vnd.github.star+json" {
		t.Errorf("accept = %v", got)
	}
	got, _ := svc.Get(ctx, repo.ID)
	if got.LastReconcileAt == nil || got.LastError != nil {
		t.Errorf("status = %+v", got)
	}
}

func TestReconcileRepairsMissedDeliveries(t *testing.T) {
	_, server := newFakeGitHub(t, 2,
		[3]string{"2", "b", "2026-02-01T00:00:00Z"},
		[3]string{"3", "c", "2026-09-30T00:00:00Z"})
	svc, repo, store := repoService(t, server)
	ctx := context.Background()
	if err := store.InsertEvents(ctx, []Event{
		{UserID: 1, Login: "a", Action: ActionStarred, OccurredAt: at("2026-01-01T00:00:00Z"), Source: SourceBackfill},
		{UserID: 2, Login: "b", Action: ActionStarred, OccurredAt: at("2026-02-01T00:00:00Z"), Source: SourceBackfill},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveState(ctx, Counts{Stars: 5}, at("2026-10-01T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	current, _ := store.CurrentStargazers(ctx)
	if len(current) != 2 || current[2] != "b" || current[3] != "c" {
		t.Fatalf("current = %v", current)
	}
	events, _, _ := store.Events(ctx, "", 10)
	var unstar, star *Event
	for i := range events {
		switch {
		case events[i].Login == "a" && events[i].Action == ActionUnstarred:
			unstar = &events[i]
		case events[i].Login == "c":
			star = &events[i]
		}
	}
	if unstar == nil || unstar.Source != SourceReconcile || !unstar.OccurredAt.Equal(at("2026-10-10T20:00:00Z")) {
		t.Errorf("unstar = %+v", unstar)
	}
	if star == nil || star.Source != SourceReconcile || !star.OccurredAt.Equal(at("2026-09-30T00:00:00Z")) {
		t.Errorf("star = %+v", star)
	}
	if stars, _, _ := store.Stars(ctx); stars != 2 {
		t.Errorf("stars = %d", stars)
	}
}

func TestReconcileSkipsStargazersWhenCountsMatch(t *testing.T) {
	fake, server := newFakeGitHub(t, 1, [3]string{"1", "a", "2026-01-01T00:00:00Z"})
	svc, repo, store := repoService(t, server)
	ctx := context.Background()
	if err := store.InsertEvents(ctx, []Event{{UserID: 1, Login: "a", Action: ActionStarred, OccurredAt: at("2026-01-01T00:00:00Z"), Source: SourceWebhook}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveState(ctx, Counts{Stars: 1}, at("2026-10-01T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	fake.repoCalls.Store(0) // Create made one validation call
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if fake.repoCalls.Load() != 1 || fake.starCalls.Load() != 0 {
		t.Fatalf("calls repo=%d stargazers=%d", fake.repoCalls.Load(), fake.starCalls.Load())
	}
	sum, _ := store.Summary(ctx, at("2026-10-10T20:00:00Z"))
	if sum.Forks != 3 || sum.Watchers != 5 {
		t.Fatalf("state = %+v", sum)
	}
}

func TestReconcileRecordsRateLimitOnTheRepository(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		fake, server := newFakeGitHub(t, 1)
		svc, repo, store := repoService(t, server)
		fake.status = status
		ctx := context.Background()
		if err := svc.ReconcileRepo(ctx, repo.ID); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("status %d: err = %v", status, err)
		}
		got, _ := svc.Get(ctx, repo.ID)
		if got.LastError == nil || got.LastReconcileAt == nil {
			t.Fatalf("status %d: repo = %+v", status, got)
		}
		if n, _ := store.EventCount(ctx); n != 0 {
			t.Fatalf("events = %d", n)
		}
	}
}

func TestReconcileAllIsolatesFailures(t *testing.T) {
	fake, server := newFakeGitHub(t, 1, [3]string{"1", "a", "2026-01-01T00:00:00Z"})
	svc, good, goodStore := repoService(t, server)
	ctx := context.Background()
	// "gone/repo" exists at creation but starts failing; a disabled repository is not touched.
	bad, _, err := svc.Create(ctx, CreateInput{Repo: "gone/repo"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	off, _, err := svc.Create(ctx, CreateInput{Repo: "velixio/off"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := svc.Update(ctx, off.ID, UpdateInput{Enabled: &disabled}, 0); err != nil {
		t.Fatal(err)
	}
	failing := newFailingTransport(server, "/repos/gone/repo")
	svc.HTTP = &http.Client{Transport: failing}
	if err := svc.ReconcileAll(ctx); err != nil {
		t.Fatalf("job should not fail: %v", err)
	}
	gotBad, _ := svc.Get(ctx, bad.ID)
	if gotBad.LastError == nil {
		t.Fatalf("bad repo status = %+v", gotBad)
	}
	gotGood, _ := svc.Get(ctx, good.ID)
	if gotGood.LastError != nil || gotGood.LastReconcileAt == nil {
		t.Fatalf("good repo status = %+v", gotGood)
	}
	if n, _ := goodStore.EventCount(ctx); n != 1 {
		t.Fatalf("good events = %d", n)
	}
	gotOff, _ := svc.Get(ctx, off.ID)
	if gotOff.LastReconcileAt != nil {
		t.Fatalf("disabled repo was reconciled: %+v", gotOff)
	}
	_ = fake
}

// failingTransport sends requests to the fake server but answers 500 for one path prefix.
type failingTransport struct {
	target *httptest.Server
	prefix string
}

func newFailingTransport(target *httptest.Server, prefix string) *failingTransport {
	return &failingTransport{target: target, prefix: prefix}
}

func (f *failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if len(r.URL.Path) >= len(f.prefix) && r.URL.Path[:len(f.prefix)] == f.prefix {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusInternalServerError)
		return rec.Result(), nil
	}
	return f.target.Client().Transport.RoundTrip(r)
}

func TestReconcileUsesEachRepositorysOwnToken(t *testing.T) {
	fake, server := newFakeGitHub(t, 0)
	svc, _, _ := repoService(t, server)
	ctx := context.Background()
	other, _, err := svc.Create(ctx, CreateInput{Repo: "velixio/public"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileRepo(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if got := fake.lastAuth.Load(); got != "" {
		t.Fatalf("tokenless repository sent %q", got)
	}
}

func TestStartSyncRunsInBackgroundAndIsRateLimited(t *testing.T) {
	_, server := newFakeGitHub(t, 1, [3]string{"1", "a", "2026-01-01T00:00:00Z"})
	svc, repo, store := repoService(t, server)
	ctx := context.Background()
	if err := svc.StartSync(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	svc.Wait()
	if n, _ := store.EventCount(ctx); n != 1 {
		t.Fatalf("events = %d", n)
	}
	if err := svc.StartSync(ctx, repo.ID); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("second sync err = %v", err)
	}
	svc.Now = func() time.Time { return at("2026-10-10T20:02:00Z") }
	if err := svc.StartSync(ctx, repo.ID); err != nil {
		t.Fatalf("sync after the gap: %v", err)
	}
	svc.Wait()
	if err := svc.StartSync(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown repo err = %v", err)
	}
}

func TestReconcileSkipsErasedUsers(t *testing.T) {
	_, server := newFakeGitHub(t, 2,
		[3]string{"1", "a", "2026-01-01T00:00:00Z"},
		[3]string{"2", "b", "2026-02-01T00:00:00Z"})
	svc, repo, store := repoService(t, server)
	ctx := context.Background()
	if err := store.InsertEvents(ctx, []Event{
		{UserID: 1, Login: "a", Action: ActionStarred, OccurredAt: at("2026-01-01T00:00:00Z"), Source: SourceBackfill},
		{UserID: 2, Login: "b", Action: ActionStarred, OccurredAt: at("2026-02-01T00:00:00Z"), Source: SourceBackfill},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EraseUser(ctx, "a", 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	events, _, _ := store.Events(ctx, "", 10)
	if len(events) != 1 || events[0].Login != "b" {
		t.Fatalf("events = %+v", events)
	}
	if stars, _, _ := store.Stars(ctx); stars != 2 {
		t.Fatalf("stars = %d", stars)
	}
}

func TestBackfillSkipsErasedUsers(t *testing.T) {
	_, server := newFakeGitHub(t, 2,
		[3]string{"1", "a", "2026-01-01T00:00:00Z"},
		[3]string{"2", "b", "2026-02-01T00:00:00Z"})
	svc, repo, store := repoService(t, server)
	ctx := context.Background()
	if _, err := svc.pool.Exec(ctx, `insert into github_star_erasures(repo,user_id) values ($1,2)`, repo.Repo); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.EventCount(ctx); n != 1 {
		t.Fatalf("events = %d", n)
	}
}

// A rejected token must not stop a public repository from syncing: the run is
// retried without it, the stars are saved, and the repository keeps a warning
// telling the admin to replace or clear the token.
func TestReconcileFallsBackWhenTheTokenIsRejected(t *testing.T) {
	fake, server := newFakeGitHub(t, 1, [3]string{"7", "ann", "2026-10-10T10:00:00Z"})
	svc, repo, store := repoService(t, server)
	fake.rejectAuth = true // the token goes stale after the repository was added
	ctx := context.Background()
	if err := svc.ReconcileRepo(ctx, repo.ID); !errors.Is(err, ErrTokenIgnored) {
		t.Fatalf("err = %v, want ErrTokenIgnored", err)
	}
	if n, _ := store.EventCount(ctx); n != 1 {
		t.Fatalf("events = %d, want the star saved without the token", n)
	}
	got, _ := svc.Get(ctx, repo.ID)
	if got.LastError == nil || !strings.Contains(*got.LastError, "token") || got.LastReconcileAt == nil {
		t.Fatalf("repo = %+v, want a token warning and a reconcile time", got)
	}
}

// Without a stored token a 401 is still an error, and nothing is retried.
func TestReconcileReportsBadTokenWhenThereIsNoFallback(t *testing.T) {
	fake, server := newFakeGitHub(t, 1)
	svc, _, _ := repoService(t, server)
	ctx := context.Background()
	other, _, err := svc.Create(ctx, CreateInput{Repo: "velixio/public"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	fake.status = http.StatusUnauthorized
	before := fake.repoCalls.Load()
	if err := svc.ReconcileRepo(ctx, other.ID); !errors.Is(err, ErrBadToken) {
		t.Fatalf("err = %v, want ErrBadToken", err)
	}
	if n := fake.repoCalls.Load() - before; n != 1 {
		t.Fatalf("sync calls = %d, want one (no retry without a token)", n)
	}
}
