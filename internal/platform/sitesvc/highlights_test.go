package sitesvc

import (
	"reflect"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func hoursAgo(now time.Time, hours float64) *time.Time {
	at := now.Add(-time.Duration(hours * float64(time.Hour)))
	return &at
}

func kinds(items []Highlight) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Kind)
	}
	return out
}

func TestBuildHighlightsThresholdBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	base := highlightInput{Kind: "web", Now: now, LastEventAt: hoursAgo(now, 1), Visitors: 100, Previous: 100}
	tests := []struct {
		name   string
		mutate func(*highlightInput)
		want   []string
	}{
		{"quiet at exactly 24h", func(in *highlightInput) { in.LastEventAt = hoursAgo(now, 24) }, []string{"quiet"}},
		{"not quiet at 23h59m", func(in *highlightInput) { in.LastEventAt = hoursAgo(now, 23.98) }, []string{"steady"}},
		{"not quiet without previous traffic", func(in *highlightInput) { in.LastEventAt = hoursAgo(now, 30); in.Previous = 0 }, []string{"steady"}},
		{"not quiet when never installed", func(in *highlightInput) { in.LastEventAt = nil; in.Visitors = 0; in.Previous = 0 }, nil},
		{"change at exactly +10%", func(in *highlightInput) { in.Visitors = 110 }, []string{"visitors_change"}},
		{"no change at +9%", func(in *highlightInput) { in.Visitors = 109 }, []string{"steady"}},
		{"change at exactly -10%", func(in *highlightInput) { in.Visitors = 90 }, []string{"visitors_change"}},
		{"no change when previous is 19", func(in *highlightInput) { in.Previous = 19; in.Visitors = 40 }, []string{"steady"}},
		{"change when previous is 20", func(in *highlightInput) { in.Previous = 20; in.Visitors = 40 }, []string{"visitors_change"}},
		{"top source at 25% and 20 visitors", func(in *highlightInput) { in.TopSource = "google.com"; in.TopSourceVisitors = 25; in.SourceTotal = 100 }, []string{"top_source"}},
		{"no top source at 24% share", func(in *highlightInput) { in.TopSource = "google.com"; in.TopSourceVisitors = 24; in.SourceTotal = 100 }, []string{"steady"}},
		{"no top source at 19 visitors", func(in *highlightInput) { in.TopSource = "google.com"; in.TopSourceVisitors = 19; in.SourceTotal = 40 }, []string{"steady"}},
		{"new issues on app", func(in *highlightInput) { in.Kind = "app"; in.NewIssues = 1 }, []string{"new_issues"}},
		{"new issues ignored on web", func(in *highlightInput) { in.NewIssues = 5 }, []string{"steady"}},
		{"crash-free drop at exactly 1 point", func(in *highlightInput) {
			in.Kind = "app"
			in.CrashFreePrevious, in.CrashFreeCurrent = ptr(0.995), ptr(0.985)
		}, []string{"crash_free_drop"}},
		{"no crash-free drop at 0.9 point", func(in *highlightInput) {
			in.Kind = "app"
			in.CrashFreePrevious, in.CrashFreeCurrent = ptr(0.995), ptr(0.986)
		}, []string{"steady"}},
		{"crash-free ignored on web", func(in *highlightInput) {
			in.CrashFreePrevious, in.CrashFreeCurrent = ptr(0.99), ptr(0.5)
		}, []string{"steady"}},
		{"steady needs visitors", func(in *highlightInput) { in.Visitors = 0; in.Previous = 0 }, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.mutate(&in)
			got := kinds(buildHighlights(in))
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("kinds=%v want %v", got, tc.want)
			}
		})
	}
}

func TestBuildHighlightsPriorityCapAndValues(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	in := highlightInput{
		Kind: "app", Now: now, LastEventAt: hoursAgo(now, 26), Visitors: 118, Previous: 100,
		NewIssues: 2, CrashFreePrevious: ptr(0.99), CrashFreeCurrent: ptr(0.976),
		TopSource: "google.com", TopSourceVisitors: 50, SourceTotal: 100,
	}
	got := buildHighlights(in)
	if want := []string{"quiet", "new_issues", "crash_free_drop"}; !reflect.DeepEqual(kinds(got), want) {
		t.Fatalf("kinds=%v want %v", kinds(got), want)
	}
	if got[0].Hours == nil || *got[0].Hours != 26 {
		t.Fatalf("quiet hours=%v", got[0].Hours)
	}
	if got[1].Count == nil || *got[1].Count != 2 {
		t.Fatalf("issue count=%v", got[1].Count)
	}
	if got[2].Points == nil || *got[2].Points < 1.39 || *got[2].Points > 1.41 {
		t.Fatalf("crash-free points=%v", got[2].Points)
	}
	in.Kind, in.LastEventAt = "web", hoursAgo(now, 1)
	got = buildHighlights(in)
	if want := []string{"visitors_change", "top_source"}; !reflect.DeepEqual(kinds(got), want) {
		t.Fatalf("kinds=%v want %v", kinds(got), want)
	}
	if got[0].Percent == nil || *got[0].Percent != 18 {
		t.Fatalf("percent=%v", got[0].Percent)
	}
	if got[1].Name != "google.com" || got[1].Share == nil || *got[1].Share != 0.5 {
		t.Fatalf("top source=%+v", got[1])
	}
}
