package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/ghstars"
	"supermetry/internal/platform/admin"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type starsEnv struct {
	handler  http.Handler
	svc      *ghstars.Service
	repo     ghstars.Repo
	store    *ghstars.Store
	secret   string
	org      int64
	otherOrg int64
	// otherRepo belongs to otherOrg.
	otherRepo ghstars.Repo
	base      string
	pool      *pgxpool.Pool
}

// newStarsEnv builds the handler with a service whose GitHub API is a fake
// that knows every repository. enabled=false leaves the service out. Org one
// has an owner, an admin, an editor and a viewer; org two has its own owner.
// "root" is an instance admin who belongs to neither organization.
func newStarsEnv(t *testing.T, enabled bool) starsEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var env starsEnv
	env.pool = pool
	for slug, target := range map[string]*int64{"one": &env.org, "two": &env.otherOrg} {
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ($1,$1) returning id`, slug).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	users := map[string]int64{}
	for _, u := range []struct {
		name  string
		admin bool
	}{{"root", true}, {"owner", false}, {"admin", false}, {"editor", false}, {"viewer", false}, {"owner2", false}} {
		var id int64
		if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ($1,'U','hash',$2) returning id`, u.name+"@example.test", u.admin).Scan(&id); err != nil {
			t.Fatal(err)
		}
		users[u.name] = id
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
	member := func(name string, role authz.Role, org int64) authz.Principal {
		return authz.Principal{UserID: users[name], OrgID: org, Role: role, SiteIDs: nil}
	}
	deps := platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"root":   {UserID: users["root"], SiteIDs: []int32{}},
			"owner":  member("owner", authz.RoleOwner, env.org),
			"admin":  member("admin", authz.RoleAdmin, env.org),
			"editor": member("editor", authz.RoleEditor, env.org),
			"viewer": member("viewer", authz.RoleViewer, env.org),
			"owner2": member("owner2", authz.RoleOwner, env.otherOrg),
		},
		Store: data,
		Admin: admin.New(data),
		Now:   now,
	}
	env.svc = svc
	env.base = "/api/private/orgs/" + jsonNumber(env.org) + "/github-stars"
	if enabled {
		deps.GitHubStars = svc
		repo, secret, err := svc.Create(ctx, env.org, ghstars.CreateInput{Repo: "velixio/supermetry", TimeZone: "Asia/Kathmandu"}, users["owner"])
		if err != nil {
			t.Fatal(err)
		}
		env.repo, env.secret = repo, secret
		env.store, _, _ = svc.StoreFor(ctx, env.org, repo.ID)
		env.otherRepo, _, err = svc.Create(ctx, env.otherOrg, ghstars.CreateInput{Repo: "velixio/supermetry"}, users["owner2"])
		if err != nil {
			t.Fatal(err)
		}
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

func (e starsEnv) q(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return e.base + path + sep + "repo_id=" + jsonNumber(e.repo.ID)
}

type starsRoute struct {
	method, path string
	manage       bool
}

// starsRoutes lists every organization route; manage marks owner/admin-only ones.
func (e starsEnv) routes() []starsRoute {
	id, other := jsonNumber(e.repo.ID), jsonNumber(e.otherRepo.ID)
	_ = other
	return []starsRoute{
		{http.MethodGet, "/repos", false}, {http.MethodPost, "/repos", true}, {http.MethodPatch, "/repos/" + id, true},
		{http.MethodDelete, "/repos/" + id + "?delete_data=false", true}, {http.MethodPost, "/repos/" + id + "/rotate-secret", true},
		{http.MethodPost, "/repos/" + id + "/sync", true},
		{http.MethodGet, "/summary?repo_id=" + id, false}, {http.MethodGet, "/series?granularity=day&repo_id=" + id, false},
		{http.MethodGet, "/events?repo_id=" + id, false}, {http.MethodGet, "/annotations?repo_id=" + id, false},
		{http.MethodPost, "/annotations?repo_id=" + id, true}, {http.MethodDelete, "/annotations/1?repo_id=" + id, true},
		{http.MethodDelete, "/users/alice?repo_id=" + id, true},
	}
}

func TestOrgGitHubStarsAccessMatrix(t *testing.T) {
	off := newStarsEnv(t, false)
	if got := starsRequest(off.handler, http.MethodGet, off.base+"/repos", "viewer", ""); got.Code != http.StatusNotFound {
		t.Fatalf("no service status = %d", got.Code)
	}
	e := newStarsEnv(t, true)
	statusNever := map[int]bool{http.StatusForbidden: true, http.StatusUnauthorized: true, http.StatusNotFound: true}
	for _, route := range e.routes() {
		name := route.method + " " + route.path
		// No credentials at all.
		if got := starsRequest(e.handler, route.method, e.base+route.path, "nobody", `{}`); got.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous = %d", name, got.Code)
		}
		// Owner of another organization: the path names an organization they are not in.
		if got := starsRequest(e.handler, route.method, e.base+route.path, "owner2", `{}`); got.Code != http.StatusNotFound {
			t.Errorf("%s other-org owner = %d", name, got.Code)
		}
		// Instance admin who is not a member of the organization.
		if got := starsRequest(e.handler, route.method, e.base+route.path, "root", `{}`); !statusNever[got.Code] {
			t.Errorf("%s non-member instance admin = %d", name, got.Code)
		}
		for _, actor := range []string{"editor", "viewer"} {
			got := starsRequest(e.handler, route.method, e.base+route.path, actor, `{}`)
			switch {
			case route.manage && got.Code != http.StatusForbidden:
				t.Errorf("%s %s = %d, want 403", name, actor, got.Code)
			case !route.manage && got.Code != http.StatusOK:
				t.Errorf("%s %s = %d, want 200", name, actor, got.Code)
			}
		}
	}
	// Owners and admins pass the permission check on reads and on writes.
	for _, actor := range []string{"owner", "admin"} {
		for _, route := range e.routes() {
			if !route.manage {
				if got := starsRequest(e.handler, route.method, e.base+route.path, actor, ""); got.Code != http.StatusOK {
					t.Errorf("%s %s %s = %d", actor, route.method, route.path, got.Code)
				}
			}
		}
		if got := starsRequest(e.handler, http.MethodPost, e.q("/annotations"), actor, `{"at":"2026-10-02T00:00:00Z","label":"x"}`); got.Code != http.StatusCreated {
			t.Errorf("%s annotation = %d %s", actor, got.Code, got.Body)
		}
	}
}

