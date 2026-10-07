package query

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/semaphore"

	"panel/internal/api"
	"panel/internal/dict"
	"panel/internal/sites"
)

// Route identifies the exact storage tier used for a query range.
type Route int

const (
	RouteRollup Route = iota
	RouteVisitorDay
	RouteCube
	RouteClosed
	RouteHot
)

// Part is an inclusive civil-date range answered by one route.
type Part struct {
	Route    Route
	From, To time.Time
}

var (
	visitorDimensions = map[string]bool{"country": true, "browser": true, "os": true, "device": true, "time": true}
	cubeDimensions    = map[string]bool{"source": true, "country": true, "browser": true, "os": true, "device": true, "time": true}
	rollupDimensions  = map[string]bool{
		"page": true, "path": true, "source": true, "country": true, "browser": true,
		"os": true, "device": true, "entry": true, "exit": true, "time": true,
	}
	globalQuerySlots = semaphore.NewWeighted(16)
)

// Plan splits an inclusive range at closedThrough and chooses the cheapest
// exact source capable of answering the closed part.
func Plan(req Normalized, closedThrough time.Time) []Part {
	if req.From.After(req.To) {
		return nil
	}
	closedThrough = civilDate(closedThrough)
	var parts []Part
	closedTo := minTime(req.To, closedThrough)
	if !closedTo.Before(req.From) {
		parts = append(parts, Part{Route: closedRoute(req), From: req.From, To: closedTo})
	}
	hotFrom := maxTime(req.From, closedThrough.AddDate(0, 0, 1))
	if !hotFrom.After(req.To) {
		parts = append(parts, Part{Route: RouteHot, From: hotFrom, To: req.To})
	}
	return parts
}

func closedRoute(req Normalized) Route {
	if len(req.Filters) == 0 && len(req.Dimensions) <= 1 && allDimensions(req.Dimensions, rollupDimensions) && metricsSupported(req.Metrics, RouteRollup) {
		return RouteRollup
	}
	if allDimensions(req.Dimensions, visitorDimensions) && filterDimensions(req.Filters, visitorDimensions) && metricsSupported(req.Metrics, RouteVisitorDay) {
		return RouteVisitorDay
	}
	if allDimensions(req.Dimensions, cubeDimensions) && filterDimensions(req.Filters, cubeDimensions) && metricsSupported(req.Metrics, RouteCube) {
		return RouteCube
	}
	return RouteClosed
}

func allDimensions(dimensions []string, allowed map[string]bool) bool {
	for _, dimension := range dimensions {
		if !allowed[dimension] {
			return false
		}
	}
	return true
}

func filterDimensions(filters []api.Filter, allowed map[string]bool) bool {
	for _, filter := range filters {
		if !allowed[filter.Dimension] {
			return false
		}
	}
	return true
}

func metricsSupported(metrics []api.Metric, route Route) bool {
	for _, metric := range metrics {
		switch route {
		case RouteRollup, RouteCube:
			if metric == api.Events || metric == api.Conversions || metric == api.ConversionRate {
				return false
			}
		case RouteVisitorDay:
			if metric == api.ScrollDepth || metric == api.Events || metric == api.Conversions || metric == api.ConversionRate {
				return false
			}
		}
	}
	return true
}

// Engine executes normalized queries against the storage tiers.
type Engine struct {
	pool        *pgxpool.Pool
	dict        *dict.Dict
	registry    sites.Registry
	forcedRoute *Route
	now         func() time.Time
}

// NewEngine constructs a query engine.
func NewEngine(pool *pgxpool.Pool, dictionary *dict.Dict, registry sites.Registry) *Engine {
	return &Engine{pool: pool, dict: dictionary, registry: registry, now: time.Now}
}

// WithForcedRoute returns a shallow test engine that reads the entire range
// from route. Production callers should use automatic planning.
func (e *Engine) WithForcedRoute(route Route) *Engine {
	clone := *e
	clone.forcedRoute = &route
	return &clone
}

type resultRow struct {
	dimensions []string
	sums       Sums
}

