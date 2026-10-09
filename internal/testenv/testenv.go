// Package testenv provides the shared analytics integration-test environment.
package testenv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/ingest"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
	"supermetry/internal/writer"
)

const TestAPIKey = "golden-test-key"

var (
	repositoryRoot = findRepositoryRoot()
	fixtureEvents  = filepath.Join(repositoryRoot, "bench", "gen", "testdata", "tiny.ndjson")
	fixtureSites   = filepath.Join(repositoryRoot, "bench", "gen", "testdata", "tiny.sites.json")
	fixtureTruth   = filepath.Join(repositoryRoot, "bench", "gen", "testdata", "tiny.truth.json")
)

type testClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *testClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type registry struct {
	byPublic map[string]sites.Site
	byID     map[int32]sites.Site
	all      []sites.Site
}

func (r *registry) ByPublicID(id string) (sites.Site, bool) {
	site, ok := r.byPublic[id]
	return site, ok
}

func (r *registry) Site(id int32) (sites.Site, bool) {
	site, ok := r.byID[id]
	return site, ok
}

func (r *registry) All() []sites.Site { return append([]sites.Site(nil), r.all...) }

type noClosedDays struct{}

func (noClosedDays) IsClosed(int32, time.Time) bool { return false }

// Env owns one HTTP server, writer, and disposable PostgreSQL schema.
type Env struct {
	pool       *pgxpool.Pool
	schema     string
	server     *httptest.Server
	clock      *testClock
	salts      *salts.Store
	writerStop context.CancelFunc
	writerDone <-chan error

	mu            sync.Mutex
	writerStopped bool
	serverStopped bool
	logs          bytes.Buffer
}

// Start creates a fully migrated analytics environment in a disposable schema.
func Start(t *testing.T) *Env {
	t.Helper()
	pool, schema := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, mustTime(t, "2026-09-25T00:00:00Z"), mustTime(t, "2026-10-05T00:00:00Z")); err != nil {
		t.Fatal(err)
	}

	reg := loadRegistry(t, fixtureSites)
	clock := &testClock{now: mustTime(t, "2026-09-27T00:00:00Z")}
	saltStore := salts.New(pool, clock)
	if err := saltStore.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	sessions := session.NewMemory(16, clock)
	w := writer.New(pool, sessions, 65536, 20*time.Millisecond, 20*time.Millisecond, noClosedDays{})
	w.SetTrackingPolicy(privacy.Identified, reg)
	writerCtx, stopWriter := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(writerCtx) }()

	handler := &truthIntake{sites: reg, salts: saltStore, dict: dictionary, sessions: sessions, writer: w}
	engine := query.NewEngine(pool, dictionary, reg)
	engine.SetTrackingMode(privacy.Identified)
	router := api.NewRouter(api.Deps{
		Config: config.Config{Tracking: config.TrackingMode{Mode: privacy.Identified}, EventPath: "/api/public/v1/events", AdminAPIKey: TestAPIKey},
		Query:  engine, SessionExplore: engine, Ingest: handler, Sessions: sessions, Now: clock.Now,
		RateLimiter: ingest.NewTokenBuckets(100_000, 100_000), Ready: func() bool { return true },
	})
	env := &Env{
		pool: pool, schema: schema, server: httptest.NewServer(router), clock: clock,
		salts: saltStore, writerStop: stopWriter, writerDone: writerDone,
	}
	t.Cleanup(func() {
		env.stopServer()
		env.stopWriter(t)
	})
	return env
}

// Pool returns the schema-isolated PostgreSQL pool.
func (e *Env) Pool() *pgxpool.Pool { return e.pool }

// URL returns the test server base URL.
func (e *Env) URL() string { return e.server.URL }

// Client returns the test server's HTTP client.
func (e *Env) Client() *http.Client { return e.server.Client() }

// ReplayTiny sends the tiny generated fixture and representative engagement pings.
func ReplayTiny(t *testing.T, env *Env) {
	t.Helper()
	replayTiny(t, env, false)
}

// ReplayTinyCollect replays the fixture and returns representative in-memory
// input secrets for persistence and log scanning.
func ReplayTinyCollect(t *testing.T, env *Env) ([]string, []string) {
	t.Helper()
	return replayTiny(t, env, true)
}

