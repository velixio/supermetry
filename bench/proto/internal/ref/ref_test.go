package ref

import (
	"reflect"
	"testing"

	"panel/bench/proto/internal/core"
)

func load(t *testing.T) *Ref {
	t.Helper()
	recs, err := ReadNDJSON("../../testdata/fixture.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	sites, err := core.LoadSitesFile("../../testdata/fixture_sites.json")
	if err != nil {
		t.Fatal(err)
	}
	return Build(recs, sites)
}

func d(s string) core.Day { v, _ := core.ParseDay(s); return v }

// Hand-computed from testdata/fixture.ndjson (see NOTES.md, "Fixture").
func TestReferenceMatchesHandComputedFixture(t *testing.T) {
	r := load(t)
	day1, day2 := d("2026-07-01"), d("2026-07-02")
	type exp struct {
		site   int32
		day    core.Day
		m      core.Metrics
		funnel []int64
	}
	for _, c := range []exp{
		{1, day1, core.Metrics{Visitors: 3, Visits: 4, Pageviews: 8, Bounces: 1, DurationMs: 2819_000}, []int64{3, 2, 1}},
		{1, day2, core.Metrics{Visitors: 3, Visits: 3, Pageviews: 3, Bounces: 2, DurationMs: 60_000}, []int64{1, 0, 0}},
		{2, day1, core.Metrics{Visitors: 3, Visits: 3, Pageviews: 4, Bounces: 1, DurationMs: 960_000}, []int64{2, 1, 0}},
		{2, day2, core.Metrics{Visitors: 1, Visits: 1, Pageviews: 1, Bounces: 1, DurationMs: 0}, []int64{0, 0, 0}},
	} {
		q := Query{Site: c.site, From: c.day, To: c.day}
		if got := r.Overview(q); got != c.m {
			t.Errorf("site %d %s overview = %+v, want %+v", c.site, c.day, got, c.m)
		}
		if got := r.Funnel(q); !reflect.DeepEqual(got, c.funnel) {
			t.Errorf("site %d %s funnel = %v, want %v", c.site, c.day, got, c.funnel)
		}
	}
	q1 := Query{Site: 1, From: day1, To: day1}
	src := r.Breakdown(q1, core.DimSource)
	if src["www.google.com"].Pageviews != 3 || src[""].Visits != 1 || src["twitter"].Visitors != 1 || src["news.ycombinator.com"].Pageviews != 2 || len(src) != 4 {
		t.Errorf("sources = %+v", src)
	}
	pages := r.Breakdown(q1, core.DimPath)
	if pages["/"].Visitors != 3 || pages["/pricing"].Pageviews != 3 || pages["/blog/x"].Bounces != 1 || pages["/"].Bounces != 0 {
		t.Errorf("pages = %+v", pages)
	}
	entry := r.Breakdown(q1, core.DimEntry)
	if entry["/"].Visitors != 2 || entry["/"].Visits != 2 || entry["/pricing"].Visits != 1 {
		t.Errorf("entry = %+v", entry)
	}
	dev := r.Breakdown(q1, core.DimDevice)
	if dev["desktop"].Visitors != 2 || dev["desktop"].Visits != 3 || dev["desktop"].Pageviews != 6 {
		t.Errorf("devices = %+v", dev)
	}
	props := r.Props(Query{Site: 1, From: day1, To: day2}, "signup", "plan")
	if !reflect.DeepEqual(props, map[string]PropRow{"free": {1, 1}, "pro": {1, 1}, "team": {1, 1}}) {
		t.Errorf("props = %+v", props)
	}
	// Two days: visitors add up (V2 and V3 are new visitors on day 2).
	if got := r.Overview(Query{Site: 1, From: day1, To: day2}).Visitors; got != 6 {
		t.Errorf("2-day visitors = %d, want 6", got)
	}
	// Filters: country=DE (session dim) and path=/pricing (page dim) combined.
	f := r.Overview(Query{Site: 1, From: day1, To: day1, Filters: []core.Filter{{Key: "country", Value: "DE"}, {Key: "path", Value: "/pricing"}}})
	if f != (core.Metrics{Visitors: 1, Visits: 1, Pageviews: 1, Bounces: 0, DurationMs: 2100_000}) {
		t.Errorf("filtered overview = %+v", f)
	}
}
