package query_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

// Paths: 1=/a 2=/b 3=/c. Sessions (all 2026-10-06, UTC):
//
//	s1 v1 a,a,b,c   s2 v2 a,b,a   s3 v3 b   s4 v4 a
//	s5 v5 a,c (DE)  s6 v6 b,a at the same instant (ties break by path id desc)
func setupJourneyTruth(t *testing.T) (run func(api.JourneyRequest) api.JourneyResponse, closeDay func()) {
	t.Helper()
	p, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, p, day("2026-10-05"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	_, err := p.Exec(ctx, `
insert into dict(kind,id,value) values (0,1,'/a'),(0,2,'/b'),(0,3,'/c');
insert into sessions (site_id,retention_class,session_id,visitor_id,start,last,local_day,entry_path,exit_path,country,pageviews,events,engagement_ms,bounce)
values
 (1,30,1,1,'2026-10-06 10:00Z','2026-10-06 10:05Z','2026-10-06',1,3,'IN',4,0,0,false),
 (1,30,2,2,'2026-10-06 10:00Z','2026-10-06 10:05Z','2026-10-06',1,1,'IN',3,0,0,false),
 (1,30,3,3,'2026-10-06 10:00Z','2026-10-06 10:00Z','2026-10-06',2,2,'IN',1,0,0,true),
 (1,30,4,4,'2026-10-06 10:00Z','2026-10-06 10:00Z','2026-10-06',1,1,'IN',1,0,0,true),
 (1,30,5,5,'2026-10-06 10:00Z','2026-10-06 10:01Z','2026-10-06',1,3,'DE',2,0,0,false),
 (1,30,6,6,'2026-10-06 10:00Z','2026-10-06 10:00Z','2026-10-06',2,1,'IN',2,0,0,false);
insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,country)
select 1,30,'2026-10-06',('2026-10-06 10:0'||m||':00Z')::timestamptz,v,v,0,p,c from (values
 (1,0,1,'IN'),(1,1,1,'IN'),(1,2,2,'IN'),(1,3,3,'IN'),
 (2,0,1,'IN'),(2,1,2,'IN'),(2,2,1,'IN'),
 (3,0,2,'IN'),(4,0,1,'IN'),
 (5,0,1,'DE'),(5,1,3,'DE'),
 (6,0,2,'IN'),(6,0,1,'IN')) x(v,m,p,c)`)
	if err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(p)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "j", TZ: time.UTC, Retention: db.Ret30}}
	engine := identifiedEngine(p, dictionary, reg)
	run = func(req api.JourneyRequest) api.JourneyResponse {
		t.Helper()
		req.SiteId = 1
		req.DateRange = dateRange("2026-10-06", "2026-10-06")
		resp, err := engine.Journey(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	closeDay = func() {
		if _, err := closer.CloseDay(ctx, p, []int32{1}, day("2026-10-06")); err != nil {
			t.Fatal(err)
		}
	}
	return run, closeDay
}

func journeyMaps(r api.JourneyResponse) (map[string]int64, map[string]int64) {
	nodes, links := map[string]int64{}, map[string]int64{}
	for _, n := range r.Nodes {
		nodes[nodeKey(n.Step, n.Path)] = n.Visitors
	}
	for _, l := range r.Links {
		links[linkKey(l.FromStep, l.FromPath, l.ToPath)] = l.Visitors
	}
	return nodes, links
}

func TestJourneyMatchesHandComputedFlowsOnHotAndClosedTiers(t *testing.T) {
	run, closeDay := setupJourneyTruth(t)
	start, end := "/a", "/c"
	de := []api.Filter{{Dimension: "country", Operator: api.Is, Values: []string{"DE"}}}
	cases := []struct {
		name         string
		req          api.JourneyRequest
		nodes, links map[string]int64
		stepTotals   map[int]int64
	}{
		{"plain", api.JourneyRequest{Steps: 3, Limit: 10},
			map[string]int64{"1:/a": 4, "1:/b": 2, "2:/b": 2, "2:exit": 2, "2:/c": 1, "2:/a": 1, "3:/c": 1, "3:/a": 1, "3:exit": 2},
			map[string]int64{"1:/a->/b": 2, "1:/a->exit": 1, "1:/a->/c": 1, "1:/b->exit": 1, "1:/b->/a": 1,
				"2:/b->/c": 1, "2:/b->/a": 1, "2:/c->exit": 1, "2:/a->exit": 1}, nil},
		{"other bucket", api.JourneyRequest{Steps: 3, Limit: 1}, nil, nil,
			map[int]int64{1: 6, 2: 6}},
		{"start /a", api.JourneyRequest{Steps: 3, Limit: 10, StartPath: &start},
			map[string]int64{"1:/a": 5, "2:/b": 2, "2:exit": 2, "2:/c": 1, "3:/c": 1, "3:/a": 1, "3:exit": 1},
			map[string]int64{"1:/a->/b": 2, "1:/a->exit": 2, "1:/a->/c": 1, "2:/b->/c": 1, "2:/b->/a": 1, "2:/c->exit": 1}, nil},
		{"end /c", api.JourneyRequest{Steps: 3, Limit: 10, EndPath: &end},
			map[string]int64{"1:/a": 1, "2:/b": 1, "2:/a": 1, "3:/c": 2},
			map[string]int64{"1:/a->/b": 1, "2:/b->/c": 1, "2:/a->/c": 1}, nil},
		{"country DE", api.JourneyRequest{Steps: 3, Limit: 10, Filters: &de},
			map[string]int64{"1:/a": 1, "2:/c": 1, "3:exit": 1},
			map[string]int64{"1:/a->/c": 1, "2:/c->exit": 1}, nil},
	}
	for _, tier := range []string{"hot", "closed"} {
		if tier == "closed" {
			closeDay()
		}
		for _, tc := range cases {
			t.Run(tier+"/"+tc.name, func(t *testing.T) {
				resp := run(tc.req)
				nodes, links := journeyMaps(resp)
				if tc.nodes != nil && !reflect.DeepEqual(nodes, tc.nodes) {
					t.Fatalf("nodes = %v, want %v", nodes, tc.nodes)
				}
				if tc.links != nil && !reflect.DeepEqual(links, tc.links) {
					t.Fatalf("links = %v, want %v", links, tc.links)
				}
				for step, want := range tc.stepTotals {
					var got int64
					for _, n := range resp.Nodes {
						if n.Step == step {
							got += n.Visitors
						}
					}
					if got != want {
						t.Fatalf("step %d node total = %d, want %d (%v)", step, got, want, nodes)
					}
				}
			})
		}
	}
}
