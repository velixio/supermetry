package ghtrending

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

// scripted answers Fetch from per-list queues; an empty queue repeats the last
// answer. Every call is recorded.
type scripted struct {
	mu      sync.Mutex
	answers map[listKey][]result
	calls   []listKey
}

type result struct {
	entries []Entry
	err     error
}

func (f *scripted) Fetch(_ context.Context, language string, since Since) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := listKey{language, since}
	f.calls = append(f.calls, k)
	q := f.answers[k]
	if len(q) == 0 {
		return nil, fmt.Errorf("no answer scripted for %v", k)
	}
	r := q[0]
	if len(q) > 1 {
		f.answers[k] = q[1:]
	}
	return r.entries, r.err
}

func (f *scripted) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

// page builds a readable list with the given repos at ranks 1..n, padded with
// filler so it clears MinEntries.
func page(repos ...string) []Entry {
	var out []Entry
	for i, r := range repos {
		out = append(out, Entry{Repo: r, Rank: i + 1})
	}
	for i := len(out); i < 8; i++ {
		out = append(out, Entry{Repo: fmt.Sprintf("filler/f%d", i), Rank: i + 1})
	}
	return out
}

// pageAt puts repo at the given rank.
func pageAt(repo string, rank int) []Entry {
	out := make([]Entry, 0, 10)
	for i := 1; i <= 10; i++ {
		if i == rank {
			out = append(out, Entry{Repo: repo, Rank: i})
		} else {
			out = append(out, Entry{Repo: fmt.Sprintf("filler/f%d", i), Rank: i})
		}
	}
	return out
}

type event struct {
	kind   string
	repoID int64
	s      Sighting
}

type recorder struct {
	mu     sync.Mutex
	events []event
}

func (r *recorder) Entered(_ context.Context, id int64, s Sighting) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{"entered", id, s})
}

func (r *recorder) Left(_ context.Context, id int64, s Sighting) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{"left", id, s})
}

func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.kind)
	}
	return out
}

