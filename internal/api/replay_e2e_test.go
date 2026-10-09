package api_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"
	"time"

	"panel/internal/api"
	"panel/internal/config"
	"panel/internal/db"
	"panel/internal/platform/authz"
	platformhttp "panel/internal/platform/http"
	"panel/internal/platform/sitesvc"
	"panel/internal/platform/store"
	"panel/internal/privacy"
	"panel/internal/replay"
	"panel/internal/salts"
	"panel/internal/session"
	"panel/internal/testpg"
)

type replayE2EClock struct{ now time.Time }

func (clock replayE2EClock) Now() time.Time { return clock.now }

type replayE2ESalts struct {
	day  time.Time
	salt []byte
}

func (source replayE2ESalts) For(day time.Time) ([]byte, bool) {
	return source.salt, day.Format(time.DateOnly) == source.day.Format(time.DateOnly)
}

type replayE2EAuth struct{ principal authz.Principal }

func (auth replayE2EAuth) Authenticate(*http.Request) (authz.Principal, bool) {
	return auth.principal, true
}

func TestReplayEndToEnd(t *testing.T) {
	if os.Getenv("PANEL_TEST_DSN") == "" {
		t.Skip("PANEL_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, _ := testpg.New(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -1), now.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Replay E2E','replay-e2e') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,allowed_hosts,settings)
		values ($1,'replaye2e001','Replay','UTC',30,array['example.test'],'{"replay":{"enabled":true,"sample_rate":100}}') returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	platformStore := &store.Store{Pool: pool}
	registry := sitesvc.NewRegistry(platformStore, 0)
	t.Cleanup(registry.Close)
	clock := replayE2EClock{now: now}
	memory := session.NewMemory(1, clock)
	salt := bytes.Repeat([]byte{7}, 32)
	saltSource := replayE2ESalts{day: now, salt: salt}
	visitorID := salts.VisitorID(salt, siteID, "192.0.2.10", "Replay E2E")
	storeDir := t.TempDir()
	blobs, err := replay.NewDiskStore(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	index := replay.NewIndexWriter(pool, time.Hour, nil)
	indexCtx, cancelIndex := context.WithCancel(ctx)
	indexDone := make(chan error, 1)
	go func() { indexDone <- index.Run(indexCtx) }()
	t.Cleanup(func() {
		if indexDone != nil {
			cancelIndex()
			<-indexDone
		}
	})
	replayEnv := config.ReplayEnv{Enabled: true}
	replayHandler := replay.NewHandler(replay.HandlerDeps{
		Sites: registry, Salts: saltSource, Sessions: memory, Blobs: blobs, Index: index, Replay: replayEnv,
	}, replay.WithClock(clock))
	pageview := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		memory.Touch(session.Hit{SiteID: siteID, TS: now, LocalDay: now, VisitorID: visitorID})
		w.WriteHeader(http.StatusAccepted)
	})
	platform := platformhttp.New(platformhttp.Deps{TrackingMode: privacy.Identified,
		Authenticator: replayE2EAuth{principal: authz.Principal{OrgID: orgID, Role: authz.RoleOwner}},
		Sites:         registry, Store: platformStore, ReplayStore: blobs, ReplayEnv: replayEnv,
		Mutations: platformhttp.Services{Sites: sitesvc.New(platformStore, registry)},
	})
	router := identifiedRouter(api.Deps{
		Config: config.Config{EventPath: "/api/event", Replay: replayEnv},
		Ingest: pageview, Replay: replayHandler, Platform: platform,
	})

	doReplayRequest(t, router, http.MethodPost, "/api/event", nil, http.StatusAccepted)
	start := doReplayRequest(t, router, http.MethodPost, "/api/replay/start", bytes.NewBufferString(`{"s":"replaye2e001","u":"https://example.test/page"}`), http.StatusOK)
	var startBody struct {
		Mode    string `json:"m"`
		Version string `json:"v"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &startBody); err != nil || startBody.Mode != "full" {
		t.Fatalf("start response = %s, err=%v", start.Body.String(), err)
	}
	first := gzipReplayE2E(t, []byte("{\"type\":4}\n"))
	second := gzipReplayE2E(t, []byte("{\"type\":2}\n"))
	doReplayRequest(t, router, http.MethodPost, "/api/replay/chunk?s=replaye2e001&q=0&p=1&v="+startBody.Version, bytes.NewReader(first), http.StatusAccepted)
	doReplayRequest(t, router, http.MethodPost, "/api/replay/chunk?s=replaye2e001&q=1&p=0&v="+startBody.Version, bytes.NewReader(second), http.StatusAccepted)

	cancelIndex()
	if err := <-indexDone; err != nil {
		t.Fatal(err)
	}
	indexDone = nil
	list := doReplayRequest(t, router, http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays?from=2026-10-08&to=2026-10-08", siteID), nil, http.StatusOK)
	var listed struct {
		Data []struct {
			SessionID int64 `json:"session_id,string"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.Data) != 1 {
		t.Fatalf("replay list = %s, err=%v", list.Body.String(), err)
	}
	data := doReplayRequest(t, router, http.MethodGet, fmt.Sprintf("/api/private/sites/%d/replays/%d/data", siteID, listed.Data[0].SessionID), nil, http.StatusOK)
	zr, err := gzip.NewReader(bytes.NewReader(data.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(plain), "{\"type\":4}\n{\"type\":2}\n"; got != want {
		t.Fatalf("replay data = %q, want %q", got, want)
	}
	doReplayRequest(t, router, http.MethodDelete, fmt.Sprintf("/api/private/sites/%d", siteID), nil, http.StatusNoContent)
	if err := replay.EraseSite(ctx, pool, blobs, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := blobs.Open(ctx, replay.SessionKey(siteID, now, listed.Data[0].SessionID)); err == nil {
		t.Fatal("site deletion left replay data on disk")
	}
}

func doReplayRequest(t *testing.T, handler http.Handler, method, target string, body io.Reader, want int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, body)
	request.RemoteAddr = netip.MustParseAddrPort("192.0.2.10:1234").String()
	request.Header.Set("User-Agent", "Replay E2E")
	request.Header.Set("Origin", "https://example.test")
	if method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", "replay-e2e")
		request.AddCookie(&http.Cookie{Name: "panel_csrf", Value: "replay-e2e"})
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s status=%d body=%s, want=%d", method, target, response.Code, response.Body.String(), want)
	}
	return response
}

func gzipReplayE2E(t *testing.T, value []byte) []byte {
	t.Helper()
	var result bytes.Buffer
	writer := gzip.NewWriter(&result)
	if _, err := writer.Write(value); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}
