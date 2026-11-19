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

	"supermetry/internal/db"
	"supermetry/internal/platform/admin"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

type pagingEnv struct {
	pool     *pgxpool.Pool
	handler  http.Handler
	adminID  int64
	memberID int64
	orgID    int64
	svcUser  int64
}

func newPagingEnv(t *testing.T) pagingEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	e := pagingEnv{pool: pool}
	var err error
	if e.svcUser, err = store.AdminServiceUserID(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('root@example.test','Root','h',true) returning id`).Scan(&e.adminID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('plain@example.test','Plain','h') returning id`).Scan(&e.memberID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Big','big') returning id`).Scan(&e.orgID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	svc := admin.New(data)
	svc.SetPasswordResetStarter(func(_ context.Context, email string) (string, error) {
		return "https://example.test/reset/" + strings.Split(email, "@")[0], nil
	})
	e.handler = platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"admin":         {UserID: e.adminID, SiteIDs: []int32{}},
			"impersonated":  {UserID: e.adminID, ImpersonatedBy: e.memberID, SiteIDs: []int32{}},
			"member":        {UserID: e.memberID, SiteIDs: []int32{}},
			"nobody-in-org": {UserID: e.memberID, OrgID: e.orgID, Role: authz.RoleOwner},
		},
		Store: data, Admin: svc, Signup: "invite", Version: "v9.9.9",
		AdminAPIKey: testAdminKey, AdminServiceUserID: e.svcUser,
		DataDir: t.TempDir(), Now: time.Now,
	})
	return e
}

func (e pagingEnv) get(t *testing.T, path string, v any) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Test-Actor", "admin")
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	if v != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatalf("%s: %v: %s", path, err, w.Body.String())
		}
	}
	return w.Code
}

func (e pagingEnv) post(actor, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	r.Header.Set("X-Test-Actor", actor)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", "t")
	r.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "t"})
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	return w
}

type pageBody struct {
	Data []struct {
		ID int64 `json:"id"`
	} `json:"data"`
	NextCursor *string `json:"next_cursor"`
}

func TestAdminListsPageWithCursorAndSort(t *testing.T) {
	e := newPagingEnv(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `insert into users(email,name,password_hash) select 'u'||g||'@example.test','N'||(g%7),'h' from generate_series(1,230) g`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "&sort=name&order=desc", "&sort=email", "&sort=created_at&order=desc"} {
		for _, limit := range []int{200, 60} {
			path := fmt.Sprintf("/api/private/admin/users?limit=%d%s", limit, query)
			seen := map[int64]bool{}
			for pages := 0; pages < 20; pages++ {
				var body pageBody
				if code := e.get(t, path, &body); code != http.StatusOK {
					t.Fatalf("%s status=%d", path, code)
				}
				for _, u := range body.Data {
					if seen[u.ID] {
						t.Fatalf("%s duplicate %d", path, u.ID)
					}
					seen[u.ID] = true
				}
				if body.NextCursor == nil {
					break
				}
				path = fmt.Sprintf("/api/private/admin/users?limit=%d%s&cursor=%s", limit, query, *body.NextCursor)
			}
			if len(seen) != 232 {
				t.Fatalf("limit=%d%s saw %d users, want 232", limit, query, len(seen))
			}
		}
	}
	var full pageBody
	e.get(t, "/api/private/admin/users?limit=200", &full)
	if len(full.Data) != 200 || full.NextCursor == nil {
		t.Fatalf("limit 200 returned %d rows next=%v", len(full.Data), full.NextCursor)
	}
	// q is an alias of search.
	var found pageBody
	e.get(t, "/api/private/admin/users?q=u17%40", &found)
	if len(found.Data) != 1 {
		t.Fatalf("q search = %d rows", len(found.Data))
	}
	for _, bad := range []string{"limit=201", "sort=password_hash", "order=up", "cursor=bogus", "limit=0"} {
		if code := e.get(t, "/api/private/admin/users?"+bad, nil); code != http.StatusBadRequest {
			t.Fatalf("%s status=%d, want 400", bad, code)
		}
	}
	for _, route := range []string{"organizations", "sites"} {
		if code := e.get(t, "/api/private/admin/"+route+"?limit=200&sort=name&order=desc", nil); code != http.StatusOK {
			t.Fatalf("%s status=%d", route, code)
		}
		if code := e.get(t, "/api/private/admin/"+route+"?sort=nope", nil); code != http.StatusBadRequest {
			t.Fatalf("%s bad sort status=%d", route, code)
		}
	}
}