type harness struct {
	svc  *Service
	pool *pgxpool.Pool
	f    *scripted
	rec  *recorder
	now  time.Time
	org  int64
	orgs []int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	h := &harness{pool: pool, f: &scripted{answers: map[listKey][]result{}}, rec: &recorder{}, now: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	for _, slug := range []string{"one", "two", "three", "four"} {
		var id int64
		if err := pool.QueryRow(context.Background(), `insert into orgs(name,slug) values ($1,$1) returning id`, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if h.org == 0 {
			h.org = id
		}
		h.orgs = append(h.orgs, id)
	}
	h.svc = NewService(pool, func() time.Time { h.now = h.now.Add(time.Minute); return h.now })
	h.svc.Fetcher, h.svc.Notifier = h.f, h.rec
	return h
}

func (h *harness) addRepo(t *testing.T, name, language string, trending bool) int64 {
	t.Helper()
	return h.addRepoIn(t, h.org, name, language, trending)
}

func (h *harness) addRepoIn(t *testing.T, org int64, name, language string, trending bool) int64 {
	t.Helper()
	var id int64
	err := h.pool.QueryRow(context.Background(), `
		insert into github_star_repos(org_id, public_id, repo, webhook_secret_enc, language, trending_enabled)
		values ($1,$2,$3,'\x00',$4,$5) returning id`, org, fmt.Sprintf("pub-%d-%s", org, name), name, language, trending).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *harness) script(language string, since Since, answers ...result) {
	h.f.answers[listKey{language, since}] = answers
}

func ok(e []Entry) result { return result{entries: e} }

func (h *harness) episodes(t *testing.T, repoID int64) []Sighting {
	t.Helper()
	var org int64
	if err := h.pool.QueryRow(context.Background(), `select org_id from github_star_repos where id=$1`, repoID).Scan(&org); err != nil {
		t.Fatal(err)
	}
	st, err := h.svc.Status(context.Background(), org, repoID)
	if err != nil {
		t.Fatal(err)
	}
	return st.Episodes
}

// Hand-computed episode sequence for repo a/a on the all-languages daily list.
// Each line is one successful fetch (or an unreadable page); "open" is whether
// an episode is open afterwards, "events" the notifications so far.
//
//	1  #7   -> enter (episode 1, first=best=last=7)          open  [entered]
//	2  #3   -> stay, best rank improves to 3                  open  [entered]
//	3  gone -> miss 1                                         open  [entered]
//	4  #5   -> back, misses reset, last=5, best stays 3       open  [entered]
//	5  gone -> miss 1                                         open
//	6  BAD  -> unreadable: nothing changes                    open
//	7  gone -> miss 2 -> close                                closed [entered left]
//	8  #9   -> re-entry, a new episode (2)                    open  [entered left entered]
func TestEpisodeStateMachine(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.addRepo(t, "a/a", "", true)
	k := listKey{"", Daily}
	unreadable := result{err: fmt.Errorf("%w: only 0 readable repositories", ErrUnreadable)}
	gone := ok(page("x/x"))

	steps := []struct {
		answer     result
		open       bool
		episodes   int
		best, last int
		events     []string
	}{
		{ok(pageAt("a/a", 7)), true, 1, 7, 7, []string{"entered"}},
		{ok(pageAt("a/a", 3)), true, 1, 3, 3, []string{"entered"}},
		{gone, true, 1, 3, 3, []string{"entered"}},
		{ok(pageAt("a/a", 5)), true, 1, 3, 5, []string{"entered"}},
		{gone, true, 1, 3, 5, []string{"entered"}},
		{unreadable, true, 1, 3, 5, []string{"entered"}},
		{gone, false, 1, 3, 5, []string{"entered", "left"}},
		{ok(pageAt("a/a", 9)), true, 2, 9, 9, []string{"entered", "left", "entered"}},
	}
	for i, step := range steps {
		h.f.answers = map[listKey][]result{k: {step.answer}, {"", Weekly}: {ok(gone.entries)}}
		if err := h.svc.Run(ctx); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		eps := h.episodes(t, id)
		if len(eps) != step.episodes {
			t.Fatalf("step %d: %d episodes, want %d", i+1, len(eps), step.episodes)
		}
		var cur Sighting
		for _, e := range eps {
			if e.Since == "daily" {
				cur = e // newest first
				break
			}
		}
		if (cur.LeftAt == nil) != step.open || cur.BestRank != step.best || cur.LastRank != step.last {
			t.Fatalf("step %d: episode %+v, want open=%v best=%d last=%d", i+1, cur, step.open, step.best, step.last)
		}
		if got := h.rec.kinds(); fmt.Sprint(got) != fmt.Sprint(step.events) {
			t.Fatalf("step %d: events %v, want %v", i+1, got, step.events)
		}
	}
	// The two episodes are distinct and the first one is closed with its ranks.
	eps := h.episodes(t, id)
	if eps[0].ID == eps[1].ID || eps[1].LeftAt == nil || eps[1].FirstRank != 7 || eps[1].BestRank != 3 {
		t.Fatalf("episodes = %+v", eps)
	}
	if h.rec.events[0].s.ID != eps[1].ID || h.rec.events[1].s.ID != eps[1].ID || h.rec.events[2].s.ID != eps[0].ID {
		t.Fatalf("events must carry the episode id: %+v", h.rec.events)
	}
}

// An unreadable or failing page never closes an episode, however often it
// repeats, and shows up as a visible state.
func TestUnreadableNeverCloses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.addRepo(t, "a/a", "", true)
	h.f.answers = map[listKey][]result{{"", Daily}: {ok(pageAt("a/a", 2))}, {"", Weekly}: {ok(page("x/x"))}}
	if err := h.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	bad := result{err: fmt.Errorf("%w: no repository rows found", ErrUnreadable)}
	down := result{err: errors.New("fetch trending: status 503")}
	for i, r := range []result{bad, bad, down, bad, down, bad} {
		h.f.answers = map[listKey][]result{{"", Daily}: {r}, {"", Weekly}: {r}}
		if err := h.svc.Run(ctx); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	for _, e := range h.episodes(t, id) {
		if e.LeftAt != nil {
			t.Fatalf("episode closed by an unreadable page: %+v", e)
		}
	}
	st, _ := h.svc.Status(ctx, h.org, id)
	if st.Status != StatusUnreadable || len(st.Current) != 1 {
		t.Fatalf("status = %+v", st)
	}
	var parseOK bool
	var lastErr string
	if err := h.pool.QueryRow(ctx, `select parse_ok, last_error from github_trending_list_state where list_language='' and since='daily'`).Scan(&parseOK, &lastErr); err != nil {
		t.Fatal(err)
	}
	if parseOK || lastErr == "" {
		t.Fatalf("parse_ok=%v last_error=%q", parseOK, lastErr)
	}
	// A good page clears the state.
	h.f.answers = map[listKey][]result{{"", Daily}: {ok(pageAt("a/a", 4))}, {"", Weekly}: {ok(page("x/x"))}}
	if err := h.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ = h.svc.Status(ctx, h.org, id); st.Status != StatusWatching || st.LastCheckedAt == nil {
		t.Fatalf("status = %+v", st)
	}
}

// One fetched list serves every repository that needs it, whoever owns it.
func TestSharedListsAreFetchedOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.addRepoIn(t, h.orgs[0], "org1/a", "Go", true)
	b := h.addRepoIn(t, h.orgs[1], "org2/b", "go", true)
	// The same GitHub repository tracked by a second organization is a
	// separate row with its own episodes, served by the same fetched list.
	a2 := h.addRepoIn(t, h.orgs[2], "org1/a", "Go", true)
	c := h.addRepo(t, "org3/c", "Rust", true)
	off := h.addRepo(t, "org4/d", "Zig", false)
	for _, k := range []listKey{{"", Daily}, {"", Weekly}, {"go", Daily}, {"go", Weekly}, {"rust", Daily}, {"rust", Weekly}} {
		h.f.answers[k] = []result{ok(page("org1/a", "org2/b"))}
	}
	if err := h.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.f.callCount(); n != 6 {
		t.Fatalf("fetches = %d, want 6 (all, go, rust x daily, weekly): %v", n, h.f.calls)
	}
	// org1/a and org2/b are on the all list and the Go list: 2 repos x 2 lists x 2 windows.
	for _, id := range []int64{a, a2, b} {
		if eps := h.episodes(t, id); len(eps) != 4 {
			t.Fatalf("repo %d episodes = %d", id, len(eps))
		}
	}
	// c is on neither list's output, and the opted-out repo is untouched.
	if len(h.episodes(t, c)) != 0 || len(h.episodes(t, off)) != 0 {
		t.Fatal("unexpected episodes")
	}
}

func TestNothingToDoWhenNobodyOptedIn(t *testing.T) {
	h := newHarness(t)
	h.addRepo(t, "a/a", "Go", false)
	if err := h.svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var runs int
	if err := h.pool.QueryRow(context.Background(), `select count(*) from github_trending_run`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if h.f.callCount() != 0 || runs != 0 {
		t.Fatalf("calls=%d runs=%d", h.f.callCount(), runs)
	}
	job := h.svc.Job()
	if job.Name != "github-trending" || job.Every != 10*time.Minute || job.Run == nil {
		t.Fatalf("job = %+v", job)
	}
}

// With more lists than the cap, runs rotate oldest-first until all were read.
func TestCapRotatesFairly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.svc.MaxLists = 3
	for i, lang := range []string{"Go", "Rust", "Zig"} {
		h.addRepo(t, fmt.Sprintf("o/r%d", i), lang, true)
	}
	for _, lang := range []string{"", "go", "rust", "zig"} {
		for _, since := range Windows {
			h.script(lang, since, ok(page("x/x")))
		}
	}
	seen := map[listKey]int{}
	var deferred []int
	for run := 0; run < 3; run++ {
		before := len(h.f.calls)
		if err := h.svc.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if got := len(h.f.calls) - before; got != 3 {
			t.Fatalf("run %d fetched %d lists, want the cap of 3", run, got)
		}
		for _, k := range h.f.calls[before:] {
			seen[k]++
		}
		var d int
		if err := h.pool.QueryRow(ctx, `select lists_deferred from github_trending_run`).Scan(&d); err != nil {
			t.Fatal(err)
		}
		deferred = append(deferred, d)
	}
	// 8 lists, 9 slots over 3 runs: every list is read at least once, none more than twice.
	if len(seen) != 8 {
		t.Fatalf("lists read over three runs = %d, want 8: %v", len(seen), seen)
	}
	for k, n := range seen {
		if n > 2 {
			t.Fatalf("%v read %d times", k, n)
		}
	}
	if fmt.Sprint(deferred) != "[5 5 5]" {
		t.Fatalf("deferred = %v", deferred)
	}
	st, err := h.svc.Status(ctx, h.org, 1)
	if err != nil || st.DeferredLists != 5 {
		t.Fatalf("status deferred = %+v, %v", st, err)
	}
}

func TestMissingLanguageListIsPaused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.addRepo(t, "a/a", "Brainfuck", true)
	h.script("", Daily, ok(page("x/x")))
	h.script("", Weekly, ok(page("x/x")))
	h.script("brainfuck", Daily, result{err: ErrNoList})
	h.script("brainfuck", Weekly, result{err: ErrNoList})
	if err := h.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	first := h.f.callCount()
	if err := h.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.f.callCount() - first; got != 2 {
		t.Fatalf("second run fetched %d lists, want only the 2 all-language ones", got)
	}
	if st, _ := h.svc.Status(ctx, h.org, id); st.Status != StatusWatching {
		t.Fatalf("a missing language list must not make the repo unreadable: %+v", st)
	}
	h.now = h.now.Add(25 * time.Hour)
	before := h.f.callCount()
	if err := h.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if h.f.callCount()-before != 4 {
		t.Fatal("paused lists must be retried after a day")
	}
}

