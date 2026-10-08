package query

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/sites"
	"panel/internal/testpg"
)

func TestBuildJourneyCollapsesDuplicatesBucketsOtherAndCountsExits(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	visits := []journeyVisit{
		{day: day, visitorID: 1, paths: []string{"/", "/", "/pricing"}},
		{day: day, visitorID: 2, paths: []string{"/", "/docs"}},
		{day: day, visitorID: 3, paths: []string{"/", "/about", "/pricing"}},
	}

	got := buildJourney(visits, journeyOptions{steps: 3, limit: 1})
	wantNodes := []api.JourneyNode{
		{Step: 1, Path: "/", Visitors: 3},
		{Step: 2, Path: journeyOther, Visitors: 2},
		{Step: 2, Path: "/about", Visitors: 1},
		{Step: 3, Path: journeyExit, Visitors: 2},
		{Step: 3, Path: "/pricing", Visitors: 1},
	}
	wantLinks := []api.JourneyLink{
		{FromStep: 1, FromPath: "/", ToPath: journeyOther, Visitors: 2},
		{FromStep: 1, FromPath: "/", ToPath: "/about", Visitors: 1},
		{FromStep: 2, FromPath: journeyOther, ToPath: journeyExit, Visitors: 2},
		{FromStep: 2, FromPath: "/about", ToPath: "/pricing", Visitors: 1},
	}
	if !reflect.DeepEqual(got.Nodes, wantNodes) {
		t.Fatalf("nodes = %#v, want %#v", got.Nodes, wantNodes)
	}
	if !reflect.DeepEqual(got.Links, wantLinks) {
		t.Fatalf("links = %#v, want %#v", got.Links, wantLinks)
	}
}

func TestBuildJourneyAnchorsAtStartOrEndPath(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	visits := []journeyVisit{
		{day: day, visitorID: 1, paths: []string{"/", "/docs", "/pricing", "/thanks"}},
		{day: day, visitorID: 2, paths: []string{"/pricing", "/pricing", "/thanks"}},
		{day: day, visitorID: 3, paths: []string{"/", "/docs"}},
	}
	start := "/pricing"
	end := "/thanks"

	started := buildJourney(visits, journeyOptions{steps: 2, limit: 10, startPath: &start})
	wantStarted := []api.JourneyNode{{Step: 1, Path: "/pricing", Visitors: 2}, {Step: 2, Path: "/thanks", Visitors: 2}}
	if !reflect.DeepEqual(started.Nodes, wantStarted) {
		t.Fatalf("start nodes = %#v, want %#v", started.Nodes, wantStarted)
	}

	ended := buildJourney(visits, journeyOptions{steps: 3, limit: 10, endPath: &end})
	wantEnded := []api.JourneyNode{
		{Step: 1, Path: "/docs", Visitors: 1},
		{Step: 2, Path: "/pricing", Visitors: 2},
		{Step: 3, Path: "/thanks", Visitors: 2},
	}
	if !reflect.DeepEqual(ended.Nodes, wantEnded) {
		t.Fatalf("end nodes = %#v, want %#v", ended.Nodes, wantEnded)
	}
}

func TestBuildJourneySQLUsesOrderedWindowAndRawEvents(t *testing.T) {
	t.Parallel()
	querySQL, _, err := buildJourneySQL(api.JourneyRequest{
		SiteId: 1,
		DateRange: api.DateRange{
			From: types.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
			To:   types.Date{Time: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)},
		},
		Steps: 3,
		Limit: 10,
	}, 30, time.UTC, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"join events e", "e.kind=0", "lag(path_id)", "partition by e.site_id, e.session_id", "order by e.ts", "e.retention_class", "s.last <", "when ended", "raw_nodes", "page_ranks", "links"} {
		if !strings.Contains(strings.ToLower(querySQL), fragment) {
			t.Errorf("SQL missing %q:\n%s", fragment, querySQL)
		}
	}
}

func TestBuildJourneySQLPlans(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	request := api.JourneyRequest{
		SiteId:    1,
		DateRange: api.DateRange{From: types.Date{Time: dateForTest("2026-10-01")}, To: types.Date{Time: dateForTest("2026-10-08")}},
		Steps:     3, Limit: 10,
	}
	filters := []api.Filter{{Dimension: "page", Operator: api.Contains, Values: []string{"docs"}}, {Dimension: "event", Operator: api.Is, Values: []string{"signup"}}}
	request.Filters = &filters
	querySQL, args, err := buildJourneySQL(request, db.Ret30, time.UTC, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "explain "+querySQL, args...); err != nil {
		t.Fatalf("plan journey SQL: %v\n%s", err, querySQL)
	}
}

func TestValidateJourneyRequestEnforcesRawRetention(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	request := api.JourneyRequest{
		SiteId: 1,
		DateRange: api.DateRange{
			From: types.Date{Time: dateForTest("2026-09-08")},
			To:   types.Date{Time: dateForTest("2026-10-08")},
		},
		Steps: 3, Limit: 10,
	}
	err := validateJourneyRequest(request, sites.Site{ID: 1, TZ: time.UTC, Retention: db.Ret30}, now)
	if err == nil || !strings.Contains(err.Error(), "raw retention window") {
		t.Fatalf("error = %v, want raw retention window error", err)
	}
	if err := validateJourneyRequest(request, sites.Site{ID: 1, TZ: time.UTC, Retention: db.RetForever}, now); err != nil {
		t.Fatalf("forever retention rejected: %v", err)
	}
}
