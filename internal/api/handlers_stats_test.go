package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/ingest"
	"supermetry/internal/realtime"
	"supermetry/internal/session"
	"supermetry/internal/sites"
)

type queryFunc struct {
	query func(api.QueryRequest) (api.QueryResponse, error)
}

func (q queryFunc) Query(_ context.Context, request api.QueryRequest) (api.QueryResponse, error) {
	return q.query(request)
}

func (queryFunc) Funnel(context.Context, api.FunnelRequest) (api.FunnelResponse, error) {
	return api.FunnelResponse{}, nil
}

func TestStatsGETTranslatesParametersToQuery(t *testing.T) {
	var got api.QueryRequest
	router := statsTestRouter(queryFunc{query: func(request api.QueryRequest) (api.QueryResponse, error) {
		got = request
		return emptyQueryResponse(), nil
	}}, nil)
	request := httptest.NewRequest(http.MethodGet,
		"/api/public/v1/stats/summary?site_id=1&period=custom&date_from=2026-09-01&date_to=2026-09-30"+
			"&metrics=visitors,pageviews&filter=country:is:US,CA&filter=prop%3Aplan:is_not:free"+
			"&compare=previous_period&timezone=Asia%2FKolkata", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got.SiteId != 1 || got.DateRange == nil || got.DateRange.From.Format("2006-01-02") != "2026-09-01" || got.DateRange.To.Format("2006-01-02") != "2026-09-30" {
		t.Fatalf("date request = %+v", got)
	}
	if got.Period != nil || got.Timezone == nil || *got.Timezone != "Asia/Kolkata" {
		t.Fatalf("period/timezone = %+v/%v", got.Period, got.Timezone)
	}
	if fmt.Sprint(got.Metrics) != "[visitors pageviews]" {
		t.Fatalf("metrics = %v", got.Metrics)
	}
	if got.Filters == nil || len(*got.Filters) != 2 || (*got.Filters)[0].Dimension != "country" || fmt.Sprint((*got.Filters)[0].Values) != "[US CA]" || (*got.Filters)[1].Dimension != "prop:plan" {
		t.Fatalf("filters = %+v", got.Filters)
	}
	if got.Compare == nil || *got.Compare != api.PreviousPeriod {
		t.Fatalf("compare = %v", got.Compare)
	}
}

func TestStatsGETRejectsInvalidParametersAsProblems(t *testing.T) {
	router := statsTestRouter(queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) {
		t.Fatal("query service called for invalid request")
		return api.QueryResponse{}, nil
	}}, nil)
	tests := []struct {
		name string
		path string
	}{
		{name: "custom period needs dates", path: "/api/public/v1/stats/summary?site_id=1&period=custom"},
		{name: "date is strict ISO", path: "/api/public/v1/stats/summary?site_id=1&period=custom&date_from=09-01-2026&date_to=2026-09-30"},
		{name: "metrics are known", path: "/api/public/v1/stats/summary?site_id=1&period=7d&metrics=visitors,money"},
		{name: "at most ten filters", path: "/api/public/v1/stats/summary?site_id=1&period=7d&" + strings.Repeat("filter=country:is:US&", 11)},
		{name: "filter operator is known", path: "/api/public/v1/stats/summary?site_id=1&period=7d&filter=country:matches:US"},
		{name: "compare is known", path: "/api/public/v1/stats/summary?site_id=1&period=7d&compare=year"},
		{name: "breakdown limit is bounded", path: "/api/public/v1/stats/breakdown?site_id=1&period=7d&dimension=page&limit=1001"},
		{name: "all limit needs CSV", path: "/api/public/v1/stats/breakdown?site_id=1&period=7d&dimension=page&limit=all"},
		{name: "hour is at most seven days", path: "/api/public/v1/stats/timeseries?site_id=1&period=30d&interval=hour"},
		{name: "format is known", path: "/api/public/v1/stats/summary?site_id=1&period=7d&format=xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.Header.Set("Authorization", "Bearer test-key")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertRecorderProblem(t, response, http.StatusBadRequest)
		})
	}
}