func TestAdminOrganizationDetailReturnsAllMembers(t *testing.T) {
	e := newPagingEnv(t)
	if _, err := e.pool.Exec(context.Background(), `
		with u as (insert into users(email,name,password_hash) select 'm'||g||'@example.test','M','h' from generate_series(1,130) g returning id)
		insert into memberships(org_id,user_id,role) select $1,id,'viewer' from u`, e.orgID); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		MemberCount int `json:"member_count"`
		Members     []struct {
			ID int64 `json:"id"`
		} `json:"members"`
		Next *string `json:"members_next_cursor"`
	}
	if code := e.get(t, fmt.Sprintf("/api/private/admin/organizations/%d", e.orgID), &detail); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if len(detail.Members) != 130 || detail.MemberCount != 130 || detail.Next != nil {
		t.Fatalf("members=%d count=%d next=%v", len(detail.Members), detail.MemberCount, detail.Next)
	}
	var page struct {
		Members []struct{ ID int64 } `json:"members"`
		Next    *string              `json:"members_next_cursor"`
	}
	path := fmt.Sprintf("/api/private/admin/organizations/%d?members_limit=100", e.orgID)
	if e.get(t, path, &page) != http.StatusOK || len(page.Members) != 100 || page.Next == nil {
		t.Fatalf("first page members=%d next=%v", len(page.Members), page.Next)
	}
	var rest struct {
		Members []struct{ ID int64 } `json:"members"`
		Next    *string              `json:"members_next_cursor"`
	}
	if e.get(t, path+"&members_cursor="+*page.Next, &rest) != http.StatusOK || len(rest.Members) != 30 || rest.Next != nil {
		t.Fatalf("second page members=%d next=%v", len(rest.Members), rest.Next)
	}
	if code := e.get(t, fmt.Sprintf("/api/private/admin/organizations/%d?members_limit=500", e.orgID), nil); code != http.StatusBadRequest {
		t.Fatalf("members_limit=500 status=%d", code)
	}
}

