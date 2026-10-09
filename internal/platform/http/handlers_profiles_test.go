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

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/profileerasure"
	"supermetry/internal/profiletombstone"
	"supermetry/internal/replay"
	"supermetry/internal/testpg"
)

type profileFixture struct {
	handler     http.Handler
	pool        *pgxpool.Pool
	siteID      int32
	disabledID  int32
	foreignSite int32
	profileID   int64
	tombstones  *profiletombstone.Store
	replays     replay.BlobStore
}

func newProfileFixture(t *testing.T, modes ...privacy.Mode) profileFixture {
	mode := privacy.Identified
	if len(modes) > 0 {
		mode = modes[0]
	}
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
		"owner":         {UserID: userID, OrgID: orgID, Role: authz.RoleOwner},
		"admin":         {UserID: userID, OrgID: orgID, Role: authz.RoleAdmin},
		"editor":        {UserID: userID, OrgID: orgID, Role: authz.RoleEditor},
		"viewer":        {UserID: userID, OrgID: orgID, Role: authz.RoleViewer, Capabilities: []string{"profiles.read", "replay.read"}},
		"key":           {KeyID: 11, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"stats:read", "profiles:read", "replay:read"}},
		"stats-only":    {UserID: userID, OrgID: orgID, Role: authz.RoleViewer},
		"profiles-only": {UserID: userID, OrgID: orgID, Role: authz.RoleViewer, Capabilities: []string{"profiles.read"}},
		"stats-key":     {KeyID: 13, OrgID: orgID, Role: authz.RoleKey, Scopes: []string{"stats:read"}},
		"restricted":    {UserID: userID, OrgID: orgID, Role: authz.RoleViewer, SiteIDs: []int32{disabledID}, Capabilities: []string{"profiles.read"}},
		"billing-only":  {UserID: userID, OrgID: orgID, Role: authz.RoleViewer, SiteIDs: []int32{}, BillingAdmin: true},
		"key-no-scope":  {KeyID: 12, OrgID: orgID, Role: authz.RoleKey},
	}
	tombstones := profiletombstone.New(pool, nil)
	diskReplays, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return profileFixture{
		handler: platformhttp.New(platformhttp.Deps{TrackingMode: mode, Authenticator: authenticator, Sites: registry, Store: data, ProfileTombstones: tombstones, ProfileErasures: profileerasure.New(pool), ReplayStore: diskReplays}),
		pool:    pool, siteID: siteID, disabledID: disabledID, foreignSite: foreignSite, profileID: profileID,
		tombstones: tombstones, replays: diskReplays,
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
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func (f profileFixture) completeErasure(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	worker := profileerasure.NewWorker(f.pool, profileerasure.New(f.pool), f.replays, nil)
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `update profile_erasure_requests set settle_after=now()-interval '1 second' where profile_id=$1 and status='settling'`, f.profileID); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
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
		Total           int  `json:"total"`
		TotalCapped     bool `json:"total_capped"`
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
	if list.Total != 1 || list.TotalCapped {
		t.Fatalf("filtered total=%d capped=%t, want 1/false", list.Total, list.TotalCapped)
	}
	var otherProfileID int64
	if err := f.pool.QueryRow(context.Background(), `insert into profiles(site_id,external_id,first_seen_at,last_seen_at)
values ($1,'other-profile','2026-10-09T08:00:00Z','2026-10-09T08:00:00Z') returning id`, f.siteID).Scan(&otherProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id)
values ($1,30,'2026-10-09','2026-10-09T08:00:00Z',99,999,1,$2)`, f.siteID, otherProfileID); err != nil {
		t.Fatal(err)
	}

	detail := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/profiles/%d?limit=2", f.profileID))
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"external_id":"user-123"`) || !strings.Contains(detail.Body.String(), `"name":"signup"`) || !strings.Contains(detail.Body.String(), `"path":"/pricing"`) || !strings.Contains(detail.Body.String(), `"next_cursor":`) {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var page struct {
		Timeline    []api.ProfileEvent `json:"timeline"`
		NextCursor  *string            `json:"next_cursor"`
		Total       int                `json:"total"`
		TotalCapped bool               `json:"total_capped"`
	}
	if err := json.NewDecoder(detail.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Timeline) != 2 || page.NextCursor == nil || page.Total != 3 || page.TotalCapped {
		t.Fatalf("first timeline page=%+v", page)
	}
	if got := page.Timeline[0].SessionId; got != "101" {
		t.Fatalf("first timeline session_id=%q, want 101", got)
	}
	next := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/profiles/%d?limit=2&cursor=%s", f.profileID, *page.NextCursor))
	if next.Code != http.StatusOK {
		t.Fatalf("next detail status=%d body=%s", next.Code, next.Body.String())
	}
	page = struct {
		Timeline    []api.ProfileEvent `json:"timeline"`
		NextCursor  *string            `json:"next_cursor"`
		Total       int                `json:"total"`
		TotalCapped bool               `json:"total_capped"`
	}{}
	if err := json.NewDecoder(next.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Timeline) != 1 || page.NextCursor != nil || page.Total != 3 || page.TotalCapped {
		t.Fatalf("second timeline page=%+v", page)
	}
}

