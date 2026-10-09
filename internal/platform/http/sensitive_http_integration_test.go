package platformhttp_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"supermetry/internal/platform/authz"
	platformhttp "supermetry/internal/platform/http"
	"testing"
	"time"

	"supermetry/internal/config"
	"supermetry/internal/replay"
)

func TestProfileReadGrantsAndDerivedReplayMetadata(t *testing.T) {
	f := newProfileFixture(t)
	for _, test := range []struct {
		actor, prefix string
		want          int
	}{
		{"stats-only", "private", 403}, {"stats-key", "public/v1", 403}, {"billing-only", "private", 404}, {"restricted", "private", 404}, {"profiles-only", "private", 200}, {"viewer", "private", 200}, {"key", "public/v1", 200},
	} {
		for _, path := range []string{fmt.Sprintf("/api/%s/sites/%d/profiles", test.prefix, f.siteID), fmt.Sprintf("/api/%s/profiles/%d", test.prefix, f.profileID)} {
			response := f.request(t, test.actor, "GET", path)
			if response.Code != test.want {
				t.Fatalf("actor=%s path=%s status=%d want=%d body=%s", test.actor, path, response.Code, test.want, response.Body.String())
			}
		}
	}
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `create table if not exists replay_sessions_202610 partition of replay_sessions for values from ('2026-10-01') to ('2026-11-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,profile_id,blob_key) values ($1,101,'2026-10-09 10:00Z','2026-10-09 10:01Z','2026-10-09',100,1,$2,'unused')`, f.siteID, f.profileID); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"profiles-only", "viewer"} {
		for _, path := range []string{fmt.Sprintf("/api/private/sites/%d/profiles", f.siteID), fmt.Sprintf("/api/private/profiles/%d", f.profileID)} {
			response := f.request(t, actor, "GET", path)
			if response.Code != 200 || strings.Contains(response.Body.String(), `"has_replay":true`) != (actor == "viewer") {
				t.Fatalf("actor=%s status=%d body=%s", actor, response.Code, response.Body.String())
			}
		}
	}
	var foreignProfile int64
	if err := f.pool.QueryRow(ctx, `insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values ($1,'foreign-person',now(),now()) returning id`, f.foreignSite).Scan(&foreignProfile); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "profiles-only", "GET", fmt.Sprintf("/api/private/profiles/%d", foreignProfile))
	if response.Code != 404 || strings.Contains(response.Body.String(), "foreign-person") {
		t.Fatalf("foreign detail status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReplayGrantAndSiteBlobIsolation(t *testing.T) {
	f := newReplayFixture(t, config.ReplayEnv{Enabled: true})
	ctx := context.Background()
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = io.WriteString(zw, "{\"event\":1}\n")
	_ = zw.Close()
	key := replay.SessionKey(f.siteID, day, 123)
	if _, err := f.blobs.Append(ctx, key, bytes.NewReader(compressed.Bytes())); err != nil {
		t.Fatal(err)
	}
	var profileID int64
	if err := f.pool.Pool.QueryRow(ctx, `insert into profiles(site_id,external_id,first_seen_at,last_seen_at) values ($1,'identified-replay',now(),now()) returning id`, f.siteID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Pool.Exec(ctx, `insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,bytes,pages,profile_id,blob_key) values ($1,123,$2::timestamptz,$2::timestamptz,$3::date,100,1,$4,$5)`, f.siteID, day, day.Format(time.DateOnly), profileID, key); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("body{}")))
	if _, err := f.blobs.PutIfAbsent(ctx, replay.CSSKey(f.siteID, hash), bytes.NewReader(compressed.Bytes())); err != nil {
		t.Fatal(err)
	}
	paths := []string{fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-01&to=2026-10-09", f.siteID), fmt.Sprintf("/api/private/sites/%d/replays/123/data", f.siteID), fmt.Sprintf("/api/private/sites/%d/replays/css/%s", f.siteID, hash)}
	for _, actor := range []string{"viewer", "editor", "billing-only", "replay-only", "combined", "owner"} {
		for _, path := range paths {
			if strings.HasSuffix(path, "/data") {
				var storedKey string
				var storedDay string
				if err := f.pool.Pool.QueryRow(ctx, `select blob_key,day::text from replay_sessions where site_id=$1 and session_id=123`, f.siteID).Scan(&storedKey, &storedDay); err != nil {
					t.Fatalf("actor=%s replay row: %v", actor, err)
				}
				if wantKey := fmt.Sprintf("replay/%d/%s/123.gz", f.siteID, storedDay); storedKey != wantKey {
					t.Fatalf("actor=%s stored replay key=%q want=%q", actor, storedKey, wantKey)
				}
			}
			response := f.request(t, actor, "GET", path, "")
			want := 200
			if actor == "viewer" || actor == "editor" {
				want = 403
			}
			if actor == "billing-only" {
				want = 404
			}
			if response.Code != want {
				t.Fatalf("actor=%s path=%s status=%d want=%d body=%s", actor, path, response.Code, want, response.Body.String())
			}
			if response.Code == 200 && strings.Contains(path, "?") {
				wantProfile := actor != "replay-only"
				if strings.Contains(response.Body.String(), fmt.Sprintf(`"profile_id":%d`, profileID)) != wantProfile {
					t.Fatalf("actor=%s profile metadata body=%s", actor, response.Body.String())
				}
			}
		}
	}
	// A valid hash at one site must not resolve at a different, authorized site.
	response := f.request(t, "foreign", "GET", fmt.Sprintf("/api/private/sites/%d/replays/css/%s", f.foreignSite, hash), "")
	if response.Code != 404 {
		t.Fatalf("foreign CSS status=%d", response.Code)
	}
	response = f.request(t, "foreign", "GET", fmt.Sprintf("/api/private/sites/%d/replays/123/data", f.foreignSite), "")
	if response.Code != 404 {
		t.Fatalf("foreign recording status=%d", response.Code)
	}
	// Even an inconsistent database index must not redirect a site's request to
	// another site's blob or to an unrelated session blob.
	for _, badKey := range []string{replay.SessionKey(f.foreignSite, day, 123), replay.SessionKey(f.siteID, day, 456), "../" + key} {
		if _, err := f.pool.Pool.Exec(ctx, `update replay_sessions set blob_key=$1 where site_id=$2 and session_id=123`, badKey, f.siteID); err != nil {
			t.Fatal(err)
		}
		response = f.request(t, "combined", "GET", paths[1], "")
		if response.Code != 404 {
			t.Fatalf("mismatched key=%q status=%d", badKey, response.Code)
		}
	}
	for _, hashPath := range []string{strings.Repeat("g", 64), "short", "..%2f..%2fsecret"} {
		response = f.request(t, "combined", http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays/css/%s", f.siteID, hashPath), "")
		if response.Code != 404 {
			t.Fatalf("invalid hash=%q status=%d", hashPath, response.Code)
		}
	}
}

func TestSharedDashboardCannotNegotiateExports(t *testing.T) {
	principal := authz.Principal{OrgID: 1, ShareID: 9, Role: authz.RoleShare, SiteIDs: []int32{10}}
	handler := platformhttp.New(platformhttp.Deps{Authenticator: actorAuthenticator{"share": principal}, Sites: siteRegistry{10: {ID: 10, OrgID: 1, TZ: time.UTC}}, Query: routeQuery{}})
	for _, suffix := range []string{"&format=csv", "&format=csv&limit=all", "&limit=all", ""} {
		request := httptest.NewRequest("GET", "/api/private/share/demo/stats/summary?period=7d"+suffix, nil)
		request.Header.Set("X-Test-Actor", "share")
		if suffix == "" {
			request.Header.Set("Accept", "text/csv")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("suffix=%s status=%d body=%s", suffix, response.Code, response.Body.String())
		}
	}
}
