package ingest_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/dict"
	"supermetry/internal/ingest"
	"supermetry/internal/platform/sitesvc"
	"supermetry/internal/platform/store"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/rehydrate"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/writer"
)

func TestAnonymousRejectsForgedIdentityAndProperties(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	seedProfileSite(t, f.pool)
	setProfilesEnabled(t, f, true)
	f.h.TrackingMode = privacy.Anonymous
	// Exercise intake independently of final persistence defense.
	f.writer.SetTrackingPolicy(privacy.Identified, f.h.Sites)
	for _, body := range []string{
		`{"site":"abc123","name":"identify","url":"https://example.com/","profile_id":"forged","traits":{"name":"person"}}`,
		`{"site":"abc123","name":"purchase","url":"https://example.com/?utm_source=person-secret&utm_campaign=person-secret#person-secret","referrer":"https://ref.example/?person-secret=yes#person-secret","profile_id":"forged","props":{"email":"person-secret"}}`,
	} {
		if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
			t.Fatalf("status=%d", rr.Code)
		}
	}
	f.stop(t)
	var profiles, events, linked, props, secrets int
	if err := f.pool.QueryRow(context.Background(), `select (select count(*) from profiles), count(*), count(profile_id), count(props), (select count(*) from dict where value like '%person-secret%') from events`).Scan(&profiles, &events, &linked, &props, &secrets); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || events != 1 || linked != 0 || props != 0 || secrets != 0 {
		t.Fatalf("profiles=%d events=%d linked=%d props=%d secret dimensions=%d", profiles, events, linked, props, secrets)
	}
}

func TestPrivacySignalsCannotBeBypassedByHeatmap(t *testing.T) {
	for _, header := range []string{"DNT", "Sec-GPC"} {
		t.Run(header, func(t *testing.T) {
			f := newHandlerFixture(t, 32, nil)
			setHeatmapsEnabled(t, f, true)
			setRespectPrivacySignals(t, f, false)
			req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(`{"site":"abc123","name":"heatmap","url":"https://example.com/","doc_h":2000,"vw":1000,"clicks":[[100,100]]}`))
			req.Header.Set("User-Agent", testUA)
			req.Header.Set(header, "1")
			req.RemoteAddr = "198.51.100.7:5555"
			rr := httptest.NewRecorder()
			f.h.ServeHTTP(rr, req)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("status=%d", rr.Code)
			}
			f.stop(t)
			var cells int
			if err := f.pool.QueryRow(context.Background(), `select count(*) from heatmap_cells`).Scan(&cells); err != nil {
				t.Fatal(err)
			}
			if cells != 0 {
				t.Fatalf("signal uploaded %d heatmap cells", cells)
			}
		})
	}
}

func TestAnonymousPathProvenanceIsEstablishedBeforeDictionaryAndSession(t *testing.T) {
	for _, tc := range []struct{ name, url, want string }{
		{"allowlisted", "https://example.com/pricing?customer=secret#secret", "/pricing"},
		{"unknown", "https://example.com/customer-secret?customer=secret#secret", "/[redacted]"},
		{"literal unicode", "https://example.com/über", "/über"},
		{"encoded unicode", "https://example.com/%C3%BCber", "/[redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerFixture(t, 32, nil)
			policy, err := privacy.NewPathPolicy([]string{"/pricing", "/über"})
			if err != nil {
				t.Fatal(err)
			}
			f.h.PathPolicy = policy
			f.writer.SetPathPolicy(policy, f.h.Dict)
			body := fmt.Sprintf(`{"site":"abc123","name":"pageview","url":%q,"path_sanitizer_version":999,"path_policy_id":"forged"}`, tc.url)
			if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
				t.Fatalf("status=%d", rr.Code)
			}
			f.stop(t)
			var path, policyID, entry, exit string
			var version int16
			if err := f.pool.QueryRow(context.Background(), `select d.value,e.path_sanitizer_version,e.path_policy_id from events e join dict d on d.kind=$1 and d.id=e.path_id`, dict.Path).Scan(&path, &version, &policyID); err != nil {
				t.Fatal(err)
			}
			if path != tc.want || version != 1 || policyID != policy.SanitizePath(tc.url).Policy {
				t.Fatalf("stored %q v%d policy=%q", path, version, policyID)
			}
			if err := f.pool.QueryRow(context.Background(), `select a.value,b.value from sessions s join dict a on a.kind=$1 and a.id=s.entry_path join dict b on b.kind=$1 and b.id=s.exit_path`, dict.Path).Scan(&entry, &exit); err != nil {
				t.Fatal(err)
			}
			if entry != tc.want || exit != tc.want {
				t.Fatalf("session retained raw path: entry=%q exit=%q", entry, exit)
			}
			var leaked int
			if err := f.pool.QueryRow(context.Background(), `select count(*) from dict where value like '%secret%'`).Scan(&leaked); err != nil {
				t.Fatal(err)
			}
			if leaked != 0 {
				t.Fatalf("raw URL reached dictionary: %d", leaked)
			}
		})
	}
}

