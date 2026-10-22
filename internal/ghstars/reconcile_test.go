package ghstars

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type fakeGitHub struct {
	repo        map[string]any
	stargazers  []map[string]any
	status      int
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
	mux.HandleFunc("GET /repos/velixio/supermetry", func(w http.ResponseWriter, r *http.Request) {
		f.repoCalls.Add(1)
		f.lastAuth.Store(r.Header.Get("Authorization"))
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		_ = json.NewEncoder(w).Encode(f.repo)
	})
	mux.HandleFunc("GET /repos/velixio/supermetry/stargazers", func(w http.ResponseWriter, r *http.Request) {
		f.starCalls.Add(1)
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

func newReconciler(store *Store, server *httptest.Server) *Reconciler {
	return &Reconciler{
		Store:  store,
		Client: &Client{BaseURL: server.URL, Token: "tok-123", HTTP: server.Client(), PageSize: 2},
		Now:    func() time.Time { return at("2026-10-10T20:00:00Z") },
	}
}

func TestReconcileBackfillsEmptyHistory(t *testing.T) {
	store, pool := newTestStore(t)
	fake, server := newFakeGitHub(t, 3,
		[3]string{"1", "a", "2026-01-01T00:00:00Z"},
		[3]string{"2", "b", "2026-02-01T00:00:00Z"},
		[3]string{"3", "c", "2026-03-01T00:00:00Z"})
	if err := newReconciler(store, server).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
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
	if err := pool.QueryRow(ctx, `select day::text from github_star_snapshots`).Scan(&day); err != nil || day != "2026-10-11" {
		t.Fatalf("snapshot day = %q, %v", day, err)
	}
	if got := fake.lastAuth.Load(); got != "Bearer tok-123" {
		t.Errorf("authorization = %v", got)
	}
	if got := fake.lastAccepts.Load(); got != "application/vnd.github.star+json" {
		t.Errorf("accept = %v", got)
	}
}

func TestReconcileRepairsMissedDeliveries(t *testing.T) {
	store, _ := newTestStore(t)
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
	// GitHub now has b and c: a unstarred and c starred without a webhook.
	_, server := newFakeGitHub(t, 2,
		[3]string{"2", "b", "2026-02-01T00:00:00Z"},
		[3]string{"3", "c", "2026-09-30T00:00:00Z"})
	if err := newReconciler(store, server).Run(ctx); err != nil {
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
	store, _ := newTestStore(t)
	ctx := context.Background()
	if err := store.InsertEvents(ctx, []Event{{UserID: 1, Login: "a", Action: ActionStarred, OccurredAt: at("2026-01-01T00:00:00Z"), Source: SourceWebhook}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveState(ctx, Counts{Stars: 1}, at("2026-10-01T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	fake, server := newFakeGitHub(t, 1, [3]string{"1", "a", "2026-01-01T00:00:00Z"})
	if err := newReconciler(store, server).Run(ctx); err != nil {
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

func TestReconcileStopsWhenRateLimited(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		store, _ := newTestStore(t)
		fake, server := newFakeGitHub(t, 1)
		fake.status = status
		r := newReconciler(store, server)
		if err := r.Run(context.Background()); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("status %d: err = %v", status, err)
		}
		if job := r.Job(); job.Run(context.Background()) != nil {
			t.Fatalf("status %d: job should retry next run without failing", status)
		}
		if n, _ := store.EventCount(context.Background()); n != 0 {
			t.Fatalf("events = %d", n)
		}
	}
}

func TestReconcileServerErrorLeavesDataAlone(t *testing.T) {
	store, _ := newTestStore(t)
	fake, server := newFakeGitHub(t, 1)
	fake.status = http.StatusInternalServerError
	err := newReconciler(store, server).Run(context.Background())
	if err == nil || errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
}

func TestReconcileSkipsErasedUsers(t *testing.T) {
	store, _ := newTestStore(t)
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
	// GitHub still lists the erased user, so its count (2) differs from the live row.
	_, server := newFakeGitHub(t, 2,
		[3]string{"1", "a", "2026-01-01T00:00:00Z"},
		[3]string{"2", "b", "2026-02-01T00:00:00Z"})
	if err := newReconciler(store, server).Run(ctx); err != nil {
		t.Fatal(err)
	}
	events, _, _ := store.Events(ctx, "", 10)
	if len(events) != 1 || events[0].Login != "b" {
		t.Fatalf("events = %+v", events)
	}
	// The live count still comes from GitHub, not from the stored events.
	if stars, _, _ := store.Stars(ctx); stars != 2 {
		t.Fatalf("stars = %d", stars)
	}
}

func TestBackfillSkipsErasedUsers(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into github_star_erasures(repo,user_id) values ($1,2)`, testRepo); err != nil {
		t.Fatal(err)
	}
	_, server := newFakeGitHub(t, 2,
		[3]string{"1", "a", "2026-01-01T00:00:00Z"},
		[3]string{"2", "b", "2026-02-01T00:00:00Z"})
	if err := newReconciler(store, server).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.EventCount(ctx); n != 1 {
		t.Fatalf("events = %d", n)
	}
}