func TestProfileListTotalIsStableAcrossPages(t *testing.T) {
	f := newProfileFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values
  ($1,'user-456','2026-10-08T09:00:00Z','2026-10-09T09:00:00Z'),
  ($1,'other-789','2026-10-08T09:00:00Z','2026-10-09T08:00:00Z')`, f.siteID); err != nil {
		t.Fatal(err)
	}
	first := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/profiles?limit=1", f.siteID))
	var page struct {
		NextCursor  *string `json:"next_cursor"`
		Total       int     `json:"total"`
		TotalCapped bool    `json:"total_capped"`
	}
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	if err := json.NewDecoder(first.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil || page.Total != 3 || page.TotalCapped {
		t.Fatalf("first page=%+v", page)
	}
	second := f.request(t, "viewer", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/profiles?limit=1&cursor=%s", f.siteID, *page.NextCursor))
	page = struct {
		NextCursor  *string `json:"next_cursor"`
		Total       int     `json:"total"`
		TotalCapped bool    `json:"total_capped"`
	}{}
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if err := json.NewDecoder(second.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.TotalCapped {
		t.Fatalf("second page=%+v", page)
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
		{name: "profile key reads list", actor: "key", method: http.MethodGet, path: publicList, want: http.StatusOK},
		{name: "profile key reads detail", actor: "key", method: http.MethodGet, path: publicDetail, want: http.StatusOK},
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

func TestDeleteProfileQueuesErasureTransactionally(t *testing.T) {
	f := newProfileFixture(t)
	response := f.request(t, "admin", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if response.Code != http.StatusAccepted {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	var accepted struct {
		ErasureID int64  `json:"erasure_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.ErasureID <= 0 || accepted.Status != "pending" {
		t.Fatalf("accepted=%+v, want positive id and pending", accepted)
	}
	wantLocation := fmt.Sprintf("/api/private/erasures/%d", accepted.ErasureID)
	if got := response.Header().Get("Location"); got != wantLocation {
		t.Fatalf("Location=%q, want %q", got, wantLocation)
	}
	ctx := context.Background()
	var profiles, events, requests, audits int
	if err := f.pool.QueryRow(ctx, `select count(*) from profiles where id=$1`, f.profileID).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from events where profile_id=$1`, f.profileID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from profile_erasure_requests where id=$1 and site_id=$2 and profile_id=$3 and status='pending'`, accepted.ErasureID, f.siteID, f.profileID).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from audit_log where action='profile.erasure_requested' and target_id=$1`, f.profileID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 || events != 3 || requests != 1 || audits != 1 {
		t.Fatalf("profiles=%d events=%d requests=%d audits=%d", profiles, events, requests, audits)
	}
	if err := f.tombstones.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if !f.tombstones.Contains(f.siteID, "user-123") {
		t.Fatal("profile erasure did not publish a tombstone")
	}
	var storedID string
	if err := f.pool.QueryRow(ctx, `select encode(external_hash,'hex') from profile_tombstones where site_id=$1`, f.siteID).Scan(&storedID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedID, "user-123") || len(storedID) != 64 {
		t.Fatalf("stored tombstone hash=%q", storedID)
	}

	duplicate := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if duplicate.Code != http.StatusAccepted {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	var duplicateAccepted struct {
		ErasureID int64 `json:"erasure_id"`
	}
	if err := json.Unmarshal(duplicate.Body.Bytes(), &duplicateAccepted); err != nil {
		t.Fatal(err)
	}
	if duplicateAccepted.ErasureID != accepted.ErasureID {
		t.Fatalf("duplicate id=%d, want %d", duplicateAccepted.ErasureID, accepted.ErasureID)
	}
}

func TestProfileErasureRequestRollbackDoesNotPublishTombstone(t *testing.T) {
	f := newProfileFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `
		create function reject_profile_erasure_request_audit() returns trigger language plpgsql as $$
		begin raise exception 'audit unavailable'; end $$;
		create trigger reject_profile_erasure_request_audit before insert on audit_log
		for each row when (new.action='profile.erasure_requested')
		execute function reject_profile_erasure_request_audit()`); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var requests, tombstones int
	if err := f.pool.QueryRow(ctx, `select count(*) from profile_erasure_requests where profile_id=$1`, f.profileID).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select count(*) from profile_tombstones where site_id=$1`, f.siteID).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || tombstones != 0 || f.tombstones.Contains(f.siteID, "user-123") {
		t.Fatalf("rollback left requests=%d tombstones=%d memory=%t", requests, tombstones, f.tombstones.Contains(f.siteID, "user-123"))
	}
}

