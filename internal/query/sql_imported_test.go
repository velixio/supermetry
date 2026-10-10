package query_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/api"
	panelimports "supermetry/internal/imports"
)

func TestQueryIncludesOnlyCompletedImportedDaysBeforeNativeData(t *testing.T) {
	pool, engine := setupTiny(t)
	completed := insertImport(t, pool, "completed")
	pending := insertImport(t, pool, "pending")
	insertImportedDay(t, pool, completed, "2026-10-04", 10, 11, 12, 2, 100)
	insertImportedDay(t, pool, completed, "2026-10-05", 20, 21, 22, 3, 200)
	insertImportedDay(t, pool, completed, "2026-10-06", 1000, 1000, 1000, 1000, 1000)
	insertImportedDay(t, pool, pending, "2026-10-03", 1000, 1000, 1000, 1000, 1000)

	req := queryRequestRange("2026-10-03", "2026-10-07", nil, nil)
	got := mustQuery(t, engine, req)
	want := map[string]float64{
		"visitors":        34,
		"visits":          37,
		"pageviews":       42,
		"bounce_rate":     100 * 7.0 / 37.0,
		"visit_duration":  660.0 / 37.0,
		"engagement_time": 30,
	}
	for metric, expected := range want {
		if actual := got.Totals[metric]; math.Abs(actual-expected) > 1e-9 {
			t.Errorf("totals[%q] = %v, want %v", metric, actual, expected)
		}
	}
	if !got.Meta.ImportedIncluded {
		t.Fatal("meta.imported_included = false, want true")
	}
	if !containsString(got.Meta.SourcesUsed, "imported") {
		t.Fatalf("sources = %v, want imported", got.Meta.SourcesUsed)
	}
}

func TestQueryImportOverlapUsesNewestCompletedImportPerDay(t *testing.T) {
	pool, engine := setupTiny(t)
	older := insertImport(t, pool, "completed")
	newer := insertImport(t, pool, "completed")
	insertImportedDay(t, pool, older, "2026-10-05", 10, 11, 12, 2, 100)
	insertImportedDay(t, pool, newer, "2026-10-05", 20, 21, 22, 3, 200)
	if _, err := pool.Exec(context.Background(), `
update imports set completed_at=case id when $1 then '2026-10-05 10:00Z'::timestamptz else '2026-10-05 11:00Z'::timestamptz end
where id in ($1,$2)`, older, newer); err != nil {
		t.Fatal(err)
	}

	got := mustQuery(t, engine, queryRequestRange("2026-10-05", "2026-10-05", nil, nil))
	if got.Totals["visitors"] != 20 || got.Totals["visits"] != 21 || got.Totals["pageviews"] != 22 {
		t.Fatalf("totals = %v, want only the newer Plausible export", got.Totals)
	}
	if !got.Meta.ImportedIncluded {
		t.Fatal("meta.imported_included = false, want true")
	}
}

