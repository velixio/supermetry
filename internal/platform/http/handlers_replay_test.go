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

	"panel/internal/config"
	"panel/internal/db"
	"panel/internal/platform/authz"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/store"
	"panel/internal/replay"
	"panel/internal/testpg"
)

type replayFixture struct {
	handler     http.Handler
	pool        *store.Store
	blobs       replay.BlobStore
	siteID      int32
	foreignSite int32
}

func newReplayFixture(t *testing.T, replayEnv config.ReplayEnv) replayFixture {
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
		siteID:      {ID: siteID, OrgID: orgID, PublicID: "replaytest01", TZ: time.UTC},
		foreignSite: {ID: foreignSite, OrgID: foreignOrgID, PublicID: "replaytest02", TZ: time.UTC},
	}
	data := &store.Store{Pool: pool}
	handler := platformhttp.New(platformhttp.Deps{
		Authenticator: actorAuthenticator{
			"owner":   {UserID: ownerID, OrgID: orgID, Role: authz.RoleOwner},
			"foreign": {UserID: foreignID, OrgID: foreignOrgID, Role: authz.RoleOwner},
			"viewer":  {UserID: ownerID, OrgID: orgID, Role: authz.RoleViewer},
			"editor":  {UserID: ownerID, OrgID: orgID, Role: authz.RoleEditor},
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
		request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "csrf"})
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
	first := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-01&to=2026-10-09&limit=2", f.siteID), "")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var page struct {
		Data []struct {
			SessionID int64 `json:"session_id"`
		} `json:"data"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.NewDecoder(first.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 2 || page.Data[0].SessionID != 102 || page.NextCursor == nil {
		t.Fatalf("first page=%+v", page)
	}
	second := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-01&to=2026-10-09&limit=2&cursor=%s", f.siteID, *page.NextCursor), "")
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"session_id":100`) {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
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
	response := f.request(t, "owner", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays/123/data", f.siteID), "")
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
	request := httptest.NewRequest(http.MethodPut, "/api/private/instance/replay-storage", strings.NewReader(`{"backend":"disk","dir":"/var/lib/panel/replay-new","migrate":true}`))
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

func TestReplayEnabledComesOnlyFromEnvironment(t *testing.T) {
	for _, envEnabled := range []bool{true, false} {
		f := newReplayFixture(t, config.ReplayEnv{Enabled: envEnabled})
		path := fmt.Sprintf("/api/private/sites/%d/replay-settings", f.siteID)
		if response := f.request(t, "owner", http.MethodPut, path, fmt.Sprintf(`{"enabled":%t}`, !envEnabled)); response.Code != http.StatusConflict {
			t.Fatalf("env=%t: PUT enabled=%t status=%d body=%s, want 409", envEnabled, !envEnabled, response.Code, response.Body.String())
		}
		if response := f.request(t, "owner", http.MethodPut, path, `{"sample_rate":40}`); response.Code != http.StatusOK {
			t.Fatalf("env=%t: PUT sample_rate status=%d body=%s, want 200", envEnabled, response.Code, response.Body.String())
		}
		response := f.request(t, "viewer", http.MethodGet, path, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), fmt.Sprintf(`"enabled":%t`, envEnabled)) {
			t.Fatalf("env=%t: GET status=%d body=%s, want enabled=%t", envEnabled, response.Code, response.Body.String(), envEnabled)
		}
	}
}