func TestProfileErasureStatusAuthorizationAndRetry(t *testing.T) {
	f := newProfileFixture(t)
	created := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var accepted struct {
		ErasureID int64 `json:"erasure_id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	privatePath := fmt.Sprintf("/api/private/erasures/%d", accepted.ErasureID)
	publicPath := fmt.Sprintf("/api/public/v1/erasures/%d", accepted.ErasureID)
	for _, test := range []struct {
		name, actor, method, path string
		want                      int
	}{
		{name: "owner private read", actor: "owner", method: http.MethodGet, path: privatePath, want: http.StatusOK},
		{name: "editor private read forbidden", actor: "editor", method: http.MethodGet, path: privatePath, want: http.StatusForbidden},
		{name: "profile key public read", actor: "key", method: http.MethodGet, path: publicPath, want: http.StatusOK},
		{name: "missing public scope", actor: "stats-key", method: http.MethodGet, path: publicPath, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := f.request(t, test.actor, test.method, test.path)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s, want=%d", response.Code, response.Body.String(), test.want)
			}
		})
	}

	if _, err := f.pool.Exec(context.Background(), `update profile_erasure_requests set status='failed',attempts=20,last_error='storage unavailable' where id=$1`, accepted.ErasureID); err != nil {
		t.Fatal(err)
	}
	retried := f.request(t, "admin", http.MethodPost, privatePath+"/retry")
	if retried.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", retried.Code, retried.Body.String())
	}
	var result struct {
		Status   string `json:"status"`
		Attempts int    `json:"attempts"`
	}
	if err := json.Unmarshal(retried.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "pending" || result.Attempts != 0 {
		t.Fatalf("retry response=%+v", result)
	}

	var foreignRequest int64
	if err := f.pool.QueryRow(context.Background(), `
		insert into profile_erasure_requests(site_id,profile_id,external_hash,requested_by)
		values ($1,999,decode(repeat('ab',32),'hex'),'user:1') returning id`, f.foreignSite).Scan(&foreignRequest); err != nil {
		t.Fatal(err)
	}
	foreign := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/erasures/%d", foreignRequest))
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("cross-org status=%d body=%s", foreign.Code, foreign.Body.String())
	}
}

func TestProfileDeleteRemovesReplays(t *testing.T) {
	f := newProfileFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	key := replay.SessionKey(f.siteID, day, 100)
	if _, err := f.replays.Append(ctx, key, strings.NewReader("replay")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key)
		values ($1,100,$2,$2,$3,6,1,$4)`, f.siteID, day.Add(time.Hour), day, key); err != nil {
		t.Fatal(err)
	}

	response := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if response.Code != http.StatusAccepted {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	f.completeErasure(t)
	if reader, err := f.replays.Open(ctx, key); err == nil {
		_ = reader.Close()
		t.Fatal("profile replay blob still exists")
	}
	var rows int
	if err := f.pool.QueryRow(ctx, `select count(*) from replay_sessions where site_id=$1 and session_id=100`, f.siteID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("profile replay rows = %d, want 0", rows)
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
	if response.Code != http.StatusAccepted {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	f.completeErasure(t)
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
	if got := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", profileID)).Code; got != http.StatusAccepted {
		t.Fatalf("delete status=%d, want 202", got)
	}
}

func TestAnonymousHistoricalReadsBlockedButErasureAllowed(t *testing.T) {
	f := newProfileFixture(t, privacy.Anonymous)
	for _, path := range []string{fmt.Sprintf("/api/private/sites/%d/profiles", f.siteID), fmt.Sprintf("/api/private/profiles/%d", f.profileID)} {
		response := f.request(t, "owner", http.MethodGet, path)
		if response.Code != http.StatusForbidden {
			t.Fatalf("read %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), "select count(*) from profiles where id=$1", f.profileID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("read restriction erased data: count=%d err=%v", count, err)
	}
	response := f.request(t, "owner", http.MethodDelete, fmt.Sprintf("/api/private/profiles/%d", f.profileID))
	if response.Code != http.StatusAccepted {
		t.Fatalf("erasure: %d %s", response.Code, response.Body.String())
	}
}
