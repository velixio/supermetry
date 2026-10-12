package query

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeLabels struct{}

func (fakeLabels) Source(id int32) string {
	return map[int32]string{0: "", 1: "google", 2: "newsletter", 3: "twitter"}[id]
}
func (fakeLabels) Campaign(id int32) string { return map[int32]string{0: "", 5: "spring"}[id] }
func (fakeLabels) Region(id int32) string   { return fmt.Sprintf("region-%d", id) }
func (fakeLabels) Path(id int32) string     { return fmt.Sprintf("/p%d", id) }

var base = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func day(n int) time.Time { return base.Add(time.Duration(n) * 24 * time.Hour) }

// Hand-computed fixture. Visitor 1 (customer 10) visits via google on day 0,
// newsletter on day 10, twitter on day 20, and buys on day 25 (30 USD) and day
// 26 (10 USD). Visitor 2 (customer 11) arrives via twitter on day 28 and buys
// 20 USD on day 28 (its own session source is twitter). Visitor 3 is anonymous
// and has only a direct visit; it buys 5 USD on day 29.
func fixture() ([]RevenueOrder, []RevenueTouch) {
	orders := []RevenueOrder{
		{At: day(25), Visitor: 1, Session: 104, Customer: 10, Micros: 30_000_000, Currency: "USD", Country: "US", RegionID: 1, PathID: 1, SourceID: 3, Product: "pro"},
		{At: day(26), Visitor: 1, Session: 105, Customer: 10, Micros: 10_000_000, Currency: "USD", Country: "US", RegionID: 1, PathID: 2, SourceID: 0, Product: "basic"},
		{At: day(28), Visitor: 2, Session: 201, Customer: 11, Micros: 20_000_000, Currency: "USD", Country: "DE", RegionID: 2, PathID: 1, SourceID: 3, Product: "pro"},
		{At: day(29), Visitor: 3, Session: 301, Micros: 5_000_000, Currency: "USD", Country: "DE", RegionID: 2, PathID: 2, Product: "basic"},
	}
	touches := []RevenueTouch{
		{Visitor: 1, Customer: 10, Session: 100, Start: day(0), SourceID: 1},
		{Visitor: 1, Customer: 10, Session: 101, Start: day(10), SourceID: 2, CampaignID: 5},
		{Visitor: 1, Customer: 10, Session: 102, Start: day(20), SourceID: 3},
		{Visitor: 1, Customer: 10, Session: 103, Start: day(24)}, // direct: never earns credit
		{Visitor: 2, Customer: 11, Session: 201, Start: day(28), SourceID: 3},
		{Visitor: 3, Session: 301, Start: day(29)},
	}
	return orders, touches
}

func rowMap(rows []RevenueGroupResult) map[string]RevenueGroupResult {
	out := map[string]RevenueGroupResult{}
	for _, row := range rows {
		out[row.Key] = row
	}
	return out
}

