package query

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

type trafficRegistry map[int32]sites.Site

func (r trafficRegistry) ByPublicID(id string) (sites.Site, bool) {
	for _, site := range r {
		if site.PublicID == id {
			return site, true
		}
	}
	return sites.Site{}, false
}

func (r trafficRegistry) Site(id int32) (sites.Site, bool) {
	site, ok := r[id]
	return site, ok
}

func (r trafficRegistry) All() []sites.Site {
	result := make([]sites.Site, 0, len(r))
	for _, site := range r {
		result = append(result, site)
	}
	return result
}

func TestTrafficDimensionsUseSiteTimezone(t *testing.T) {
	_, engine := setupTrafficFixture(t)
	tests := []struct {
		name        string
		siteID      int32
		from        string
		to          string
		wantWeekday string
		wantHour    string
	}{
		{name: "UTC", siteID: 1, from: "2026-10-10", to: "2026-10-10", wantWeekday: "6", wantHour: "18"},
		{name: "Asia Kolkata", siteID: 2, from: "2026-10-10", to: "2026-10-10", wantWeekday: "6", wantHour: "23"},
		{name: "Asia Kathmandu", siteID: 3, from: "2026-10-11", to: "2026-10-11", wantWeekday: "0", wantHour: "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := engine.Query(context.Background(), trafficRequest(tt.siteID, tt.from, tt.to, nil))
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Rows) != 1 || response.Rows[0]["weekday"] != tt.wantWeekday || response.Rows[0]["hour_of_day"] != tt.wantHour {
				t.Fatalf("rows=%v, want weekday=%s hour=%s", response.Rows, tt.wantWeekday, tt.wantHour)
			}
		})
	}
}