func TestOrgGitHubStarsRepoIDsDoNotCrossOrganizations(t *testing.T) {
	e := newStarsEnv(t, true)
	foreign := jsonNumber(e.otherRepo.ID)
	// Org one's owner uses org two's repository id on every id-taking route.
	for _, route := range []starsRoute{
		{http.MethodPatch, "/repos/" + foreign, true}, {http.MethodDelete, "/repos/" + foreign + "?delete_data=true", true},
		{http.MethodPost, "/repos/" + foreign + "/rotate-secret", true}, {http.MethodPost, "/repos/" + foreign + "/sync", true},
		{http.MethodGet, "/summary?repo_id=" + foreign, false}, {http.MethodGet, "/series?granularity=day&repo_id=" + foreign, false},
		{http.MethodGet, "/events?repo_id=" + foreign, false}, {http.MethodGet, "/annotations?repo_id=" + foreign, false},
		{http.MethodPost, "/annotations?repo_id=" + foreign, true}, {http.MethodDelete, "/annotations/1?repo_id=" + foreign, true},
		{http.MethodDelete, "/users/alice?repo_id=" + foreign, true},
	} {
		body := ""
		switch {
		case route.method == http.MethodPatch:
			body = `{}`
		case route.method == http.MethodPost && strings.Contains(route.path, "annotations"):
			body = `{"at":"2026-10-02T00:00:00Z","label":"x"}`
		}
		if got := starsRequest(e.handler, route.method, e.base+route.path, "owner", body); got.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", route.method, route.path, got.Code)
		}
	}
	// The listing only holds the organization's own repository, and org two's still exists.
	list := starsRequest(e.handler, http.MethodGet, e.base+"/repos", "owner", "")
	if strings.Count(list.Body.String(), `"repo":"velixio/supermetry"`) != 1 || !strings.Contains(list.Body.String(), `"id":`+jsonNumber(e.repo.ID)+`,`) {
		t.Fatalf("list = %s", list.Body)
	}
	if _, err := e.svc.Get(context.Background(), e.otherOrg, e.otherRepo.ID); err != nil {
		t.Fatalf("org two's repository was touched: %v", err)
	}
}

