package platformhttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/platform/authz"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	"panel/internal/testpg"
)

type profileFixture struct {
	handler     http.Handler
	pool        *pgxpool.Pool
	siteID      int32
	disabledID  int32
	foreignSite int32
	profileID   int64
}

func newProfileFixture(t *testing.T) profileFixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var userID, orgID, foreignOrgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('profiles@example.test','Profiler','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Profiles','profiles') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Foreign Profiles','foreign-profiles') returning id`).Scan(&foreignOrgID); err != nil {
		t.Fatal(err)
	}
	var siteID, disabledID, foreignSite int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,settings) values ($1,'profiles0001','Enabled','UTC',30,'{"profiles_enabled":true}') returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'profiles0002','Disabled','UTC',30) returning id`, orgID).Scan(&disabledID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,settings) values ($1,'profiles0003','Foreign','UTC',30,'{"profiles_enabled":true}') returning id`, foreignOrgID).Scan(&foreignSite); err != nil {
		t.Fatal(err)
	}
	traits := `{"email":"person@example.test","plan":"pro"}`
	var profileID int64
	if err := pool.QueryRow(ctx, `insert into profiles(site_id,external_id,traits,first_seen_at,last_seen_at) values ($1,'user-123',$2,'2026-10-08T09:00:00Z','2026-10-09T10:00:00Z') returning id`, siteID, traits).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	var nameID, pathID int32
	if err := pool.QueryRow(ctx, `insert into dict(kind,value) values (7,'signup') returning id`).Scan(&nameID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into dict(kind,value) values (0,'/pricing') returning id`).Scan(&pathID); err != nil {
		t.Fatal(err)
	}
	for index, sessionID := range []int64{100, 100, 101} {
		_, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,props,profile_id) values ($1,30,'2026-10-09',$2,10,$3,1,$4,$5,'{"source":"test"}',$6)`, siteID, time.Date(2026, 10, 9, 10+index, 0, 0, 0, time.UTC), sessionID, nameID, pathID, profileID)
		if err != nil {
			t.Fatal(err)
		}
	}
	data := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(data, 0)
	t.Cleanup(registry.Close)
	authenticator := actorAuthenticator{
		"owner":        {UserID: userID, OrgID: orgID, Role: authz.RoleOwner},
		"admin":        {UserID: userID, OrgID: orgID, Role: authz.RoleAdmin},
		"editor":       {UserID: userID, OrgID: orgID, Role: authz.RoleEditor},
		"viewer":       {UserID: userID, OrgID: orgID, Role: authz.RoleViewer},
		"key":          {KeyID: 11, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"stats:read"}},
		"key-no-scope": {KeyID: 12, OrgID: orgID, Role: authz.RoleKey},
	}
	return profileFixture{
		handler: platformhttp.New(platformhttp.Deps{Authenticator: authenticator, Sites: registry, Store: data}),
		pool:    pool, siteID: siteID, disabledID: disabledID, foreignSite: foreignSite, profileID: profileID,
	}
}

func (f profileFixture) request(t *testing.T, actor, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	if actor != "" {
		request.Header.Set("X-Test-Actor", actor)
	}
	if method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", "token")
		request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func TestProfilesListAndDetail(t *testing.T) {
	f := newProfileFixture(t)
	disabled := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/profiles", f.disabledID))
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"profiles_enabled":false`) || !strings.Contains(disabled.Body.String(), `"data":[]`) {
		t.Fatalf("disabled status=%d body=%s", disabled.Code, disabled.Body.String())
	}

	listed := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/profiles?search=user-12&limit=1", f.siteID))
	if listed.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	var list struct {
		ProfilesEnabled bool `json:"profiles_enabled"`
		Data            []struct {
			ID         int64          `json:"id"`
			ExternalID string         `json:"external_id"`
			Traits     map[string]any `json:"traits"`
			Sessions   int64          `json:"sessions"`
			Events     int64          `json:"events"`
		} `json:"data"`
	}
	if err := json.NewDecoder(listed.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if !list.ProfilesEnabled || len(list.Data) != 1 || list.Data[0].ID != f.profileID || list.Data[0].ExternalID != "user-123" || list.Data[0].Traits["plan"] != "pro" || list.Data[0].Sessions != 2 || list.Data[0].Events != 3 {
		t.Fatalf("list=%+v", list)
	}

	detail := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/profiles/%d?limit=2", f.profileID))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"external_id":"user-123"`) || !strings.Contains(detail.Body.String(), `"name":"signup"`) || !strings.Contains(detail.Body.String(), `"path":"/pricing"`) || !strings.Contains(detail.Body.String(), `"next_cursor":`) {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var page struct {
		Timeline   []api.ProfileEvent `json:"timeline"`
		NextCursor *string            `json:"next_cursor"`
	}
	if err := json.NewDecoder(detail.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Timeline) != 2 || page.NextCursor == nil {
		t.Fatalf("first timeline page=%+v", page)
	}
	next := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/profiles/%d?limit=2&cursor=%s", f.profileID, *page.NextCursor))
	if next.Code != http.StatusOK {
		t.Fatalf("next detail status=%d body=%s", next.Code, next.Body.String())
	}
	page = struct {
		Timeline   []api.ProfileEvent `json:"timeline"`
		NextCursor *string            `json:"next_cursor"`
	}{}
	if err := json.NewDecoder(next.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Timeline) != 1 || page.NextCursor != nil {
		t.Fatalf("second timeline page=%+v", page)
	}
}

func TestProfilesAuthorization(t *testing.T) {
	f := newProfileFixture(t)
	privateList := fmt.Sprintf("/api/private/sites/%d/profiles", f.siteID)
	publicList := fmt.Sprintf("/api/public/v1/sites/%d/profiles", f.siteID)
	privateDetail := fmt.Sprintf("/api/private/profiles/%d", f.profileID)
	publicDetail := fmt.Sprintf("/api/public/v1/profiles/%d", f.profileID)
	tests := []struct {
		name, actor, method, path string
		want                      int
	}{
		{name: "viewer reads list", actor: "viewer", method: http.MethodGet, path: privateList, want: http.StatusOK},
		{name: "viewer reads detail", actor: "viewer", method: http.MethodGet, path: privateDetail, want: http.StatusOK},
		{name: "stats key reads list", actor: "key", method: http.MethodGet, path: publicList, want: http.StatusOK},
		{name: "stats key reads detail", actor: "key", method: http.MethodGet, path: publicDetail, want: http.StatusOK},
		{name: "public missing scope", actor: "key-no-scope", method: http.MethodGet, path: publicList, want: http.StatusForbidden},
		{name: "editor cannot delete", actor: "editor", method: http.MethodDelete, path: privateDetail, want: http.StatusForbidden},
		{name: "viewer cannot delete", actor: "viewer", method: http.MethodDelete, path: privateDetail, want: http.StatusForbidden},
		{name: "cross org hidden", actor: "viewer", method: http.MethodGet, path: fmt.Sprintf("/api/private/sites/%d/profiles", f.foreignSite), want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := f.request(t, tt.actor, tt.method, tt.path)
			if response.Code != tt.want {
				t.Fatalf("status=%d body=%s, want=%d", response.Code, response.Body.String(), tt.want)
			}
		})
	}
}

