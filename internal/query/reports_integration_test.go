package query_test

import (
	"context"
	"reflect"
	"testing"

	"panel/internal/api"
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

func nodeKey(step int, path string) string {
	return string(rune('0'+step)) + ":" + path
}

func linkKey(step int, from, to string) string {
	return string(rune('0'+step)) + ":" + from + "->" + to
}