func TestUnknownPublicAPIRouteReturnsProblem(t *testing.T) {
	router := statsTestRouter(queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) { return emptyQueryResponse(), nil }}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/not-a-route", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertRecorderProblem(t, response, http.StatusNotFound)
}

func TestStatsTimeseriesOrdersAndZeroFillsDays(t *testing.T) {
	router := statsTestRouter(queryFunc{query: func(request api.QueryRequest) (api.QueryResponse, error) {
		if request.Dimensions == nil || fmt.Sprint(*request.Dimensions) != "[time]" {
			t.Fatalf("dimensions = %v", request.Dimensions)
		}
		return api.QueryResponse{
			Columns: []string{"time", "visitors"},
			Rows: []map[string]any{
				{"time": "2026-10-03", "visitors": float64(3)},
				{"time": "2026-10-01", "visitors": float64(1)},
			},
			Totals: map[string]float64{"visitors": 4},
			Meta:   api.QueryMeta{SourcesUsed: []string{"rollup"}},
		}, nil
	}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/timeseries?site_id=1&period=custom&date_from=2026-10-01&date_to=2026-10-03&interval=day&metrics=visitors", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body api.QueryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Rows) != 3 || body.Rows[0]["time"] != "2026-10-01" || body.Rows[1]["time"] != "2026-10-02" || body.Rows[1]["visitors"] != float64(0) || body.Rows[2]["time"] != "2026-10-03" {
		t.Fatalf("rows = %#v", body.Rows)
	}
}

func TestStatsTimeseriesReadsEveryCursorPageBeforeZeroFill(t *testing.T) {
	calls := 0
	router := statsTestRouter(queryFunc{query: func(request api.QueryRequest) (api.QueryResponse, error) {
		calls++
		if request.Limit == nil || *request.Limit != 1000 {
			t.Fatalf("limit=%v", request.Limit)
		}
		response := api.QueryResponse{Columns: []string{"time", "visitors"}, Totals: map[string]float64{"visitors": 3}, Meta: api.QueryMeta{}}
		if calls == 1 {
			next := "next"
			response.Rows = []map[string]any{{"time": "2026-10-01", "visitors": float64(1)}}
			response.NextCursor = &next
		} else {
			if value(request.Cursor) != "next" {
				t.Fatalf("cursor=%q", value(request.Cursor))
			}
			response.Rows = []map[string]any{{"time": "2026-10-02", "visitors": float64(2)}}
		}
		return response, nil
	}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/timeseries?site_id=1&period=custom&date_from=2026-10-01&date_to=2026-10-02&metrics=visitors", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || calls != 2 || !strings.Contains(response.Body.String(), `"time":"2026-10-02","visitors":2`) {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

func TestStatsTimeseriesIntervals(t *testing.T) {
	tests := []struct {
		name       string
		interval   string
		from       string
		to         string
		columns    []string
		rows       []map[string]any
		wantTimes  []string
		wantValues []float64
		wantDim    string
	}{
		{
			name: "week aggregates and zero fills ISO weeks", interval: "week", from: "2026-09-29", to: "2026-10-14",
			columns: []string{"time", "visitors"}, rows: []map[string]any{{"time": "2026-09-30", "visitors": float64(2)}, {"time": "2026-10-02", "visitors": float64(3)}, {"time": "2026-10-13", "visitors": float64(4)}},
			wantTimes: []string{"2026-09-28", "2026-10-05", "2026-10-12"}, wantValues: []float64{5, 0, 4}, wantDim: "time",
		},
		{
			name: "month aggregates and zero fills months", interval: "month", from: "2026-01-15", to: "2026-03-02",
			columns: []string{"time", "visitors"}, rows: []map[string]any{{"time": "2026-01-20", "visitors": float64(7)}, {"time": "2026-03-01", "visitors": float64(2)}},
			wantTimes: []string{"2026-01-01", "2026-02-01", "2026-03-01"}, wantValues: []float64{7, 0, 2}, wantDim: "time",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := statsTestRouter(queryFunc{query: func(request api.QueryRequest) (api.QueryResponse, error) {
				if request.Dimensions == nil || len(*request.Dimensions) != 1 || (*request.Dimensions)[0] != tt.wantDim {
					t.Fatalf("dimensions=%v want %q", request.Dimensions, tt.wantDim)
				}
				return api.QueryResponse{Columns: tt.columns, Rows: tt.rows, Totals: map[string]float64{}, Meta: api.QueryMeta{}}, nil
			}}, nil)
			path := fmt.Sprintf("/api/public/v1/stats/timeseries?site_id=1&period=custom&date_from=%s&date_to=%s&interval=%s&metrics=visitors", tt.from, tt.to, tt.interval)
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer test-key")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body api.QueryResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Rows) != len(tt.wantTimes) {
				t.Fatalf("rows=%#v", body.Rows)
			}
			for index, row := range body.Rows {
				if row["time"] != tt.wantTimes[index] || row["visitors"] != tt.wantValues[index] {
					t.Fatalf("row %d=%#v want time=%q visitors=%v", index, row, tt.wantTimes[index], tt.wantValues[index])
				}
			}
		})
	}
}

func TestStatsTimeseriesHourUsesEngineDimensionAndZeroFills(t *testing.T) {
	router := statsTestRouter(queryFunc{query: func(request api.QueryRequest) (api.QueryResponse, error) {
		if request.Dimensions == nil || fmt.Sprint(*request.Dimensions) != "[hour]" {
			t.Fatalf("dimensions=%v", request.Dimensions)
		}
		return api.QueryResponse{
			Columns: []string{"hour", "visitors"},
			Rows:    []map[string]any{{"hour": "2026-10-08T10:00:00", "visitors": float64(2)}},
			Totals:  map[string]float64{"visitors": 2}, Meta: api.QueryMeta{},
		}, nil
	}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/timeseries?site_id=1&period=custom&date_from=2026-10-08&date_to=2026-10-08&interval=hour&metrics=visitors", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body api.QueryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(body.Columns) != "[time visitors]" || len(body.Rows) != 24 {
		t.Fatalf("columns=%v rows=%d", body.Columns, len(body.Rows))
	}
	if body.Rows[0]["time"] != "2026-10-08T00:00:00" || body.Rows[0]["visitors"] != float64(0) || body.Rows[10]["time"] != "2026-10-08T10:00:00" || body.Rows[10]["visitors"] != float64(2) || body.Rows[23]["time"] != "2026-10-08T23:00:00" {
		t.Fatalf("hour rows=%#v", body.Rows)
	}
}

func TestStatsTimeseriesWeekRequeriesBucketsForNonAdditiveMetrics(t *testing.T) {
	calls := 0
	router := statsTestRouter(queryFunc{query: func(request api.QueryRequest) (api.QueryResponse, error) {
		calls++
		if calls == 1 {
			return api.QueryResponse{
				Columns: []string{"time", "bounce_rate"},
				Rows: []map[string]any{
					{"time": "2026-10-01", "bounce_rate": float64(10)},
					{"time": "2026-10-02", "bounce_rate": float64(20)},
				},
				Totals: map[string]float64{"bounce_rate": 30}, Meta: api.QueryMeta{},
			}, nil
		}
		if request.Dimensions == nil || len(*request.Dimensions) != 0 || request.DateRange == nil {
			t.Fatalf("bucket request=%+v", request)
		}
		value := float64(25)
		if request.DateRange.From.Format("2006-01-02") == "2026-10-05" {
			value = 50
		}
		return api.QueryResponse{Columns: []string{"bounce_rate"}, Totals: map[string]float64{"bounce_rate": value}, Meta: api.QueryMeta{}}, nil
	}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/timeseries?site_id=1&period=custom&date_from=2026-10-01&date_to=2026-10-10&interval=week&metrics=bounce_rate", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body api.QueryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(body.Rows) != 2 || body.Rows[0]["bounce_rate"] != float64(25) || body.Rows[1]["bounce_rate"] != float64(50) {
		t.Fatalf("calls=%d rows=%#v", calls, body.Rows)
	}
}

func TestStatsTimeseriesAlignsPreviousPeriodComparisonBuckets(t *testing.T) {
	router := statsTestRouter(queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) {
		return api.QueryResponse{
			Columns: []string{"time", "visitors"},
			Rows:    []map[string]any{{"time": "2026-10-01", "visitors": float64(4)}},
			Totals:  map[string]float64{"visitors": 4}, Meta: api.QueryMeta{},
			Comparison: &api.ComparisonResult{
				DateRange: dateRangeForTest("2026-09-28", "2026-09-30"),
				Rows:      []map[string]any{{"time": "2026-09-29", "visitors": float64(2)}},
				Totals:    map[string]float64{"visitors": 2},
			},
		}, nil
	}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/timeseries?site_id=1&period=custom&date_from=2026-10-01&date_to=2026-10-03&interval=day&metrics=visitors&compare=previous_period", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body api.QueryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Comparison == nil || len(body.Comparison.Rows) != 3 || body.Comparison.Rows[0]["time"] != "2026-10-01" || body.Comparison.Rows[1]["visitors"] != float64(2) {
		t.Fatalf("comparison=%+v", body.Comparison)
	}
}

func dateRangeForTest(from, to string) api.DateRange {
	fromDay, _ := time.Parse("2006-01-02", from)
	toDay, _ := time.Parse("2006-01-02", to)
	return api.DateRange{From: openapi_types.Date{Time: fromDay}, To: openapi_types.Date{Time: toDay}}
}

func TestStatsCSVUsesFormatAndDynamicFilename(t *testing.T) {
	router := statsTestRouter(queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) {
		return api.QueryResponse{
			Columns: []string{"page", "visitors"},
			Rows:    []map[string]any{{"page": "/docs", "visitors": float64(2)}},
			Totals:  map[string]float64{"visitors": 2}, Meta: api.QueryMeta{},
		}, nil
	}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/stats/breakdown?site_id=1&period=custom&date_from=2026-10-01&date_to=2026-10-03&dimension=page&format=csv", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if got, want := response.Header().Get("Content-Disposition"), `attachment; filename="tiny-breakdown-2026-10-01-2026-10-03.csv"`; got != want {
		t.Fatalf("Content-Disposition=%q want %q", got, want)
	}
}

func TestQueryCSVLimitAllStreamsPagesAndCapsRows(t *testing.T) {
	const rowsPerPage = 1000
	calls := 0
	router := statsTestRouter(queryFunc{query: func(request api.QueryRequest) (api.QueryResponse, error) {
		calls++
		if request.Limit == nil || *request.Limit != rowsPerPage {
			t.Fatalf("limit = %v", request.Limit)
		}
		wantCursor := ""
		if calls > 1 {
			wantCursor = fmt.Sprintf("cursor-%d", calls-1)
		}
		if value(request.Cursor) != wantCursor {
			t.Fatalf("call %d cursor=%q want %q", calls, value(request.Cursor), wantCursor)
		}
		rows := make([]map[string]any, rowsPerPage)
		for i := range rows {
			rows[i] = map[string]any{"page": fmt.Sprintf("/%d", i), "visitors": float64(1)}
		}
		next := fmt.Sprintf("cursor-%d", calls)
		return api.QueryResponse{Columns: []string{"page", "visitors"}, Rows: rows, Totals: map[string]float64{"visitors": rowsPerPage}, Meta: api.QueryMeta{}, NextCursor: &next}, nil
	}}, nil)
	body := `{"site_id":1,"period":"7d","metrics":["visitors"],"dimensions":["page"]}`
	request := httptest.NewRequest(http.MethodPost, "/api/public/v1/query?format=csv&limit=all", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-key")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if calls != 100 {
		t.Fatalf("query calls=%d want 100", calls)
	}
	if lines := strings.Count(response.Body.String(), "\n"); lines != 100001 {
		t.Fatalf("CSV lines=%d want 100001", lines)
	}
	if got := response.Header().Get("Content-Disposition"); got != `attachment; filename="tiny-query-2026-10-02-2026-10-08.csv"` {
		t.Fatalf("Content-Disposition=%q", got)
	}
}

type realtimeStore struct {
	since time.Time
}

func (s *realtimeStore) Touch(session.Hit) session.Result { return session.Result{} }
func (s *realtimeStore) Dirty(int) []session.Snapshot     { return nil }
func (s *realtimeStore) Restore([]session.Snapshot)       {}
func (s *realtimeStore) Expire(time.Time) int             { return 0 }
func (s *realtimeStore) ActiveSince(_ int32, since time.Time) (int, []int32) {
	s.since = since
	return 4, []int32{12, 34}
}

func (s *realtimeStore) ActiveBreakdownsSince(_ int32, since time.Time) (int, []session.ActiveValue, []session.ActiveValue) {
	s.since = since
	return 4, []session.ActiveValue{{ID: 12, Visitors: 3}, {ID: 34, Visitors: 1}}, []session.ActiveValue{{ID: 56, Visitors: 2}}
}

func TestRealtimeUsesThirtyMinuteWindowAndIncludesSourcesShape(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	store := &realtimeStore{}
	router := statsTestRouter(queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) {
		return emptyQueryResponse(), nil
	}}, store)
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/realtime?site_id=1", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if want := now.Add(-30 * time.Minute); !store.since.Equal(want) {
		t.Fatalf("since=%s want %s", store.since, want)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["top_sources"]; !ok {
		t.Fatalf("top_sources missing: %s", response.Body.String())
	}
	sources := body["top_sources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["source"] != "Google" || sources[0].(map[string]any)["visitors"] != float64(2) {
		t.Fatalf("top_sources=%#v", sources)
	}
	pages := body["top_pages"].([]any)
	if len(pages) != 2 || pages[0].(map[string]any)["page"] != "/docs" {
		t.Fatalf("top_pages=%#v", pages)
	}
	for _, field := range []string{
		"visitors_per_minute", "pageviews_last_30m", "top_entry_pages",
		"countries", "regions", "cities", "devices", "browsers",
		"operating_systems", "events", "recent_activity",
	} {
		if _, ok := body[field]; !ok {
			t.Fatalf("%s missing: %s", field, response.Body.String())
		}
	}
}

