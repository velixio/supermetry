package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
)

const (
	maxRevenueOrders      = 20000
	maxRevenueTouches     = 200000
	defaultRevenueRows    = 50
	maxRevenueRows        = 500
	maxRevenueCustomers   = 200
	maxRevenueSearchRunes = 100
)

// ErrRevenueInput marks a request the caller can fix.
var ErrRevenueInput = api.ErrRevenueInput

// RevenueReportRequest selects the period (inclusive civil dates in the site
// time zone), the breakdown and the attribution model.
type RevenueReportRequest = api.RevenueReportRequest

type dictLabels struct{ d *dict.Dict }

func (l dictLabels) lookup(kind dict.Kind, id int32) string {
	if id == 0 || l.d == nil {
		return ""
	}
	value, _ := l.d.Value(kind, id)
	return value
}
func (l dictLabels) Source(id int32) string   { return l.lookup(dict.Source, id) }
func (l dictLabels) Campaign(id int32) string { return l.lookup(dict.Campaign, id) }
func (l dictLabels) Region(id int32) string   { return l.lookup(dict.Region, id) }
func (l dictLabels) Path(id int32) string     { return l.lookup(dict.Path, id) }

func normalizeRevenueRequest(req RevenueReportRequest) (RevenueReportRequest, error) {
	from, to := civilDate(req.From), civilDate(req.To)
	if from.IsZero() || to.IsZero() || from.After(to) {
		return req, fmt.Errorf("%w: from and to are required and from must not be after to", ErrRevenueInput)
	}
	if daysInclusive(from, to) > maxRangeDays {
		return req, fmt.Errorf("%w: date range must not exceed %d days", ErrRevenueInput, maxRangeDays)
	}
	req.From, req.To = from, to
	switch req.Dimension {
	case RevenueByCountry, RevenueByRegion, RevenueBySource, RevenueByCampaign, RevenueByPage, RevenueByProduct:
	default:
		return req, fmt.Errorf("%w: unknown breakdown %q", ErrRevenueInput, req.Dimension)
	}
	switch req.Attribution {
	case "":
		req.Attribution = RevenueAttributionEvent
	case RevenueAttributionEvent, RevenueAttributionFirstClick, RevenueAttributionLastClick:
	default:
		return req, fmt.Errorf("%w: unknown attribution model %q", ErrRevenueInput, req.Attribution)
	}
	if req.LookbackDays == 0 {
		req.LookbackDays = DefaultRevenueLookbackDays
	}
	if req.LookbackDays < 1 || req.LookbackDays > MaxRevenueLookbackDays {
		return req, fmt.Errorf("%w: lookback_days must be between 1 and %d", ErrRevenueInput, MaxRevenueLookbackDays)
	}
	if req.Limit == 0 {
		req.Limit = defaultRevenueRows
	}
	if req.Limit < 1 || req.Limit > maxRevenueRows {
		return req, fmt.Errorf("%w: limit must be between 1 and %d", ErrRevenueInput, maxRevenueRows)
	}
	return req, nil
}

func revenueWindow(from, to time.Time, loc *time.Location) (time.Time, time.Time) {
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	return start, end
}

