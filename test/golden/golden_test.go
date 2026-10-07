package golden_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/closer"
	"panel/internal/config"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/ingest"
	"panel/internal/query"
	"panel/internal/salts"
	"panel/internal/session"
	"panel/internal/sites"
	"panel/internal/testpg"
	"panel/internal/writer"
)

const (
	fixtureEvents = "../../bench/gen/testdata/tiny.ndjson"
	fixtureSites  = "../../bench/gen/testdata/tiny.sites.json"
	fixtureTruth  = "../../bench/gen/testdata/tiny.truth.json"
	testAPIKey    = "golden-test-key"
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
func (r *registry) Site(id int32) (sites.Site, bool) { site, ok := r.byID[id]; return site, ok }
func (r *registry) All() []sites.Site                { return append([]sites.Site(nil), r.all...) }

type noClosedDays struct{}

func (noClosedDays) IsClosed(int32, time.Time) bool { return false }

type panelEnv struct {
	pool       *pgxpool.Pool
	server     *httptest.Server
	clock      *testClock
	salts      *salts.Store
	writerStop context.CancelFunc
	writerDone <-chan error
	registry   *registry
	truth      []truthRow
}

func TestGoldenTinyTruth(t *testing.T) {
	env := startPanel(t)
	replayTiny(t, env, fixtureEvents)
	env.CloseAll(t)
	if mismatches := verifyAgainstTruth(t, env, fixtureTruth); mismatches != 0 {
		t.Fatalf("%d mismatches against truth", mismatches)
	}
}

func TestGracefulShutdownLosesNothing(t *testing.T) {
	pool, schema := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	root := filepath.Clean(filepath.Join("..", ".."))
	binary := filepath.Join(t.TempDir(), "panel")
	build := exec.Command("go", "build", "-o", binary, "./cmd/panel")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build panel: %v\n%s", err, output)
	}

	address := freeAddress(t)
	sitesPath := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(sitesPath, []byte(`[{"id":1,"public_id":"shutdown","timezone":"UTC","retention_days":30,"allowed_hosts":["shutdown.test"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(binary, "serve")
	command.Env = append(os.Environ(),
		"PANEL_DATABASE_URL="+schemaDSN(t, schema),
		"PANEL_LISTEN="+address,
		"PANEL_BASE_URL=http://"+address,
		"PANEL_SECRET_KEY="+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		"PANEL_ADMIN_API_KEY="+testAPIKey,
		"PANEL_SITES_FILE="+sitesPath,
		"PANEL_FLUSH_EVERY=1h",
		"PANEL_SESSION_FLUSH=1h",
	)
	var logs bytes.Buffer
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped && command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	waitReady(t, "http://"+address+"/readyz", command, &logs)

	client := &http.Client{Timeout: 5 * time.Second}
	body := `{"site":"shutdown","name":"pageview","url":"https://shutdown.test/"}`
	for i := 0; i < 2000; i++ {
		req, err := http.NewRequest(http.MethodPost, "http://"+address+"/api/public/v1/events", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/131.0 Safari/537.36")
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("send event %d: %v", i, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("send event %d: status %d", i, response.StatusCode)
		}
	}

	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("panel shutdown: %v\n%s", err, logs.String())
	}
	stopped = true

	var count int
	if err := pool.QueryRow(ctx, `select count(*) from events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2000 {
		t.Fatalf("events after shutdown = %d, want 2000", count)
	}
}

func startPanel(t *testing.T) *panelEnv {
	t.Helper()
	pool, _ := testpg.New(t)
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
	writerCtx, stopWriter := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(writerCtx) }()

	handler := &truthIntake{sites: reg, salts: saltStore, dict: dictionary, sessions: sessions, writer: w}
	engine := query.NewEngine(pool, dictionary, reg)
	router := api.NewRouter(api.Deps{
		Config: config.Config{EventPath: "/api/public/v1/events", AdminAPIKey: testAPIKey},
		Query:  engine, Ingest: handler, Sessions: sessions, Now: clock.Now,
		RateLimiter: ingest.NewTokenBuckets(100_000, 100_000), Ready: func() bool { return true },
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &panelEnv{
		pool: pool, server: server, clock: clock, salts: saltStore, writerStop: stopWriter,
		writerDone: writerDone, registry: reg,
	}
}

func replayTiny(t *testing.T, env *panelEnv, path string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	client := env.server.Client()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	var saltDay string
	for line := 1; scanner.Scan(); line++ {
		var event fixtureEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("event line %d: %v", line, err)
		}
		now := mustTime(t, event.TS)
		env.clock.Set(now)
		if day := now.UTC().Format(time.DateOnly); day != saltDay {
			if err := env.salts.Refresh(context.Background()); err != nil {
				t.Fatalf("refresh salts for %s: %v", day, err)
			}
			saltDay = day
		}

		request, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/public/v1/events", bytes.NewReader(scanner.Bytes()))
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
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

// truthIntake is the golden fixture adapter used by the prototype's bulk
// verifier: it trusts the fixture's already-enriched dimensions while still
// exercising sessionization, the bounded writer, closing and the HTTP API.
// The production intake path is exercised by TestGracefulShutdownLosesNothing.
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

	kind := int16(1)
	var nameID int32
	if event.Name == "pageview" {
		kind = 0
	} else {
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

func (env *panelEnv) CloseAll(t *testing.T) {
	t.Helper()
	env.writerStop()
	if err := <-env.writerDone; err != nil {
		t.Fatalf("stop writer: %v", err)
	}
	rows := loadTruth(t, fixtureTruth)
	env.truth = rows
	byDay := make(map[string][]int32)
	for _, row := range rows {
		byDay[row.Date] = append(byDay[row.Date], row.Site)
	}
	for day, siteIDs := range byDay {
		parsed, err := time.Parse(time.DateOnly, day)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := closer.CloseDay(context.Background(), env.pool, siteIDs, parsed); err != nil {
			t.Fatalf("close %s: %v", day, err)
		}
	}
}

func verifyAgainstTruth(t *testing.T, env *panelEnv, path string) int {
	t.Helper()
	rows := env.truth
	if rows == nil {
		rows = loadTruth(t, path)
	}
	mismatches := 0
	report := func(format string, args ...any) {
		mismatches++
		if mismatches <= 50 {
			t.Logf(format, args...)
		}
	}
	type span struct {
		from, to                    string
		visitors, visits, pageviews int64
		bounces                     int64
		durationSeconds             float64
	}
	spans := make(map[int32]*span)
	for _, truth := range rows {
		current := spans[truth.Site]
		if current == nil {
			current = &span{from: truth.Date, to: truth.Date}
			spans[truth.Site] = current
		}
		if truth.Date < current.from {
			current.from = truth.Date
		}
		if truth.Date > current.to {
			current.to = truth.Date
		}
		current.visitors += truth.Visitors
		current.visits += truth.Visits
		current.pageviews += truth.Pageviews
		current.bounces += truth.Bounces
		current.durationSeconds += truth.DurationSeconds()

		response := queryAPI(t, env, api.QueryRequest{
			SiteId: truth.Site, DateRange: dateRange(t, truth.Date, truth.Date),
			Metrics: []api.Metric{api.Visitors, api.Visits, api.Pageviews, api.ViewsPerVisit, api.BounceRate, api.VisitDuration},
		})
		checks := map[string]float64{
			"visitors": float64(truth.Visitors), "visits": float64(truth.Visits), "pageviews": float64(truth.Pageviews),
			"views_per_visit": ratio(float64(truth.Pageviews), truth.Visits),
			"bounce_rate":     percent(truth.Bounces, truth.Visits),
			"visit_duration":  ratio(truth.DurationSeconds(), truth.Visits),
		}
		for metric, want := range checks {
			if got := response.Totals[metric]; !near(got, want) {
				report("site %d %s %s: got %v want %v", truth.Site, truth.Date, metric, got, want)
			}
		}
		for dimension, want := range truth.Top {
			limit := len(want)
			dimensions := []string{dimension}
			descending := api.Desc
			orderBy := []api.OrderBy{
				{Column: "visitors", Direction: &descending},
				{Column: "pageviews", Direction: &descending},
			}
			response := queryAPI(t, env, api.QueryRequest{
				SiteId: truth.Site, DateRange: dateRange(t, truth.Date, truth.Date), Dimensions: &dimensions,
				Metrics: []api.Metric{api.Visitors, api.Pageviews}, Limit: &limit, OrderBy: &orderBy,
			})
			got := make(map[string]topEntry, len(response.Rows))
			for _, row := range response.Rows {
				value, _ := row[dimension].(string)
				value = truthLabel(dimension, value)
				got[value] = topEntry{Value: value, Visitors: int64(row["visitors"].(float64)), Pageviews: int64(row["pageviews"].(float64))}
			}
			for _, entry := range want {
				if actual, ok := got[entry.Value]; !ok || actual.Visitors != entry.Visitors || actual.Pageviews != entry.Pageviews {
					report("site %d %s %s=%q: got %+v want %+v", truth.Site, truth.Date, dimension, entry.Value, actual, entry)
				}
			}
		}

		for _, dimension := range []string{"country", "source", "path", "entry"} {
			for index, entry := range truth.Top[dimension] {
				if index == 3 {
					break
				}
				filters := []api.Filter{{Dimension: dimension, Operator: api.Is, Values: []string{entry.Value}}}
				filtered := queryAPI(t, env, api.QueryRequest{
					SiteId: truth.Site, DateRange: dateRange(t, truth.Date, truth.Date), Filters: &filters,
					Metrics: []api.Metric{api.Visitors, api.Pageviews},
				})
				if got := int64(filtered.Totals["visitors"]); got != entry.Visitors {
					report("site %d %s filter %s=%q visitors: got %d want %d", truth.Site, truth.Date, dimension, entry.Value, got, entry.Visitors)
				}
				if got := int64(filtered.Totals["pageviews"]); got != entry.Pageviews {
					report("site %d %s filter %s=%q pageviews: got %d want %d", truth.Site, truth.Date, dimension, entry.Value, got, entry.Pageviews)
				}
			}
		}

		funnel := funnelAPI(t, env, api.FunnelRequest{
			SiteId: truth.Site, DateRange: *dateRange(t, truth.Date, truth.Date),
			Steps: []api.FunnelStep{
				{Type: api.Page, Match: "exact", Value: "/"},
				{Type: api.Page, Match: "exact", Value: "/pricing"},
				{Type: api.Event, Match: "exact", Value: "signup"},
			},
		})
		for index, want := range truth.FunnelF {
			if got := funnel.Steps[index].Visitors; got != want {
				report("site %d %s funnel step %d: got %d want %d", truth.Site, truth.Date, index+1, got, want)
			}
		}

		propertyDimension := []string{"prop:plan"}
		properties := queryAPI(t, env, api.QueryRequest{
			SiteId: truth.Site, DateRange: dateRange(t, truth.Date, truth.Date), Dimensions: &propertyDimension,
			Metrics: []api.Metric{api.Events},
		})
		gotPlans := make(map[string]int64, len(properties.Rows))
		for _, row := range properties.Rows {
			value, _ := row["prop:plan"].(string)
			gotPlans[value] = int64(row["events"].(float64))
		}
		for plan, want := range truth.SignupPlan {
			if got := gotPlans[plan]; got != want {
				report("site %d %s signup.plan=%s: got %d want %d", truth.Site, truth.Date, plan, got, want)
			}
		}
	}

	for siteID, span := range spans {
		response := queryAPI(t, env, api.QueryRequest{
			SiteId: siteID, DateRange: dateRange(t, span.from, span.to),
			Metrics: []api.Metric{api.Visitors, api.Visits, api.Pageviews, api.BounceRate, api.VisitDuration},
		})
		checks := map[string]float64{
			"visitors": float64(span.visitors), "visits": float64(span.visits), "pageviews": float64(span.pageviews),
			"bounce_rate": percent(span.bounces, span.visits), "visit_duration": ratio(span.durationSeconds, span.visits),
		}
		for metric, want := range checks {
			if got := response.Totals[metric]; !near(got, want) {
				report("site %d range %s..%s %s: got %v want %v", siteID, span.from, span.to, metric, got, want)
			}
		}
	}
	return mismatches
}

func queryAPI(t *testing.T, env *panelEnv, request api.QueryRequest) api.QueryResponse {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/public/v1/query", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(response.Body)
		t.Fatalf("query status %d: %s", response.StatusCode, message)
	}
	var result api.QueryResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func funnelAPI(t *testing.T, env *panelEnv, request api.FunnelRequest) api.FunnelResponse {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/public/v1/funnels/query", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(response.Body)
		t.Fatalf("funnel status %d: %s", response.StatusCode, message)
	}
	var result api.FunnelResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

type fixtureEvent struct {
	TS          string            `json:"ts"`
	Site        int               `json:"site"`
	Host        string            `json:"host"`
	Path        string            `json:"path"`
	Referrer    string            `json:"ref"`
	UTMSource   string            `json:"utm_source"`
	UTMMedium   string            `json:"utm_medium"`
	UTMCampaign string            `json:"utm_campaign"`
	Name        string            `json:"name"`
	Props       map[string]string `json:"props"`
	IP          string            `json:"ip"`
	UserAgent   string            `json:"ua"`
	ScreenWidth int               `json:"screen_w"`
	Country     string            `json:"country"`
	Browser     string            `json:"browser"`
	OS          string            `json:"os"`
	Device      string            `json:"device"`
}

type topEntry struct {
	Value     string `json:"value"`
	Visitors  int64  `json:"visitors"`
	Pageviews int64  `json:"pageviews"`
}

type truthRow struct {
	Site         int32                 `json:"site"`
	Date         string                `json:"date"`
	Visitors     int64                 `json:"visitors"`
	Visits       int64                 `json:"visits"`
	Pageviews    int64                 `json:"pageviews"`
	Bounces      int64                 `json:"bounces"`
	DurationSumS json.Number           `json:"duration_sum_s"`
	Top          map[string][]topEntry `json:"top"`
	FunnelF      []int64               `json:"funnel_f"`
	SignupPlan   map[string]int64      `json:"signup_plan"`
}

func (r truthRow) DurationSeconds() float64 {
	value, _ := strconv.ParseFloat(string(r.DurationSumS), 64)
	return value
}

func loadTruth(t *testing.T, path string) []truthRow {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var fixture struct {
		Rows []truthRow `json:"rows"`
	}
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Rows
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
		site := sites.Site{ID: record.ID, PublicID: strconv.Itoa(int(record.ID)), TZ: location, Retention: db.Ret30, AllowedHosts: []string{record.Host}}
		r.byPublic[site.PublicID] = site
		r.byID[site.ID] = site
		r.all = append(r.all, site)
	}
	return r
}

func dateRange(t *testing.T, from, to string) *api.DateRange {
	t.Helper()
	fromTime, err := time.Parse(time.DateOnly, from)
	if err != nil {
		t.Fatal(err)
	}
	toTime, err := time.Parse(time.DateOnly, to)
	if err != nil {
		t.Fatal(err)
	}
	return &api.DateRange{From: openapi_types.Date{Time: fromTime}, To: openapi_types.Date{Time: toTime}}
}

func truthLabel(dimension, value string) string {
	if dimension != "source" {
		return value
	}
	switch value {
	case "":
		return "(direct)"
	case "Google":
		return "www.google.com"
	case "Bing":
		return "www.bing.com"
	case "DuckDuckGo":
		return "duckduckgo.com"
	case "Ecosia":
		return "www.ecosia.org"
	default:
		return value
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func ratio(numerator float64, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return numerator / float64(denominator)
}

func percent(numerator, denominator int64) float64 { return ratio(float64(numerator)*100, denominator) }

func near(got, want float64) bool { return math.Abs(got-want) <= 0.001*math.Max(1, math.Abs(want)) }

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func schemaDSN(t *testing.T, schema string) string {
	t.Helper()
	parsed, err := url.Parse(os.Getenv("PANEL_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	queryValues := parsed.Query()
	queryValues.Set("options", "-csearch_path="+schema)
	parsed.RawQuery = queryValues.Encode()
	return parsed.String()
}

func waitReady(t *testing.T, endpoint string, command *exec.Cmd, logs *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if command.ProcessState != nil && command.ProcessState.Exited() {
			t.Fatalf("panel exited before ready: %s", logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("panel did not become ready: %s", logs.String())
}