type realtimeLabels struct{}

func (realtimeLabels) Value(kind dict.Kind, id int32) (string, bool) {
	values := map[dict.Kind]map[int32]string{
		dict.Path:      {12: "/docs", 34: "/pricing"},
		dict.Source:    {56: "Google"},
		dict.Region:    {70: "California"},
		dict.City:      {71: "San Francisco"},
		dict.Browser:   {72: "Firefox"},
		dict.OS:        {73: "Linux"},
		dict.EventName: {74: "Signup"},
	}
	value, ok := values[kind][id]
	return value, ok
}

type richRealtimeStore struct{ *realtimeStore }

func (s richRealtimeStore) RealtimeSnapshot(_ int32, now time.Time) session.RealtimeSnapshot {
	minutes := make([]session.MinuteVisitors, 30)
	for i := range minutes {
		minutes[i] = session.MinuteVisitors{Minute: now.Add(time.Duration(i-29) * time.Minute), Visitors: i}
	}
	return session.RealtimeSnapshot{
		ActiveVisitors: 3, Pages: []session.ActiveValue{{ID: 12, Visitors: 3}}, Sources: []session.ActiveValue{{ID: 56, Visitors: 2}},
		EntryPages: []session.ActiveValue{{ID: 34, Visitors: 2}}, Countries: []session.ActiveCountry{{CountryCode: "US", Visitors: 3}},
		Regions: []session.ActiveLocation{{ID: 70, CountryCode: "US", Visitors: 2}}, Cities: []session.ActiveLocation{{ID: 71, CountryCode: "US", Visitors: 1}},
		Devices: []session.ActiveValue{{ID: 0, Visitors: 2}}, Browsers: []session.ActiveValue{{ID: 72, Visitors: 2}}, OperatingSystems: []session.ActiveValue{{ID: 73, Visitors: 2}},
		VisitorsPerMinute: minutes, Pageviews: 9, Events: []session.ActiveEvent{{NameID: 74, Count: 4}},
		RecentActivity: []session.RecentActivity{{Kind: 1, PathID: 12, NameID: 74, CountryCode: "US", CityID: 71, Device: 0, BrowserID: 72, SourceID: 56, Timestamp: now}},
	}
}