func replayTiny(t *testing.T, env *Env, collect bool) ([]string, []string) {
	t.Helper()
	file, err := os.Open(fixtureEvents)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	client := env.server.Client()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	var saltDay string
	var ips, userAgents []string
	pageviews := 0
	for line := 1; scanner.Scan(); line++ {
		var event fixtureEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("event line %d: %v", line, err)
		}
		if collect && len(ips) < 2 {
			ips = appendUnique(ips, event.IP)
			userAgents = appendUnique(userAgents, event.UserAgent)
		}
		now := mustTime(t, event.TS)
		env.clock.Set(now)
		if day := now.UTC().Format(time.DateOnly); day != saltDay {
			if err := env.salts.Refresh(context.Background()); err != nil {
				t.Fatalf("refresh salts for %s: %v", day, err)
			}
			saltDay = day
		}
		sendFixture(t, env, client, scanner.Bytes(), line)

		if event.Name == "pageview" {
			pageviews++
			// A representative half of page exits keeps this measurement aligned
			// with the prototype's estimated 40-60% hot-tier overhead.
			if pageviews%2 == 0 {
				event.Name = "engagement"
				event.EngagementMS = 10_000
				event.ScrollPct = 50
				body, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				sendFixture(t, env, client, body, line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return ips, userAgents
}

func sendFixture(t *testing.T, env *Env, client *http.Client, body []byte, line int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/public/v1/events", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "text/plain")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("event line %d: %v", line, err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("event line %d: status %d", line, response.StatusCode)
	}
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// CloseAll drains the writer and closes every site-day represented by the fixture.
func (e *Env) CloseAll(t *testing.T) {
	t.Helper()
	e.stopWriter(t)
	byDay := loadCloseDays(t)
	for day, siteIDs := range byDay {
		parsed, err := time.Parse(time.DateOnly, day)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := closer.CloseDay(context.Background(), e.pool, siteIDs, parsed); err != nil {
			t.Fatalf("close %s: %v", day, err)
		}
	}
}

func (e *Env) stopWriter(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	if e.writerStopped {
		e.mu.Unlock()
		return
	}
	e.writerStopped = true
	e.writerStop()
	done := e.writerDone
	e.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("stop writer: %v", err)
	}
}

// SIGTERM performs the test server's graceful listener shutdown.
func (e *Env) SIGTERM(t *testing.T) {
	t.Helper()
	e.stopServer()
}

func (e *Env) stopServer() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.serverStopped {
		return
	}
	e.serverStopped = true
	e.server.Close()
}

// Logs returns all logs captured by the in-process test environment.
func (e *Env) Logs() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logs.String()
}

// EventsBytes returns the tuple bytes occupied by engagement or non-engagement events.
func (e *Env) EventsBytes(t *testing.T, engagement bool) int64 {
	t.Helper()
	operator := "<>"
	if engagement {
		operator = "="
	}
	var size int64
	if err := e.pool.QueryRow(context.Background(),
		"select coalesce(sum(pg_column_size(e)), 0)::bigint from events e where kind "+operator+" 2").Scan(&size); err != nil {
		t.Fatal(err)
	}
	return size
}

// PgDumpSchema returns a plain-text dump of the environment's isolated schema.
func PgDumpSchema(t *testing.T, env *Env) string {
	t.Helper()
	path, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skip("pg_dump is not installed")
	}
	command := exec.Command(path,
		"--dbname="+os.Getenv("SUPERMETRY_TEST_DSN"),
		"--schema="+env.schema,
		"--format=plain",
		"--no-owner",
		"--no-privileges",
	)
	command.Env = appendWithoutPGOptions(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pg_dump schema: %v\n%s", err, output)
	}
	return string(output)
}

func appendWithoutPGOptions(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "PGOPTIONS=") {
			result = append(result, entry)
		}
	}
	return result
}

type truthIntake struct {
	sites    sites.Registry
	salts    *salts.Store
	dict     *dict.Dict
	sessions session.Store
	writer   *writer.Writer
}