func TestQueryCutsImportsOffAtEarlierNativeEvent(t *testing.T) {
	pool, engine := setupTiny(t)
	ensureSiteRowForImport(t, pool)
	_, err := pool.Exec(context.Background(), `
insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind, path_id)
values
  (1, 30, '2026-10-05', '2026-10-05 12:00Z', 999, 999, 0, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	importID := insertImport(t, pool, "completed")
	insertImportedDay(t, pool, importID, "2026-10-04", 10, 10, 10, 0, 0)
	insertImportedDay(t, pool, importID, "2026-10-05", 1000, 1000, 1000, 0, 0)

	got := mustQuery(t, engine, queryRequestRange("2026-10-04", "2026-10-07", nil, nil))
	if got.Totals["visitors"] != 14 {
		t.Fatalf("visitors = %v, want ten imported before the event plus four native", got.Totals["visitors"])
	}
}

func TestQueryImportSkipsOnlyDaysWithNativeTracking(t *testing.T) {
	pool, engine := setupTiny(t)
	ensureSiteRowForImport(t, pool)
	if _, err := pool.Exec(context.Background(), `
insert into events
  (site_id, retention_class, local_day, ts, visitor_id, session_id, kind, path_id)
values
  (1, 30, '2026-10-05', '2026-10-05 12:00Z', 999, 999, 0, 1)`); err != nil {
		t.Fatal(err)
	}
	importID := insertImport(t, pool, "completed")
	insertImportedDay(t, pool, importID, "2026-10-05", 1000, 1000, 1000, 0, 0)
	insertImportedDay(t, pool, importID, "2026-10-09", 10, 10, 10, 0, 0)

	got := mustQuery(t, engine, queryRequestRange("2026-10-05", "2026-10-09", []string{"time"}, nil))
	rows := rowsByDimension(got, "time")
	if row := rows["2026-10-05"]; row != nil {
		t.Fatalf("native event-only day = %v, want no imported aggregate row", row)
	}
	if rows["2026-10-09"]["visitors"] != float64(10) {
		t.Fatalf("later import-only day = %v, want visitors=10", rows["2026-10-09"])
	}
	if !got.Meta.ImportedIncluded {
		t.Fatal("meta.imported_included = false, want true for the non-overlapping day")
	}
}

func TestQueryImportFlagFalseWhenEveryImportedDayHasNativeTracking(t *testing.T) {
	pool, engine := setupTiny(t)
	importID := insertImport(t, pool, "completed")
	insertImportedDay(t, pool, importID, "2026-10-06", 1000, 1000, 1000, 0, 0)

	got := mustQuery(t, engine, queryRequestRange("2026-10-06", "2026-10-06", nil, nil))
	if got.Meta.ImportedIncluded || containsString(got.Meta.SourcesUsed, "imported") {
		t.Fatalf("meta = %+v, want imported data excluded because native rows cover the day", got.Meta)
	}
}

func TestQueryCanExcludeImportedData(t *testing.T) {
	pool, engine := setupTiny(t)
	importID := insertImport(t, pool, "completed")
	insertImportedDay(t, pool, importID, "2026-10-05", 20, 21, 22, 3, 200)

	req := queryRequestRange("2026-10-05", "2026-10-07", nil, nil)
	disabled := false
	req.IncludeImported = &disabled
	got := mustQuery(t, engine, req)
	if got.Totals["visitors"] != 4 || got.Totals["visits"] != 5 || got.Totals["pageviews"] != 8 {
		t.Fatalf("totals = %v, want native-only visitors=4 visits=5 pageviews=8", got.Totals)
	}
	if got.Meta.ImportedIncluded || containsString(got.Meta.SourcesUsed, "imported") {
		t.Fatalf("meta = %+v, want no imported data", got.Meta)
	}
}

func TestQueryIncludesImportedTimeSeries(t *testing.T) {
	pool, engine := setupTiny(t)
	importID := insertImport(t, pool, "completed")
	insertImportedDay(t, pool, importID, "2026-10-05", 20, 21, 22, 3, 200)
	insertImportedDay(t, pool, importID, "2026-10-06", 1000, 1000, 1000, 1000, 1000)

	got := mustQuery(t, engine, queryRequestRange("2026-10-05", "2026-10-07", []string{"time"}, nil))
	rows := rowsByDimension(got, "time")
	if rows["2026-10-05"]["visitors"] != float64(20) {
		t.Fatalf("imported time row = %v, want visitors=20", rows["2026-10-05"])
	}
	if rows["2026-10-06"]["visitors"] != float64(2) {
		t.Fatalf("first native day row = %v, want native visitors=2", rows["2026-10-06"])
	}
}

func TestQueryIncludesImportedDimensionAggregates(t *testing.T) {
	pool, engine := setupTiny(t)
	importID := insertImport(t, pool, "completed")
	dimensions := []string{"page", "source", "country", "browser", "os", "device", "entry", "exit", "region", "city"}
	for code, dimension := range dimensions {
		value := "imported-" + dimension
		insertImportedDimension(t, pool, importID, "2026-10-05", int16(code+1), value, 10, 11, 12, 2, 100, 13)
		insertImportedDimension(t, pool, importID, "2026-10-06", int16(code+1), value, 1000, 1000, 1000, 1000, 1000, 1000)
	}

	for _, dimension := range dimensions {
		t.Run(dimension, func(t *testing.T) {
			req := queryRequestRange("2026-10-05", "2026-10-07", []string{dimension}, nil)
			req.Metrics = append(req.Metrics, api.Events)
			got := mustQuery(t, engine, req)
			row := rowsByDimension(got, dimension)["imported-"+dimension]
			if row == nil {
				t.Fatalf("rows = %v, want imported dimension", got.Rows)
			}
			if row["visitors"] != float64(10) || row["events"] != float64(13) {
				t.Fatalf("imported row = %v, want visitors=10 events=13", row)
			}
			if row["engagement_time"] != float64(0) {
				t.Fatalf("imported engagement_time = %v, want native-only zero", row["engagement_time"])
			}
		})
	}
}

func TestQueryImportCustomEventsUseEventNotPageDimension(t *testing.T) {
	pool, engine := setupTiny(t)
	importID := insertImport(t, pool, "completed")
	insertImportedDimension(t, pool, importID, "2026-10-05", 1, panelimports.EventValuePrefix+"Signup", 2, 0, 0, 0, 0, 7)

	eventRequest := queryRequestRange("2026-10-05", "2026-10-05", []string{"event"}, nil)
	eventRequest.Metrics = []api.Metric{api.Visitors, api.Events}
	events := mustQuery(t, engine, eventRequest)
	event := rowsByDimension(events, "event")["Signup"]
	if event == nil || event["visitors"] != float64(2) || event["events"] != float64(7) {
		t.Fatalf("event row = %v, want Signup visitors=2 events=7", event)
	}

	pageRequest := queryRequestRange("2026-10-05", "2026-10-05", []string{"page"}, nil)
	pageRequest.Metrics = []api.Metric{api.Events}
	pages := mustQuery(t, engine, pageRequest)
	if row := rowsByDimension(pages, "page")[panelimports.EventValuePrefix+"Signup"]; row != nil {
		t.Fatalf("custom event leaked into page rows: %v", row)
	}
}

func TestQueryImportCustomEventsApproximateGoalConversions(t *testing.T) {
	pool, engine := setupTiny(t)
	importID := insertImport(t, pool, "completed")
	insertImportedDimension(t, pool, importID, "2026-10-05", 1, panelimports.EventValuePrefix+"Signup", 2, 0, 0, 0, 0, 7)
	const goalID int64 = 42
	engine.SetGoalLookup(goalLookup{1: {{ID: goalID, SiteID: 1, Kind: "event", Match: "exact", Value: "Signup", Name: "signup"}}})
	dimensions := []string{"goal"}
	request := api.QueryRequest{
		SiteId: 1, DateRange: ptr(dateRange("2026-10-05", "2026-10-05")),
		Dimensions: &dimensions, Metrics: []api.Metric{api.Conversions},
	}

	got := mustQuery(t, engine, request)
	row := rowsByDimension(got, "goal")[fmt.Sprint(goalID)]
	if row == nil || row["conversions"] != float64(7) {
		t.Fatalf("goal row = %v, want seven imported event occurrences as approximate conversions", row)
	}
	if got.Totals["conversions"] != float64(7) {
		t.Fatalf("goal totals = %v, want seven approximate conversions", got.Totals)
	}
	if !got.Meta.ImportedIncluded {
		t.Fatal("meta.imported_included = false, want true")
	}
}

func TestQueryDoesNotAddImportedRowsForUnsupportedShape(t *testing.T) {
	pool, engine := setupTiny(t)
	importID := insertImport(t, pool, "completed")
	insertImportedDimension(t, pool, importID, "2026-10-05", 3, "imported-country", 10, 11, 12, 2, 100, 13)

	req := queryRequestRange("2026-10-05", "2026-10-07", []string{"country"}, []api.Filter{filter("country", api.Is, "imported-country")})
	got := mustQuery(t, engine, req)
	if got.Meta.ImportedIncluded {
		t.Fatalf("meta = %+v, aggregate imports cannot answer filtered queries exactly", got.Meta)
	}
}

func insertImport(t *testing.T, pool *pgxpool.Pool, status string) int64 {
	t.Helper()
	ensureSiteRowForImport(t, pool)
	var id int64
	err := pool.QueryRow(context.Background(), `
insert into imports (site_id, source, status, file_name)
values (1, 'plausible', $1, 'fixture.zip')
returning id`, status).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ensureSiteRowForImport(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
insert into orgs (id, name, slug) overriding system value
values (1, 'Query test', 'query-test')
on conflict (id) do nothing;
insert into sites (id, org_id, public_id, name, timezone, retention_class) overriding system value
values (1, 1, 'querytest001', 'Query test', 'UTC', 30)
on conflict (id) do nothing`)
	if err != nil {
		t.Fatal(err)
	}
}