// Query runs a typed analytics query and returns finalized metric values.
func (e *Engine) Query(ctx context.Context, req api.QueryRequest) (api.QueryResponse, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return api.QueryResponse{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(req.SiteId)
	if !ok {
		return api.QueryResponse{}, fmt.Errorf("unknown site %d", req.SiteId)
	}
	now := time.Now()
	if e.now != nil {
		now = e.now()
	}
	normalized, problem := Normalize(req, site, now)
	if problem != nil {
		return api.QueryResponse{}, errors.New(problemDetail(problem))
	}
	closedThrough, err := e.closedThrough(ctx, normalized.SiteID, normalized.From, normalized.To)
	if err != nil {
		return api.QueryResponse{}, err
	}
	parts := Plan(normalized, closedThrough)
	if e.forcedRoute != nil {
		parts = []Part{{Route: *e.forcedRoute, From: normalized.From, To: normalized.To}}
	}

	rows, sources, err := e.queryParts(ctx, normalized, parts)
	if err != nil {
		return api.QueryResponse{}, err
	}
	totalSums := Sums{}
	if len(normalized.Dimensions) == 0 {
		for _, row := range rows {
			totalSums.add(row.sums)
		}
	} else {
		totalReq := normalized
		totalReq.Dimensions = nil
		totalRows, _, queryErr := e.queryParts(ctx, totalReq, parts)
		if queryErr != nil {
			return api.QueryResponse{}, queryErr
		}
		for _, row := range totalRows {
			totalSums.add(row.sums)
		}
	}

	rows = mergeRows(rows)
	sortResultRows(rows, normalized)
	offset, err := cursorOffset(normalized.Cursor)
	if err != nil {
		return api.QueryResponse{}, err
	}
	if offset > len(rows) {
		offset = len(rows)
	}
	rows = rows[offset:]
	var nextCursor *string
	if len(rows) > normalized.Limit {
		next := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + normalized.Limit)))
		nextCursor = &next
		rows = rows[:normalized.Limit]
	}
	response := api.QueryResponse{
		Columns:    append(append([]string(nil), normalized.Dimensions...), metricNames(normalized.Metrics)...),
		Rows:       make([]map[string]interface{}, 0, len(rows)),
		Totals:     finalMetrics(normalized.Metrics, totalSums),
		Meta:       api.QueryMeta{SourcesUsed: sources, Cached: false},
		NextCursor: nextCursor,
	}
	for _, row := range rows {
		item := make(map[string]interface{}, len(normalized.Dimensions)+len(normalized.Metrics))
		for i, dimension := range normalized.Dimensions {
			item[dimension] = row.dimensions[i]
		}
		for metric, value := range finalMetrics(normalized.Metrics, row.sums) {
			item[metric] = value
		}
		response.Rows = append(response.Rows, item)
	}
	return response, nil
}

func (e *Engine) queryParts(ctx context.Context, req Normalized, parts []Part) ([]resultRow, []string, error) {
	var rows []resultRow
	var sources []string
	seen := make(map[string]bool)
	for _, part := range parts {
		partRows, err := e.queryPart(ctx, req, part)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, partRows...)
		source := part.Route.String()
		if !seen[source] {
			seen[source] = true
			sources = append(sources, source)
		}
	}
	return rows, sources, nil
}

func (e *Engine) queryPart(ctx context.Context, req Normalized, part Part) ([]resultRow, error) {
	switch part.Route {
	case RouteRollup:
		return e.queryRollup(ctx, req, part)
	case RouteVisitorDay:
		return e.queryVisitorDay(ctx, req, part)
	case RouteCube:
		return e.queryCube(ctx, req, part)
	case RouteClosed:
		return e.queryClosed(ctx, req, part)
	case RouteHot:
		return e.queryHot(ctx, req, part)
	default:
		return nil, fmt.Errorf("unknown query route %d", part.Route)
	}
}