func TestTrafficDimensionsHandleDSTTransitions(t *testing.T) {
	_, engine := setupTrafficFixture(t)
	tests := []struct {
		name string
		from string
		want map[string][2]float64
	}{
		{
			name: "spring forward omits hour two", from: "2026-03-08",
			want: map[string][2]float64{"0/1": {1, 1}, "0/3": {1, 1}},
		},
		{
			name: "fall back combines repeated hour once", from: "2026-11-01",
			want: map[string][2]float64{"0/1": {2, 2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := engine.Query(context.Background(), trafficRequest(4, tt.from, tt.from, nil))
			if err != nil {
				t.Fatal(err)
			}
			if got := trafficCells(response); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("cells=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestTrafficDimensionsCountDistinctVisitorsAndApplyFilters(t *testing.T) {
	_, engine := setupTrafficFixture(t)
	tests := []struct {
		name    string
		filters []api.Filter
		want    map[string][2]float64
	}{
		{name: "all countries", want: map[string][2]float64{"6/10": {3, 14}, "0/10": {1, 1}}},
		{name: "country India", filters: []api.Filter{{Dimension: "country", Operator: api.Is, Values: []string{"IN"}}}, want: map[string][2]float64{"6/10": {2, 9}, "0/10": {1, 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := engine.Query(context.Background(), trafficRequest(5, "2026-10-10", "2026-10-11", tt.filters))
			if err != nil {
				t.Fatal(err)
			}
			if got := trafficCells(response); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("cells=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestTrafficDimensionsReturnNoRowsForEmptySite(t *testing.T) {
	_, engine := setupTrafficFixture(t)
	response, err := engine.Query(context.Background(), trafficRequest(6, "2026-10-10", "2026-10-11", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Rows) != 0 {
		t.Fatalf("rows=%v, want none", response.Rows)
	}
}

func TestTrafficDimensionsEnforceRawRetentionWindow(t *testing.T) {
	_, engine := setupTrafficFixture(t)
	engine.now = func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }

	if _, err := engine.Query(context.Background(), trafficRequest(7, "2026-10-01", "2026-10-30", nil)); err != nil {
		t.Fatalf("30-day range inside retention returned %v", err)
	}
	_, err := engine.Query(context.Background(), trafficRequest(7, "2026-09-30", "2026-10-29", nil))
	const want = "Traffic breakdowns are available only within the site's 30-day raw retention window"
	if err == nil || err.Error() != want {
		t.Fatalf("error=%v, want %q", err, want)
	}
}

func setupTrafficFixture(t *testing.T) (*pgxpool.Pool, *Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, dates := range [][2]time.Time{
		{time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
		{time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)},
	} {
		if err := db.EnsurePartitions(ctx, pool, dates[0], dates[1]); err != nil {
			t.Fatal(err)
		}
	}
	_, err := pool.Exec(ctx, `
insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,country,pageviews)
values
  (1,0,1,1,'2026-10-10 18:20Z','2026-10-10 18:20Z','2026-10-10','IN',1),
  (2,0,1,1,'2026-10-10 18:20Z','2026-10-10 18:20Z','2026-10-10','IN',1),
  (3,0,1,1,'2026-10-10 18:20Z','2026-10-10 18:20Z','2026-10-11','IN',1),
  (4,0,1,101,'2026-03-08 06:30Z','2026-03-08 06:30Z','2026-03-08','US',1),
  (4,0,2,102,'2026-03-08 07:30Z','2026-03-08 07:30Z','2026-03-08','US',1),
  (4,0,3,103,'2026-11-01 05:30Z','2026-11-01 05:30Z','2026-11-01','US',1),
  (4,0,4,104,'2026-11-01 06:30Z','2026-11-01 06:30Z','2026-11-01','US',1),
  (5,0,1,10,'2026-10-10 10:00Z','2026-10-10 10:00Z','2026-10-10','IN',2),
  (5,0,2,10,'2026-10-10 10:30Z','2026-10-10 10:30Z','2026-10-10','IN',3),
  (5,0,3,11,'2026-10-10 10:45Z','2026-10-10 10:45Z','2026-10-10','IN',4),
  (5,0,4,12,'2026-10-10 10:50Z','2026-10-10 10:50Z','2026-10-10','US',5),
  (5,0,5,13,'2026-10-11 10:00Z','2026-10-11 10:00Z','2026-10-11','IN',1)`)
	if err != nil {
		t.Fatal(err)
	}
	utc := time.UTC
	kolkata := mustLocation(t, "Asia/Kolkata")
	kathmandu := mustLocation(t, "Asia/Kathmandu")
	newYork := mustLocation(t, "America/New_York")
	registry := trafficRegistry{
		1: {ID: 1, PublicID: "trafficutc01", TZ: utc, Retention: db.RetForever},
		2: {ID: 2, PublicID: "traffickol02", TZ: kolkata, Retention: db.RetForever},
		3: {ID: 3, PublicID: "trafficktm03", TZ: kathmandu, Retention: db.RetForever},
		4: {ID: 4, PublicID: "trafficnyc04", TZ: newYork, Retention: db.RetForever},
		5: {ID: 5, PublicID: "trafficmet05", TZ: utc, Retention: db.RetForever},
		6: {ID: 6, PublicID: "trafficnil06", TZ: utc, Retention: db.RetForever},
		7: {ID: 7, PublicID: "trafficret07", TZ: utc, Retention: db.Ret30},
	}
	engine := identifiedEngine(pool, nil, registry)
	engine.now = func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }
	return pool, engine
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func trafficRequest(siteID int32, from, to string, filters []api.Filter) api.QueryRequest {
	fromDay, _ := time.Parse(time.DateOnly, from)
	toDay, _ := time.Parse(time.DateOnly, to)
	dimensions := []string{"weekday", "hour_of_day"}
	limit := 168
	return api.QueryRequest{
		SiteId: siteID,
		DateRange: &api.DateRange{
			From: openapi_types.Date{Time: fromDay},
			To:   openapi_types.Date{Time: toDay},
		},
		Metrics:    []api.Metric{api.Visitors, api.Pageviews},
		Dimensions: &dimensions,
		Filters:    &filters,
		Limit:      &limit,
	}
}

func trafficCells(response api.QueryResponse) map[string][2]float64 {
	result := make(map[string][2]float64, len(response.Rows))
	for _, row := range response.Rows {
		key := fmt.Sprintf("%s/%s", row["weekday"], row["hour_of_day"])
		result[key] = [2]float64{row["visitors"].(float64), row["pageviews"].(float64)}
	}
	return result
}

func TestBuildHotSQLUsesBoundSiteTimezoneForTrafficDimensions(t *testing.T) {
	querySQL, args, err := buildHotSQL(Normalized{
		SiteID: 1, From: testDate("2026-10-10"), To: testDate("2026-10-10"), PGZone: "Asia/Kathmandu",
		Dimensions: []string{"weekday", "hour_of_day"}, Metrics: []api.Metric{api.Visitors, api.Pageviews},
	}, Part{Route: RouteHot, From: testDate("2026-10-10"), To: testDate("2026-10-10")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(querySQL, "extract(dow") || !strings.Contains(querySQL, "extract(hour") {
		t.Fatalf("traffic dimension SQL missing local date parts:\n%s", querySQL)
	}
	found := 0
	for _, arg := range args {
		if arg == "Asia/Kathmandu" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("timezone bindings=%d args=%v, want one per dimension", found, args)
	}
	customSQL, customArgs, err := buildCustomHotSQL(Normalized{
		SiteID: 1, From: testDate("2026-10-10"), To: testDate("2026-10-10"), PGZone: "Asia/Kathmandu",
		Dimensions: []string{"event", "weekday", "hour_of_day"}, Metrics: []api.Metric{api.Events},
	}, Part{Route: RouteHot, From: testDate("2026-10-10"), To: testDate("2026-10-10")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(customSQL, "extract(dow from e.ts") || !strings.Contains(customSQL, "extract(hour from e.ts") {
		t.Fatalf("custom-event traffic dimensions do not use event time:\n%s", customSQL)
	}
	found = 0
	for _, arg := range customArgs {
		if arg == "Asia/Kathmandu" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("custom-event timezone bindings=%d args=%v, want one per dimension", found, customArgs)
	}
}
