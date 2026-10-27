package ghstars

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gazer struct {
	id    int64
	login string
	at    string
}

// fakeLarge is a fake GitHub with REST counts, a REST stargazer endpoint and a
// cursor-based GraphQL stargazer listing, newest first.
type fakeLarge struct {
	mu          sync.Mutex
	stars       int
	gazers      []gazer // newest first
	afters      []string
	restStatus  int // when 422, /stargazers answers "pagination is limited"
	restStarHit atomic.Int32
	restHit     atomic.Int32
	gqlHit      atomic.Int32
}

func newFakeLarge(t *testing.T, stars int, gazers ...gazer) (*fakeLarge, *httptest.Server) {
	t.Helper()
	f := &fakeLarge{stars: stars, gazers: gazers}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{name}", func(w http.ResponseWriter, _ *http.Request) {
		f.restHit.Add(1)
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"stargazers_count": f.stars, "forks_count": 2, "subscribers_count": 3, "open_issues_count": 1})
	})
	mux.HandleFunc("GET /repos/{owner}/{name}/stargazers", func(w http.ResponseWriter, _ *http.Request) {
		f.restStarHit.Add(1)
		f.mu.Lock()
		status := f.restStatus
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"message":"In order to keep the API fast for everyone, pagination is limited for this resource."}`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		f.gqlHit.Add(1)
		var body struct {
			Variables struct {
				First int     `json:"first"`
				After *string `json:"after"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		start := 0
		after := ""
		if body.Variables.After != nil {
			after = *body.Variables.After
			start, _ = strconv.Atoi(after)
		}
		f.afters = append(f.afters, after)
		end := start + body.Variables.First
		if end > len(f.gazers) {
			end = len(f.gazers)
		}
		var edges []map[string]any
		for _, g := range f.gazers[start:end] {
			edges = append(edges, map[string]any{"starredAt": g.at, "node": map[string]any{"databaseId": g.id, "login": g.login}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"stargazers": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": end < len(f.gazers), "endCursor": strconv.Itoa(end)},
			"edges":    edges,
		}}}})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return f, server
}

// Eight stargazers, newest first; ids equal their rank from the oldest.
var eight = []gazer{
	{8, "h", "2026-10-08T00:00:00Z"}, {7, "g", "2026-10-07T00:00:00Z"}, {6, "f", "2026-10-06T00:00:00Z"},
	{5, "e", "2026-10-05T00:00:00Z"}, {4, "d", "2026-10-04T00:00:00Z"}, {3, "c", "2026-10-03T00:00:00Z"},
	{2, "b", "2026-10-02T00:00:00Z"}, {1, "a", "2026-10-01T00:00:00Z"},
}