type apiMinuteSource struct {
	minutes []session.MinuteVisitors
}

func (s apiMinuteSource) Minutes(context.Context, int32, time.Time, time.Time) ([]session.MinuteVisitors, error) {
	return s.minutes, nil
}

type panicMinuteSource struct{}

func (panicMinuteSource) Minutes(context.Context, int32, time.Time, time.Time) ([]session.MinuteVisitors, error) {
	panic("realtime summary must not read durable minutes")
}

func TestRealtimeUsesConfiguredMinuteSource(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 45, 0, time.UTC)
	router := identifiedRouter(api.Deps{
		Config: config.Config{AdminAPIKey: "test-key"}, Query: queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) { return emptyQueryResponse(), nil }},
		RateLimiter: ingest.NewTokenBuckets(0, 1000), Sessions: session.NewMemory(1, nil),
		MinuteSource: apiMinuteSource{minutes: []session.MinuteVisitors{{Minute: time.Date(2026, 10, 8, 11, 59, 0, 0, time.UTC), Visitors: 6}}},
		Sites:        orgSites{1: sites.Site{ID: 1, OrgID: 1, PublicID: "tiny", TZ: time.UTC, Retention: db.Ret30}},
		Now:          func() time.Time { return now },
	})
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/realtime?site_id=1", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		VisitorsPerMinute []api.RealtimeMinute `json:"visitors_per_minute"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := body.VisitorsPerMinute[28].Visitors; got != 6 {
		t.Fatalf("stored visitors=%d, want 6", got)
	}
}

