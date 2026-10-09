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

func TestBuildJourneySQLRoutesClosedHotAndMixedRanges(t *testing.T) {
	t.Parallel()
	request := api.JourneyRequest{
		SiteId: 1,
		DateRange: api.DateRange{
			From: types.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
			To:   types.Date{Time: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)},
		},
		Steps: 3,
		Limit: 10,
	}
	for _, tc := range []struct {
		name          string
		closedThrough time.Time
		wantClosed    bool
		wantHot       bool
	}{
		{name: "closed", closedThrough: dateForTest("2026-10-08"), wantClosed: true},
		{name: "hot", closedThrough: dateForTest("2026-09-30"), wantHot: true},
		{name: "mixed", closedThrough: dateForTest("2026-10-04"), wantClosed: true, wantHot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			querySQL, _, err := buildJourneySQL(request, db.Ret30, time.UTC, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), tc.closedThrough)
			if err != nil {
				t.Fatal(err)
			}
			lower := strings.ToLower(querySQL)
			if got := strings.Contains(lower, "from sessions_closed s"); got != tc.wantClosed {
				t.Errorf("closed source present = %t, want %t:\n%s", got, tc.wantClosed, querySQL)
			}
			if got := strings.Contains(lower, "join events e"); got != tc.wantHot {
				t.Errorf("hot source present = %t, want %t:\n%s", got, tc.wantHot, querySQL)
			}
			for _, fragment := range []string{"raw_nodes", "page_ranks", "links"} {
				if !strings.Contains(lower, fragment) {
					t.Errorf("SQL missing %q:\n%s", fragment, querySQL)
				}
			}
			if tc.wantClosed && !strings.Contains(lower, "unnest(s.seq) with ordinality") {
				t.Errorf("closed SQL does not preserve sequence order:\n%s", querySQL)
			}
			if tc.wantHot {
				for _, fragment := range []string{"e.kind=0", "lag(path_id)", "partition by e.site_id,e.session_id", "order by e.ts,e.path_id desc", "e.retention_class", "s.last <"} {
					if !strings.Contains(lower, fragment) {
						t.Errorf("hot SQL missing %q:\n%s", fragment, querySQL)
					}
				}
			}
		})
	}
}

func TestJourneyCombinesClosedAndHotSequences(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, dateForTest("2026-09-01"), dateForTest("2026-10-09")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into dict(kind,id,value) values (0,1,'/closed'),(0,2,'/done'),(0,3,'/hot');
insert into sessions_closed
 (site_id,day,visitor_id,session_id,entry_path,exit_path,pageviews,custom,duration_ms,engagement_ms,vd_n,flags,seq,seq_engage,seq_scroll)
values (1,'2026-09-01',11,101,1,2,3,0,1000,0,1,1,array[5,6,10],array[0,0,0],array[0,0,0]);
insert into day_close(site_id,day,status,version) values (1,'2026-09-01','done',1);
insert into sessions
 (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,pageviews,bounce)
values (1,30,102,12,'2026-10-08 12:00Z','2026-10-08 12:01Z','2026-10-08',3,2,2,false);
insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id)
values (1,30,'2026-10-08','2026-10-08 12:00Z',12,102,0,3),
       (1,30,'2026-10-08','2026-10-08 12:00Z',12,102,0,2)`); err != nil {
		t.Fatal(err)
	}
	engine := identifiedEngine(pool, nil, vitalsRegistry{site: sites.Site{ID: 1, TZ: time.UTC, Retention: db.Ret30}})
	engine.now = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
	response, err := engine.Journey(ctx, api.JourneyRequest{
		SiteId:    1,
		DateRange: api.DateRange{From: types.Date{Time: dateForTest("2026-09-01")}, To: types.Date{Time: dateForTest("2026-10-08")}},
		Steps:     2,
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantNodes := []api.JourneyNode{
		{Step: 1, Path: "/closed", Visitors: 1},
		{Step: 1, Path: "/hot", Visitors: 1},
		{Step: 2, Path: "/done", Visitors: 2},
	}
	if !reflect.DeepEqual(response.Nodes, wantNodes) {
		t.Fatalf("nodes = %#v, want %#v", response.Nodes, wantNodes)
	}
	if len(response.Links) != 2 {
		t.Fatalf("links = %#v, want two closed/hot links", response.Links)
	}
	for _, link := range response.Links {
		if link.ToPath != "/done" || link.Visitors != 1 {
			t.Fatalf("link = %#v, want one visitor ending at /done", link)
		}
	}
	filters := []api.Filter{{Dimension: "page", Operator: api.Is, Values: []string{"/closed"}}}
	filtered, err := engine.Journey(ctx, api.JourneyRequest{
		SiteId:    1,
		DateRange: api.DateRange{From: types.Date{Time: dateForTest("2026-09-01")}, To: types.Date{Time: dateForTest("2026-10-08")}},
		Filters:   &filters,
		Steps:     2,
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantFiltered := []api.JourneyNode{{Step: 1, Path: "/closed", Visitors: 1}, {Step: 2, Path: "/done", Visitors: 1}}
	if !reflect.DeepEqual(filtered.Nodes, wantFiltered) {
		t.Fatalf("filtered nodes = %#v, want %#v", filtered.Nodes, wantFiltered)
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
	querySQL, args, err := buildJourneySQL(request, db.Ret30, time.UTC, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "explain "+querySQL, args...); err != nil {
		t.Fatalf("plan journey SQL: %v\n%s", err, querySQL)
	}
}

func TestValidateJourneyRequestAllowsClosedHistory(t *testing.T) {
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
	if err := validateJourneyRequest(request, sites.Site{ID: 1, TZ: time.UTC, Retention: db.Ret30}, now); err != nil {
		t.Fatalf("closed history rejected: %v", err)
	}
}
