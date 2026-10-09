package platformhttp_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/replay"
	"supermetry/internal/testpg"
)

type replayFixture struct {
	handler     http.Handler
	pool        *store.Store
	blobs       replay.BlobStore
	siteID      int32
	foreignSite int32
}

func newReplayFixture(t *testing.T, replayEnv config.ReplayEnv, modes ...privacy.Mode) replayFixture {
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
	if _, err := pool.Exec(ctx, `create table replay_sessions_202610 partition of replay_sessions for values from ('2026-10-01') to ('2026-11-01')`); err != nil {
		t.Fatal(err)
	}
	var ownerID, foreignID, orgID, foreignOrgID int64
	for _, statement := range []struct {
		sql  string
		args []any
		dest any
	}{
		{`insert into users(email,name,password_hash) values ('replay-owner@example.test','Owner','hash') returning id`, nil, &ownerID},
		{`insert into users(email,name,password_hash) values ('replay-foreign@example.test','Foreign','hash') returning id`, nil, &foreignID},
		{`insert into orgs(name,slug) values ('Replay','replay') returning id`, nil, &orgID},
		{`insert into orgs(name,slug) values ('Replay Foreign','replay-foreign') returning id`, nil, &foreignOrgID},
	} {
		if err := pool.QueryRow(ctx, statement.sql, statement.args...).Scan(statement.dest); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner'),($3,$4,'owner')`, orgID, ownerID, foreignOrgID, foreignID); err != nil {
		t.Fatal(err)
	}
	var siteID, foreignSite int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'replaytest01','Replay','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'replaytest02','Foreign','UTC',30) returning id`, foreignOrgID).Scan(&foreignSite); err != nil {
		t.Fatal(err)
	}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := siteRegistry{
		siteID:      {ID: siteID, OrgID: orgID, PublicID: "replaytest01", TZ: time.UTC, Retention: db.Ret30},
		foreignSite: {ID: foreignSite, OrgID: foreignOrgID, PublicID: "replaytest02", TZ: time.UTC, Retention: db.Ret30},
	}
	data := &store.Store{Pool: pool}
	handler := platformhttp.New(platformhttp.Deps{TrackingMode: mode,
		Authenticator: actorAuthenticator{
			"owner":        {UserID: ownerID, OrgID: orgID, Role: authz.RoleOwner},
			"foreign":      {UserID: foreignID, OrgID: foreignOrgID, Role: authz.RoleOwner},
			"viewer":       {UserID: ownerID, OrgID: orgID, Role: authz.RoleViewer},
			"editor":       {UserID: ownerID, OrgID: orgID, Role: authz.RoleEditor},
			"replay-only":  {UserID: ownerID, OrgID: orgID, Role: authz.RoleViewer, Capabilities: []string{"replay.read"}},
			"combined":     {UserID: ownerID, OrgID: orgID, Role: authz.RoleViewer, Capabilities: []string{"replay.read", "profiles.read"}},
			"billing-only": {UserID: ownerID, OrgID: orgID, Role: authz.RoleViewer, SiteIDs: []int32{}, BillingAdmin: true},
		},
		Sites: registry, Store: data, ReplayStore: blobs, ReplayEnv: replayEnv,
		DataDir: t.TempDir(), SecretKey: bytes.Repeat([]byte{7}, 32), AdminAPIKey: "instance-admin-key",
	})
	return replayFixture{handler: handler, pool: data, blobs: blobs, siteID: siteID, foreignSite: foreignSite}
}

