package ghstars

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func seedStar(t *testing.T, store *Store, userID int64, login string, when string) {
	t.Helper()
	err := store.InsertEvents(context.Background(), []Event{{UserID: userID, Login: login, Action: ActionStarred, OccurredAt: at(when), Source: SourceWebhook}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTwoOrganizationsTrackingTheSameRepositoryAreIsolated(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	orgA, orgB := newOrg(t, svc, "org-a"), newOrg(t, svc, "org-b")
	repoA, secretA, err := svc.Create(ctx, orgA, CreateInput{Repo: "acme/tool"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	repoB, secretB, err := svc.Create(ctx, orgB, CreateInput{Repo: "acme/tool"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if repoA.ID == repoB.ID || secretA == secretB || repoA.WebhookURL == repoB.WebhookURL {
		t.Fatalf("repos must be independent: %+v %+v", repoA, repoB)
	}
	if _, _, err := svc.Create(ctx, orgA, CreateInput{Repo: "ACME/tool"}, 0); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate in one org = %v, want ErrExists", err)
	}
	storeA, _, _ := svc.StoreFor(ctx, orgA, repoA.ID)
	storeB, _, _ := svc.StoreFor(ctx, orgB, repoB.ID)
	seedStar(t, storeA, 1, "ann", "2026-10-02T10:00:00Z")
	seedStar(t, storeA, 2, "bob", "2026-10-03T10:00:00Z")
	seedStar(t, storeB, 3, "cat", "2026-10-03T11:00:00Z")
	if _, err := storeA.AddAnnotation(ctx, at("2026-10-03T00:00:00Z"), "launch", 0); err != nil {
		t.Fatal(err)
	}

	if n, _ := storeA.EventCount(ctx); n != 2 {
		t.Fatalf("org A events = %d, want 2", n)
	}
	if n, _ := storeB.EventCount(ctx); n != 1 {
		t.Fatalf("org B events = %d, want 1", n)
	}
	events, _, _ := storeB.Events(ctx, "", 50)
	if len(events) != 1 || events[0].Login != "cat" {
		t.Fatalf("org B events = %+v", events)
	}
	from, to := at("2026-10-01T00:00:00Z"), at("2026-10-05T00:00:00Z")
	pointsA, err := storeA.Series(ctx, "day", from, to)
	if err != nil {
		t.Fatal(err)
	}
	pointsB, _ := storeB.Series(ctx, "day", from, to)
	if last := pointsA[len(pointsA)-1].Cumulative; last != 2 {
		t.Fatalf("org A cumulative = %d, want 2", last)
	}
	if last := pointsB[len(pointsB)-1].Cumulative; last != 1 {
		t.Fatalf("org B cumulative = %d, want 1", last)
	}
	if anns, _ := storeB.Annotations(ctx); len(anns) != 0 {
		t.Fatalf("org B sees org A annotations: %+v", anns)
	}

	// Erasure in org A leaves org B untouched and its tombstone does not block org B.
	if _, err := storeA.EraseUser(ctx, "ann", 0); err != nil {
		t.Fatal(err)
	}
	seedStar(t, storeB, 1, "ann", "2026-10-04T10:00:00Z")
	if n, _ := storeB.EventCount(ctx); n != 2 {
		t.Fatalf("org B events after org A erasure = %d, want 2", n)
	}
	seedStar(t, storeA, 1, "ann", "2026-10-04T10:00:00Z")
	if n, _ := storeA.EventCount(ctx); n != 1 {
		t.Fatalf("erased user must stay out of org A: events = %d, want 1", n)
	}

	// Another organization cannot reach the repository by id.
	if _, _, err := svc.StoreFor(ctx, orgB, repoA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org StoreFor = %v", err)
	}
	if _, err := svc.Get(ctx, orgB, repoA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Get = %v", err)
	}
	disabled := false
	if _, err := svc.Update(ctx, orgB, repoA.ID, UpdateInput{Enabled: &disabled}, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Update = %v", err)
	}
	if _, _, err := svc.RotateSecret(ctx, orgB, repoA.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org RotateSecret = %v", err)
	}
	if _, err := svc.Delete(ctx, orgB, repoA.ID, true, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org Delete = %v", err)
	}
	if err := svc.StartSync(ctx, orgB, repoA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org StartSync = %v", err)
	}
	if list, _ := svc.List(ctx, orgB); len(list) != 1 || list[0].ID != repoB.ID {
		t.Fatalf("org B list = %+v", list)
	}

	// Deleting org A's repository removes only its data.
	if removed, err := svc.Delete(ctx, orgA, repoA.ID, true, 0); err != nil || !removed {
		t.Fatalf("delete = %v, %v", removed, err)
	}
	if n, _ := storeB.EventCount(ctx); n != 2 {
		t.Fatalf("org B events after org A delete = %d, want 2", n)
	}
	if n, _ := storeA.EventCount(ctx); n != 0 {
		t.Fatalf("org A events after delete = %d, want 0", n)
	}
}

func TestDeletingAnOrganizationRemovesItsStarData(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	orgA, orgB := newOrg(t, svc, "gone"), newOrg(t, svc, "stays")
	repoA, _, _ := svc.Create(ctx, orgA, CreateInput{Repo: "acme/tool"}, 0)
	repoB, _, _ := svc.Create(ctx, orgB, CreateInput{Repo: "acme/tool"}, 0)
	storeA, _, _ := svc.StoreFor(ctx, orgA, repoA.ID)
	storeB, _, _ := svc.StoreFor(ctx, orgB, repoB.ID)
	seedStar(t, storeA, 1, "ann", "2026-10-02T10:00:00Z")
	seedStar(t, storeB, 1, "ann", "2026-10-02T10:00:00Z")
	if _, err := svc.pool.Exec(ctx, `delete from orgs where id=$1`, orgA); err != nil {
		t.Fatal(err)
	}
	var repos, events int
	_ = svc.pool.QueryRow(ctx, `select count(*) from github_star_repos`).Scan(&repos)
	_ = svc.pool.QueryRow(ctx, `select count(*) from github_star_events`).Scan(&events)
	if repos != 1 || events != 1 {
		t.Fatalf("repos=%d events=%d, want 1 and 1", repos, events)
	}
}

func TestRepositoryLimitDefaultsOverridesAndNeverDeletes(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	org, other := newOrg(t, svc, "capped"), newOrg(t, svc, "other")
	add := func(org int64, name string) error {
		_, _, err := svc.Create(ctx, org, CreateInput{Repo: name}, 0)
		return err
	}
	lim, err := svc.GetOrgLimit(ctx, org)
	if err != nil || lim.Limit == nil || *lim.Limit != 5 || lim.Source != "default" {
		t.Fatalf("default limit = %+v, %v", lim, err)
	}
	for i := 0; i < 5; i++ {
		if err := add(org, "o/r"+string(rune('a'+i))); err != nil {
			t.Fatalf("repo %d: %v", i, err)
		}
	}
	err = add(org, "o/sixth")
	if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "5 repositories") {
		t.Fatalf("sixth = %v, want ErrLimit mentioning the cap", err)
	}
	// The cap is per organization.
	if err := add(other, "o/ra"); err != nil {
		t.Fatalf("other org: %v", err)
	}

	// Lower below the count: nothing is deleted, creation is refused, existing repos keep working.
	two := 2
	if err := svc.SetOrgLimit(ctx, org, &two, 0); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.List(ctx, org); len(list) != 5 {
		t.Fatalf("lowering the limit deleted repos: %d left", len(list))
	}
	if err := add(org, "o/seventh"); !errors.Is(err, ErrLimit) {
		t.Fatalf("over a lowered limit = %v", err)
	}
	list, _ := svc.List(ctx, org)
	if err := svc.ReconcileRepo(ctx, list[0].ID); err != nil {
		t.Fatalf("existing repo must keep syncing: %v", err)
	}

	// Zero means none.
	zero := 0
	if err := svc.SetOrgLimit(ctx, other, &zero, 0); err != nil {
		t.Fatal(err)
	}
	if err := add(other, "o/rb"); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit 0 = %v", err)
	}

	// Unlimited is nil.
	if err := svc.SetOrgLimit(ctx, org, nil, 0); err != nil {
		t.Fatal(err)
	}
	lim, _ = svc.GetOrgLimit(ctx, org)
	if lim.Limit != nil || lim.Source != "org" || lim.Tracked != 5 {
		t.Fatalf("unlimited = %+v", lim)
	}
	for i := 0; i < 8; i++ {
		if err := add(org, "u/r"+string(rune('a'+i))); err != nil {
			t.Fatalf("unlimited repo %d: %v", i, err)
		}
	}

	// Reset goes back to the instance default, which the instance can change.
	if err := svc.ResetOrgLimit(ctx, other, 0); err != nil {
		t.Fatal(err)
	}
	svc.RepoLimit = 1
	lim, _ = svc.GetOrgLimit(ctx, other)
	if lim.Limit == nil || *lim.Limit != 1 || lim.Source != "default" {
		t.Fatalf("after reset = %+v", lim)
	}
	if err := add(other, "o/rc"); !errors.Is(err, ErrLimit) {
		t.Fatalf("instance default 1 with one repo = %v", err)
	}

	neg := -3
	if err := svc.SetOrgLimit(ctx, org, &neg, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative = %v", err)
	}
	if err := svc.SetOrgLimit(ctx, 987654, &two, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown org = %v", err)
	}
}

func TestPublicCounterIsOptInAndOldestPublicWins(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	orgA, orgB, orgC := newOrg(t, svc, "a"), newOrg(t, svc, "b"), newOrg(t, svc, "c")
	mk := func(org int64, public bool, stars int) Repo {
		repo, _, err := svc.Create(ctx, org, CreateInput{Repo: "acme/tool", PublicCounter: public}, 0)
		if err != nil {
			t.Fatal(err)
		}
		store, _, _ := svc.StoreFor(ctx, org, repo.ID)
		if err := store.SaveState(ctx, Counts{Stars: stars}, at("2026-10-10T12:00:00Z")); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	first := mk(orgA, false, 100) // oldest but private
	second := mk(orgB, true, 200)
	mk(orgC, true, 300)
	h := &PublicCount{Service: svc}
	get := func() (int, string) {
		rec := publicRequest(h, http.MethodGet, "/api/github/stars/acme/tool")
		return rec.Code, rec.Body.String()
	}
	if code, body := get(); code != 200 || body != "{\"stars\":200}\n" {
		t.Fatalf("oldest public = %d %q", code, body)
	}
	// Opting the oldest in makes it win.
	on := true
	if _, err := svc.Update(ctx, orgA, first.ID, UpdateInput{PublicCounter: &on}, 0); err != nil {
		t.Fatal(err)
	}
	if code, body := get(); code != 200 || body != "{\"stars\":100}\n" {
		t.Fatalf("after opt-in = %d %q", code, body)
	}
	// Disabled or not public falls through to the next; none left gives the common 404.
	off := false
	if _, err := svc.Update(ctx, orgA, first.ID, UpdateInput{Enabled: &off}, 0); err != nil {
		t.Fatal(err)
	}
	if code, body := get(); code != 200 || body != "{\"stars\":200}\n" {
		t.Fatalf("after disable = %d %q", code, body)
	}
	for _, o := range []struct {
		org int64
		id  int64
	}{{orgB, second.ID}} {
		if _, err := svc.Update(ctx, o.org, o.id, UpdateInput{PublicCounter: &off}, 0); err != nil {
			t.Fatal(err)
		}
	}
	// orgC is still public; make everything non-public.
	listC, _ := svc.List(ctx, orgC)
	if _, err := svc.Update(ctx, orgC, listC[0].ID, UpdateInput{PublicCounter: &off}, 0); err != nil {
		t.Fatal(err)
	}
	if code, body := get(); code != 404 || body != notFoundBody {
		t.Fatalf("no public repo = %d %q", code, body)
	}
	// A deleted organization stops serving.
	if _, err := svc.Update(ctx, orgC, listC[0].ID, UpdateInput{PublicCounter: &on}, 0); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(); code != 200 {
		t.Fatalf("re-enabled = %d", code)
	}
	if _, err := svc.pool.Exec(ctx, `update orgs set deleted_at=now() where id=$1`, orgC); err != nil {
		t.Fatal(err)
	}
	if code, body := get(); code != 404 || body != notFoundBody {
		t.Fatalf("deleted org = %d %q", code, body)
	}
}

func TestScheduledSyncsRespectTheGlobalConcurrencyCap(t *testing.T) {
	svc, server := newTestService(t)
	var inFlight, peak, calls atomic.Int32
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		calls.Add(1)
		time.Sleep(40 * time.Millisecond)
		if strings.HasSuffix(r.URL.Path, "/stargazers") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"stargazers_count":0}`))
	})
	ctx := context.Background()
	for _, slug := range []string{"o1", "o2", "o3"} {
		org := newOrg(t, svc, slug)
		for _, name := range []string{"acme/x", "acme/y"} {
			if _, _, err := svc.Create(ctx, org, CreateInput{Repo: name}, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	calls.Store(0)
	peak.Store(0)
	svc.Stagger = -1
	if err := svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 6 {
		t.Fatalf("expected every repository to be synced, calls = %d", calls.Load())
	}
	if got := peak.Load(); got != 2 {
		t.Fatalf("peak concurrency = %d, want exactly the cap of 2", got)
	}
	svc.Concurrency = 1
	peak.Store(0)
	if err := svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrency = %d, want 1", got)
	}
}

func TestScheduledSyncsFailOnePerRepositoryAndSkipDeletedOrganizations(t *testing.T) {
	svc, server := newTestService(t)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/limited/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/stargazers") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"stargazers_count":0}`))
	})
	ctx := context.Background()
	orgA, orgB, orgDead := newOrg(t, svc, "a"), newOrg(t, svc, "b"), newOrg(t, svc, "dead")
	good, _, _ := svc.Create(ctx, orgA, CreateInput{Repo: "acme/ok"}, 0)
	limited, _, _ := svc.Create(ctx, orgB, CreateInput{Repo: "limited/repo"}, 0)
	dead, _, _ := svc.Create(ctx, orgDead, CreateInput{Repo: "acme/ok"}, 0)
	if _, err := svc.pool.Exec(ctx, `update orgs set deleted_at=now() where id=$1`, orgDead); err != nil {
		t.Fatal(err)
	}
	svc.Stagger = -1
	if err := svc.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	g, _ := svc.Get(ctx, orgA, good.ID)
	l, _ := svc.Get(ctx, orgB, limited.ID)
	d, _ := svc.Get(ctx, orgDead, dead.ID)
	if g.LastError != nil || g.LastReconcileAt == nil {
		t.Fatalf("good repo = %+v", g)
	}
	if l.LastError == nil || !strings.Contains(*l.LastError, "rate limit") {
		t.Fatalf("limited repo should carry its own error: %+v", l)
	}
	if d.LastReconcileAt != nil {
		t.Fatalf("deleted org's repo was synced: %+v", d)
	}
}

