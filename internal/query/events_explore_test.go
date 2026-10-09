package query

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/sites"
	"panel/internal/testpg"
)

func TestEngine_EventsExploreNewestFirstWithFiltersAndCursor(t *testing.T) {
	pool, engine := setupEventsExplore(t)
	seedEventsExplore(t, pool)

	filters := []api.Filter{{Dimension: "country", Operator: api.Is, Values: []string{"US"}}}
	first, err := engine.Events(context.Background(), api.EventExploreRequest{
		SiteID: 1, From: exploreDay("2026-10-06"), To: exploreDay("2026-10-07"), Filters: filters, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 || first.Data[0].Name != "signup" || first.Data[0].Page != "/pricing" || first.Data[0].Country != "US" || first.Data[0].City != "Singapore" || first.Data[0].Device != "mobile" || first.Data[0].Browser != "Chrome" || first.Data[0].Os != "Linux" || first.Data[0].Source != "Google" {
		t.Fatalf("first page = %+v", first)
	}
	if first.Data[0].Props == nil || (*first.Data[0].Props)["plan"] != "pro" || first.NextCursor == nil {
		t.Fatalf("first page props/cursor = %+v", first)
	}
	if got := first.Data[0].SessionId; got != "20" {
		t.Fatalf("session_id = %q, want 20", got)
	}
	if first.Total != 2 || first.TotalCapped {
		t.Fatalf("filtered total=%d capped=%t, want 2/false", first.Total, first.TotalCapped)
	}

	second, err := engine.Events(context.Background(), api.EventExploreRequest{
		SiteID: 1, From: exploreDay("2026-10-06"), To: exploreDay("2026-10-07"), Filters: filters, Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].Kind != "pageview" || second.Data[0].Name != "pageview" || second.Data[0].Page != "/" || second.NextCursor != nil {
		t.Fatalf("second page = %+v", second)
	}
	if second.Total != first.Total || second.TotalCapped {
		t.Fatalf("second total=%d capped=%t, want %d/false", second.Total, second.TotalCapped, first.Total)
	}
}

func TestEngine_EventsExploreCursorIsStableWhenNewerEventsArrive(t *testing.T) {
	pool, engine := setupEventsExplore(t)
	seedEventsExplore(t, pool)
	request := api.EventExploreRequest{SiteID: 1, From: exploreDay("2026-10-06"), To: exploreDay("2026-10-07"), Limit: 1}

	first, err := engine.Events(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 || first.NextCursor == nil || first.Data[0].Country != "IN" {
		t.Fatalf("first page = %+v", first)
	}
	// A newer event lands between page requests; an offset cursor would now
	// repeat the first row instead of continuing after it.
	if _, err := pool.Exec(context.Background(), `insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,country)
values (1,30,'2026-10-07','2026-10-07 12:00Z',4,40,0,0,1,'DE')`); err != nil {
		t.Fatal(err)
	}
	request.Limit = 100
	request.Cursor = first.NextCursor
	rest, err := engine.Events(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest.Data) != 2 || rest.Data[0].Country != "US" || rest.Data[0].Name != "signup" || rest.Data[1].Kind != api.ExplorePageview || rest.NextCursor != nil {
		t.Fatalf("second page = %+v", rest.Data)
	}
}

func TestEngine_EventsExploreNameAndPropertyReports(t *testing.T) {
	pool, engine := setupEventsExplore(t)
	seedEventsExplore(t, pool)

	events, err := engine.Events(context.Background(), api.EventExploreRequest{
		SiteID: 1, From: exploreDay("2026-10-06"), To: exploreDay("2026-10-07"), Name: "signup", Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Data) != 2 || events.Data[0].Name != "signup" || events.Data[1].Name != "signup" {
		t.Fatalf("named events = %+v", events.Data)
	}
	if events.Total != 2 || events.TotalCapped {
		t.Fatalf("name-filtered total=%d capped=%t, want 2/false", events.Total, events.TotalCapped)
	}

	properties, err := engine.Properties(context.Background(), api.PropertyExploreRequest{
		SiteID: 1, From: exploreDay("2026-10-06"), To: exploreDay("2026-10-07"), Event: "signup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(properties) != 2 || properties[0].Key != "plan" || properties[0].Events != 2 || properties[1].Key != "tier" || properties[1].Events != 1 {
		t.Fatalf("properties = %+v", properties)
	}

	values, err := engine.PropertyValues(context.Background(), api.PropertyValueExploreRequest{
		SiteID: 1, Key: "plan", From: exploreDay("2026-10-06"), To: exploreDay("2026-10-07"), Event: "signup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Value != "pro" || values[0].Events != 2 {
		t.Fatalf("property values = %+v", values)
	}
}

func TestEngine_EventsExploreSuggestions(t *testing.T) {
	pool, engine := setupEventsExplore(t)
	seedEventsExplore(t, pool)

	tests := []struct {
		name, dimension, query, want string
	}{
		{name: "dictionary dimension", dimension: "page", query: "ric", want: "/pricing"},
		{name: "plain dimension", dimension: "country", query: "u", want: "US"},
		{name: "event", dimension: "event", query: "sign", want: "signup"},
		{name: "property", dimension: "prop:plan", query: "ro", want: "pro"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, err := engine.Suggestions(context.Background(), api.SuggestionExploreRequest{
				SiteID: 1, Dimension: tt.dimension, Query: tt.query, From: exploreDay("2026-10-06"), To: exploreDay("2026-10-07"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(values) != 1 || values[0].Value != tt.want || values[0].Count < 1 {
				t.Fatalf("suggestions = %+v, want %q", values, tt.want)
			}
		})
	}
}

func TestNormalizeEventsExploreRejectsInvalidInput(t *testing.T) {
	site := sites.Site{ID: 1, TZ: time.UTC, Retention: db.Ret7}
	allowed, err := normalizeEventExplore(api.EventExploreRequest{
		SiteID: 1, From: exploreDay("2026-10-07"), To: exploreDay("2026-10-07"), Limit: 10,
		Filters: []api.Filter{{Dimension: "utm_content", Operator: api.Is, Values: []string{"hero"}}},
	}, site, exploreDay("2026-10-08"))
	if err != nil || len(allowed.filters) != 1 {
		t.Fatalf("utm_content filter rejected: normalized=%+v err=%v", allowed, err)
	}
	tests := []struct {
		name string
		req  api.EventExploreRequest
		want string
	}{
		{name: "foreign site", req: api.EventExploreRequest{SiteID: 2, From: exploreDay("2026-10-07"), To: exploreDay("2026-10-07"), Limit: 10}, want: "site_id does not match"},
		{name: "reverse range", req: api.EventExploreRequest{SiteID: 1, From: exploreDay("2026-10-08"), To: exploreDay("2026-10-07"), Limit: 10}, want: "invalid date range"},
		{name: "outside retention", req: api.EventExploreRequest{SiteID: 1, From: exploreDay("2026-09-01"), To: exploreDay("2026-09-02"), Limit: 10}, want: "raw retention window"},
		{name: "limit", req: api.EventExploreRequest{SiteID: 1, From: exploreDay("2026-10-07"), To: exploreDay("2026-10-07"), Limit: 101}, want: "limit must be between 1 and 100"},
		{name: "cursor", req: api.EventExploreRequest{SiteID: 1, From: exploreDay("2026-10-07"), To: exploreDay("2026-10-07"), Limit: 10, Cursor: stringPointer("bad")}, want: "invalid cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeEventExplore(tt.req, site, exploreDay("2026-10-08"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("normalizeEventExplore() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestBuildEventsExploreSQLPrunesPartitionsAndCapsRows(t *testing.T) {
	req := normalizedEventExplore{
		siteID: 1, retention: db.Ret30, from: exploreDay("2026-10-06"), to: exploreDay("2026-10-07"),
		start: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), end: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), limit: 20,
	}
	querySQL, args, err := buildEventsExploreSQL(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"e.site_id=", "e.retention_class=", "e.local_day between", "e.ts>=", "e.ts<", "limit"} {
		if !strings.Contains(querySQL, fragment) {
			t.Errorf("SQL missing %q:\n%s", fragment, querySQL)
		}
	}
	if len(args) < 7 {
		t.Fatalf("args = %#v", args)
	}
}

func setupEventsExplore(t *testing.T) (*pgxpool.Pool, *Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, exploreDay("2026-10-01"), exploreDay("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	registry := vitalsRegistry{site: sites.Site{ID: 1, PublicID: "events", TZ: time.UTC, Retention: db.Ret30}}
	engine := NewEngine(pool, dictionary, registry)
	engine.now = func() time.Time { return exploreDay("2026-10-08") }
	return pool, engine
}

func seedEventsExplore(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
insert into dict(kind,id,value) values
  (0,1,'/'),(0,2,'/pricing'),(2,3,'Google'),(7,4,'signup'),(9,5,'Chrome'),(10,6,'Linux'),(12,7,'Singapore');
insert into events
  (site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,source_id,country,browser_id,os_id,device,city_id,props)
values
  (1,30,'2026-10-06','2026-10-06 09:00Z',1,10,0,0,1,3,'US',5,6,0,7,null),
  (1,30,'2026-10-07','2026-10-07 10:00Z',2,20,1,4,2,3,'US',5,6,1,7,'{"plan":"pro","tier":"team"}'),
  (1,30,'2026-10-07','2026-10-07 11:00Z',3,30,1,4,2,0,'IN',5,6,0,0,'{"plan":"pro"}')`)
	if err != nil {
		t.Fatal(err)
	}
}

func exploreDay(value string) time.Time {
	day, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return openapi_types.Date{Time: day}.Time
}

func stringPointer(value string) *string { return &value }
