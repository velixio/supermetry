package query_test

import (
	"context"
	"reflect"
	"testing"

	"supermetry/internal/api"
)

func TestJourneyReportMatchesRawFixture(t *testing.T) {
	_, engine := setupTiny(t)
	response, err := engine.Journey(context.Background(), api.JourneyRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-07"), Steps: 3, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	wantNodes := map[string]int64{
		"1:/": 2, "1:/blog/post": 2,
		"2:/pricing": 2, "2:/docs": 1, "2:exit": 2,
		"3:exit": 2,
	}
	gotNodes := make(map[string]int64, len(response.Nodes))
	for _, node := range response.Nodes {
		gotNodes[nodeKey(node.Step, node.Path)] = node.Visitors
	}
	if !reflect.DeepEqual(gotNodes, wantNodes) {
		t.Fatalf("nodes = %#v, want %#v", gotNodes, wantNodes)
	}

	wantLinks := map[string]int64{
		"1:/->/pricing": 2, "1:/->/docs": 1,
		"1:/blog/post->exit": 2,
		"2:/pricing->exit":   2, "2:/docs->exit": 1,
	}
	gotLinks := make(map[string]int64, len(response.Links))
	for _, link := range response.Links {
		gotLinks[linkKey(link.FromStep, link.FromPath, link.ToPath)] = link.Visitors
	}
	if !reflect.DeepEqual(gotLinks, wantLinks) {
		t.Fatalf("links = %#v, want %#v", gotLinks, wantLinks)
	}
}

func TestRetentionReportMatchesDailyIdentityFixture(t *testing.T) {
	_, engine := setupTiny(t)
	response, err := engine.Retention(context.Background(), api.RetentionRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-08"), Interval: api.RetentionDay,
	})
	if err != nil {
		t.Fatal(err)
	}

	if response.IdentityWindow != api.DailyIdentityWindow || response.RetentionMode != api.WithinDaySessionReturn {
		t.Fatalf("identity metadata = %q/%q", response.IdentityWindow, response.RetentionMode)
	}
	want := [][]int64{{2, 1}, {2, 0}, {2, 0}}
	if len(response.Cohorts) != len(want) {
		t.Fatalf("cohorts = %#v, want %d rows", response.Cohorts, len(want))
	}
	for index, retained := range want {
		if response.Cohorts[index].Size != 2 || !reflect.DeepEqual(response.Cohorts[index].Retained, retained) {
			t.Errorf("cohort %d = %#v, want size 2 retained %v", index, response.Cohorts[index], retained)
		}
	}
}

func TestRetentionReportMatchesProfileFixtureAcrossDays(t *testing.T) {
	pool, engine := setupTiny(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		insert into orgs(id,name,slug) overriding system value values (1,'Profiles','profiles-query');
		insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value
		values (1,1,'profilequery','Profiles','UTC',30);
		insert into profiles(id,site_id,external_id,first_seen_at,last_seen_at) overriding system value values
		  (1,1,'one','2026-10-06 10:00Z','2026-10-08 08:00Z'),
		  (2,1,'two','2026-10-06 11:00Z','2026-10-08 12:00Z'),
		  (3,1,'three','2026-10-07 11:00Z','2026-10-08 12:00Z');
		update events set profile_id=1 where session_id in (101,201,301);
		update events set profile_id=2 where session_id in (102,302);
		update events set profile_id=3 where session_id in (202,302) and profile_id is null`)
	if err != nil {
		t.Fatal(err)
	}
	identity := api.ProfileRetentionIdentity
	response, err := engine.Retention(ctx, api.RetentionRequest{
		SiteId: 1, DateRange: dateRange("2026-10-06", "2026-10-08"), Interval: api.RetentionDay, Identity: &identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.IdentityWindow != api.ProfileIdentityWindow || response.RetentionMode != api.ClassicProfileReturn {
		t.Fatalf("identity metadata = %q/%q", response.IdentityWindow, response.RetentionMode)
	}
	if len(response.Cohorts) != 2 {
		t.Fatalf("profile cohorts = %#v, want 2", response.Cohorts)
	}
	want := []api.RetentionCohort{
		{Start: response.Cohorts[0].Start, Size: 2, Retained: []int64{2, 1, 2}},
		{Start: response.Cohorts[1].Start, Size: 1, Retained: []int64{1, 0, 0}},
	}
	if !reflect.DeepEqual(response.Cohorts, want) {
		t.Fatalf("profile cohorts = %#v, want %#v", response.Cohorts, want)
	}
}

func nodeKey(step int, path string) string {
	return string(rune('0'+step)) + ":" + path
}

func linkKey(step int, from, to string) string {
	return string(rune('0'+step)) + ":" + from + "->" + to
}