// RevenueReport breaks revenue down by one dimension with an attribution model.
// Orders and visits come from retained raw events and sessions, so the report
// covers the site's retention window, and customer figures exist only when the
// site is in identified mode with profiles enabled.
func (e *Engine) RevenueReport(ctx context.Context, siteID int32, req RevenueReportRequest) (api.RevenueReport, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return api.RevenueReport{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(siteID)
	if !ok {
		return api.RevenueReport{}, fmt.Errorf("unknown site %d", siteID)
	}
	req, err := normalizeRevenueRequest(req)
	if err != nil {
		return api.RevenueReport{}, err
	}
	loc := site.TZ
	if loc == nil {
		loc = time.UTC
	}
	start, end := revenueWindow(req.From, req.To, loc)
	identified := e.trackingMode == privacy.Identified && site.ProfilesEnabled
	retention := int16(site.Retention)

	orders, truncated, err := e.loadRevenueOrders(ctx, siteID, retention, start, end, identified)
	if err != nil {
		return api.RevenueReport{}, err
	}
	for i := range orders {
		if orders[i].Currency == "" {
			orders[i].Currency = site.RevenueCurrency
		}
		if site.RevenueCurrency != "" && orders[i].Currency != site.RevenueCurrency {
			return api.RevenueReport{}, ErrMixedRevenueCurrency
		}
	}
	lookback := time.Duration(req.LookbackDays) * 24 * time.Hour
	var touches []RevenueTouch
	if req.Attribution != RevenueAttributionEvent && (req.Dimension == RevenueBySource || req.Dimension == RevenueByCampaign) && len(orders) > 0 {
		touches, err = e.loadRevenueTouches(ctx, siteID, retention, orders, lookback)
		if err != nil {
			return api.RevenueReport{}, err
		}
	}
	rows, totals, err := AggregateRevenue(orders, touches, req.Dimension, req.Attribution, lookback, dictLabels{e.dict}, req.Limit)
	if err != nil {
		return api.RevenueReport{}, err
	}
	visitors, err := e.revenueVisitors(ctx, siteID, retention, start, end)
	if err != nil {
		return api.RevenueReport{}, err
	}
	report := api.RevenueReport{
		Currency: site.RevenueCurrency, Breakdown: req.Dimension, Attribution: req.Attribution, LookbackDays: req.LookbackDays,
		Orders: totals.Orders, Revenue: micros(totals.RevenueMicros), AverageOrderValue: micros(totals.AverageOrderMicro),
		Visitors: visitors, UniqueCustomers: totals.UniqueCustomers, RepeatCustomers: totals.RepeatCustomers, RepeatRate: totals.RepeatRate,
		Truncated: truncated,
	}
	if visitors > 0 {
		report.RevenuePerVisitor = micros(totals.RevenueMicros) / float64(visitors)
	}
	for _, row := range rows {
		report.Rows = append(report.Rows, api.RevenueReportRow{
			Key: row.Key, Orders: row.Orders, Revenue: micros(row.RevenueMicros), AverageOrderValue: micros(row.AverageOrderMicro),
			UniqueCustomers: row.UniqueCustomers, RepeatCustomers: row.RepeatCustomers, RepeatRate: row.RepeatRate,
		})
	}
	return report, nil
}

func micros(value int64) float64 { return float64(value) / 1_000_000 }

func (e *Engine) loadRevenueOrders(ctx context.Context, siteID int32, retention int16, start, end time.Time, identified bool) ([]RevenueOrder, bool, error) {
	rows, err := e.pool.Query(ctx, `
select e.ts, e.visitor_id, e.session_id, coalesce(e.profile_id,0), e.revenue_micros, coalesce(e.revenue_currency::text,''),
       e.country::text, e.region_id, e.path_id, e.source_id, e.campaign_id, coalesce(e.props->>'product','')
from events e
where e.site_id=$1 and e.retention_class=$2 and e.kind=1 and e.revenue_micros>0 and e.ts>=$3 and e.ts<$4
order by e.ts, e.session_id
limit $5`, siteID, retention, start, end, maxRevenueOrders+1)
	if err != nil {
		return nil, false, fmt.Errorf("load revenue orders: %w", err)
	}
	defer rows.Close()
	var orders []RevenueOrder
	for rows.Next() {
		var order RevenueOrder
		if err := rows.Scan(&order.At, &order.Visitor, &order.Session, &order.Customer, &order.Micros, &order.Currency,
			&order.Country, &order.RegionID, &order.PathID, &order.SourceID, &order.CampaignID, &order.Product); err != nil {
			return nil, false, err
		}
		if !identified {
			order.Customer = 0
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(orders) > maxRevenueOrders {
		return orders[:maxRevenueOrders], true, nil
	}
	return orders, false, nil
}

func (e *Engine) loadRevenueTouches(ctx context.Context, siteID int32, retention int16, orders []RevenueOrder, lookback time.Duration) ([]RevenueTouch, error) {
	earliest, latest := orders[0].At, orders[0].At
	customers := map[int64]struct{}{}
	visitors := map[int64]struct{}{}
	for _, order := range orders {
		if order.At.Before(earliest) {
			earliest = order.At
		}
		if order.At.After(latest) {
			latest = order.At
		}
		if order.Customer != 0 {
			customers[order.Customer] = struct{}{}
		} else {
			visitors[order.Visitor] = struct{}{}
		}
	}
	from := earliest.Add(-lookback)
	var touches []RevenueTouch
	if len(customers) > 0 {
		rows, err := e.pool.Query(ctx, `
select s.start, s.session_id, s.visitor_id, s.source_id, s.campaign_id, p.profile_id
from (select distinct session_id, profile_id from events
      where site_id=$1 and retention_class=$2 and profile_id = any($3) and ts>=$4 and ts<=$5) p
join sessions s on s.site_id=$1 and s.retention_class=$2 and s.session_id=p.session_id and s.start>=$4 and s.start<=$5
limit $6`, siteID, retention, int64Keys(customers), from, latest, maxRevenueTouches)
		if err != nil {
			return nil, fmt.Errorf("load customer touches: %w", err)
		}
		touches, err = scanTouches(rows, touches, true)
		if err != nil {
			return nil, err
		}
	}
	if len(visitors) > 0 {
		rows, err := e.pool.Query(ctx, `
select start, session_id, visitor_id, source_id, campaign_id from sessions
where site_id=$1 and retention_class=$2 and visitor_id = any($3) and start>=$4 and start<=$5
limit $6`, siteID, retention, int64Keys(visitors), from, latest, maxRevenueTouches)
		if err != nil {
			return nil, fmt.Errorf("load visitor touches: %w", err)
		}
		touches, err = scanTouches(rows, touches, false)
		if err != nil {
			return nil, err
		}
	}
	return touches, nil
}

type touchRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

func scanTouches(rows touchRows, into []RevenueTouch, withCustomer bool) ([]RevenueTouch, error) {
	defer rows.Close()
	for rows.Next() {
		var touch RevenueTouch
		var err error
		if withCustomer {
			err = rows.Scan(&touch.Start, &touch.Session, &touch.Visitor, &touch.SourceID, &touch.CampaignID, &touch.Customer)
		} else {
			err = rows.Scan(&touch.Start, &touch.Session, &touch.Visitor, &touch.SourceID, &touch.CampaignID)
		}
		if err != nil {
			return nil, err
		}
		into = append(into, touch)
	}
	return into, rows.Err()
}

func int64Keys(set map[int64]struct{}) []int64 {
	keys := make([]int64, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

// revenueVisitors counts daily visitors: visitor ids are salted per day, so the
// same person on two days counts twice, as on the main dashboard.
func (e *Engine) revenueVisitors(ctx context.Context, siteID int32, retention int16, start, end time.Time) (int64, error) {
	var visitors int64
	err := e.pool.QueryRow(ctx, `
select count(*) from (select distinct local_day, visitor_id from sessions
                      where site_id=$1 and retention_class=$2 and start>=$3 and start<$4) v`, siteID, retention, start, end).Scan(&visitors)
	if err != nil {
		return 0, fmt.Errorf("count revenue visitors: %w", err)
	}
	return visitors, nil
}

// RevenueCustomers lists identified customers by revenue. It is available only
// in identified mode with profiles enabled, and external ids may be personal
// data, so callers must gate it behind a permission.
func (e *Engine) RevenueCustomers(ctx context.Context, siteID int32, from, to time.Time, search string, limit int) ([]api.RevenueCustomer, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return nil, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(siteID)
	if !ok {
		return nil, fmt.Errorf("unknown site %d", siteID)
	}
	if e.trackingMode != privacy.Identified || !site.ProfilesEnabled {
		return nil, errors.New("customers require identified tracking with profiles enabled")
	}
	from, to = civilDate(from), civilDate(to)
	if from.IsZero() || to.IsZero() || from.After(to) || daysInclusive(from, to) > maxRangeDays {
		return nil, fmt.Errorf("%w: invalid date range", ErrRevenueInput)
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > maxRevenueCustomers {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrRevenueInput, maxRevenueCustomers)
	}
	search = strings.TrimSpace(search)
	if len([]rune(search)) > maxRevenueSearchRunes {
		return nil, fmt.Errorf("%w: search is too long", ErrRevenueInput)
	}
	loc := site.TZ
	if loc == nil {
		loc = time.UTC
	}
	start, end := revenueWindow(from, to, loc)
	pattern := "%"
	if search != "" {
		pattern = "%" + escapeLike(search) + "%"
	}
	rows, err := e.pool.Query(ctx, `
select p.id, p.external_id, count(*), coalesce(sum(e.revenue_micros),0), min(e.ts), max(e.ts)
from events e join profiles p on p.id=e.profile_id and p.site_id=e.site_id
where e.site_id=$1 and e.retention_class=$2 and e.kind=1 and e.revenue_micros>0 and e.ts>=$3 and e.ts<$4
  and p.external_id ilike $5 escape '\'
group by p.id, p.external_id
order by sum(e.revenue_micros) desc, p.id
limit $6`, siteID, int16(site.Retention), start, end, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("list revenue customers: %w", err)
	}
	defer rows.Close()
	var customers []api.RevenueCustomer
	for rows.Next() {
		var customer api.RevenueCustomer
		var total int64
		if err := rows.Scan(&customer.ProfileId, &customer.ExternalId, &customer.Orders, &total, &customer.FirstOrderAt, &customer.LastOrderAt); err != nil {
			return nil, err
		}
		customer.Revenue = micros(total)
		customers = append(customers, customer)
	}
	return customers, rows.Err()
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