func TestOrgGitHubStarsRemovedAdminRoutes(t *testing.T) {
	e := newStarsEnv(t, true)
	for _, path := range []string{"/api/private/admin/github-stars/repos", "/api/private/admin/github-stars/summary?repo_id=1"} {
		got := starsRequest(e.handler, http.MethodGet, path, "root", "")
		if got.Code == http.StatusOK {
			t.Errorf("%s still served: %d", path, got.Code)
		}
	}
}

func TestOrgGitHubStarsRepoLifecycle(t *testing.T) {
	e := newStarsEnv(t, true)
	base := e.base
	list := starsRequest(e.handler, http.MethodGet, base+"/repos", "viewer", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), e.secret) || strings.Contains(list.Body.String(), "webhook_secret") {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
	if !strings.Contains(list.Body.String(), `"public_counter":false`) {
		t.Fatalf("new repositories must default to a private counter: %s", list.Body)
	}

	created := starsRequest(e.handler, http.MethodPost, base+"/repos", "admin", `{"repo":"Velixio/Docs","token":"ghp_secret","time_zone":"Asia/Kolkata"}`)
	var out struct {
		Repo struct {
			ID            int64  `json:"id"`
			OrgID         int64  `json:"org_id"`
			Repo          string `json:"repo"`
			HasToken      bool   `json:"has_token"`
			TimeZone      string `json:"time_zone"`
			PublicCounter bool   `json:"public_counter"`
		} `json:"repo"`
		WebhookSecret string `json:"webhook_secret"`
		WebhookURL    string `json:"webhook_url"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &out) != nil {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	if out.Repo.Repo != "velixio/docs" || out.Repo.OrgID != e.org || out.Repo.PublicCounter || !out.Repo.HasToken || out.Repo.TimeZone != "Asia/Kolkata" || len(out.WebhookSecret) != 64 || !strings.HasPrefix(out.WebhookURL, "https://stats.example.test/api/github/webhook/") {
		t.Fatalf("created = %+v", out)
	}
	if strings.Contains(created.Body.String(), "ghp_secret") {
		t.Fatal("token echoed")
	}
	id := jsonNumber(out.Repo.ID)
	relist := starsRequest(e.handler, http.MethodGet, base+"/repos", "viewer", "")
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
		if got := starsRequest(e.handler, http.MethodPost, base+"/repos", "admin", tc.body); got.Code != tc.code {
			t.Errorf("%s status = %d %s", name, got.Code, got.Body)
		}
	}

	patched := starsRequest(e.handler, http.MethodPatch, base+"/repos/"+id, "admin", `{"enabled":false,"clear_token":true,"time_zone":"UTC","public_counter":true}`)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"enabled":false`) || !strings.Contains(patched.Body.String(), `"has_token":false`) || !strings.Contains(patched.Body.String(), `"public_counter":true`) {
		t.Fatalf("patch = %d %s", patched.Code, patched.Body)
	}
	if got := starsRequest(e.handler, http.MethodPatch, base+"/repos/99999", "admin", `{}`); got.Code != http.StatusNotFound {
		t.Fatalf("patch unknown = %d", got.Code)
	}

	rotated := starsRequest(e.handler, http.MethodPost, base+"/repos/"+id+"/rotate-secret", "owner", "")
	var rot struct {
		WebhookSecret string `json:"webhook_secret"`
	}
	if rotated.Code != http.StatusOK || json.Unmarshal(rotated.Body.Bytes(), &rot) != nil || len(rot.WebhookSecret) != 64 || rot.WebhookSecret == out.WebhookSecret {
		t.Fatalf("rotate = %d %s", rotated.Code, rotated.Body)
	}

	if got := starsRequest(e.handler, http.MethodDelete, base+"/repos/"+id, "admin", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("delete without delete_data = %d", got.Code)
	}
	disabled := starsRequest(e.handler, http.MethodDelete, base+"/repos/"+id+"?delete_data=false", "admin", "")
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"enabled":false`) {
		t.Fatalf("disable = %d %s", disabled.Code, disabled.Body)
	}
	if got := starsRequest(e.handler, http.MethodDelete, base+"/repos/"+id+"?delete_data=true", "admin", ""); got.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", got.Code)
	}
	if got := starsRequest(e.handler, http.MethodDelete, base+"/repos/"+id+"?delete_data=true", "admin", ""); got.Code != http.StatusNotFound {
		t.Fatalf("remove again = %d", got.Code)
	}
}

func TestOrgGitHubStarsRepositoryLimit(t *testing.T) {
	e := newStarsEnv(t, true)
	// One repository exists; the default allows five.
	for i := 0; i < 4; i++ {
		if got := starsRequest(e.handler, http.MethodPost, e.base+"/repos", "owner", `{"repo":"o/r`+jsonNumber(int64(i))+`"}`); got.Code != http.StatusCreated {
			t.Fatalf("repo %d = %d %s", i, got.Code, got.Body)
		}
	}
	over := starsRequest(e.handler, http.MethodPost, e.base+"/repos", "owner", `{"repo":"o/one-too-many"}`)
	if over.Code != http.StatusConflict || !strings.Contains(over.Body.String(), "may track 5 repositories") {
		t.Fatalf("over the limit = %d %s", over.Code, over.Body)
	}
	// The other organization has its own allowance.
	if got := starsRequest(e.handler, http.MethodPost, "/api/private/orgs/"+jsonNumber(e.otherOrg)+"/github-stars/repos", "owner2", `{"repo":"o/mine"}`); got.Code != http.StatusCreated {
		t.Fatalf("other org create = %d %s", got.Code, got.Body)
	}

	limitPath := "/api/private/admin/organizations/" + jsonNumber(e.org) + "/github-star-repo-limit"
	var lim struct {
		Limit   *int   `json:"limit"`
		Source  string `json:"source"`
		Tracked int    `json:"tracked"`
	}
	read := func(method, body string) (int, string) {
		got := starsRequest(e.handler, method, limitPath, "root", body)
		lim.Limit, lim.Source, lim.Tracked = nil, "", 0
		_ = json.Unmarshal(got.Body.Bytes(), &lim)
		return got.Code, got.Body.String()
	}
	if code, body := read(http.MethodGet, ""); code != http.StatusOK || lim.Limit == nil || *lim.Limit != 5 || lim.Source != "default" || lim.Tracked != 5 {
		t.Fatalf("get limit = %d %s", code, body)
	}
	// Lowering below the count refuses new repositories and deletes nothing.
	if code, body := read(http.MethodPut, `{"limit":2}`); code != http.StatusOK || lim.Limit == nil || *lim.Limit != 2 || lim.Source != "org" {
		t.Fatalf("set 2 = %d %s", code, body)
	}
	if got := starsRequest(e.handler, http.MethodPost, e.base+"/repos", "owner", `{"repo":"o/again"}`); got.Code != http.StatusConflict {
		t.Fatalf("over lowered limit = %d", got.Code)
	}
	if got := starsRequest(e.handler, http.MethodGet, e.base+"/repos", "owner", ""); strings.Count(got.Body.String(), `"org_id"`) != 5 {
		t.Fatalf("repositories were removed: %s", got.Body)
	}
	if got := starsRequest(e.handler, http.MethodPost, e.base+"/repos/"+jsonNumber(e.repo.ID)+"/sync", "owner", ""); got.Code != http.StatusAccepted {
		t.Fatalf("existing repository stopped syncing: %d", got.Code)
	}
	e.svc.Wait()
	// null is unlimited, 0 is none, and the default can be restored.
	if code, body := read(http.MethodPut, `{"limit":null}`); code != http.StatusOK || lim.Limit != nil || lim.Source != "org" {
		t.Fatalf("set null = %d %s", code, body)
	}
	if got := starsRequest(e.handler, http.MethodPost, e.base+"/repos", "owner", `{"repo":"o/unlimited"}`); got.Code != http.StatusCreated {
		t.Fatalf("unlimited create = %d %s", got.Code, got.Body)
	}
	if code, body := read(http.MethodPut, `{"limit":0}`); code != http.StatusOK || lim.Limit == nil || *lim.Limit != 0 {
		t.Fatalf("set 0 = %d %s", code, body)
	}
	if got := starsRequest(e.handler, http.MethodPost, e.base+"/repos", "owner", `{"repo":"o/none"}`); got.Code != http.StatusConflict {
		t.Fatalf("limit 0 create = %d", got.Code)
	}
	if code, body := read(http.MethodDelete, ""); code != http.StatusOK || lim.Source != "default" || lim.Limit == nil || *lim.Limit != 5 {
		t.Fatalf("reset = %d %s", code, body)
	}
	for _, bad := range []string{`{}`, `{"limit":-1}`, `{"limit":"5"}`, `{"limit":1.5}`, `{"limit":1,"x":2}`} {
		if code, _ := read(http.MethodPut, bad); code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", bad, code)
		}
	}
	// Only instance admins may change it.
	for _, actor := range []string{"owner", "viewer", "owner2"} {
		if got := starsRequest(e.handler, http.MethodPut, limitPath, actor, `{"limit":99}`); got.Code != http.StatusForbidden {
			t.Errorf("%s set limit = %d", actor, got.Code)
		}
	}
	if got := starsRequest(e.handler, http.MethodPut, "/api/private/admin/organizations/99999/github-star-repo-limit", "root", `{"limit":1}`); got.Code != http.StatusNotFound {
		t.Errorf("unknown org = %d", got.Code)
	}
}

func TestOrgGitHubStarsSync(t *testing.T) {
	e := newStarsEnv(t, true)
	path := e.base + "/repos/" + jsonNumber(e.repo.ID) + "/sync"
	if got := starsRequest(e.handler, http.MethodPost, path, "admin", ""); got.Code != http.StatusAccepted {
		t.Fatalf("sync = %d %s", got.Code, got.Body)
	}
	e.svc.Wait()
	if got := starsRequest(e.handler, http.MethodPost, path, "admin", ""); got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("second sync = %d", got.Code)
	}
	if got := starsRequest(e.handler, http.MethodPost, e.base+"/repos/99999/sync", "admin", ""); got.Code != http.StatusNotFound {
		t.Fatalf("unknown sync = %d", got.Code)
	}
}

func TestOrgGitHubStarsRequiresAKnownRepo(t *testing.T) {
	e := newStarsEnv(t, true)
	for _, path := range []string{"/summary", "/events", "/annotations", "/series?granularity=day"} {
		if got := starsRequest(e.handler, http.MethodGet, e.base+path, "viewer", ""); got.Code != http.StatusBadRequest {
			t.Errorf("%s without repo_id = %d", path, got.Code)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		if got := starsRequest(e.handler, http.MethodGet, e.base+path+sep+"repo_id=99999", "viewer", ""); got.Code != http.StatusNotFound {
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

func TestOrgGitHubStarsReads(t *testing.T) {
	e := newStarsEnv(t, true)
	seedStars(t, e.store)
	other, _, err := e.svc.Create(context.Background(), e.org, ghstars.CreateInput{Repo: "velixio/other"}, 0)
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
	empty := starsRequest(e.handler, http.MethodGet, e.base+"/summary?repo_id="+jsonNumber(other.ID), "admin", "")
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

func TestOrgGitHubStarsAnnotationsAndErasure(t *testing.T) {
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

func TestOrgGitHubStarsMonthSeries(t *testing.T) {
	e := newStarsEnv(t, true)
	seedStars(t, e.store)
	var series struct {
		TimeZone string `json:"time_zone"`
		Data     []struct {
			Bucket                        string
			Gained, Lost, Net, Cumulative int
		} `json:"data"`
	}
	// Default range: 12 months, but not before the first event (Oct 2 in Kathmandu).
	got := starsRequest(e.handler, http.MethodGet, e.q("/series?granularity=month"), "viewer", "")
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &series) != nil {
		t.Fatalf("month series = %d %s", got.Code, got.Body)
	}
	if len(series.Data) != 1 || !strings.HasPrefix(series.Data[0].Bucket, "2026-10-01T00:00:00+05:45") || series.Data[0].Gained != 2 || series.Data[0].Lost != 1 || series.Data[0].Net != 1 || series.Data[0].Cumulative != 1 {
		t.Fatalf("month series = %+v", series)
	}
	// An explicit range reaching further back shows empty months.
	got = starsRequest(e.handler, http.MethodGet, e.q("/series?granularity=month&from=2026-07-01T00:00:00Z&to=2026-10-03T00:00:00Z"), "viewer", "")
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &series) != nil || len(series.Data) != 4 || series.Data[0].Cumulative != 0 || series.Data[3].Cumulative != 1 {
		t.Fatalf("explicit month series = %d %+v", got.Code, series)
	}
}