func TestDeleteProfileErasesEventsAndAuditsTransactionally(t *testing.T) {
	f := newProfileFixture(t)
	response := f.request(t, "admin", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	ctx := context.Background()
	var profiles, events, audits int
	if err := f.pool.QueryRow(ctx, `select count(*) from profiles where id=$1`, f.profileID).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from events where profile_id=$1`, f.profileID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from audit_log where action='profile.erased' and target_id=$1`, f.profileID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || events != 0 || audits != 1 {
		t.Fatalf("profiles=%d events=%d audits=%d", profiles, events, audits)
	}
}

// Erasure removes every row that can be tied back to the profile: its events,
// the anonymous events of the sessions it was identified in, the hot session
// rows and the closed-tier rows for those sessions and visitor-days. Rows of
// other sessions and visitors stay.
func TestDeleteProfileErasesLinkedSessionsInBothTiers(t *testing.T) {
	f := newProfileFixture(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 9, 9, 59, 0, 0, time.UTC)
	// Pre-identify pageview in session 100, and an unrelated visitor's session 200.
	exec(`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind) values ($1,30,'2026-10-09',$2,10,100,0),($1,30,'2026-10-09',$2,20,200,0)`, f.siteID, at)
	exec(`insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day) values ($1,30,100,10,$2,$2,'2026-10-09'),($1,30,101,10,$2,$2,'2026-10-09'),($1,30,200,20,$2,$2,'2026-10-09')`, f.siteID, at)
	exec(`insert into sessions_closed(site_id,day,visitor_id,session_id,pageviews,custom,duration_ms,engagement_ms,vd_n,flags,seq,seq_engage,seq_scroll) values ($1,'2026-10-09',10,100,1,1,0,0,1,0,'{}','{}','{}'),($1,'2026-10-09',20,200,1,0,0,0,1,0,'{}','{}','{}')`, f.siteID)
	exec(`insert into custom_closed(site_id,day,visitor_id,name_id,path_id) values ($1,'2026-10-09',10,1,1),($1,'2026-10-09',20,1,1)`, f.siteID)

	response := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	for _, check := range []struct {
		table string
		want  string
	}{
		{"events", "200"},
		{"sessions", "200"},
		{"sessions_closed", "200"},
	} {
		var got string
		if err := f.pool.QueryRow(ctx, `select coalesce(string_agg(session_id::text, ',' order by session_id), '') from `+check.table+` where site_id=$1`, f.siteID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s sessions left=%q, want %q", check.table, got, check.want)
		}
	}
	var visitors string
	if err := f.pool.QueryRow(ctx, `select coalesce(string_agg(visitor_id::text, ','), '') from custom_closed where site_id=$1`, f.siteID).Scan(&visitors); err != nil {
		t.Fatal(err)
	}
	if visitors != "20" {
		t.Fatalf("custom_closed visitors left=%q, want 20", visitors)
	}
}

// Turning profiles off must not strand stored identities: owners still need
// to find and erase them on request.
func TestDisabledSiteProfilesStayErasable(t *testing.T) {
	f := newProfileFixture(t)
	var profileID int64
	if err := f.pool.QueryRow(context.Background(), `insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values ($1,'left-over',now(),now()) returning id`, f.disabledID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	list := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/profiles", f.disabledID))
	var page api.ProfileList
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || list.Code != http.StatusOK {
		t.Fatalf("list status=%d err=%v", list.Code, err)
	}
	if page.ProfilesEnabled || len(page.Data) != 1 {
		t.Fatalf("list enabled=%t rows=%d, want false/1", page.ProfilesEnabled, len(page.Data))
	}
	if got := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/profiles/%d", profileID)).Code; got != http.StatusOK {
		t.Fatalf("detail status=%d, want 200", got)
	}
	if got := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", profileID)).Code; got != http.StatusNoContent {
		t.Fatalf("delete status=%d, want 204", got)
	}
}