func TestRealtimeSummaryMatchesFullRealtimeWithoutDatabaseAccess(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	store := session.NewMemory(1, nil)
	for _, hit := range []session.Hit{
		{SiteID: 1, TS: now.Add(-time.Minute), LocalDay: now, VisitorID: 101, PathID: 12},
		{SiteID: 1, TS: now.Add(-10 * time.Minute), LocalDay: now, VisitorID: 202, PathID: 34},
		{SiteID: 1, TS: now.Add(-31 * time.Minute), LocalDay: now, VisitorID: 303, PathID: 56},
		{SiteID: 2, TS: now.Add(-time.Minute), LocalDay: now, VisitorID: 404, PathID: 78},
	} {
		store.Touch(hit)
	}
	router := identifiedRouter(api.Deps{
		Config: config.Config{AdminAPIKey: "test-key"}, Sessions: store,
		RateLimiter: ingest.NewTokenBuckets(0, 1000),
		Sites:       orgSites{1: sites.Site{ID: 1, OrgID: 1, PublicID: "tiny", TZ: time.UTC, Retention: db.Ret30}},
		Now:         func() time.Time { return now },
	})

	full := realtimeRequest(t, router, "/api/public/v1/realtime?site_id=1", "Bearer test-key")
	summary := realtimeRequest(t, router, "/api/public/v1/realtime/summary?site_id=1", "Bearer test-key")
	if full.Code != http.StatusOK || summary.Code != http.StatusOK {
		t.Fatalf("full status=%d body=%s; summary status=%d body=%s", full.Code, full.Body.String(), summary.Code, summary.Body.String())
	}
	var fullBody struct {
		ActiveVisitors  int64 `json:"active_visitors"`
		VisitorsLast30m int64 `json:"visitors_last_30m"`
	}
	var summaryBody map[string]int64
	if err := json.Unmarshal(full.Body.Bytes(), &fullBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(summary.Body.Bytes(), &summaryBody); err != nil {
		t.Fatal(err)
	}
	if len(summaryBody) != 2 || summaryBody["active_visitors"] != fullBody.ActiveVisitors || summaryBody["visitors_last_30m"] != fullBody.VisitorsLast30m {
		t.Fatalf("summary=%v full=%+v", summaryBody, fullBody)
	}
	if fullBody.ActiveVisitors != 1 || fullBody.VisitorsLast30m != 2 {
		t.Fatalf("full realtime counters=%+v, want 1 active and 2 in 30m", fullBody)
	}
	if got := summary.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}

	noDatabaseRouter := identifiedRouter(api.Deps{
		Config: config.Config{AdminAPIKey: "test-key"}, Sessions: store, MinuteSource: panicMinuteSource{},
		RateLimiter: ingest.NewTokenBuckets(0, 1000),
		Sites:       orgSites{1: sites.Site{ID: 1, OrgID: 1}},
		Now:         func() time.Time { return now },
	})
	withoutDatabase := realtimeRequest(t, noDatabaseRouter, "/api/public/v1/realtime/summary?site_id=1", "Bearer test-key")
	if withoutDatabase.Code != http.StatusOK {
		t.Fatalf("summary without database status=%d body=%s", withoutDatabase.Code, withoutDatabase.Body.String())
	}
}

