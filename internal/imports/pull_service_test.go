package imports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	platformcrypto "supermetry/internal/platform/crypto"
)

func testBox(t *testing.T, info string) *platformcrypto.SecretBox {
	t.Helper()
	box, err := platformcrypto.NewSecretBox(bytes.Repeat([]byte{7}, 32), info)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func newTestPullService(t *testing.T, store *Store, opts PullOptions) *PullService {
	t.Helper()
	if opts.HTTP.Sleep == nil {
		opts.HTTP.Sleep = func(context.Context, time.Duration) error { return nil }
	}
	return NewPullService(store, testBox(t, "supermetry/imports/test"), opts)
}

func orgOfSite(t *testing.T, store *Store, siteID int32) int64 {
	t.Helper()
	var orgID int64
	if err := store.pool.QueryRow(context.Background(), `select org_id from sites where id=$1`, siteID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	return orgID
}

func waitStatus(t *testing.T, store *Store, id int64, want ...Status) Import {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		got, err := store.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, status := range want {
			if got.Status == status {
				return got
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("import %d stuck in %s (%v)", id, got.Status, got.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runViaManager queues a pull on a Manager worker and waits for a final state.
func runViaManager(t *testing.T, store *Store, svc *PullService, imp Import) Import {
	t.Helper()
	manager := NewManager(store, 4)
	manager.SetPullRunner(svc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = manager.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	if err := manager.Queue(Job{ImportID: imp.ID, Source: imp.Source, Pull: true}); err != nil {
		t.Fatal(err)
	}
	return waitStatus(t, store, imp.ID, StatusCompleted, StatusFailed)
}

func count(t *testing.T, store *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := store.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func plausibleRequest(base, from, to string) PullRequest {
	return PullRequest{Method: MethodPlausibleAPI, BaseURL: base, RemoteSiteID: "example.com", Secret: "key-1", From: from, To: to}
}

func TestPlausiblePullRunsThroughManagerPreviewAndUndo(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	orgID := orgOfSite(t, store, siteID)
	fake := newPlausibleFake(t, "key-1")
	svc := newTestPullService(t, store, PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})

	// Dry run first: nothing is stored.
	preview, err := svc.Preview(ctx, siteID, orgID, plausibleRequest(fake.URL, "2026-01-01", "2026-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary.Days != 2 || preview.Summary.Totals.Visitors != 20 || preview.Summary.Totals.Pageviews != 60 ||
		preview.Summary.Dimensions["page"] != 2 || preview.Summary.DateFrom != "2026-01-01" || preview.Summary.DateTo != "2026-01-02" {
		t.Fatalf("preview = %+v", preview.Summary)
	}
	if n := count(t, store, `select count(*) from imported_day`); n != 0 {
		t.Fatalf("preview stored %d imported_day rows", n)
	}
	if n := count(t, store, `select count(*) from imports`); n != 0 {
		t.Fatalf("preview created %d imports", n)
	}

	imp, err := svc.Create(ctx, siteID, orgID, userID, plausibleRequest(fake.URL, "2026-01-01", "2026-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if imp.Source != SourcePlausible || imp.Status != StatusPending || imp.Method != "plausible_api" ||
		imp.Progress == nil || imp.Progress.TotalDays != 2 || imp.Progress.DoneDays != 0 || imp.FileName != "Plausible API: example.com" {
		t.Fatalf("created = %+v progress=%+v", imp, imp.Progress)
	}
	// The key is encrypted at rest and the config holds no secret.
	var sealed []byte
	var config string
	if err := store.pool.QueryRow(ctx, `select credentials,config::text from import_pulls where import_id=$1`, imp.ID).Scan(&sealed, &config); err != nil {
		t.Fatal(err)
	}
	if len(sealed) == 0 || bytes.Contains(sealed, []byte("key-1")) || strings.Contains(config, "key-1") {
		t.Fatalf("credentials not sealed: sealed=%d config=%s", len(sealed), config)
	}

	done := runViaManager(t, store, svc, imp)
	if done.Status != StatusCompleted || done.Error != nil {
		t.Fatalf("done = %+v err=%v", done, done.Error)
	}
	if done.Progress.DoneDays != 2 || done.Progress.TotalDays != 2 || done.Progress.Rows != 30 || done.Progress.CursorDay == nil || *done.Progress.CursorDay != "2026-01-02" {
		t.Fatalf("progress = %+v", done.Progress)
	}
	if done.DateFrom == nil || done.DateFrom.Format(time.DateOnly) != "2026-01-01" || done.DateTo.Format(time.DateOnly) != "2026-01-02" || done.Rows != 30 {
		t.Fatalf("range/rows = %v %v %d", done.DateFrom, done.DateTo, done.Rows)
	}
	if n := count(t, store, `select count(*) from imported_day where import_id=$1`, imp.ID); n != 2 {
		t.Fatalf("imported_day rows = %d", n)
	}
	if n := count(t, store, `select count(*) from imported_day_dim where import_id=$1`, imp.ID); n != 28 {
		t.Fatalf("imported_day_dim rows = %d", n)
	}
	if n := count(t, store, `select sum(visitors) from imported_day where import_id=$1`, imp.ID); n != 20 {
		t.Fatalf("visitors = %d", n)
	}
	var eventValue string
	if err := store.pool.QueryRow(ctx, `select value from imported_day_dim where import_id=$1 and events>0 limit 1`, imp.ID).Scan(&eventValue); err != nil || eventValue != EventValuePrefix+"Signup" {
		t.Fatalf("event row = %q err=%v", eventValue, err)
	}
	// Credentials are dropped once the pull ends.
	if n := count(t, store, `select count(*) from import_pulls where import_id=$1 and credentials is null`, imp.ID); n != 1 {
		t.Fatal("credentials were not cleared after completion")
	}

	// A second preview of the same days reports the overlap with the import.
	again, err := svc.Preview(ctx, siteID, orgID, plausibleRequest(fake.URL, "2026-01-01", "2026-01-03"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Overlap.ImportedDays != 2 || again.Overlap.LiveDays != 0 {
		t.Fatalf("overlap = %+v", again.Overlap)
	}

	// The listing carries the progress for the wizard.
	list, err := store.List(ctx, siteID)
	if err != nil || len(list) != 1 || list[0].Progress == nil || list[0].Method != "plausible_api" {
		t.Fatalf("list = %+v err=%v", list, err)
	}

	// Undo removes everything, including the pull row.
	if deleted, err := store.Delete(ctx, imp.ID); err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	if n := count(t, store, `select (select count(*) from imported_day)+(select count(*) from imported_day_dim)+(select count(*) from import_pulls)`); n != 0 {
		t.Fatalf("%d rows left after undo", n)
	}
}

func TestPullResumesFromCursorAfterSimulatedCrash(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	orgID := orgOfSite(t, store, siteID)
	fake := newPlausibleFake(t, "key-1")
	svc := newTestPullService(t, store, PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})
	// 74 days: chunks Jan 1-31, Feb 1-Mar 3, Mar 4-15.
	imp, err := svc.Create(ctx, siteID, orgID, userID, plausibleRequest(fake.URL, "2026-01-01", "2026-03-15"))
	if err != nil {
		t.Fatal(err)
	}
	if imp.Progress.TotalDays != 74 {
		t.Fatalf("total days = %d", imp.Progress.TotalDays)
	}

	// The process "crashes" (context cancelled) when the third chunk starts.
	runCtx, crash := context.WithCancel(ctx)
	fake.before = func(w http.ResponseWriter, r plausibleRecorded, _ int) bool {
		if r.Key == "" && r.From == "2026-03-04" {
			crash()
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	if err := store.SetRunning(ctx, imp.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunPull(runCtx, imp.ID); err == nil {
		t.Fatal("expected the interrupted run to return an error")
	}
	mid, err := store.Get(ctx, imp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mid.Status != StatusRunning || mid.Progress.CursorDay == nil || *mid.Progress.CursorDay != "2026-03-03" || mid.Progress.DoneDays != 62 {
		t.Fatalf("after crash: status=%s progress=%+v", mid.Status, mid.Progress)
	}
	// Startup recovery keeps the pull (file imports would be failed).
	if err := store.FailRunningAtStartup(ctx); err != nil {
		t.Fatal(err)
	}
	ids, err := svc.Recover(ctx)
	if err != nil || len(ids) != 1 || ids[0] != imp.ID {
		t.Fatalf("recover = %v %v", ids, err)
	}
	if got, _ := store.Get(ctx, imp.ID); got.Status != StatusPending {
		t.Fatalf("recovered status = %s", got.Status)
	}

	fake.before = nil
	if err := store.SetRunning(ctx, imp.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunPull(ctx, imp.ID); err != nil {
		t.Fatal(err)
	}
	// Only the unfinished chunk was fetched again.
	starts := fake.chunkStarts()
	want := []string{"2026-01-01", "2026-02-01", "2026-03-04", "2026-03-04"}
	if strings.Join(starts, ",") != strings.Join(want, ",") {
		t.Fatalf("chunk starts = %v, want %v", starts, want)
	}
	final, err := store.Get(ctx, imp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusCompleted || final.Progress.DoneDays != 74 || final.Progress.Rows != 74*15 {
		t.Fatalf("final = %s progress=%+v", final.Status, final.Progress)
	}
	if n := count(t, store, `select count(*) from imported_day where import_id=$1`, imp.ID); n != 74 {
		t.Fatalf("imported days = %d, want 74 with no duplicates", n)
	}
	if n := count(t, store, `select sum(visitors) from imported_day where import_id=$1`, imp.ID); n != 740 {
		t.Fatalf("visitors = %d, want 740", n)
	}
}

func TestAppendPullChunkIsIdempotentAndStopsWhenUndone(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	svc := newTestPullService(t, store, PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})
	fake := newPlausibleFake(t, "key-1")
	imp, err := svc.Create(ctx, siteID, orgOfSite(t, store, siteID), userID, plausibleRequest(fake.URL, "2026-01-01", "2026-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunning(ctx, imp.ID); err != nil {
		t.Fatal(err)
	}
	chunk := DayRange{date(t, "2026-01-01"), date(t, "2026-01-02")}
	agg := newAggregate()
	agg.Days[chunk.From] = Metrics{Visitors: 3}
	agg.Dimensions[DimensionKey{chunk.From, DimPage, "/"}] = Metrics{Pageviews: 3}
	for i := 0; i < 2; i++ { // a retry after a crash between fetch and store
		if err := store.AppendPullChunk(ctx, imp.ID, chunk, agg, ParseStats{Rows: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, store, `select count(*) from imported_day where import_id=$1`, imp.ID); n != 1 {
		t.Fatalf("rows after retry = %d", n)
	}
	// Chunks are invisible to queries until the import completes.
	if n := count(t, store, `select count(*) from imports where id=$1 and status='completed'`, imp.ID); n != 0 {
		t.Fatal("import completed too early")
	}
	// Undo while running: the worker's next write is refused and RunPull ends quietly.
	if deleted, err := store.Delete(ctx, imp.ID); err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	if err := store.AppendPullChunk(ctx, imp.ID, chunk, agg, ParseStats{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("append after undo = %v", err)
	}
	if err := svc.RunPull(ctx, imp.ID); err != nil {
		t.Fatalf("RunPull of a removed import = %v", err)
	}
}

func TestPullFailureStoresMessageAndDropsCredentials(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	fake := newPlausibleFake(t, "other-key")
	svc := newTestPullService(t, store, PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})
	imp, err := svc.Create(ctx, siteID, orgOfSite(t, store, siteID), userID, plausibleRequest(fake.URL, "2026-01-01", "2026-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	done := runViaManager(t, store, svc, imp)
	if done.Status != StatusFailed || done.Error == nil || !strings.Contains(*done.Error, "rejected the credentials") || strings.Contains(*done.Error, "key-1") {
		t.Fatalf("done = %s err=%v", done.Status, done.Error)
	}
	if n := count(t, store, `select count(*) from import_pulls where import_id=$1 and credentials is null`, imp.ID); n != 1 {
		t.Fatal("failed pull kept its credentials")
	}
	if n := count(t, store, `select count(*) from imported_day`); n != 0 {
		t.Fatal("failed pull left data behind")
	}
}

func TestPullStaysResumableOnShutdown(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	fake := newPlausibleFake(t, "key-1")
	svc := newTestPullService(t, store, PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})
	imp, err := svc.Create(ctx, siteID, orgOfSite(t, store, siteID), userID, plausibleRequest(fake.URL, "2026-01-01", "2026-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, 2)
	manager.SetPullRunner(svc)
	if err := manager.Queue(Job{ImportID: imp.ID, Source: imp.Source, Pull: true}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_ = manager.Run(cancelled)
	if got, _ := store.Get(ctx, imp.ID); got.Status != StatusPending {
		t.Fatalf("status after shutdown = %s, want pending (resumable)", got.Status)
	}
}

func TestPullRejectsPrivateBaseURLs(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	orgID := orgOfSite(t, store, siteID)
	resolver := fakeResolver{"evil.example": "10.1.2.3", "ok.example": "93.184.216.34", "localhost": "127.0.0.1", "meta.example": "169.254.169.254"}
	svc := NewPullService(store, testBox(t, "supermetry/imports/test"), PullOptions{Resolver: resolver})
	for _, base := range []string{
		"http://127.0.0.1:8080", "http://localhost:8000", "http://[::1]/", "http://10.0.0.5", "https://192.168.1.10/matomo",
		"http://169.254.169.254/latest/meta-data", "http://evil.example", "http://meta.example", "http://[::ffff:127.0.0.1]/", "http://0.0.0.0",
	} {
		_, err := svc.Create(ctx, siteID, orgID, userID, PullRequest{Method: MethodMatomoAPI, BaseURL: base, RemoteSiteID: "1", Secret: "tok", From: "2026-01-01", To: "2026-01-02"})
		if !errors.Is(err, ErrPullInvalid) || !strings.Contains(err.Error(), "private or internal") {
			t.Errorf("%s: err = %v", base, err)
		}
		if _, err := svc.Preview(ctx, siteID, orgID, plausibleRequest(base, "2026-01-01", "2026-01-02")); !errors.Is(err, ErrPullInvalid) {
			t.Errorf("preview %s: err = %v", base, err)
		}
	}
	for _, base := range []string{"ftp://ok.example", "http://user:pw@ok.example", "http://ok.example/?x=1", "", "not a url"} {
		_, err := svc.Create(ctx, siteID, orgID, userID, PullRequest{Method: MethodMatomoAPI, BaseURL: base, RemoteSiteID: "1", Secret: "tok", From: "2026-01-01", To: "2026-01-02"})
		if !errors.Is(err, ErrPullInvalid) {
			t.Errorf("%q: err = %v", base, err)
		}
	}
	// A public host passes validation (nothing is fetched at create time).
	if _, err := svc.Create(ctx, siteID, orgID, userID, PullRequest{Method: MethodMatomoAPI, BaseURL: "https://ok.example/matomo/", RemoteSiteID: "1", Secret: "tok", From: "2026-01-01", To: "2026-01-02"}); err != nil {
		t.Fatalf("public host: %v", err)
	}
	// The dialer repeats the check at connect time, so DNS rebinding after
	// validation cannot reach a loopback server either.
	local := newPlausibleFake(t, "k")
	client := newPullClient(PullHTTPOptions{})
	_, _, err := client.do(ctx, "", func() (*http.Request, error) { return http.NewRequest(http.MethodGet, local.URL, nil) })
	if err == nil || !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("guarded dial err = %v", err)
	}
	if len(local.recorded()) != 0 {
		t.Fatal("the loopback server received a request")
	}
}

type fakeResolver map[string]string

func (r fakeResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	if address, ok := r[host]; ok {
		return []netip.Addr{netip.MustParseAddr(address)}, nil
	}
	return nil, errors.New("no such host")
}

func TestPullRedirectsAreNotFollowed(t *testing.T) {
	fake := newPlausibleFake(t, "k")
	fake.before = func(w http.ResponseWriter, _ plausibleRecorded, _ int) bool {
		w.Header().Set("Location", "http://169.254.169.254/")
		w.WriteHeader(http.StatusFound)
		return true
	}
	puller, _ := newPlausibleTestPuller(fake, "k", plausiblePageSize)
	_, _, err := puller.Fetch(context.Background(), DayRange{date(t, "2026-01-01"), date(t, "2026-01-01")})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("err = %v", err)
	}
}

func TestPullDecryptionFailureIsReported(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	fake := newPlausibleFake(t, "key-1")
	svc := newTestPullService(t, store, PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})
	imp, err := svc.Create(ctx, siteID, orgOfSite(t, store, siteID), userID, plausibleRequest(fake.URL, "2026-01-01", "2026-01-01"))
	if err != nil {
		t.Fatal(err)
	}
	other := NewPullService(store, testBox(t, "a-different-purpose"), PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})
	if err := store.SetRunning(ctx, imp.ID); err != nil {
		t.Fatal(err)
	}
	if err := other.RunPull(ctx, imp.ID); err == nil || !strings.Contains(err.Error(), "decrypted") {
		t.Fatalf("err = %v", err)
	}
}

func TestMatomoPullEndToEnd(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	fake := newMatomoFake(t, "tok-1")
	svc := newTestPullService(t, store, PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})
	request := PullRequest{Method: MethodMatomoAPI, BaseURL: fake.URL + "/", RemoteSiteID: "1", Secret: "tok-1", From: "2026-02-01", To: "2026-02-03"}
	imp, err := svc.Create(ctx, siteID, orgOfSite(t, store, siteID), userID, request)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Source != SourceMatomo {
		t.Fatalf("source = %s", imp.Source)
	}
	done := runViaManager(t, store, svc, imp)
	if done.Status != StatusCompleted {
		t.Fatalf("done = %s %v", done.Status, done.Error)
	}
	if n := count(t, store, `select sum(pageviews) from imported_day where import_id=$1`, imp.ID); n != 90 {
		t.Fatalf("pageviews = %d, want 90", n)
	}
	if n := count(t, store, `select sum(events) from imported_day_dim where import_id=$1`, imp.ID); n != 36 {
		t.Fatalf("events = %d, want 3 days x (9 + 3)", n)
	}
}

func TestPullCapabilitiesAndGA4NotConfigured(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	orgID := orgOfSite(t, store, siteID)
	svc := newTestPullService(t, store, PullOptions{})
	caps := svc.Capabilities()
	if !caps.PlausibleAPI.Available || !caps.MatomoAPI.Available || caps.GA4API.Available || !strings.Contains(caps.GA4API.Reason, "SUPERMETRY_GA4_OAUTH_CLIENT_ID") {
		t.Fatalf("caps = %+v", caps)
	}
	if _, err := svc.StartGA4(ctx, userID, orgID, siteID); !errors.Is(err, ErrGA4NotConfigured) {
		t.Fatalf("start = %v", err)
	}
	if _, err := svc.CallbackGA4(ctx, userID, orgID, "c", "s", ""); !errors.Is(err, ErrGA4NotConfigured) {
		t.Fatalf("callback = %v", err)
	}
	if _, err := svc.ListGA4Connections(ctx, orgID); !errors.Is(err, ErrGA4NotConfigured) {
		t.Fatalf("list = %v", err)
	}
	if _, err := svc.ListGA4Properties(ctx, orgID, 1); !errors.Is(err, ErrGA4NotConfigured) {
		t.Fatalf("properties = %v", err)
	}
	_, err := svc.Create(ctx, siteID, orgID, userID, PullRequest{Method: MethodGA4API, Property: "properties/123", ConnectionID: 1, From: "2026-01-01", To: "2026-01-02"})
	if !errors.Is(err, ErrGA4NotConfigured) {
		t.Fatalf("create = %v", err)
	}
	// Client id without a secret is still "not configured".
	partial := newTestPullService(t, store, PullOptions{GA4: GA4Config{ClientID: "x", BaseURL: "https://p.example"}})
	if partial.Capabilities().GA4API.Available {
		t.Fatal("partial config must not enable GA4")
	}
}

// ga4Connect runs the OAuth handshake against the fake and returns the new connection id.
func ga4Connect(t *testing.T, svc *PullService, fake *googleFake, userID, orgID int64, siteID int32) int64 {
	t.Helper()
	ctx := context.Background()
	authURL, err := svc.StartGA4(ctx, userID, orgID, siteID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authURL)
	state := parsed.Query().Get("state")
	if _, err := svc.CallbackGA4(ctx, userID, orgID, "good-code", state, ""); err != nil {
		t.Fatal(err)
	}
	connections, err := svc.ListGA4Connections(ctx, orgID)
	if err != nil || len(connections) == 0 {
		t.Fatalf("connections = %v %v", connections, err)
	}
	return connections[len(connections)-1].ID
}

func TestGA4OAuthStoresEncryptedRefreshTokenScopedToOrg(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	orgID := orgOfSite(t, store, siteID)
	fake := newGoogleFake(t)
	svc := newTestPullService(t, store, PullOptions{GA4: fake.config(), HTTP: PullHTTPOptions{AllowPrivate: true}})
	if !svc.Capabilities().GA4API.Available {
		t.Fatal("GA4 should be available")
	}

	authURL, err := svc.StartGA4(ctx, userID, orgID, siteID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authURL)
	q := parsed.Query()
	if !strings.HasPrefix(authURL, fake.URL+"/auth?") || q.Get("redirect_uri") != "https://panel.example.test/api/private/imports/ga4/callback" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("scope") != ga4Scope ||
		q.Get("access_type") != "offline" || q.Get("client_id") != "client-id" || strings.Contains(authURL, "client-secret") {
		t.Fatalf("auth url = %s", authURL)
	}
	state := q.Get("state")
	if len(state) < 40 {
		t.Fatalf("state too short: %q", state)
	}
	// The state is stored only as a hash.
	if n := count(t, store, `select count(*) from import_ga4_states where state_hash=$1`, ga4StateHash(state)); n != 1 {
		t.Fatal("state hash not stored")
	}

	// A different user cannot complete the flow, and the state is consumed.
	if _, err := svc.CallbackGA4(ctx, userID+999, orgID, "good-code", state, ""); !errors.Is(err, ErrGA4InvalidState) {
		t.Fatalf("other user = %v", err)
	}
	if _, err := svc.CallbackGA4(ctx, userID, orgID, "good-code", state, ""); !errors.Is(err, ErrGA4InvalidState) {
		t.Fatalf("replayed state = %v", err)
	}

	authURL, _ = svc.StartGA4(ctx, userID, orgID, siteID)
	parsed, _ = url.Parse(authURL)
	q = parsed.Query()
	gotSite, err := svc.CallbackGA4(ctx, userID, orgID, "good-code", q.Get("state"), "")
	if err != nil || gotSite != siteID {
		t.Fatalf("callback = %d %v", gotSite, err)
	}
	// PKCE: the verifier sent to Google hashes to the challenge in the consent URL.
	last := fake.tokenForms[len(fake.tokenForms)-1]
	sum := sha256.Sum256([]byte(last["code_verifier"]))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") || last["redirect_uri"] != q.Get("redirect_uri") {
		t.Fatalf("token exchange form = %v", last)
	}

	var sealed []byte
	var connectionID int64
	if err := store.pool.QueryRow(ctx, `select id,refresh_token from import_ga4_connections where org_id=$1`, orgID).Scan(&connectionID, &sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(fake.refreshToken)) || bytes.Contains(sealed, []byte("refresh")) {
		t.Fatal("refresh token stored in plaintext")
	}
	plain, err := svc.ga4RefreshToken(ctx, orgID, connectionID)
	if err != nil || plain != fake.refreshToken {
		t.Fatalf("round trip = %q %v", plain, err)
	}
	// Another purpose key cannot open it; another organization cannot see it.
	if _, err := testBox(t, "other").Open(sealed); err == nil {
		t.Fatal("sealed token opened with a different key")
	}
	var otherOrg int64
	if err := store.pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Other','other-org') returning id`).Scan(&otherOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ga4RefreshToken(ctx, otherOrg, connectionID); !errors.Is(err, ErrGA4Connection) {
		t.Fatalf("cross-org read = %v", err)
	}
	if list, _ := svc.ListGA4Connections(ctx, otherOrg); len(list) != 0 {
		t.Fatalf("cross-org list = %v", list)
	}
	if _, err := svc.ListGA4Properties(ctx, otherOrg, connectionID); !errors.Is(err, ErrGA4Connection) {
		t.Fatalf("cross-org properties = %v", err)
	}

	// Denied consent and expired states.
	authURL, _ = svc.StartGA4(ctx, userID, orgID, siteID)
	parsed, _ = url.Parse(authURL)
	if _, err := svc.CallbackGA4(ctx, userID, orgID, "", parsed.Query().Get("state"), "access_denied"); !errors.Is(err, ErrGA4Denied) {
		t.Fatalf("denied = %v", err)
	}
	authURL, _ = svc.StartGA4(ctx, userID, orgID, siteID)
	parsed, _ = url.Parse(authURL)
	svc.now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := svc.CallbackGA4(ctx, userID, orgID, "good-code", parsed.Query().Get("state"), ""); !errors.Is(err, ErrGA4InvalidState) {
		t.Fatalf("expired = %v", err)
	}
	svc.now = time.Now

	// Properties come from the Admin API account summaries, paged and filtered.
	properties, err := svc.ListGA4Properties(ctx, orgID, connectionID)
	if err != nil || len(properties) != 2 || properties[0].Property != "properties/123" || properties[1].DisplayName != "Blog site" || properties[1].Account != "Blog" {
		t.Fatalf("properties = %+v err=%v", properties, err)
	}

	// Disconnecting deletes the sealed token and revokes it at Google.
	if err := svc.DeleteGA4Connection(ctx, orgID, connectionID); err != nil {
		t.Fatal(err)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != fake.refreshToken {
		t.Fatalf("revoked = %v", fake.revoked)
	}
	if _, err := svc.ga4RefreshToken(ctx, orgID, connectionID); !errors.Is(err, ErrGA4Connection) {
		t.Fatalf("after delete = %v", err)
	}
}

func TestGA4PullEndToEndWithConnection(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	orgID := orgOfSite(t, store, siteID)
	fake := newGoogleFake(t)
	cfg := fake.config()
	cfg.PageSize = 3
	svc := newTestPullService(t, store, PullOptions{GA4: cfg, HTTP: PullHTTPOptions{AllowPrivate: true}})
	connection := ga4Connect(t, svc, fake, userID, orgID, siteID)

	request := PullRequest{Method: MethodGA4API, Property: "properties/123", ConnectionID: connection, From: "2026-03-01", To: "2026-03-02", Secret: "ignored"}
	imp, err := svc.Create(ctx, siteID, orgID, userID, request)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Source != SourceGA4 || imp.FileName != "Google Analytics API: properties/123" {
		t.Fatalf("imp = %+v", imp)
	}
	// No secret is stored for a GA4 pull; the connection holds the token.
	if n := count(t, store, `select count(*) from import_pulls where import_id=$1 and credentials is null and ga4_connection_id=$2`, imp.ID, connection); n != 1 {
		t.Fatal("GA4 pull row should reference the connection and hold no credentials")
	}
	done := runViaManager(t, store, svc, imp)
	if done.Status != StatusCompleted {
		t.Fatalf("done = %s %v", done.Status, done.Error)
	}
	if n := count(t, store, `select sum(visitors) from imported_day where import_id=$1`, imp.ID); n != 20 {
		t.Fatalf("visitors = %d", n)
	}
	if n := count(t, store, `select sum(events) from imported_day_dim where import_id=$1`, imp.ID); n != 10 {
		t.Fatalf("events = %d, want 2 days x 5", n)
	}
	// One access token served the whole run (refresh grants: 1 here, plus the
	// property validation at Create and the run itself reading the connection).
	refreshes := 0
	for _, form := range fake.tokenForms {
		if form["grant_type"] == "refresh_token" {
			refreshes++
		}
	}
	if refreshes != 1 {
		t.Fatalf("refresh grants = %d, want 1", refreshes)
	}

	// A connection of another organization cannot be used.
	var otherOrg int64
	if err := store.pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Other','other-org') returning id`).Scan(&otherOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, siteID, otherOrg, userID, request); !errors.Is(err, ErrGA4Connection) {
		t.Fatalf("cross-org create = %v", err)
	}
	if _, err := svc.Create(ctx, siteID, orgID, userID, PullRequest{Method: MethodGA4API, Property: "properties/abc", ConnectionID: connection, From: "2026-03-01", To: "2026-03-02"}); !errors.Is(err, ErrPullInvalid) {
		t.Fatalf("bad property = %v", err)
	}
}

func TestPullRequestValidation(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	orgID := orgOfSite(t, store, siteID)
	svc := newTestPullService(t, store, PullOptions{HTTP: PullHTTPOptions{AllowPrivate: true}})
	base := plausibleRequest("http://example.test", "2026-01-01", "2026-01-02")
	cases := map[string]func(*PullRequest){
		"bad from":    func(r *PullRequest) { r.From = "yesterday" },
		"reversed":    func(r *PullRequest) { r.From, r.To = "2026-02-01", "2026-01-01" },
		"too long":    func(r *PullRequest) { r.From, r.To = "2000-01-01", "2026-01-01" },
		"no key":      func(r *PullRequest) { r.Secret = " " },
		"site chars":  func(r *PullRequest) { r.RemoteSiteID = "a b;c" },
		"method":      func(r *PullRequest) { r.Method = "bogus" },
		"matomo site": func(r *PullRequest) { r.Method, r.RemoteSiteID = MethodMatomoAPI, "example.com" },
	}
	for name, mutate := range cases {
		request := base
		mutate(&request)
		if _, err := svc.Create(ctx, siteID, orgID, userID, request); !errors.Is(err, ErrPullInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Plausible defaults to the hosted origin when no base URL is given.
	request := base
	request.BaseURL = ""
	svc2 := NewPullService(store, testBox(t, "supermetry/imports/test"), PullOptions{Resolver: fakeResolver{"plausible.io": "104.18.1.1"}})
	imp, err := svc2.Create(ctx, siteID, orgID, userID, request)
	if err != nil {
		t.Fatal(err)
	}
	pull, err := store.loadPull(ctx, imp.ID)
	if err != nil || pull.Request.BaseURL != DefaultPlausibleURL {
		t.Fatalf("base = %q err=%v", pull.Request.BaseURL, err)
	}
	if _, err := svc.Preview(ctx, siteID, orgID, plausibleRequest("http://example.test", "2026-01-01", "2026-06-30")); !errors.Is(err, ErrPullInvalid) {
		t.Fatalf("long preview = %v", err)
	}
}
