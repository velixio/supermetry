package query

import (
	"sort"
	"time"

	"supermetry/internal/api"
)

// Revenue attribution models. Event attribution credits the source stored on
// the order itself; first and last click look for earlier visits by the same
// visitor inside a lookback window.
const (
	RevenueAttributionEvent      = "event"
	RevenueAttributionFirstClick = "first_click"
	RevenueAttributionLastClick  = "last_click"

	DefaultRevenueLookbackDays = 30
	MaxRevenueLookbackDays     = 90
)

// ErrMixedRevenueCurrency is returned when the orders in a report use more
// than one currency; amounts in different currencies are never added.
var ErrMixedRevenueCurrency = api.ErrMixedRevenueCurrency

// RevenueOrder is one revenue-bearing event. Customer is a profile id and is
// zero for anonymous orders; refunds and negative amounts are not orders.
type RevenueOrder struct {
	At         time.Time
	Visitor    int64
	Session    int64
	Customer   int64
	Micros     int64
	Currency   string
	Country    string
	RegionID   int32
	PathID     int32
	SourceID   int32
	CampaignID int32
	Product    string
}

// RevenueTouch is one visit (session) that can receive attribution credit.
// Visitor ids are salted per day, so a visit belongs to the same person as an
// order only when both carry the same customer (profile) id; otherwise they
// match on the visitor id, which links same-day visits only.
type RevenueTouch struct {
	Visitor    int64
	Customer   int64
	Session    int64
	Start      time.Time
	SourceID   int32
	CampaignID int32
}

// RevenueTotals summarise a set of orders. UniqueCustomers and RepeatRate are
// nil when no order carries a customer (anonymous mode).
type RevenueTotals struct {
	Orders            int64
	RevenueMicros     int64
	AverageOrderMicro int64
	UniqueCustomers   *int64
	RepeatCustomers   *int64
	RepeatRate        *float64
}

// RevenueAccumulator collects orders for one group.
type RevenueAccumulator struct {
	orders    int64
	micros    int64
	customers map[int64]int64
}

func (a *RevenueAccumulator) add(order RevenueOrder) {
	a.orders++
	a.micros += order.Micros
	if order.Customer != 0 {
		if a.customers == nil {
			a.customers = map[int64]int64{}
		}
		a.customers[order.Customer]++
	}
}

func (a *RevenueAccumulator) totals() RevenueTotals {
	t := RevenueTotals{Orders: a.orders, RevenueMicros: a.micros}
	if a.orders > 0 {
		t.AverageOrderMicro = a.micros / a.orders
	}
	if len(a.customers) > 0 {
		unique := int64(len(a.customers))
		var repeat int64
		for _, count := range a.customers {
			if count >= 2 {
				repeat++
			}
		}
		rate := float64(repeat) / float64(unique) * 100
		t.UniqueCustomers, t.RepeatCustomers, t.RepeatRate = &unique, &repeat, &rate
	}
	return t
}

// RevenueGroupResult is one breakdown row; Key is empty for the direct bucket.
type RevenueGroupResult struct {
	Key string
	RevenueTotals
}

// RevenueDimensions are the supported breakdowns.
const (
	RevenueByCountry  = "country"
	RevenueByRegion   = "region"
	RevenueBySource   = "source"
	RevenueByCampaign = "campaign"
	RevenueByPage     = "page"
	RevenueByProduct  = "product"
)

// RevenueLabeler resolves dictionary ids to display text.
type RevenueLabeler interface {
	Source(id int32) string
	Campaign(id int32) string
	Region(id int32) string
	Path(id int32) string
}

type identity struct{ customer, visitor int64 }

func orderIdentity(o RevenueOrder) identity {
	if o.Customer != 0 {
		return identity{customer: o.Customer}
	}
	return identity{visitor: o.Visitor}
}

func touchIdentity(t RevenueTouch) identity {
	if t.Customer != 0 {
		return identity{customer: t.Customer}
	}
	return identity{visitor: t.Visitor}
}

// SelectTouch picks the touch that earns credit for an order, or false when no
// qualifying visit exists in the window. Only visits with a source or campaign
// qualify (direct visits never steal credit); ties on start time go to the
// lower session id so results are deterministic.
func SelectTouch(order RevenueOrder, touches []RevenueTouch, model string, lookback time.Duration) (RevenueTouch, bool) {
	var best RevenueTouch
	found := false
	earliest := order.At.Add(-lookback)
	for _, touch := range touches {
		if touchIdentity(touch) != orderIdentity(order) || touch.Start.After(order.At) || touch.Start.Before(earliest) {
			continue
		}
		if touch.SourceID == 0 && touch.CampaignID == 0 {
			continue
		}
		if !found {
			best, found = touch, true
			continue
		}
		switch model {
		case RevenueAttributionFirstClick:
			if touch.Start.Before(best.Start) || (touch.Start.Equal(best.Start) && touch.Session < best.Session) {
				best = touch
			}
		default:
			if touch.Start.After(best.Start) || (touch.Start.Equal(best.Start) && touch.Session < best.Session) {
				best = touch
			}
		}
	}
	return best, found
}

// AggregateRevenue groups orders by the dimension and returns the rows (largest
// revenue first, at most limit) plus totals over all orders. It fails when the
// orders use more than one currency.
func AggregateRevenue(orders []RevenueOrder, touches []RevenueTouch, dimension, model string, lookback time.Duration, labels RevenueLabeler, limit int) ([]RevenueGroupResult, RevenueTotals, error) {
	currency := ""
	for _, order := range orders {
		if order.Currency == "" {
			continue
		}
		if currency != "" && currency != order.Currency {
			return nil, RevenueTotals{}, ErrMixedRevenueCurrency
		}
		currency = order.Currency
	}
	byPerson := map[identity][]RevenueTouch{}
	for _, touch := range touches {
		byPerson[touchIdentity(touch)] = append(byPerson[touchIdentity(touch)], touch)
	}
	var total RevenueAccumulator
	groups := map[string]*RevenueAccumulator{}
	for _, order := range orders {
		total.add(order)
		key := revenueKey(order, byPerson[orderIdentity(order)], dimension, model, lookback, labels)
		group := groups[key]
		if group == nil {
			group = &RevenueAccumulator{}
			groups[key] = group
		}
		group.add(order)
	}
	rows := make([]RevenueGroupResult, 0, len(groups))
	for key, group := range groups {
		rows = append(rows, RevenueGroupResult{Key: key, RevenueTotals: group.totals()})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].RevenueMicros != rows[j].RevenueMicros {
			return rows[i].RevenueMicros > rows[j].RevenueMicros
		}
		return rows[i].Key < rows[j].Key
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, total.totals(), nil
}

func revenueKey(order RevenueOrder, touches []RevenueTouch, dimension, model string, lookback time.Duration, labels RevenueLabeler) string {
	switch dimension {
	case RevenueByCountry:
		return order.Country
	case RevenueByRegion:
		return labels.Region(order.RegionID)
	case RevenueByPage:
		return labels.Path(order.PathID)
	case RevenueByProduct:
		return order.Product
	case RevenueBySource, RevenueByCampaign:
		sourceID, campaignID := order.SourceID, order.CampaignID
		if model == RevenueAttributionFirstClick || model == RevenueAttributionLastClick {
			if touch, ok := SelectTouch(order, touches, model, lookback); ok {
				sourceID, campaignID = touch.SourceID, touch.CampaignID
			} else {
				sourceID, campaignID = 0, 0
			}
		}
		if dimension == RevenueBySource {
			return labels.Source(sourceID)
		}
		return labels.Campaign(campaignID)
	}
	return ""
}