func TestAnonymousPathExclusionsRunBeforeRedaction(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setExclusions(t, f, nil, []string{"/private/*"})
	if rr := post(t, f.h, `{"site":"abc123","name":"pageview","url":"https://example.com/private/customer-secret"}`, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rr.Code)
	}
	f.stop(t)
	var events, sessions, leaked int
	if err := f.pool.QueryRow(context.Background(), `select (select count(*) from events),(select count(*) from sessions),(select count(*) from dict where value like '%secret%')`).Scan(&events, &sessions, &leaked); err != nil {
		t.Fatal(err)
	}
	if events != 0 || sessions != 0 || leaked != 0 {
		t.Fatalf("excluded request entered storage: events=%d sessions=%d raw dimensions=%d", events, sessions, leaked)
	}
}

func TestIdentifiedRequestCannotForgeVerifiedPathMarker(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	f.h.TrackingMode = privacy.Identified
	setProfilesEnabled(t, f, true)
	policy, err := privacy.NewPathPolicy([]string{"/pricing"})
	if err != nil {
		t.Fatal(err)
	}
	f.h.PathPolicy = policy
	f.writer.SetPathPolicy(policy, f.h.Dict)
	body := fmt.Sprintf(`{"site":"abc123","name":"pageview","url":"https://example.com/pricing","path_sanitizer_version":1,"path_policy_id":%q}`, policy.SanitizePath("/pricing").Policy)
	if rr := post(t, f.h, body, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rr.Code)
	}
	f.stop(t)
	var version int16
	var policyID string
	if err := f.pool.QueryRow(context.Background(), `select path_sanitizer_version,path_policy_id from events`).Scan(&version, &policyID); err != nil {
		t.Fatal(err)
	}
	if version != 0 || policyID != "" {
		t.Fatalf("client forged provenance: v%d %q", version, policyID)
	}
}

func TestAnonymousHeatmapPathRedactedBeforeDictionary(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	setHeatmapsEnabled(t, f, true)
	if rr := post(t, f.h, `{"site":"abc123","name":"heatmap","url":"https://example.com/customer-secret","doc_h":2000,"vw":1000,"clicks":[[100,100]]}`, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rr.Code)
	}
	f.stop(t)
	var path string
	if err := f.pool.QueryRow(context.Background(), `select d.value from heatmap_cells h join dict d on d.kind=$1 and d.id=h.path_id`, dict.Path).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != "/[redacted]" {
		t.Fatalf("heatmap raw path=%q", path)
	}
}