func insertImportedDay(t *testing.T, pool *pgxpool.Pool, importID int64, day string, visitors, visits, pageviews, bounces, durationSeconds int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
insert into imported_day (site_id, import_id, day, visitors, visits, pageviews, bounces, visit_duration_s)
values (1, $1, $2::date, $3, $4, $5, $6, $7)`, importID, day, visitors, visits, pageviews, bounces, durationSeconds)
	if err != nil {
		t.Fatal(err)
	}
}

func insertImportedDimension(t *testing.T, pool *pgxpool.Pool, importID int64, day string, dim int16, value string, visitors, visits, pageviews, bounces, durationSeconds, events int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
insert into imported_day_dim (site_id, import_id, day, dim, value, visitors, visits, pageviews, bounces, visit_duration_s, events)
values (1, $1, $2::date, $3, $4, $5, $6, $7, $8, $9, $10)`, importID, day, dim, value, visitors, visits, pageviews, bounces, durationSeconds, events)
	if err != nil {
		t.Fatal(err)
	}
}

func rowsByDimension(response api.QueryResponse, dimension string) map[string]map[string]interface{} {
	rows := make(map[string]map[string]interface{}, len(response.Rows))
	for _, row := range response.Rows {
		value, _ := row[dimension].(string)
		rows[value] = row
	}
	return rows
}