func TestSeriesMonthBucketsFollowTheConfiguredTimeZone(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	repo, _, err := svc.Create(ctx, orgOf(t, svc), CreateInput{Repo: "acme/months", TimeZone: "Asia/Kolkata"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	store, _, _ := svc.StoreFor(ctx, orgOf(t, svc), repo.ID)
	ev := func(user int64, login, action, when string) Event {
		return Event{UserID: user, Login: login, Action: action, OccurredAt: at(when), Source: SourceWebhook}
	}
	// Kolkata is UTC+5:30. Hand-computed local months:
	//   08-31T18:30Z = Sep 1 00:00 IST (exact boundary)      -> September
	//   09-30T19:00Z = Oct 1 00:30 IST (UTC would say Sept)  -> October
	//   10-15T00:00Z                                         -> October
	//   10-31T19:00Z = Nov 1 00:30 IST (UTC would say Oct)   -> November
	//   11-10T00:00Z unstar of user 1                        -> November
	if err := store.InsertEvents(ctx, []Event{
		ev(4, "dee", ActionStarred, "2026-08-31T18:30:00Z"),
		ev(1, "ann", ActionStarred, "2026-09-30T19:00:00Z"),
		ev(3, "cat", ActionStarred, "2026-10-15T00:00:00Z"),
		ev(2, "bob", ActionStarred, "2026-10-31T19:00:00Z"),
		ev(1, "ann", ActionUnstarred, "2026-11-10T00:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	points, err := store.Series(ctx, "month", at("2026-08-31T18:30:00Z"), at("2026-11-30T18:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ month, gained, lost, net, cumulative int }{
		{9, 1, 0, 1, 1}, {10, 2, 0, 2, 3}, {11, 1, 1, 0, 3},
	}
	if len(points) != len(want) {
		t.Fatalf("points = %+v", points)
	}
	for i, w := range want {
		p := points[i]
		if int(p.Bucket.Month()) != w.month || p.Bucket.Day() != 1 || p.Bucket.Hour() != 0 || p.Gained != w.gained || p.Lost != w.lost || p.Net != w.net || p.Cumulative != w.cumulative {
			t.Errorf("point %d = %+v, want month %d gained %d lost %d net %d cumulative %d", i, p, w.month, w.gained, w.lost, w.net, w.cumulative)
		}
		if _, off := p.Bucket.Zone(); off != 5*3600+1800 {
			t.Errorf("point %d offset = %d", i, off)
		}
	}
	// A range starting mid-month keeps the earlier stars as the baseline of the first bucket.
	points, _ = store.Series(ctx, "month", at("2026-10-31T18:30:00Z"), at("2026-11-30T18:30:00Z"))
	if len(points) != 1 || points[0].Cumulative != 3 || points[0].Gained != 1 || points[0].Lost != 1 {
		t.Fatalf("november only = %+v", points)
	}
	if _, err := store.Series(ctx, "month", at("2000-01-01T00:00:00Z"), at("2026-01-01T00:00:00Z")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over-long month range = %v", err)
	}
	first, ok, err := store.FirstActivity(ctx)
	if err != nil || !ok || !first.Equal(at("2026-08-31T18:30:00Z")) {
		t.Fatalf("first activity = %v %v %v", first, ok, err)
	}
	empty, _, _ := svc.Create(ctx, orgOf(t, svc), CreateInput{Repo: "acme/empty"}, 0)
	emptyStore, _, _ := svc.StoreFor(ctx, orgOf(t, svc), empty.ID)
	if _, ok, _ := emptyStore.FirstActivity(ctx); ok {
		t.Fatal("empty repository has no first activity")
	}
}