func (f replayFixture) request(t *testing.T, actor, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Test-Actor", actor)
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("X-CSRF-Token", "csrf")
		request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "csrf"})
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func TestReplaySettingsLockedFieldRejected(t *testing.T) {
	mask := true
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true, Mask: &mask})
	response := f.request(t, "owner", http.MethodPut, fmt.Sprintf("/api/private/sites/%d/replay-settings", f.siteID), `{"mask":false}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", response.Code, response.Body.String())
	}
}

func TestReplaySettingsUpdateRequiresOwnerOrAdmin(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	path := fmt.Sprintf("/api/private/sites/%d/replay-settings", f.siteID)
	for _, actor := range []string{"viewer", "editor"} {
		if response := f.request(t, actor, http.MethodPut, path, `{"enabled":true}`); response.Code != http.StatusForbidden {
			t.Fatalf("%s PUT status=%d body=%s, want 403", actor, response.Code, response.Body.String())
		}
		if response := f.request(t, actor, http.MethodGet, path, ""); response.Code != http.StatusOK {
			t.Fatalf("%s GET status=%d body=%s, want 200", actor, response.Code, response.Body.String())
		}
	}
	if response := f.request(t, "owner", http.MethodPut, path, `{"enabled":true}`); response.Code != http.StatusOK {
		t.Fatalf("owner PUT status=%d body=%s, want 200", response.Code, response.Body.String())
	}
}

func TestReplaySettingsUpdatePreservesOtherSiteSettings(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	if _, err := f.pool.Pool.Exec(context.Background(), `update sites set settings='{"profiles_enabled":true}'::jsonb where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "owner", http.MethodPut, fmt.Sprintf("/api/private/sites/%d/replay-settings", f.siteID), `{"enabled":true,"sample_rate":25}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var profilesEnabled bool
	if err := f.pool.Pool.QueryRow(context.Background(), `select settings->'profiles_enabled'='true'::jsonb from sites where id=$1`, f.siteID).Scan(&profilesEnabled); err != nil {
		t.Fatal(err)
	}
	if !profilesEnabled {
		t.Fatal("replay settings update removed profiles_enabled")
	}
}

func TestReplayListPaginates(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		started := time.Date(2026, 10, 8, 10+index, 0, 0, 0, time.UTC)
		_, err := f.pool.Pool.Exec(ctx, `insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key) values ($1,$2,$3,$3,'2026-10-08',100,2,$4)`,
			f.siteID, 100+index, started, replay.SessionKey(f.siteID, started, int64(100+index)))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Pool.Exec(ctx, `insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key)