func TestAdminAuditFiltersAndCursor(t *testing.T) {
	e := newPagingEnv(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		actor := e.adminID
		if i%3 == 0 {
			actor = e.memberID
		}
		if _, err := e.pool.Exec(ctx, `insert into audit_log(org_id,actor_user_id,action,target_type,target_id,created_at) values (null,$1,$2,'organization',$3,$4)`,
			actor, fmt.Sprintf("instance.thing%d.done", i%2), 40+i%2, base.Add(time.Duration(i/2)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string) int {
		path, n := "/api/private/admin/audit?limit=4&"+query, 0
		for pages := 0; pages < 20; pages++ {
			var body pageBody
			if code := e.get(t, path, &body); code != http.StatusOK {
				t.Fatalf("%s status=%d", path, code)
			}
			n += len(body.Data)
			if body.NextCursor == nil {
				return n
			}
			path = "/api/private/admin/audit?limit=4&" + query + "&cursor=" + *body.NextCursor
		}
		t.Fatal("did not terminate")
		return 0
	}
	if n := count(""); n != 30 {
		t.Fatalf("all = %d", n)
	}
	if n := count(fmt.Sprintf("actor=%d", e.memberID)); n != 10 {
		t.Fatalf("actor = %d", n)
	}
	if n := count("action=instance.thing1.done"); n != 15 {
		t.Fatalf("action = %d", n)
	}
	if n := count("action=instance.*"); n != 30 {
		t.Fatalf("action prefix = %d", n)
	}
	if n := count("org=41"); n != 15 {
		t.Fatalf("org = %d", n)
	}
	if n := count("from=2026-05-01T00%3A05%3A00Z&to=2026-05-01T00%3A10%3A00Z"); n != 10 {
		t.Fatalf("range = %d", n)
	}
	for _, bad := range []string{"actor=x", "org=0", "from=yesterday", "cursor=zzz"} {
		if code := e.get(t, "/api/private/admin/audit?"+bad, nil); code != http.StatusBadRequest {
			t.Fatalf("%s status=%d, want 400", bad, code)
		}
	}
}

func TestAdminUserDetailRevokeAndResetAreAuditedAndGuarded(t *testing.T) {
	e := newPagingEnv(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `insert into user_sessions(id_hash,user_id,expires_at,device_label) values (sha256('a'::bytea),$1,now()+interval '1 day','x'),(sha256('b'::bytea),$1,now()+interval '1 day','x')`, e.memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'admin')`, e.orgID, e.memberID); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Email          string `json:"email"`
		TOTPEnabled    bool   `json:"totp_enabled"`
		ActiveSessions int    `json:"active_sessions"`
		LastLoginAt    any    `json:"last_login_at"`
		Memberships    []struct {
			OrgID int64  `json:"org_id"`
			Role  string `json:"role"`
		} `json:"memberships"`
	}
	if code := e.get(t, fmt.Sprintf("/api/private/admin/users/%d", e.memberID), &detail); code != http.StatusOK {
		t.Fatalf("detail status=%d", code)
	}
	if detail.Email != "plain@example.test" || detail.ActiveSessions != 2 || len(detail.Memberships) != 1 || detail.Memberships[0].Role != "admin" {
		t.Fatalf("detail = %+v", detail)
	}
	if code := e.get(t, fmt.Sprintf("/api/private/admin/users/%d", e.svcUser), nil); code != http.StatusNotFound {
		t.Fatalf("system user detail status=%d, want 404", code)
	}

	revokePath := fmt.Sprintf("/api/private/admin/users/%d/sessions/revoke", e.memberID)
	resetPath := fmt.Sprintf("/api/private/admin/users/%d/password-reset", e.memberID)
	// Non-admins and impersonated admin sessions are refused and change nothing.
	for _, actor := range []string{"member", "impersonated", ""} {
		for _, path := range []string{revokePath, resetPath, fmt.Sprintf("/api/private/admin/users/%d", e.memberID)} {
			var code int
			if strings.HasSuffix(path, fmt.Sprint(e.memberID)) {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				if actor != "" {
					r.Header.Set("X-Test-Actor", actor)
				}
				w := httptest.NewRecorder()
				e.handler.ServeHTTP(w, r)
				code = w.Code
			} else {
				code = e.post(actor, path).Code
			}
			want := http.StatusForbidden
			if actor == "" {
				want = http.StatusUnauthorized
			}
			if code != want {
				t.Fatalf("%q %s status=%d, want %d", actor, path, code, want)
			}
		}
	}
	var sessions, audits int
	e.pool.QueryRow(ctx, `select count(*) from user_sessions where user_id=$1`, e.memberID).Scan(&sessions)
	e.pool.QueryRow(ctx, `select count(*) from audit_log where action in ('instance.user.sessions_revoked','instance.user.password_reset_started')`).Scan(&audits)
	if sessions != 2 || audits != 0 {
		t.Fatalf("refused calls changed state: sessions=%d audits=%d", sessions, audits)
	}

	w := e.post("admin", revokePath)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"revoked":2`) {
		t.Fatalf("revoke status=%d body=%s", w.Code, w.Body.String())
	}
	w = e.post("admin", resetPath)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reset_url":"https://example.test/reset/plain"`) || !strings.Contains(w.Body.String(), `"sent":false`) {
		t.Fatalf("reset status=%d body=%s", w.Code, w.Body.String())
	}
	for _, action := range []string{"instance.user.sessions_revoked", "instance.user.password_reset_started"} {
		var n int
		e.pool.QueryRow(ctx, `select count(*) from audit_log where action=$1 and actor_user_id=$2 and target_id=$3 and org_id is null`, action, e.adminID, e.memberID).Scan(&n)
		if n != 1 {
			t.Fatalf("%s audit rows = %d", action, n)
		}
	}
	if strings.Contains(fmt.Sprint(auditMetadata(t, e)), "reset/plain") {
		t.Fatal("audit metadata contains the reset link")
	}
	if w := e.post("admin", "/api/private/admin/users/999999/sessions/revoke"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown user revoke status=%d", w.Code)
	}

	// The admin API key acts as the service user and is audited as such.
	r := httptest.NewRequest(http.MethodPost, revokePath, nil)
	r.Header.Set("Authorization", "Bearer "+testAdminKey)
	kw := httptest.NewRecorder()
	e.handler.ServeHTTP(kw, r)
	if kw.Code != http.StatusOK {
		t.Fatalf("key revoke status=%d body=%s", kw.Code, kw.Body.String())
	}
	var n int
	e.pool.QueryRow(ctx, `select count(*) from audit_log where action='instance.user.sessions_revoked' and actor_user_id=$1`, e.svcUser).Scan(&n)
	if n != 1 {
		t.Fatalf("key-actor audit rows = %d", n)
	}
}

