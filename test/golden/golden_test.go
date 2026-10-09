package golden_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/enrich"
	"supermetry/internal/testenv"
	"supermetry/internal/testpg"
)

const (
	fixtureTruth = "../../bench/gen/testdata/tiny.truth.json"
	testAPIKey   = testenv.TestAPIKey
)

func TestGoldenTinyTruth(t *testing.T) {
	env := testenv.Start(t)
	testenv.ReplayTiny(t, env)
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
	binary := filepath.Join(t.TempDir(), "supermetry")
	build := exec.Command("go", "build", "-o", binary, "./cmd/supermetry")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build supermetry: %v\n%s", err, output)
	}

	address := freeAddress(t)
	sitesPath := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(sitesPath, []byte(`[{"id":1,"public_id":"shutdown","timezone":"UTC","retention_days":30,"allowed_hosts":["shutdown.test"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(binary, "serve")
	command.Env = append(os.Environ(),
		"SUPERMETRY_DATABASE_URL="+schemaDSN(t, schema),
		"SUPERMETRY_LISTEN="+address,
		"SUPERMETRY_BASE_URL=http://"+address,
		"SUPERMETRY_SECRET_KEY="+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		"SUPERMETRY_ADMIN_API_KEY="+testAPIKey,
		"SUPERMETRY_SITES_FILE="+sitesPath,
		"SUPERMETRY_FLUSH_EVERY=1h",
		"SUPERMETRY_SESSION_FLUSH=1h",
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
		t.Fatalf("supermetry shutdown: %v\n%s", err, logs.String())
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

func verifyAgainstTruth(t *testing.T, env *testenv.Env, path string) int {
	t.Helper()
	classifier, err := enrich.NewSourceClassifier()
	if err != nil {
		t.Fatal(err)
	}
	rows := loadTruth(t, path)
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
			rawLimit := len(want)
			want = comparisonTop(classifier, dimension, want)
			limit := rawLimit
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
				value = truthLabel(classifier, dimension, value)
				entry := got[value]
				entry.Value = value
				entry.Visitors += int64(row["visitors"].(float64))
				entry.Pageviews += int64(row["pageviews"].(float64))
				got[value] = entry
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

func queryAPI(t *testing.T, env *testenv.Env, request api.QueryRequest) api.QueryResponse {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := rateLimitedRequest(t, env, "/api/public/v1/query", body)
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

func funnelAPI(t *testing.T, env *testenv.Env, request api.FunnelRequest) api.FunnelResponse {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := rateLimitedRequest(t, env, "/api/public/v1/funnels/query", body)
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

func rateLimitedRequest(t *testing.T, env *testenv.Env, path string, body []byte) *http.Response {
	t.Helper()
	for attempt := 0; attempt < 5; attempt++ {
		req, err := http.NewRequest(http.MethodPost, env.URL()+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		response, err := env.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusTooManyRequests {
			return response
		}
		_ = response.Body.Close()
		retryAfter, err := strconv.Atoi(response.Header.Get("Retry-After"))
		if err != nil || retryAfter < 1 {
			retryAfter = 1
		}
		time.Sleep(time.Duration(retryAfter) * time.Second)
	}
	t.Fatal("API remained rate limited after retries")
	return nil
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

func comparisonTop(classifier *enrich.SourceClassifier, dimension string, entries []topEntry) []topEntry {
	if dimension != "source" {
		return entries
	}
	positions := make(map[string]int, len(entries))
	result := make([]topEntry, 0, len(entries))
	for _, entry := range entries {
		entry.Value = truthLabel(classifier, dimension, entry.Value)
		if position, ok := positions[entry.Value]; ok {
			result[position].Visitors += entry.Visitors
			result[position].Pageviews += entry.Pageviews
			continue
		}
		positions[entry.Value] = len(result)
		result = append(result, entry)
	}
	return result
}

func truthLabel(classifier *enrich.SourceClassifier, dimension, value string) string {
	if dimension != "source" {
		return value
	}
	if value == "" || value == "(direct)" {
		return "(direct)"
	}
	if canonical, _, known := classifier.CanonicalSource(value); known {
		return canonical
	}
	return strings.TrimPrefix(value, "www.")
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
	parsed, err := url.Parse(os.Getenv("SUPERMETRY_TEST_DSN"))
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
	// Full-suite runs start several PostgreSQL-backed packages concurrently;
	// allow migrations and GeoIP setup to finish under that expected load.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if command.ProcessState != nil && command.ProcessState.Exited() {
			t.Fatalf("supermetry exited before ready: %s", logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("supermetry did not become ready: %s", logs.String())
}
