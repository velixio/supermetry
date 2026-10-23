package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/ghstars"
	"supermetry/internal/platform/admin"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type starsEnv struct {
	handler http.Handler
	svc     *ghstars.Service
	repo    ghstars.Repo
	store   *ghstars.Store
	secret  string
}

// newStarsEnv builds the handler with a service whose GitHub API is a fake
// that knows every repository. enabled=false leaves the service out.
func newStarsEnv(t *testing.T, enabled bool) starsEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var adminID, memberID int64
	for _, u := range []struct {
		email string
		admin bool
		id    *int64
	}{{"root@example.test", true, &adminID}, {"member@example.test", false, &memberID}} {
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ($1,'U','hash',$2) returning id`, u.email, u.admin).Scan(u.id); err != nil {
			t.Fatal(err)
		}
	}
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stargazers") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/missing") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"stargazers_count":0}`))
	}))
	t.Cleanup(github.Close)
	box, err := platformcrypto.NewSecretBox(make([]byte, 32), "test/stars")
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) }
	svc := ghstars.NewService(pool, box, "https://stats.example.test", now)
	svc.APIBase, svc.HTTP = github.URL, github.Client()
	t.Cleanup(svc.Wait)
	data := &store.Store{Pool: pool}
	deps := platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"admin":  {UserID: adminID, SiteIDs: []int32{}},
			"member": {UserID: memberID, SiteIDs: []int32{}},
		},
		Store: data,
		Admin: admin.New(data),
		Now:   now,
	}
	env := starsEnv{svc: svc}
	if enabled {
		deps.GitHubStars = svc
		repo, secret, err := svc.Create(ctx, ghstars.CreateInput{Repo: "velixio/supermetry", TimeZone: "Asia/Kathmandu"}, adminID)
		if err != nil {
			t.Fatal(err)
		}
		env.repo, env.secret = repo, secret
		env.store, _, _ = svc.StoreFor(ctx, repo.ID)
	}
	env.handler = platformhttp.New(deps)
	return env
}

func starsRequest(handler http.Handler, method, path, actor, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Test-Actor", actor)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "t")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "t"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

const starsBase = "/api/private/admin/github-stars"

func (e starsEnv) q(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return starsBase + path + sep + "repo_id=" + jsonNumber(e.repo.ID)
}

func TestAdminGitHubStarsAuthAndUnavailable(t *testing.T) {
	off := newStarsEnv(t, false)
	if got := starsRequest(off.handler, http.MethodGet, starsBase+"/repos", "member", ""); got.Code != http.StatusForbidden {
		t.Fatalf("member status = %d", got.Code)
	}
	if got := starsRequest(off.handler, http.MethodGet, starsBase+"/repos", "admin", ""); got.Code != http.StatusNotFound {
		t.Fatalf("no service status = %d", got.Code)
	}
	e := newStarsEnv(t, true)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/repos"}, {http.MethodPost, "/repos"}, {http.MethodPatch, "/repos/1"}, {http.MethodDelete, "/repos/1?delete_data=false"},
		{http.MethodPost, "/repos/1/rotate-secret"}, {http.MethodPost, "/repos/1/sync"},
		{http.MethodGet, "/summary?repo_id=1"}, {http.MethodGet, "/series?granularity=day&repo_id=1"}, {http.MethodGet, "/events?repo_id=1"},
		{http.MethodGet, "/annotations?repo_id=1"}, {http.MethodPost, "/annotations?repo_id=1"}, {http.MethodDelete, "/annotations/1?repo_id=1"},
		{http.MethodDelete, "/users/alice?repo_id=1"},
	} {
		if got := starsRequest(e.handler, route.method, starsBase+route.path, "member", `{}`); got.Code != http.StatusForbidden {
			t.Errorf("%s %s member status = %d", route.method, route.path, got.Code)
		}
	}
}