func auditMetadata(t *testing.T, e pagingEnv) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `select metadata::text from audit_log where org_id is null`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestAdminSystemStatusAndAuth(t *testing.T) {
	e := newPagingEnv(t)
	var sys struct {
		Version        string `json:"version"`
		SchemaVersion  int64  `json:"schema_version"`
		Latest         int64  `json:"latest_schema_version"`
		UptimeSeconds  int64  `json:"uptime_seconds"`
		SMTPConfigured bool   `json:"smtp_configured"`
		Database       struct {
			Size int64 `json:"size_bytes"`
			Used int   `json:"connections"`
			Max  int   `json:"max_connections"`
		} `json:"database"`
		DataDir *struct {
			Free *uint64 `json:"free_bytes"`
		} `json:"data_dir"`
		ReplayDir *struct{} `json:"replay_dir"`
		Jobs      []struct {
			Name string `json:"name"`
		} `json:"jobs"`
		Ingest *struct{} `json:"ingest"`
		Update *struct {
			Current string `json:"current_version"`
		} `json:"update"`
	}
	if code := e.get(t, "/api/private/admin/system", &sys); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if sys.Version != "v9.9.9" || sys.SchemaVersion == 0 || sys.SchemaVersion != sys.Latest || sys.Database.Size <= 0 || sys.Database.Used < 1 || sys.Database.Max < sys.Database.Used {
		t.Fatalf("system = %+v", sys)
	}
	if sys.DataDir == nil || sys.DataDir.Free == nil || *sys.DataDir.Free == 0 || sys.ReplayDir != nil || len(sys.Jobs) == 0 || sys.Ingest == nil || sys.Update == nil {
		t.Fatalf("system = %+v", sys)
	}
	// The response is cached for 15 s: a second call reports the same snapshot.
	var again struct {
		GeneratedAt string `json:"generated_at"`
	}
	var first struct {
		GeneratedAt string `json:"generated_at"`
	}
	e.get(t, "/api/private/admin/system", &first)
	e.get(t, "/api/private/admin/system", &again)
	if first.GeneratedAt == "" || first.GeneratedAt != again.GeneratedAt {
		t.Fatalf("cache miss: %q vs %q", first.GeneratedAt, again.GeneratedAt)
	}
	for _, actor := range []string{"member", "impersonated"} {
		r := httptest.NewRequest(http.MethodGet, "/api/private/admin/system", nil)
		r.Header.Set("X-Test-Actor", actor)
		w := httptest.NewRecorder()
		e.handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d", actor, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/private/admin/system", nil)
	r.Header.Set("Authorization", "Bearer "+testAdminKey)
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("key status=%d", w.Code)
	}
}