func TestRealtimeSummaryAuthorizationMatchesFullRealtime(t *testing.T) {
	tests := []struct {
		name  string
		auth  api.Authenticator
		sites orgSites
		want  int
	}{
		{name: "unauthenticated", auth: api.StaticKey("test-key"), sites: orgSites{1: {ID: 1, OrgID: 1}}, want: http.StatusUnauthorized},
		{name: "missing stats read scope", auth: principalAuth{principal: api.Principal{OrgID: 1, Scopes: []string{"sites:read"}, SiteIDs: []int32{1}}}, sites: orgSites{1: {ID: 1, OrgID: 1}}, want: http.StatusForbidden},
		{name: "unknown site", auth: principalAuth{principal: api.Principal{OrgID: 1, Scopes: []string{"stats:read"}}}, sites: orgSites{}, want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := identifiedRouter(api.Deps{
				PublicAuth: test.auth, Sessions: session.NewMemory(1, nil), Sites: test.sites,
				RateLimiter: ingest.NewTokenBuckets(0, 1000),
			})
			authorization := ""
			if test.name != "unauthenticated" {
				authorization = "ignored"
			}
			full := realtimeRequest(t, router, "/api/public/v1/realtime?site_id=1", authorization)
			summary := realtimeRequest(t, router, "/api/public/v1/realtime/summary?site_id=1", authorization)
			if full.Code != test.want || summary.Code != full.Code || summary.Body.String() != full.Body.String() {
				t.Fatalf("full status=%d body=%s; summary status=%d body=%s", full.Code, full.Body.String(), summary.Code, summary.Body.String())
			}
		})
	}
}