func (h *truthIntake) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	var event fixtureEvent
	if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	site, ok := h.sites.Site(int32(event.Site))
	if !ok {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	now, err := time.Parse(time.RFC3339, event.TS)
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	localDay := salts.LocalDay(now, site.TZ)
	salt, ok := h.salts.For(localDay)
	if !ok {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	visitorID := salts.VisitorID(salt, site.ID, event.IP, event.UserAgent)
	var previousVisitorID int64
	localNow := now.In(site.TZ)
	localMidnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, site.TZ)
	if localNow.Sub(localMidnight) <= 30*time.Minute {
		if previous, found := h.salts.For(localDay.AddDate(0, 0, -1)); found {
			previousVisitorID = salts.VisitorID(previous, site.ID, event.IP, event.UserAgent)
		}
	}

	pathID, err := h.id(request.Context(), dict.Path, event.Path)
	if err != nil {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	referrerID, err := h.id(request.Context(), dict.Referrer, event.Referrer)
	if err != nil {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	sourceID, err := h.id(request.Context(), dict.Source, fixtureSource(event))
	if err != nil {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	mediumID, err := h.id(request.Context(), dict.Medium, event.UTMMedium)
	if err != nil {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	campaignID, err := h.id(request.Context(), dict.Campaign, event.UTMCampaign)
	if err != nil {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	browserID, err := h.id(request.Context(), dict.Browser, event.Browser)
	if err != nil {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	osID, err := h.id(request.Context(), dict.OS, event.OS)
	if err != nil {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	var kind int16
	var nameID int32
	switch event.Name {
	case "pageview":
		kind = 0
	case "engagement":
		kind = 2
	default:
		kind = 1
		nameID, err = h.id(request.Context(), dict.EventName, event.Name)
		if err != nil {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	hit := session.Hit{
		SiteID: site.ID, Retention: site.Retention, TS: now, LocalDay: localDay,
		VisitorID: visitorID, PrevVisitorID: previousVisitorID, Kind: kind,
		PathID: pathID, NameID: nameID, Source: sourceID, Medium: mediumID,
		Campaign: campaignID, Country: event.Country, Browser: browserID,
		OS: osID, Device: fixtureDevice(event.Device),
		EngagementMS: int32(event.EngagementMS), ScrollPct: int16(event.ScrollPct),
	}
	result := h.sessions.Touch(hit)
	hit.VisitorID = result.VisitorID
	durable := writer.Event{Hit: hit, SessionID: result.SessionID, Screen: int16(event.ScreenWidth), ReferrerID: referrerID}
	if kind == 1 {
		durable.Props = event.Props
	}
	if !h.writer.Enqueue(durable) {
		response.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	response.WriteHeader(http.StatusAccepted)
}

func (h *truthIntake) id(ctx context.Context, kind dict.Kind, value string) (int32, error) {
	if value == "" {
		return 0, nil
	}
	ids, err := h.dict.IDs(ctx, kind, []string{value})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

type fixtureEvent struct {
	TS           string            `json:"ts"`
	Site         int               `json:"site"`
	Host         string            `json:"host"`
	Path         string            `json:"path"`
	Referrer     string            `json:"ref"`
	UTMSource    string            `json:"utm_source"`
	UTMMedium    string            `json:"utm_medium"`
	UTMCampaign  string            `json:"utm_campaign"`
	Name         string            `json:"name"`
	Props        map[string]string `json:"props"`
	IP           string            `json:"ip"`
	UserAgent    string            `json:"ua"`
	ScreenWidth  int               `json:"screen_w"`
	Country      string            `json:"country"`
	Browser      string            `json:"browser"`
	OS           string            `json:"os"`
	Device       string            `json:"device"`
	EngagementMS int               `json:"engagement_ms,omitempty"`
	ScrollPct    int               `json:"scroll_pct,omitempty"`
}

// fixtureSource intentionally mirrors the generator's raw source labels. The
// golden comparison canonicalizes labels without changing generator counts.
func fixtureSource(event fixtureEvent) string {
	if event.UTMSource != "" {
		return event.UTMSource
	}
	referrer, err := url.Parse(event.Referrer)
	if err != nil || referrer.Hostname() == "" || strings.EqualFold(referrer.Hostname(), event.Host) {
		return "(direct)"
	}
	return strings.ToLower(referrer.Hostname())
}

func fixtureDevice(value string) int16 {
	switch value {
	case "mobile":
		return 1
	case "tablet":
		return 2
	default:
		return 0
	}
}

func loadRegistry(t *testing.T, path string) *registry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		ID       int32  `json:"id"`
		Host     string `json:"host"`
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	r := &registry{byPublic: make(map[string]sites.Site), byID: make(map[int32]sites.Site)}
	for _, record := range records {
		location, err := time.LoadLocation(record.Timezone)
		if err != nil {
			t.Fatal(err)
		}
		site := sites.Site{ProfilesEnabled: true, ID: record.ID, PublicID: strconv.Itoa(int(record.ID)), TZ: location, Retention: db.Ret30, AllowedHosts: []string{record.Host}}
		r.byPublic[site.PublicID] = site
		r.byID[site.ID] = site
		r.all = append(r.all, site)
	}
	return r
}

func loadCloseDays(t *testing.T) map[string][]int32 {
	t.Helper()
	file, err := os.Open(fixtureTruth)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var fixture struct {
		Rows []struct {
			Site int32  `json:"site"`
			Date string `json:"date"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(file).Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	byDay := make(map[string][]int32)
	for _, row := range fixture.Rows {
		byDay[row.Date] = append(byDay[row.Date], row.Site)
	}
	return byDay
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func findRepositoryRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("testenv: locate source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
