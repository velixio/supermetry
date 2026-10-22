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
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func newStarsHandler(t *testing.T, enabled bool) (http.Handler, *ghstars.Store) {
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
	loc, _ := time.LoadLocation("Asia/Kathmandu")
	stars := ghstars.NewStore(pool, "velixio/supermetry", loc)
	data := &store.Store{Pool: pool}
	deps := platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"admin":  {UserID: adminID, SiteIDs: []int32{}},
			"member": {UserID: memberID, SiteIDs: []int32{}},
		},
		Store: data,
		Admin: admin.New(data),
		Now:   func() time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) },
	}
	if enabled {
		deps.GitHubStars = stars
	}
	return platformhttp.New(deps), stars
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

func TestAdminGitHubStarsAuthAndDisabled(t *testing.T) {
	handler, _ := newStarsHandler(t, false)
	path := "/api/private/admin/github-stars/summary"
	if got := starsRequest(handler, http.MethodGet, path, "member", ""); got.Code != http.StatusForbidden {
		t.Fatalf("member status = %d", got.Code)
	}
	if got := starsRequest(handler, http.MethodGet, path, "admin", ""); got.Code != http.StatusNotFound {
		t.Fatalf("disabled status = %d", got.Code)
	}
	handler, _ = newStarsHandler(t, true)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/private/admin/github-stars/series?granularity=day"},
		{http.MethodGet, "/api/private/admin/github-stars/events"},
		{http.MethodGet, "/api/private/admin/github-stars/annotations"},
		{http.MethodPost, "/api/private/admin/github-stars/annotations"},
		{http.MethodDelete, "/api/private/admin/github-stars/annotations/1"},
		{http.MethodDelete, "/api/private/admin/github-stars/users/alice"},
	} {
		if got := starsRequest(handler, route.method, route.path, "member", `{}`); got.Code != http.StatusForbidden {
			t.Errorf("%s %s member status = %d", route.method, route.path, got.Code)
		}
	}
}

func TestAdminGitHubStarsReads(t *testing.T) {
	handler, stars := newStarsHandler(t, true)
	seedStars(t, stars)

	var summary struct {
		Stars  int `json:"stars"`
		Forks  int `json:"forks"`
		Net24h int `json:"net_24h"`
		Net7d  int `json:"net_7d"`
	}
	got := starsRequest(handler, http.MethodGet, "/api/private/admin/github-stars/summary", "admin", "")
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &summary) != nil {
		t.Fatalf("summary = %d %s", got.Code, got.Body)
	}
	// Window ends 2026-10-03T00:00Z: +1 alice, +1 bob, -1 alice = net 1.
	if summary.Stars != 1 || summary.Forks != 2 || summary.Net24h != 1 || summary.Net7d != 1 {
		t.Fatalf("summary = %+v", summary)
	}

	var series struct {
		TimeZone string `json:"time_zone"`
		Data     []struct {
			Gained, Lost, Net, Cumulative int
		} `json:"data"`
	}
	// Kathmandu day Oct 2 runs 2026-10-01T18:15Z to 2026-10-02T18:15Z and holds all three events.
	got = starsRequest(handler, http.MethodGet, "/api/private/admin/github-stars/series?granularity=day&from=2026-10-01T18:15:00Z&to=2026-10-02T18:15:00Z", "admin", "")
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &series) != nil {
		t.Fatalf("series = %d %s", got.Code, got.Body)
	}
	if series.TimeZone != "Asia/Kathmandu" || len(series.Data) != 1 || series.Data[0].Gained != 2 || series.Data[0].Lost != 1 || series.Data[0].Cumulative != 1 {
		t.Fatalf("series = %+v", series)
	}
	for _, bad := range []string{"granularity=week", "granularity=day&from=yesterday", "granularity=hour&from=2026-01-01T00:00:00Z"} {
		if got := starsRequest(handler, http.MethodGet, "/api/private/admin/github-stars/series?"+bad, "admin", ""); got.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d", bad, got.Code)
		}
	}

	var events struct {
		Data       []struct{ Login, Action string } `json:"data"`
		NextCursor *string                          `json:"next_cursor"`
	}
	got = starsRequest(handler, http.MethodGet, "/api/private/admin/github-stars/events?limit=2", "admin", "")
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &events) != nil {
		t.Fatalf("events = %d %s", got.Code, got.Body)
	}
	if len(events.Data) != 2 || events.Data[0].Action != "unstarred" || events.NextCursor == nil {
		t.Fatalf("events = %+v", events)
	}
	if got := starsRequest(handler, http.MethodGet, "/api/private/admin/github-stars/events?limit=x", "admin", ""); got.Code != http.StatusBadRequest {
		t.Errorf("bad limit status = %d", got.Code)
	}
}

func TestAdminGitHubStarsAnnotationsAndErasure(t *testing.T) {
	handler, stars := newStarsHandler(t, true)
	seedStars(t, stars)
	base := "/api/private/admin/github-stars"

	created := starsRequest(handler, http.MethodPost, base+"/annotations", "admin", `{"at":"2026-10-02T00:00:00Z","label":"Launch"}`)
	var annotation struct {
		ID        int64  `json:"id"`
		Label     string `json:"label"`
		CreatedBy *int64 `json:"created_by"`
	}
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &annotation) != nil || annotation.Label != "Launch" || annotation.CreatedBy == nil {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	for _, body := range []string{`{"at":"2026-10-02T00:00:00Z","label":""}`, `{"label":"no time"}`, `{"at":"nope","label":"x"}`, `{"at":"2026-10-02T00:00:00Z","label":"x","extra":1}`} {
		if got := starsRequest(handler, http.MethodPost, base+"/annotations", "admin", body); got.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d", body, got.Code)
		}
	}
	if list := starsRequest(handler, http.MethodGet, base+"/annotations", "admin", ""); list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Launch") {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
	path := base + "/annotations/" + jsonNumber(annotation.ID)
	if got := starsRequest(handler, http.MethodDelete, path, "admin", ""); got.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", got.Code)
	}
	if got := starsRequest(handler, http.MethodDelete, path, "admin", ""); got.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", got.Code)
	}
	if got := starsRequest(handler, http.MethodDelete, base+"/annotations/abc", "admin", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", got.Code)
	}

	erased := starsRequest(handler, http.MethodDelete, base+"/users/Alice", "admin", "")
	if erased.Code != http.StatusOK || !strings.Contains(erased.Body.String(), `"deleted_events":2`) {
		t.Fatalf("erase = %d %s", erased.Code, erased.Body)
	}
	if n, _ := stars.EventCount(context.Background()); n != 1 {
		t.Fatalf("remaining events = %d", n)
	}
	if got := starsRequest(handler, http.MethodDelete, base+"/users/bad%20login", "admin", ""); got.Code != http.StatusBadRequest {
		t.Fatalf("bad login = %d", got.Code)
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