func TestPrivateRealtimeSummaryUsesDashboardSessionAuthorization(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	store := session.NewMemory(1, nil)
	store.Touch(session.Hit{SiteID: 1, TS: now.Add(-time.Minute), LocalDay: now, VisitorID: 101, PathID: 12})
	router := identifiedRouter(api.Deps{
		PrivateAuth: principalAuth{principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}}},
		Sessions:    store,
		Sites:       orgSites{1: {ID: 1, OrgID: 7}},
		Now:         func() time.Time { return now },
	})

	full := realtimeRequest(t, router, "/api/private/realtime?site_id=1", "")
	summary := realtimeRequest(t, router, "/api/private/realtime/summary?site_id=1", "")
	if full.Code != http.StatusOK || summary.Code != http.StatusOK {
		t.Fatalf("full status=%d body=%s; summary status=%d body=%s", full.Code, full.Body.String(), summary.Code, summary.Body.String())
	}
	if !strings.Contains(summary.Body.String(), `"active_visitors":1`) || !strings.Contains(summary.Body.String(), `"visitors_last_30m":1`) {
		t.Fatalf("summary body=%s", summary.Body.String())
	}
}

func realtimeRequest(t *testing.T, router http.Handler, path, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestDataHealthShowsRollupBackfillProgress(t *testing.T) {
	router := identifiedRouter(api.Deps{
		Config:      config.Config{AdminAPIKey: "test-key"},
		PrivateAuth: api.StaticKey("test-key"),
		RollupBackfillProgress: func(context.Context) (bool, int, int, error) {
			return true, 37, 120, nil
		},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/private/health", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body api.DataHealthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RollupBackfill.Status != "running" || body.RollupBackfill.DaysDone != 37 || body.RollupBackfill.TotalDays != 120 {
		t.Fatalf("rollup backfill = %+v, want running 37/120", body.RollupBackfill)
	}
}

var _ realtime.MinuteSource = apiMinuteSource{}

func TestRealtimeResolvesRichSnapshotLabels(t *testing.T) {
	router := statsTestRouter(queryFunc{query: func(api.QueryRequest) (api.QueryResponse, error) { return emptyQueryResponse(), nil }}, richRealtimeStore{&realtimeStore{}})
	request := httptest.NewRequest(http.MethodGet, "/api/public/v1/realtime?site_id=1", nil)
	request.Header.Set("Authorization", "Bearer test-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		VisitorsPerMinute []api.RealtimeMinute `json:"visitors_per_minute"`
		Pageviews         int64                `json:"pageviews_last_30m"`
		Devices           []api.RealtimeLabel  `json:"devices"`
		Events            []api.RealtimeEvent  `json:"events"`
		Recent            []api.RecentActivity `json:"recent_activity"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.VisitorsPerMinute) != 30 || body.Pageviews != 9 || body.Devices[0].Label != "Desktop" || body.Events[0].Name != "Signup" {
		t.Fatalf("rich response=%+v body=%s", body, response.Body.String())
	}
	if len(body.Recent) != 1 || body.Recent[0].Kind != "custom" || body.Recent[0].City != "San Francisco" || body.Recent[0].EventName != "Signup" {
		t.Fatalf("recent=%+v", body.Recent)
	}
}

func TestRealtimeAuthorization(t *testing.T) {
	tests := []struct {
		name      string
		principal api.Principal
		want      int
	}{
		{name: "missing stats read scope", principal: api.Principal{OrgID: 1, Scopes: []string{"sites:read"}, SiteIDs: []int32{1}}, want: http.StatusForbidden},
		{name: "foreign site is hidden", principal: api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}, want: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := identifiedRouter(api.Deps{
				Config: config.Config{}, Query: queryFunc{}, Sessions: session.NewMemory(1, nil),
				PublicAuth: principalAuth{principal: test.principal}, RateLimiter: ingest.NewTokenBuckets(0, 1000),
				Sites: orgSites{1: sites.Site{ID: 1, OrgID: 1, PublicID: "tiny", TZ: time.UTC}},
			})
			request := httptest.NewRequest(http.MethodGet, "/api/public/v1/realtime?site_id=1", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertRecorderProblem(t, response, test.want)
		})
	}
}

func statsTestRouter(query queryFunc, store session.Store) http.Handler {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	return identifiedRouter(api.Deps{
		Config: config.Config{AdminAPIKey: "test-key"}, Query: query,
		RateLimiter: ingest.NewTokenBuckets(0, 1000), Sessions: store,
		Dictionary: realtimeLabels{},
		Sites:      orgSites{1: sites.Site{ID: 1, OrgID: 1, PublicID: "tiny", TZ: time.UTC, Retention: db.Ret30}},
		Now:        func() time.Time { return now },
	})
}

func emptyQueryResponse() api.QueryResponse {
	return api.QueryResponse{Columns: []string{"visitors"}, Rows: []map[string]any{}, Totals: map[string]float64{}, Meta: api.QueryMeta{}}
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func (queryFunc) Journey(context.Context, api.JourneyRequest) (api.JourneyResponse, error) {
	return api.JourneyResponse{}, nil
}

func (queryFunc) Retention(context.Context, api.RetentionRequest) (api.RetentionResponse, error) {
	return api.RetentionResponse{}, nil
}