func TestAdminGitHubStarsRepoLifecycle(t *testing.T) {
	e := newStarsEnv(t, true)
	list := starsRequest(e.handler, http.MethodGet, starsBase+"/repos", "admin", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), e.secret) || strings.Contains(list.Body.String(), "webhook_secret") {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}

	created := starsRequest(e.handler, http.MethodPost, starsBase+"/repos", "admin", `{"repo":"Velixio/Docs","token":"ghp_secret","time_zone":"Asia/Kolkata"}`)
	var out struct {
		Repo struct {
			ID       int64  `json:"id"`
			Repo     string `json:"repo"`
			HasToken bool   `json:"has_token"`
			TimeZone string `json:"time_zone"`
		} `json:"repo"`
		WebhookSecret string `json:"webhook_secret"`
		WebhookURL    string `json:"webhook_url"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &out) != nil {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	if out.Repo.Repo != "velixio/docs" || !out.Repo.HasToken || out.Repo.TimeZone != "Asia/Kolkata" || len(out.WebhookSecret) != 64 || !strings.HasPrefix(out.WebhookURL, "https://stats.example.test/api/github/webhook/") {
		t.Fatalf("created = %+v", out)
	}
	if strings.Contains(created.Body.String(), "ghp_secret") {
		t.Fatal("token echoed")
	}
	id := jsonNumber(out.Repo.ID)
	relist := starsRequest(e.handler, http.MethodGet, starsBase+"/repos", "admin", "")
	if strings.Contains(relist.Body.String(), out.WebhookSecret) || strings.Contains(relist.Body.String(), "ghp_secret") || !strings.Contains(relist.Body.String(), `"has_token":true`) {
		t.Fatalf("relist leaks or hides: %s", relist.Body)
	}

	for name, tc := range map[string]struct {
		body string
		code int
	}{
		"duplicate":  {`{"repo":"velixio/docs"}`, http.StatusConflict},
		"bad shape":  {`{"repo":"nope"}`, http.StatusBadRequest},
		"bad zone":   {`{"repo":"a/b","time_zone":"Mars/Base"}`, http.StatusBadRequest},
		"not on gh":  {`{"repo":"x/missing"}`, http.StatusBadRequest},
		"extra keys": {`{"repo":"a/b","secret":"x"}`, http.StatusBadRequest},
	} {
		if got := starsRequest(e.handler, http.MethodPost, starsBase+"/repos", "admin", tc.body); got.Code != tc.code {
			t.Errorf("%s status = %d %s", name, got.Code, got.Body)
		}
	}

	patched := starsRequest(e.handler, http.MethodPatch, starsBase+"/repos/"+id, "admin", `{"enabled":false,"clear_token":true,"time_zone":"UTC"}`)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"enabled":false`) || !strings.Contains(patched.Body.String(), `"has_token":false`) {
		t.Fatalf("patch = %d %s", patched.Code, patched.Body)
	}
	if got := starsRequest(e.handler, http.MethodPatch, starsBase+"/repos/99999", "admin", `{}`); got.Code != http.StatusNotFound {
		t.Fatalf("patch unknown = %d", got.Code)
	}

	rotated := starsRequest(e.handler, http.MethodPost, starsBase+"/repos/"+id+"/rotate-secret", "admin", "")
	var rot struct {
		WebhookSecret string `json:"webhook_secret"`
	}
	if rotated.Code != http.StatusOK || json.Unmarshal(rotated.Body.Bytes(), &rot) != nil || len(rot.WebhookSecret) != 64 || rot.WebhookSecret == out.WebhookSecret {
		t.Fatalf("rotate = %d %s", rotated.Code, rotated.Body)
	}

	if got := starsRequest(e.handler, http.MethodDelete, starsBase+"/repos/"+id, "admin", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("delete without delete_data = %d", got.Code)
	}
	disabled := starsRequest(e.handler, http.MethodDelete, starsBase+"/repos/"+id+"?delete_data=false", "admin", "")
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"enabled":false`) {
		t.Fatalf("disable = %d %s", disabled.Code, disabled.Body)
	}
	if got := starsRequest(e.handler, http.MethodDelete, starsBase+"/repos/"+id+"?delete_data=true", "admin", ""); got.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", got.Code)
	}
	if got := starsRequest(e.handler, http.MethodDelete, starsBase+"/repos/"+id+"?delete_data=true", "admin", ""); got.Code != http.StatusNotFound {
		t.Fatalf("remove again = %d", got.Code)
	}
}

func TestAdminGitHubStarsSync(t *testing.T) {
	e := newStarsEnv(t, true)
	path := starsBase + "/repos/" + jsonNumber(e.repo.ID) + "/sync"
	if got := starsRequest(e.handler, http.MethodPost, path, "admin", ""); got.Code != http.StatusAccepted {
		t.Fatalf("sync = %d %s", got.Code, got.Body)
	}
	e.svc.Wait()
	if got := starsRequest(e.handler, http.MethodPost, path, "admin", ""); got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("second sync = %d", got.Code)
	}
	if got := starsRequest(e.handler, http.MethodPost, starsBase+"/repos/99999/sync", "admin", ""); got.Code != http.StatusNotFound {
		t.Fatalf("unknown sync = %d", got.Code)
	}
}

func TestAdminGitHubStarsRequiresAKnownRepo(t *testing.T) {
	e := newStarsEnv(t, true)
	for _, path := range []string{"/summary", "/events", "/annotations", "/series?granularity=day"} {
		if got := starsRequest(e.handler, http.MethodGet, starsBase+path, "admin", ""); got.Code != http.StatusBadRequest {
			t.Errorf("%s without repo_id = %d", path, got.Code)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		if got := starsRequest(e.handler, http.MethodGet, starsBase+path+sep+"repo_id=99999", "admin", ""); got.Code != http.StatusNotFound {
			t.Errorf("%s unknown repo = %d", path, got.Code)
		}
	}
}

func seedStars(t *testing.T, stars *ghstars.Store) {
	t.Helper()
	parse := func(v string) time.Time {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	ctx := context.Background()
	if err := stars.InsertEvents(ctx, []ghstars.Event{
		{UserID: 1, Login: "alice", Action: ghstars.ActionStarred, OccurredAt: parse("2026-10-02T03:00:00Z"), Source: ghstars.SourceWebhook},
		{UserID: 2, Login: "bob", Action: ghstars.ActionStarred, OccurredAt: parse("2026-10-02T04:00:00Z"), Source: ghstars.SourceWebhook},
		{UserID: 1, Login: "alice", Action: ghstars.ActionUnstarred, OccurredAt: parse("2026-10-02T05:00:00Z"), Source: ghstars.SourceWebhook},
	}); err != nil {
		t.Fatal(err)
	}
	if err := stars.SaveState(ctx, ghstars.Counts{Stars: 1, Forks: 2}, parse("2026-10-02T05:00:00Z")); err != nil {
		t.Fatal(err)
	}
}

func TestAdminGitHubStarsReads(t *testing.T) {
	e := newStarsEnv(t, true)
	seedStars(t, e.store)
	other, _, err := e.svc.Create(context.Background(), ghstars.CreateInput{Repo: "velixio/other"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	var summary struct {
		Stars    int `json:"stars"`
		Forks    int `json:"forks"`
		Net24h   int `json:"net_24h"`
		Net7d    int `json:"net_7d"`
		Recorded int `json:"recorded"`
	}
	got := starsRequest(e.handler, http.MethodGet, e.q("/summary"), "admin", "")
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &summary) != nil {
		t.Fatalf("summary = %d %s", got.Code, got.Body)
	}
	// Window ends 2026-10-03T00:00Z: +1 alice, +1 bob, -1 alice = net 1.
	if summary.Stars != 1 || summary.Forks != 2 || summary.Net24h != 1 || summary.Net7d != 1 || summary.Recorded != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	// Another repository's data is separate.
	empty := starsRequest(e.handler, http.MethodGet, starsBase+"/summary?repo_id="+jsonNumber(other.ID), "admin", "")
	if !strings.Contains(empty.Body.String(), `"stars":0`) || !strings.Contains(empty.Body.String(), `"net_7d":0`) {
		t.Fatalf("other summary = %s", empty.Body)
	}

	var series struct {
		TimeZone string `json:"time_zone"`
		Data     []struct {
			Gained, Lost, Net, Cumulative int
		} `json:"data"`
	}
	// Kathmandu day Oct 2 runs 2026-10-01T18:15Z to 2026-10-02T18:15Z and holds all three events.
	got = starsRequest(e.handler, http.MethodGet, e.q("/series?granularity=day&from=2026-10-01T18:15:00Z&to=2026-10-02T18:15:00Z"), "admin", "")
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &series) != nil {
		t.Fatalf("series = %d %s", got.Code, got.Body)
	}
	if series.TimeZone != "Asia/Kathmandu" || len(series.Data) != 1 || series.Data[0].Gained != 2 || series.Data[0].Lost != 1 || series.Data[0].Cumulative != 1 {
		t.Fatalf("series = %+v", series)
	}
	for _, bad := range []string{"granularity=week", "granularity=day&from=yesterday", "granularity=hour&from=2026-01-01T00:00:00Z"} {
		if got := starsRequest(e.handler, http.MethodGet, e.q("/series?"+bad), "admin", ""); got.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d", bad, got.Code)
		}
	}

	var events struct {
		Data       []struct{ Login, Action string } `json:"data"`
		NextCursor *string                          `json:"next_cursor"`
	}
	got = starsRequest(e.handler, http.MethodGet, e.q("/events?limit=2"), "admin", "")
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &events) != nil {
		t.Fatalf("events = %d %s", got.Code, got.Body)
	}
	if len(events.Data) != 2 || events.Data[0].Action != "unstarred" || events.NextCursor == nil {
		t.Fatalf("events = %+v", events)
	}
	if got := starsRequest(e.handler, http.MethodGet, e.q("/events?limit=x"), "admin", ""); got.Code != http.StatusBadRequest {
		t.Errorf("bad limit status = %d", got.Code)
	}
}

func TestAdminGitHubStarsAnnotationsAndErasure(t *testing.T) {
	e := newStarsEnv(t, true)
	seedStars(t, e.store)

	created := starsRequest(e.handler, http.MethodPost, e.q("/annotations"), "admin", `{"at":"2026-10-02T00:00:00Z","label":"Launch"}`)
	var annotation struct {
		ID        int64  `json:"id"`
		Label     string `json:"label"`
		CreatedBy *int64 `json:"created_by"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &annotation) != nil || annotation.Label != "Launch" || annotation.CreatedBy == nil {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	for _, body := range []string{`{"at":"2026-10-02T00:00:00Z","label":""}`, `{"label":"no time"}`, `{"at":"nope","label":"x"}`, `{"at":"2026-10-02T00:00:00Z","label":"x","extra":1}`} {
		if got := starsRequest(e.handler, http.MethodPost, e.q("/annotations"), "admin", body); got.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d", body, got.Code)
		}
	}
	if list := starsRequest(e.handler, http.MethodGet, e.q("/annotations"), "admin", ""); list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Launch") {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
	path := e.q("/annotations/" + jsonNumber(annotation.ID))
	if got := starsRequest(e.handler, http.MethodDelete, path, "admin", ""); got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", got.Code)
	}
	if got := starsRequest(e.handler, http.MethodDelete, path, "admin", ""); got.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", got.Code)
	}
	if got := starsRequest(e.handler, http.MethodDelete, e.q("/annotations/abc"), "admin", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", got.Code)
	}

	erased := starsRequest(e.handler, http.MethodDelete, e.q("/users/Alice"), "admin", "")
	if erased.Code != http.StatusOK || !strings.Contains(erased.Body.String(), `"deleted_events":2`) {
		t.Fatalf("erase = %d %s", erased.Code, erased.Body)
	}
	if n, _ := e.store.EventCount(context.Background()); n != 1 {
		t.Fatalf("remaining events = %d", n)
	}
	if got := starsRequest(e.handler, http.MethodDelete, e.q("/users/bad%20login"), "admin", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("bad login = %d", got.Code)
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