func TestRevenueTotalsAndCustomers(t *testing.T) {
	orders, touches := fixture()
	_, totals, err := AggregateRevenue(orders, touches, RevenueByCountry, RevenueAttributionEvent, 30*24*time.Hour, fakeLabels{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 4 orders, 65 USD, average 16.25 USD; customers 10 and 11, only 10 repeats.
	if totals.Orders != 4 || totals.RevenueMicros != 65_000_000 || totals.AverageOrderMicro != 16_250_000 {
		t.Fatalf("totals = %+v", totals)
	}
	if totals.UniqueCustomers == nil || *totals.UniqueCustomers != 2 || *totals.RepeatCustomers != 1 || *totals.RepeatRate != 50 {
		t.Fatalf("customers = %v repeat = %v rate = %v", totals.UniqueCustomers, totals.RepeatCustomers, totals.RepeatRate)
	}
}

func TestRevenueByCountryProductAndPage(t *testing.T) {
	orders, touches := fixture()
	byCountry, _, _ := AggregateRevenue(orders, touches, RevenueByCountry, RevenueAttributionEvent, 30*24*time.Hour, fakeLabels{}, 0)
	if m := rowMap(byCountry); m["US"].RevenueMicros != 40_000_000 || m["US"].Orders != 2 || m["DE"].RevenueMicros != 25_000_000 {
		t.Fatalf("country rows = %+v", byCountry)
	}
	if byCountry[0].Key != "US" {
		t.Fatalf("largest group first, got %q", byCountry[0].Key)
	}
	byProduct, _, _ := AggregateRevenue(orders, touches, RevenueByProduct, RevenueAttributionEvent, 30*24*time.Hour, fakeLabels{}, 0)
	if m := rowMap(byProduct); m["pro"].RevenueMicros != 50_000_000 || m["basic"].RevenueMicros != 15_000_000 {
		t.Fatalf("product rows = %+v", byProduct)
	}
	byPage, _, _ := AggregateRevenue(orders, touches, RevenueByPage, RevenueAttributionEvent, 30*24*time.Hour, fakeLabels{}, 0)
	if m := rowMap(byPage); m["/p1"].RevenueMicros != 50_000_000 || m["/p2"].Orders != 2 {
		t.Fatalf("page rows = %+v", byPage)
	}
}

func TestRevenueAttributionModels(t *testing.T) {
	orders, touches := fixture()
	lookback := 30 * 24 * time.Hour
	// Event attribution credits the order's own source: twitter 50, direct 15.
	event, _, _ := AggregateRevenue(orders, touches, RevenueBySource, RevenueAttributionEvent, lookback, fakeLabels{}, 0)
	if m := rowMap(event); m["twitter"].RevenueMicros != 50_000_000 || m[""].RevenueMicros != 15_000_000 {
		t.Fatalf("event = %+v", event)
	}
	// Last click: visitor 1's orders (day 25 and 26) credit the day-20 twitter
	// visit (the day-24 direct visit is skipped); visitor 2 credits its own
	// twitter visit; visitor 3 has no qualifying visit and stays direct.
	last, _, _ := AggregateRevenue(orders, touches, RevenueBySource, RevenueAttributionLastClick, lookback, fakeLabels{}, 0)
	if m := rowMap(last); m["twitter"].RevenueMicros != 60_000_000 || m[""].RevenueMicros != 5_000_000 || len(m) != 2 {
		t.Fatalf("last click = %+v", last)
	}
	// First click with a 30-day lookback: the day-0 google visit is 25 days
	// before the day-25 order (inside the window) and 26 days before the second.
	first, _, _ := AggregateRevenue(orders, touches, RevenueBySource, RevenueAttributionFirstClick, lookback, fakeLabels{}, 0)
	if m := rowMap(first); m["google"].RevenueMicros != 40_000_000 || m["twitter"].RevenueMicros != 20_000_000 || m[""].RevenueMicros != 5_000_000 {
		t.Fatalf("first click = %+v", first)
	}
	// A 7-day lookback drops the google and newsletter visits: first click
	// falls through to the day-20 twitter visit for visitor 1.
	short, _, _ := AggregateRevenue(orders, touches, RevenueBySource, RevenueAttributionFirstClick, 7*24*time.Hour, fakeLabels{}, 0)
	if m := rowMap(short); m["twitter"].RevenueMicros != 60_000_000 || m["google"].Orders != 0 {
		t.Fatalf("short lookback = %+v", short)
	}
	// Campaign follows the same chosen touch. For the day-25 order a 15-day
	// window is [day 10, day 25] and the boundary is inclusive, so first click
	// picks the day-10 newsletter visit (campaign "spring"); a 14-day window is
	// [day 11, day 25], which starts at the day-20 twitter visit (no campaign).
	campaign, _, _ := AggregateRevenue(orders[:1], touches, RevenueByCampaign, RevenueAttributionFirstClick, 15*24*time.Hour, fakeLabels{}, 0)
	if len(campaign) != 1 || campaign[0].Key != "spring" {
		t.Fatalf("15-day campaign = %+v", campaign)
	}
	campaign, _, _ = AggregateRevenue(orders[:1], touches, RevenueByCampaign, RevenueAttributionFirstClick, 14*24*time.Hour, fakeLabels{}, 0)
	if len(campaign) != 1 || campaign[0].Key != "" {
		t.Fatalf("14-day campaign = %+v", campaign)
	}
}

func TestSelectTouchTieAndBoundary(t *testing.T) {
	order := RevenueOrder{At: day(10), Visitor: 1}
	touches := []RevenueTouch{
		{Visitor: 1, Session: 9, Start: day(5), SourceID: 2},
		{Visitor: 1, Session: 3, Start: day(5), SourceID: 1},
		{Visitor: 1, Session: 4, Start: day(11), SourceID: 3}, // after the order: ignored
		{Visitor: 2, Session: 8, Start: day(6), SourceID: 3},  // other visitor
	}
	for _, model := range []string{RevenueAttributionFirstClick, RevenueAttributionLastClick} {
		got, ok := SelectTouch(order, touches, model, 5*24*time.Hour)
		if !ok || got.Session != 3 {
			t.Fatalf("%s tie should pick the lower session id, got %+v %v", model, got, ok)
		}
	}
	// A touch exactly at the lookback boundary is included; one nanosecond older is not.
	if _, ok := SelectTouch(order, touches, RevenueAttributionLastClick, 5*24*time.Hour); !ok {
		t.Fatal("boundary touch must be included")
	}
	if _, ok := SelectTouch(order, touches, RevenueAttributionLastClick, 5*24*time.Hour-time.Nanosecond); ok {
		t.Fatal("touch just outside the window must be excluded")
	}
}

func TestRevenueRefusesMixedCurrencyAndHandlesAnonymous(t *testing.T) {
	orders := []RevenueOrder{{Micros: 1_000_000, Currency: "USD"}, {Micros: 2_000_000, Currency: "EUR"}}
	if _, _, err := AggregateRevenue(orders, nil, RevenueByCountry, RevenueAttributionEvent, time.Hour, fakeLabels{}, 0); !errors.Is(err, ErrMixedRevenueCurrency) {
		t.Fatalf("err = %v", err)
	}
	_, totals, err := AggregateRevenue([]RevenueOrder{{Micros: 3_000_000, Currency: "USD"}}, nil, RevenueByCountry, RevenueAttributionEvent, time.Hour, fakeLabels{}, 0)
	if err != nil || totals.UniqueCustomers != nil || totals.RepeatRate != nil || totals.Orders != 1 {
		t.Fatalf("anonymous totals = %+v err = %v", totals, err)
	}
	rows, _, _ := AggregateRevenue([]RevenueOrder{{Micros: 1, Country: "A"}, {Micros: 2, Country: "B"}, {Micros: 3, Country: "C"}}, nil, RevenueByCountry, RevenueAttributionEvent, time.Hour, fakeLabels{}, 2)
	if len(rows) != 2 || rows[0].Key != "C" {
		t.Fatalf("limit = %+v", rows)
	}
}

func TestRevenueLinksAcrossDaysOnlyThroughCustomer(t *testing.T) {
	order := RevenueOrder{At: day(5), Visitor: 77, Customer: 9}
	// Different visitor ids on earlier days (the id is re-salted daily) still
	// belong to customer 9; an anonymous visit with visitor id 77 belongs to a
	// different identity because the order is identified.
	touches := []RevenueTouch{
		{Visitor: 11, Customer: 9, Session: 1, Start: day(1), SourceID: 1},
		{Visitor: 22, Customer: 9, Session: 2, Start: day(3), SourceID: 2},
		{Visitor: 77, Session: 3, Start: day(4), SourceID: 3},
	}
	if got, ok := SelectTouch(order, touches, RevenueAttributionFirstClick, 10*24*time.Hour); !ok || got.SourceID != 1 {
		t.Fatalf("first click = %+v %v", got, ok)
	}
	if got, ok := SelectTouch(order, touches, RevenueAttributionLastClick, 10*24*time.Hour); !ok || got.SourceID != 2 {
		t.Fatalf("last click = %+v %v", got, ok)
	}
	anonymous := RevenueOrder{At: day(5), Visitor: 77}
	if got, ok := SelectTouch(anonymous, touches, RevenueAttributionLastClick, 10*24*time.Hour); !ok || got.SourceID != 3 {
		t.Fatalf("anonymous order should match only its visitor id, got %+v %v", got, ok)
	}
}