func TestVerifiedPageRequestsSurviveFlushAndCloseWithoutLegacyLookalikes(t *testing.T) {
	f := newHandlerFixture(t, 32, nil)
	ctx := context.Background()
	policy, err := privacy.NewPathPolicy([]string{"/public"})
	if err != nil {
		t.Fatal(err)
	}
	f.h.PathPolicy = policy
	f.writer.SetPathPolicy(policy, f.h.Dict)
	for _, request := range []struct{ url, remote string }{
		{"https://example.com/public?person=secret", "198.51.100.7:5555"},
		{"https://example.com/public", "198.51.100.7:5555"},
		{"https://example.com/customer-secret", "198.51.100.8:5555"},
	} {
		body := fmt.Sprintf(`{"site":"abc123","name":"pageview","url":%q}`, request.url)
		if rr := post(t, f.h, body, testUA, request.remote); rr.Code != http.StatusAccepted {
			t.Fatalf("status=%d", rr.Code)
		}
	}
	f.stop(t)
	// A legacy row with the very same dictionary label must not acquire trust.
	// Omitting the provenance columns exercises their actual database defaults.
	if _, err := f.pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id)
 select e.site_id,e.retention_class,e.local_day,e.ts,e.visitor_id,e.session_id,e.kind,e.path_id
 from events e join dict d on d.kind=$1 and d.id=e.path_id where d.value='/public' limit 1`, dict.Path); err != nil {
		t.Fatal(err)
	}
	var raw, unverified int
	if err := f.pool.QueryRow(ctx, `select count(*),count(*) filter(where path_sanitizer_version=0) from events`).Scan(&raw, &unverified); err != nil {
		t.Fatal(err)
	}
	if raw != 4 || unverified != 1 {
		t.Fatalf("mixed fixture events=%d unverified=%d", raw, unverified)
	}
	engine := query.NewEngine(f.pool, f.h.Dict, f.h.Sites)
	engine.SetTrackingMode(privacy.Anonymous)
	engine.SetPathPolicy(policy)
	day := fakeNow().UTC().Truncate(24 * time.Hour)
	dimensions := []string{"page"}
	request := api.QueryRequest{SiteId: 1, DateRange: &api.DateRange{From: openapi_types.Date{Time: day}, To: openapi_types.Date{Time: day}}, Metrics: []api.Metric{api.Visitors, api.Visits, api.Pageviews}, Dimensions: &dimensions}
	check := func(stage string) {
		t.Helper()
		response, err := engine.Query(ctx, request)
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if response.Totals["pageviews"] != 3 || response.Totals["visitors"] != 2 || response.Totals["visits"] != 2 {
			t.Fatalf("%s totals=%v", stage, response.Totals)
		}
		if len(response.Rows) != 2 {
			t.Fatalf("%s rows=%v", stage, response.Rows)
		}
		seen := map[string]bool{}
		for _, row := range response.Rows {
			path, ok := row["page"].(string)
			if !ok {
				t.Fatalf("%s invalid label=%v", stage, row)
			}
			want := float64(1)
			if path == "/public" {
				want = 2
			} else if path != "/[redacted]" {
				t.Fatalf("%s unsafe path=%q", stage, path)
			}
			if seen[path] || row["pageviews"] != want || row["visitors"] != float64(1) || row["visits"] != float64(1) {
				t.Fatalf("%s row=%v", stage, row)
			}
			seen[path] = true
		}
		if response.Meta.Privacy == nil || response.Meta.Privacy.PolicyId != policy.Fingerprint() || !response.Meta.Privacy.UnverifiedExcluded {
			t.Fatalf("%s missing verified scope=%+v", stage, response.Meta)
		}
	}
	check("hot")
	if _, err := closer.CloseDay(ctx, f.pool, []int32{1}, day); err != nil {
		t.Fatal(err)
	}
	engine.InvalidateSite(1)
	check("closed")
}

func TestIdentifiedRestartPreservesNewPathsOnlyWithProjectOptIn(t *testing.T) {
	for _, tc := range []struct {
		name        string
		profiles    bool
		wantPath    string
		wantVersion int16
	}{
		{"project profiles off", false, "/[redacted]", 1},
		{"project profiles on", true, "/new-private", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerFixture(t, 32, nil)
			ctx := context.Background()
			if rr := post(t, f.h, `{"site":"abc123","name":"pageview","url":"https://example.com/historic-private"}`, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
				t.Fatalf("anonymous status=%d", rr.Code)
			}
			f.stop(t)
			var historicalSession int64
			if err := f.pool.QueryRow(ctx, `select session_id from events`).Scan(&historicalSession); err != nil {
				t.Fatal(err)
			}

			// Simulate a process restart against the same database. The project setting
			// is loaded through the real registry; changing instance mode alone is not
			// project opt-in. Advance beyond idle timeout to establish a new session.
			seedProfileSite(t, f.pool)
			if _, err := f.pool.Exec(ctx, `update sites set settings=jsonb_build_object('profiles_enabled',$1::boolean) where id=1`, tc.profiles); err != nil {
				t.Fatal(err)
			}
			registry := sitesvc.NewRegistry(&store.Store{Pool: f.pool}, time.Hour)
			t.Cleanup(registry.Close)
			site, ok := registry.Site(1)
			if !ok || site.ProfilesEnabled != tc.profiles {
				t.Fatalf("loaded project=%+v found=%t", site, ok)
			}
			clock := &fakeClock{now: f.clock.Now().Add(session.IdleTimeout + time.Minute)}
			sessions := session.NewMemory(4, clock)
			if _, err := rehydrate.Run(ctx, f.pool, sessions, clock.Now()); err != nil {
				t.Fatal(err)
			}
			dictionary := dict.New(f.pool)
			if err := dictionary.Load(ctx); err != nil {
				t.Fatal(err)
			}
			saltStore := salts.New(f.pool, clock)
			if err := saltStore.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			w := writer.New(f.pool, sessions, 32, time.Hour, time.Hour)
			w.SetTrackingPolicy(privacy.Identified, registry)
			w.SetPathPolicy(privacy.PathPolicy{}, dictionary)
			handler := &ingest.Handler{TrackingMode: privacy.Identified, Sites: registry, Enricher: f.h.Enricher, Salts: saltStore, Dict: dictionary, Sessions: sessions, Writer: w, Clock: clock}
			runCtx, cancel := context.WithCancel(ctx)
			stopped := make(chan error, 1)
			go func() { stopped <- w.Run(runCtx) }()
			restarted := &handlerFixture{h: handler, pool: f.pool, writer: w, clock: clock, cancel: cancel, stopped: stopped}
			t.Cleanup(func() { restarted.stop(t) })
			if rr := post(t, handler, `{"site":"profiles0001","name":"pageview","url":"https://example.com/new-private"}`, testUA, "198.51.100.7:5555"); rr.Code != http.StatusAccepted {
				t.Fatalf("identified status=%d", rr.Code)
			}
			restarted.stop(t)

			rows, err := f.pool.Query(ctx, `select e.session_id,d.value,e.path_sanitizer_version,e.path_policy_id,a.value,b.value
   from events e join dict d on d.kind=$1 and d.id=e.path_id
   join sessions s on s.site_id=e.site_id and s.session_id=e.session_id
   join dict a on a.kind=$1 and a.id=s.entry_path join dict b on b.kind=$1 and b.id=s.exit_path
   order by e.ts`, dict.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			for rows.Next() {
				var sessionID int64
				var path, policyID, entry, exit string
				var version int16
				if err := rows.Scan(&sessionID, &path, &version, &policyID, &entry, &exit); err != nil {
					t.Fatal(err)
				}
				wantPath, wantVersion := "/[redacted]", int16(1)
				if count == 0 {
					if sessionID != historicalSession {
						t.Fatal("historical session replaced")
					}
				} else {
					wantPath, wantVersion = tc.wantPath, tc.wantVersion
					if sessionID == historicalSession {
						t.Fatal("restart did not create new session")
					}
				}
				if path != wantPath || entry != wantPath || exit != wantPath || version != wantVersion {
					t.Fatalf("row=%d path=%q entry=%q exit=%q version=%d; want path=%q version=%d", count, path, entry, exit, version, wantPath, wantVersion)
				}
				validPolicy := (version == 1 && policyID == (privacy.PathPolicy{}).Fingerprint()) || (version == 0 && policyID == "")
				if !validPolicy {
					t.Fatalf("row=%d policy=%q", count, policyID)
				}
				count++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if count != 2 {
				t.Fatalf("events/session rows=%d, want2", count)
			}
			var recovered int
			if err := f.pool.QueryRow(ctx, `select count(*) from dict where kind=$1 and value='/historic-private'`, dict.Path).Scan(&recovered); err != nil {
				t.Fatal(err)
			}
			if recovered != 0 {
				t.Fatal("historical original path was retained or reconstructed")
			}
		})
	}
}