values ($1,999,'2026-10-10 10:00Z','2026-10-10 10:00Z','2026-10-10',100,2,'outside-range')`, f.siteID); err != nil {
		t.Fatal(err)
	}
	first := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-01&to=2026-10-09&limit=2", f.siteID), "")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var page struct {
		Data []struct {
			SessionID int64 `json:"session_id,string"`
		} `json:"data"`
		NextCursor  *string `json:"next_cursor"`
		Total       int     `json:"total"`
		TotalCapped bool    `json:"total_capped"`
	}
	if err := json.NewDecoder(first.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 2 || page.Data[0].SessionID != 102 || page.NextCursor == nil || page.Total != 3 || page.TotalCapped {
		t.Fatalf("first page=%+v", page)
	}
	second := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-01&to=2026-10-09&limit=2&cursor=%s", f.siteID, *page.NextCursor), "")
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"session_id":"100"`) {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	page = struct {
		Data []struct {
			SessionID int64 `json:"session_id,string"`
		} `json:"data"`
		NextCursor  *string `json:"next_cursor"`
		Total       int     `json:"total"`
		TotalCapped bool    `json:"total_capped"`
	}{}
	if err := json.NewDecoder(second.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || page.TotalCapped {
		t.Fatalf("second page total=%d capped=%t, want 3/false", page.Total, page.TotalCapped)
	}
}

func TestReplayListTotalRespectsDateRangeAndCaps(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	ctx := context.Background()
	if _, err := f.pool.Pool.Exec(ctx, `insert into replay_sessions
  (site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key)
select $1,g,'2026-10-08 10:00Z','2026-10-08 10:00Z','2026-10-08',1,1,'cap/'||g
from generate_series(1,10001) g`, f.siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Pool.Exec(ctx, `insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key)
values ($1,20000,'2026-10-01 10:00Z','2026-10-01 10:00Z','2026-10-01',1,1,'outside')`, f.siteID); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-08&to=2026-10-08&limit=1", f.siteID), "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Total       int  `json:"total"`
		TotalCapped bool `json:"total_capped"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 10000 || !page.TotalCapped {
		t.Fatalf("total=%d capped=%t, want 10000/true", page.Total, page.TotalCapped)
	}
}

func TestReplayListIncludesHotClosedAndMissingSessionDetails(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	ctx := context.Background()
	if err := db.EnsurePartitions(ctx, f.pool.Pool, time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Pool.Exec(ctx, `insert into dict(kind,id,value) values
  (9,91,'Firefox'),(9,92,'Safari'),(10,101,'Linux'),(10,102,'iOS'),(12,121,'Berlin'),(12,122,'Pune')`); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`insert into day_close(site_id,day,status) values ($1,'2026-10-07','done')`,
		`insert into sessions
  (site_id,retention_class,session_id,visitor_id,start,last,local_day,country,browser_id,os_id,device,city_id,pageviews,events)
values ($1,30,201,1,'2026-10-08 10:00Z','2026-10-08 10:05Z','2026-10-08','DE',91,101,0,121,2,3)`,
		`insert into sessions_closed
  (site_id,day,visitor_id,session_id,country,browser_id,os_id,device,city_id,pageviews,custom,duration_ms,engagement_ms,vd_n,flags,seq,seq_engage,seq_scroll,started_at)
values ($1,'2026-10-07',2,202,'IN',92,102,1,122,4,6,60000,1000,1,0,array[]::integer[],array[]::integer[],array[]::smallint[],'2026-10-07 09:00Z')`,
		`insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key) values
  ($1,201,'2026-10-08 10:00Z','2026-10-08 10:05Z','2026-10-08',100,2,'hot'),
  ($1,202,'2026-10-07 09:00Z','2026-10-07 09:01Z','2026-10-07',100,4,'closed'),
  ($1,203,'2026-10-06 08:00Z','2026-10-06 08:01Z','2026-10-06',100,1,'missing')`,
	}
	for _, statement := range statements {
		if _, err := f.pool.Pool.Exec(ctx, statement, f.siteID); err != nil {
			t.Fatal(err)
		}
	}

	response := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-01&to=2026-10-09", f.siteID), "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Data []struct {
			SessionID int64  `json:"session_id,string"`
			Browser   string `json:"browser"`
			OS        string `json:"os"`
			Device    string `json:"device"`
			Country   string `json:"country"`
			City      string `json:"city"`
			Actions   int32  `json:"actions"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 3 {
		t.Fatalf("data=%+v", page.Data)
	}
	want := map[int64]struct {
		browser, os, device, country, city string
		actions                            int32
	}{
		201: {"Firefox", "Linux", "desktop", "DE", "Berlin", 5},
		202: {"Safari", "iOS", "mobile", "IN", "Pune", 10},
		203: {"", "", "", "", "", 0},
	}
	for _, item := range page.Data {
		expected := want[item.SessionID]
		if item.Browser != expected.browser || item.OS != expected.os || item.Device != expected.device || item.Country != expected.country || item.City != expected.city || item.Actions != expected.actions {
			t.Errorf("session %d details=%+v, want=%+v", item.SessionID, item, expected)
		}
	}
}

func TestReplayDataServesGzip(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	ctx := context.Background()
	started := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	key := replay.SessionKey(f.siteID, started, 123)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write([]byte("{\"event\":1}\n"))
	_ = zw.Close()
	if _, err := f.blobs.Append(ctx, key, bytes.NewReader(compressed.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Pool.Exec(ctx, `insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key) values ($1,123,$2,$2,'2026-10-08',$3,1,$4)`, f.siteID, started, compressed.Len(), key); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays/123/data", f.siteID), nil)
	request.Header.Set("X-Test-Actor", "owner")
	response := &replayDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Encoding") != "gzip" || response.Header().Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	zr, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "{\"event\":1}\n" {
		t.Fatalf("data=%q", plain)
	}
	if !response.writeDeadlineCleared {
		t.Fatal("replay download did not clear the server write deadline")
	}
}

type replayDeadlineRecorder struct {
	*httptest.ResponseRecorder
	writeDeadlineCleared bool
}

func (r *replayDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	r.writeDeadlineCleared = deadline.IsZero()
	return nil
}

func TestInstanceStorageNeverReturnsSecret(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	stored, err := replay.SealStoredStorage(bytes.Repeat([]byte{7}, 32), replay.StoredStorage{Backend: "s3", S3: replay.S3Settings{
		Endpoint: "https://s3.example", Bucket: "replays", Region: "test", AccessKey: "access", SecretKey: "super-secret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Pool.Exec(context.Background(), `update instance_settings set settings=jsonb_set(settings,'{replay_storage}',$1::jsonb)`, stored); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "owner", http.MethodGet, "/api/private/instance/replay-storage", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "super-secret") || !strings.Contains(response.Body.String(), `"secret_key_set":true`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestInstanceStorageMigrationRecordsSealedEndpoints(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	stored, err := replay.SealStoredStorage(bytes.Repeat([]byte{7}, 32), replay.StoredStorage{Backend: "s3", S3: replay.S3Settings{
		Endpoint: "https://s3.example", Bucket: "replays", Region: "test", AccessKey: "access", SecretKey: "super-secret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Pool.Exec(context.Background(), `update instance_settings set settings=jsonb_set(settings,'{replay_storage}',$1::jsonb)`, stored); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/private/instance/replay-storage", strings.NewReader(`{"backend":"disk","dir":"/var/lib/supermetry/replay-new","migrate":true}`))
	request.Header.Set("Authorization", "Bearer instance-admin-key")
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var migration []byte
	if err := f.pool.Pool.QueryRow(context.Background(), `select settings->'replay_migration' from instance_settings where id=true`).Scan(&migration); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		From        replay.StoredStorage `json:"from"`
		To          replay.StoredStorage `json:"to"`
		RequestedAt time.Time            `json:"requested_at"`
	}
	if err := json.Unmarshal(migration, &decoded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(migration), "super-secret") || decoded.From.Backend != "s3" || decoded.To.Backend != "disk" || decoded.RequestedAt.IsZero() {
		t.Fatalf("migration=%s", migration)
	}
}

func TestReplayEndpointsForbiddenAcrossOrgs(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	response := f.request(t, "foreign", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-01&to=2026-10-09", f.siteID), "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", response.Code, response.Body.String())
	}
}

func TestReplayRequiresBothInstanceAndProjectPermission(t *testing.T) {
	for _, instance := range []bool{true, false} {
		t.Run(fmt.Sprint(instance), func(t *testing.T) {
			f := newReplayFixture(t, config.ReplayEnv{Enabled: instance})
			path := fmt.Sprintf("/api/private/sites/%d/replay-settings", f.siteID)
			response := f.request(t, "owner", http.MethodGet, path, "")
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":false`) {
				t.Fatalf("fresh settings: %d %s", response.Code, response.Body.String())
			}
			response = f.request(t, "owner", http.MethodPut, path, `{"enabled":true}`)
			want := http.StatusConflict
			if instance {
				want = http.StatusOK
			}
			if response.Code != want {
				t.Fatalf("enable: %d %s", response.Code, response.Body.String())
			}
			response = f.request(t, "owner", http.MethodPut, path, `{"enabled":false}`)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":false`) {
				t.Fatalf("disable: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAnonymousReplayReadsBlocked(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true}, privacy.Anonymous)
	for _, suffix := range []string{"", "/100/data", "/css/" + strings.Repeat("a", 64)} {
		response := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays%s", f.siteID, suffix), "")
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", suffix, response.Code, response.Body.String())
		}
	}
}

// Session IDs use all 64 bits; JavaScript numbers round anything above 2^53,
// so the API must send them as decimal strings like the sessions API does.
func TestReplayListSendsLargeSessionIDsAsStrings(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	const sessionID int64 = 1683029196918025731
	started := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	if _, err := f.pool.Pool.Exec(context.Background(), `insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,blob_key) values ($1,$2,$3,$3,'2026-10-08',100,1,$4)`,
		f.siteID, sessionID, started, replay.SessionKey(f.siteID, started, sessionID)); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-01&to=2026-10-09", f.siteID), "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"session_id":"1683029196918025731"`) {
		t.Fatalf("status=%d body=%s, want session_id as a string", response.Code, response.Body.String())
	}
}

func TestReplaySettingsAllowStricterProjectCaps(t *testing.T) {
	mask, rate, retention, quota := true, 20, 30, int64(1000)
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true, Mask: &mask, SampleRate: &rate, Retention: &retention, QuotaBytes: &quota})
	path := fmt.Sprintf("/api/private/sites/%d/replay-settings", f.siteID)
	for _, body := range []string{`{"sample_rate":21}`, `{"retention_days":31}`, `{"quota_bytes":1001}`, `{"mask":false}`} {
		response := f.request(t, "owner", http.MethodPut, path, body)
		if response.Code != http.StatusConflict {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
	response := f.request(t, "owner", http.MethodPut, path, `{"enabled":true,"mask":true,"sample_rate":10,"retention_days":7,"quota_bytes":500}`)
	if response.Code != http.StatusOK {
		t.Fatalf("tightening: %d %s", response.Code, response.Body.String())
	}
	var settings replay.SiteSettings
	if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || !settings.Mask.V || settings.SampleRate.V != 10 || settings.RetentionDays.V != 7 || settings.QuotaBytes.V != 500 {
		t.Fatalf("settings=%+v", settings)
	}
}