func (e *Engine) closedThrough(ctx context.Context, siteID int32, from, to time.Time) (time.Time, error) {
	var missing *time.Time
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
select min(day)::date from generate_series($2::date,$3::date,interval '1 day') days(day)
where not exists (
  select 1 from day_close closed
  where closed.site_id=$1 and closed.day=days.day and closed.status='done'
)`, siteID, from, to).Scan(&missing)
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("query closed-through day: %w", err)
	}
	if missing == nil {
		return civilDate(to), nil
	}
	return civilDate(*missing).AddDate(0, 0, -1), nil
}

func mergeRows(rows []resultRow) []resultRow {
	merged := make(map[string]*resultRow, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		key := strings.Join(row.dimensions, "\x00")
		current := merged[key]
		if current == nil {
			copyRow := resultRow{dimensions: append([]string(nil), row.dimensions...)}
			current = &copyRow
			merged[key] = current
			order = append(order, key)
		}
		current.sums.add(row.sums)
	}
	result := make([]resultRow, 0, len(merged))
	for _, key := range order {
		result = append(result, *merged[key])
	}
	return result
}

func sortResultRows(rows []resultRow, req Normalized) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, order := range req.OrderBy {
			left, right, numeric := orderValues(rows[i], rows[j], req, order.Column)
			ascending := order.Direction != nil && *order.Direction == api.Asc
			if left != right {
				if !numeric {
					if ascending {
						return left < right
					}
					return left > right
				}
				leftNumber, _ := strconv.ParseFloat(left, 64)
				rightNumber, _ := strconv.ParseFloat(right, 64)
				if ascending {
					return leftNumber < rightNumber
				}
				return leftNumber > rightNumber
			}
		}
		if len(req.Metrics) > 0 {
			left := Metric(req.Metrics[0]).Final(rows[i].sums)
			right := Metric(req.Metrics[0]).Final(rows[j].sums)
			if left != right {
				return left > right
			}
		}
		return strings.Join(rows[i].dimensions, "\x00") < strings.Join(rows[j].dimensions, "\x00")
	})
}

func orderValues(left, right resultRow, req Normalized, column string) (string, string, bool) {
	for _, metric := range req.Metrics {
		if string(metric) == column {
			return strconv.FormatFloat(Metric(metric).Final(left.sums), 'g', -1, 64), strconv.FormatFloat(Metric(metric).Final(right.sums), 'g', -1, 64), true
		}
	}
	for i, dimension := range req.Dimensions {
		if dimension == column && i < len(left.dimensions) && i < len(right.dimensions) {
			return left.dimensions[i], right.dimensions[i], false
		}
	}
	return "", "", false
}

func cursorOffset(cursor *string) (int, error) {
	if cursor == nil || *cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(*cursor)
	if err != nil {
		return 0, errors.New("invalid query cursor")
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, errors.New("invalid query cursor")
	}
	return offset, nil
}

func finalMetrics(metrics []api.Metric, sums Sums) map[string]float64 {
	result := make(map[string]float64, len(metrics))
	for _, metric := range metrics {
		result[string(metric)] = Metric(metric).Final(sums)
	}
	return result
}

func metricNames(metrics []api.Metric) []string {
	result := make([]string, len(metrics))
	for i, metric := range metrics {
		result[i] = string(metric)
	}
	return result
}

func (s *Sums) add(other Sums) {
	s.Visitors += other.Visitors
	s.Visits += other.Visits
	s.Pageviews += other.Pageviews
	s.Bounces += other.Bounces
	s.DurationMS += other.DurationMS
	s.EngagementMS += other.EngagementMS
	s.ScrollSum += other.ScrollSum
	s.ScrollN += other.ScrollN
	s.Events += other.Events
	s.Converted += other.Converted
}

func problemDetail(problem *api.Problem) string {
	if problem.Detail != nil {
		return *problem.Detail
	}
	return problem.Title
}

func (route Route) String() string {
	switch route {
	case RouteRollup:
		return "rollup"
	case RouteVisitorDay:
		return "visitor_day"
	case RouteCube:
		return "cube_day"
	case RouteClosed:
		return "closed"
	case RouteHot:
		return "hot"
	default:
		return "unknown"
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