// largeService tracks velixio/supermetry with small numbers: stars above 5
// count as large, GraphQL pages hold 3 stargazers and a chunk is one page.
func largeService(t *testing.T, server *httptest.Server, token string) (*Service, Repo, *Store) {
	t.Helper()
	svc, _ := newTestService(t)
	svc.APIBase, svc.HTTP = server.URL, server.Client()
	svc.Now = func() time.Time { return at("2026-10-10T20:00:00Z") }
	svc.LargeThreshold, svc.ChunkPages, svc.GraphQLPageSize = 5, 1, 3
	repo, _, err := svc.Create(context.Background(), orgOf(t, svc), CreateInput{Repo: testRepo, Token: token, TimeZone: "UTC"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	store, _, err := svc.StoreFor(context.Background(), orgOf(t, svc), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo, store
}

func sourcesByUser(t *testing.T, store *Store) map[int64]string {
	t.Helper()
	events, _, err := store.Events(context.Background(), "", 200)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]string{}
	for _, e := range events {
		out[e.UserID] = e.Source
	}
	return out
}

func TestLargeRepoImportsInResumableChunks(t *testing.T) {
	fake, server := newFakeLarge(t, 8, eight...)
	svc, repo, store := largeService(t, server, "tok")
	ctx := context.Background()
	// Webhooks recorded f before the import; d asked to be erased.
	if err := store.InsertEvents(ctx, []Event{{UserID: 6, Login: "f", Action: ActionStarred, OccurredAt: at("2026-10-06T00:00:00Z"), Source: SourceWebhook}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pool.Exec(ctx, `insert into github_star_erasures(repo_id,user_id) values ($1,4)`, repo.ID); err != nil {
		t.Fatal(err)
	}

	// Run 1: the newest page (h, g, f). f is already known.
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	row, _ := svc.load(ctx, repo.ID)
	if row.backfillCursor == nil || *row.backfillCursor != "3" || row.backfillComplete || row.LastError != nil || row.LastReconcileAt == nil {
		t.Fatalf("after run 1: cursor=%v complete=%v err=%v", row.backfillCursor, row.backfillComplete, row.LastError)
	}
	if got := sourcesByUser(t, store); len(got) != 3 || got[8] != SourceBackfill || got[7] != SourceBackfill || got[6] != SourceWebhook {
		t.Fatalf("after run 1: %v", got)
	}
	// The headline number comes from the counts call, and the snapshot is saved on every run.
	if stars, _, _ := store.Stars(ctx); stars != 8 {
		t.Fatalf("stars = %d", stars)
	}
	var snapshots int
	_ = svc.pool.QueryRow(ctx, `select count(*) from github_star_snapshots`).Scan(&snapshots)
	if snapshots != 1 {
		t.Fatalf("snapshots = %d", snapshots)
	}

	// Run 2 resumes after the saved cursor: e, d (erased), c.
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	// Run 3 finishes: b, a.
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	row, _ = svc.load(ctx, repo.ID)
	if listed, _ := svc.Get(ctx, orgOf(t, svc), repo.ID); !listed.HistoryComplete {
		t.Fatal("the repository response should report the history as complete")
	}
	if row.backfillCursor != nil || !row.backfillComplete || row.LastError != nil {
		t.Fatalf("after run 3: cursor=%v complete=%v err=%v", row.backfillCursor, row.backfillComplete, row.LastError)
	}
	got := sourcesByUser(t, store)
	want := map[int64]string{8: SourceBackfill, 7: SourceBackfill, 6: SourceWebhook, 5: SourceBackfill, 3: SourceBackfill, 2: SourceBackfill, 1: SourceBackfill}
	if len(got) != len(want) {
		t.Fatalf("users = %v", got)
	}
	for id, source := range want {
		if got[id] != source {
			t.Errorf("user %d source = %q, want %q", id, got[id], source)
		}
	}
	if _, erased := got[4]; erased {
		t.Error("the erased user was imported")
	}
	fake.mu.Lock()
	afters := append([]string(nil), fake.afters...)
	fake.mu.Unlock()
	if len(afters) != 3 || afters[0] != "" || afters[1] != "3" || afters[2] != "6" {
		t.Fatalf("cursors sent = %q", afters)
	}
	if fake.restStarHit.Load() != 0 {
		t.Fatalf("REST stargazers called %d times for a large repository", fake.restStarHit.Load())
	}
	// A completed large repository does not import again.
	hits := fake.gqlHit.Load()
	if err := svc.ImportPending(ctx); err != nil || fake.gqlHit.Load() != hits {
		t.Fatalf("import job after completion: err=%v, graphql calls %d -> %d", err, hits, fake.gqlHit.Load())
	}
}

func TestChunkTimeBudgetStopsAfterAPage(t *testing.T) {
	fake, server := newFakeLarge(t, 8, eight...)
	svc, repo, store := largeService(t, server, "tok")
	svc.ChunkPages = 0                // no page cap
	svc.ChunkBudget = time.Nanosecond // spent as soon as the first page is saved
	ctx := context.Background()
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	row, _ := svc.load(ctx, repo.ID)
	if row.backfillCursor == nil || *row.backfillCursor != "3" || row.backfillComplete || fake.gqlHit.Load() != 1 {
		t.Fatalf("cursor=%v complete=%v calls=%d", row.backfillCursor, row.backfillComplete, fake.gqlHit.Load())
	}
	if n, _ := store.EventCount(ctx); n != 3 {
		t.Fatalf("events = %d", n)
	}
	// With a generous budget the next run goes to the end in one go.
	svc.ChunkBudget = time.Minute
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	row, _ = svc.load(ctx, repo.ID)
	if !row.backfillComplete || row.backfillCursor != nil {
		t.Fatalf("not complete: %+v", row)
	}
	if n, _ := store.EventCount(ctx); n != 8 {
		t.Fatalf("events = %d", n)
	}
}

func TestChunkStopsWhenCancelledAndKeepsItsProgress(t *testing.T) {
	_, server := newFakeLarge(t, 8, eight...)
	svc, repo, store := largeService(t, server, "tok")
	svc.ChunkPages = 0
	ctx, cancel := context.WithCancel(context.Background())
	svc.pageHook = func() { cancel() } // the context ends right after the first page is saved
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if n, _ := store.EventCount(context.Background()); n != 3 {
		t.Fatalf("events = %d", n)
	}
}

func TestLargeRepoIncrementalStopsAtTheFirstKnownPage(t *testing.T) {
	fake, server := newFakeLarge(t, 8, eight...)
	svc, repo, store := largeService(t, server, "tok")
	svc.ChunkPages = 0
	ctx := context.Background()
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if row, _ := svc.load(ctx, repo.ID); !row.backfillComplete {
		t.Fatal("setup: import did not complete")
	}
	// Two new stars arrive without a webhook; GitHub now lists j, i, then the old ones.
	fake.mu.Lock()
	fake.stars = 10
	fake.gazers = append([]gazer{{10, "j", "2026-10-10T00:00:00Z"}, {9, "i", "2026-10-09T00:00:00Z"}}, fake.gazers...)
	fake.afters = nil
	fake.mu.Unlock()
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	got := sourcesByUser(t, store)
	if len(got) != 10 || got[10] != SourceReconcile || got[9] != SourceReconcile || got[8] != SourceBackfill {
		t.Fatalf("users = %v", got)
	}
	// Page 1 (j, i, h) has two new stars; page 2 (g, f, e) is fully known, so the walk stops there.
	fake.mu.Lock()
	afters := append([]string(nil), fake.afters...)
	fake.mu.Unlock()
	if len(afters) != 2 || afters[0] != "" || afters[1] != "3" {
		t.Fatalf("pages read = %q", afters)
	}
	if stars, _, _ := store.Stars(ctx); stars != 10 {
		t.Fatalf("stars = %d", stars)
	}
	// Nothing new: one page, nothing inserted.
	fake.mu.Lock()
	fake.afters = nil
	fake.mu.Unlock()
	svc.lastSync = map[int64]time.Time{}
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	pages := len(fake.afters)
	fake.mu.Unlock()
	if n, _ := store.EventCount(ctx); n != 10 || pages != 1 {
		t.Fatalf("events=%d pages=%d", n, pages)
	}
}

func TestLargeRepoWithoutATokenNeedsOneButStillSavesTheSnapshot(t *testing.T) {
	fake, server := newFakeLarge(t, 8, eight...)
	svc, repo, store := largeService(t, server, "")
	ctx := context.Background()
	if err := svc.ReconcileRepo(ctx, repo.ID); !errors.Is(err, ErrTokenRequired) {
		t.Fatalf("err = %v", err)
	}
	if fake.gqlHit.Load() != 0 || fake.restStarHit.Load() != 0 {
		t.Fatal("stargazers were requested without a token")
	}
	var snapshots, stars int
	_ = svc.pool.QueryRow(ctx, `select count(*) from github_star_snapshots`).Scan(&snapshots)
	if s, _, _ := store.Stars(ctx); s != 8 {
		t.Fatalf("stars = %d", s)
	}
	_ = stars
	if snapshots != 1 {
		t.Fatalf("snapshots = %d", snapshots)
	}
	if row, _ := svc.load(ctx, repo.ID); row.LastError == nil {
		t.Fatal("the missing token should be reported on the repository")
	}
}

func TestRESTPaginationLimitFallsBackToGraphQL(t *testing.T) {
	fake, server := newFakeLarge(t, 4, eight[4:]...) // 4 stars: below the large threshold
	fake.restStatus = http.StatusUnprocessableEntity
	svc, repo, store := largeService(t, server, "tok")
	svc.ChunkPages = 0
	ctx := context.Background()
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatalf("err = %v", err)
	}
	if fake.restStarHit.Load() != 1 || fake.gqlHit.Load() == 0 {
		t.Fatalf("rest=%d graphql=%d", fake.restStarHit.Load(), fake.gqlHit.Load())
	}
	if n, _ := store.EventCount(ctx); n != 4 {
		t.Fatalf("events = %d", n)
	}
	if row, _ := svc.load(ctx, repo.ID); !row.backfillComplete || row.LastError != nil {
		t.Fatalf("row = %+v", row)
	}
	// Any other 422 is still an error.
	fake.restStatus = 0
	if !errors.Is(ErrPaginationLimited, ErrPaginationLimited) {
		t.Fatal("sentinel")
	}
}

func TestSmallRepoFullDiffMarksTheHistoryComplete(t *testing.T) {
	_, server := newFakeLarge(t, 0)
	svc, repo, _ := largeService(t, server, "tok")
	ctx := context.Background()
	if row, _ := svc.load(ctx, repo.ID); row.backfillComplete {
		t.Fatal("a new repository starts incomplete")
	}
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if row, _ := svc.load(ctx, repo.ID); !row.backfillComplete {
		t.Fatal("the full REST diff should mark the history complete")
	}
}

func TestImportJobIsANoOpForSmallAndCompleteRepositories(t *testing.T) {
	fake, server := newFakeLarge(t, 3, eight[5:]...)
	svc, repo, store := largeService(t, server, "tok")
	ctx := context.Background()
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil { // small: full REST diff, complete
		t.Fatal(err)
	}
	if job := svc.ImportJob(); job.Name != "github-stars-import" || job.Every != 10*time.Minute {
		t.Fatalf("job = %+v", job)
	}
	fake.restHit.Store(0)
	fake.gqlHit.Store(0)
	if err := svc.ImportPending(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.restHit.Load() != 0 || fake.gqlHit.Load() != 0 {
		t.Fatalf("a small complete repository was contacted: rest=%d graphql=%d", fake.restHit.Load(), fake.gqlHit.Load())
	}
	// A disabled large repository is not touched either.
	if _, err := store.pool.Exec(ctx, `update github_star_state set stars=100 where repo_id=$1`, repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `update github_star_repos set backfill_complete=false, enabled=false where id=$1`, repo.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportPending(ctx); err != nil || fake.restHit.Load() != 0 {
		t.Fatalf("disabled repository contacted: %v", err)
	}
	// Enabled, large and incomplete: the job works on it.
	if _, err := store.pool.Exec(ctx, `update github_star_repos set enabled=true where id=$1`, repo.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ImportPending(ctx); err != nil || fake.restHit.Load() == 0 {
		t.Fatalf("pending repository skipped: %v", err)
	}
}

func TestImportAndSyncNeverOverlap(t *testing.T) {
	_, server := newFakeLarge(t, 8, eight...)
	svc, repo, _ := largeService(t, server, "tok")
	ctx := context.Background()
	if !svc.acquire(repo.ID) {
		t.Fatal("could not take the lock")
	}
	if err := svc.ReconcileRepo(ctx, repo.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("reconcile while locked: %v", err)
	}
	if err := svc.StartSync(ctx, orgOf(t, svc), repo.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("sync while locked: %v", err)
	}
	if err := svc.ImportPending(ctx); err != nil {
		t.Fatalf("the job should skip a busy repository quietly: %v", err)
	}
	svc.release(repo.ID)
	if err := svc.StartSync(ctx, orgOf(t, svc), repo.ID); err != nil {
		t.Fatalf("sync after release: %v", err)
	}
	svc.Wait()
	if err := svc.ReconcileRepo(ctx, repo.ID); err != nil {
		t.Fatalf("lock leaked after the sync: %v", err)
	}
}