func TestAnnotationOncePerEpisode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.addRepo(t, "a/a", "Go", true)
	gone := ok(page("x/x"))
	run := func(allDaily, goDaily result) {
		h.f.answers = map[listKey][]result{{"", Daily}: {allDaily}, {"", Weekly}: {gone}, {"go", Daily}: {goDaily}, {"go", Weekly}: {gone}}
		if err := h.svc.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	labels := func() []string {
		rows, err := h.pool.Query(ctx, `select label from github_star_annotations a where a.repo_id=$1 order by a.id`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var l string
			rows.Scan(&l)
			out = append(out, l)
		}
		return out
	}
	run(ok(pageAt("a/a", 7)), ok(pageAt("a/a", 2)))
	run(ok(pageAt("a/a", 4)), ok(pageAt("a/a", 1)))
	want := []string{"On trending: all languages, daily, #7", "On trending: Go, daily, #2"}
	if got := labels(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	// Leave and re-enter: the new episode gets its own marker.
	run(gone, gone)
	run(gone, gone)
	run(ok(pageAt("a/a", 5)), gone)
	got := labels()
	if len(got) != 3 || got[2] != "On trending: all languages, daily, #5" {
		t.Fatalf("labels = %v", got)
	}
}

func TestDisablingClosesEpisodesQuietly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.addRepo(t, "a/a", "", true)
	h.f.answers = map[listKey][]result{{"", Daily}: {ok(pageAt("a/a", 1))}, {"", Weekly}: {ok(pageAt("a/a", 2))}}
	if err := h.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetEnabled(ctx, h.org, id, false); err != nil {
		t.Fatal(err)
	}
	st, _ := h.svc.Status(ctx, h.org, id)
	if st.Status != StatusOff || len(st.Current) != 0 || len(st.Episodes) != 2 {
		t.Fatalf("status = %+v", st)
	}
	if got := h.rec.kinds(); fmt.Sprint(got) != "[entered entered]" {
		t.Fatalf("events = %v", got)
	}
	if err := h.svc.SetEnabled(ctx, h.org, 9999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := h.svc.Status(ctx, h.org, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	// Another organization's repository is not found either.
	if _, err := h.svc.Status(ctx, h.orgs[1], id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org status err = %v", err)
	}
	if err := h.svc.SetEnabled(ctx, h.orgs[1], id, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org enable err = %v", err)
	}
}

// The migration: constraints and cascade.
func TestMigrationSchema(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.addRepo(t, "a/a", "", false)
	var enabled bool
	var language string
	if err := h.pool.QueryRow(ctx, `select trending_enabled, language from github_star_repos where id=$1`, id).Scan(&enabled, &language); err != nil || enabled || language != "" {
		t.Fatalf("defaults: %v %v %q", err, enabled, language)
	}
	ins := func(since string) error {
		_, err := h.pool.Exec(ctx, `insert into github_trending_sightings(repo_id, since, first_rank, best_rank, last_rank, first_seen_at, last_seen_at) values ($1,$2,1,1,1,now(),now())`, id, since)
		return err
	}
	if err := ins("monthly"); err == nil {
		t.Fatal("since must be daily or weekly")
	}
	if err := ins("daily"); err != nil {
		t.Fatal(err)
	}
	if err := ins("daily"); err == nil {
		t.Fatal("two open episodes for one list must be refused")
	}
	if _, err := h.pool.Exec(ctx, `delete from github_star_repos where id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	h.pool.QueryRow(ctx, `select count(*) from github_trending_sightings`).Scan(&n)
	if n != 0 {
		t.Fatal("sightings must cascade with the repository")
	}
}
