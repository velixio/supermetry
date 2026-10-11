package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"golang.org/x/sync/semaphore"

	"supermetry/internal/api"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
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
		"os": true, "device": true, "entry": true, "exit": true, "region": true,
		"city": true, "utm_source": true, "utm_medium": true, "utm_campaign": true,
		"browser_version": true, "os_version": true,
		"screen_size": true, "language": true, "hostname": true, "time": true,
	}
	globalQuerySlots = semaphore.NewWeighted(16)
)

// Plan splits an inclusive range at closedThrough and chooses the cheapest
// exact source capable of answering the closed part.
func Plan(req Normalized, closedThrough time.Time) []Part {
	return planParts(req, req.From, func(day time.Time) bool {
		return !day.After(civilDate(closedThrough))
	})
}

func (m closeMetadata) plan(req Normalized) []Part {
	return planParts(req, m.dataStart(req.From), func(day time.Time) bool {
		_, ok := m.closed[day]
		return ok
	})
}

func planParts(req Normalized, from time.Time, isClosed func(time.Time) bool) []Part {
	if req.From.After(req.To) {
		return nil
	}
	from = maxTime(req.From, civilDate(from))
	if from.After(req.To) {
		return nil
	}
	// Sub-day and weekday/hour grid buckets read retained raw sessions because
	// the closed tier stores civil days only.
	if containsAny(req.Dimensions, "hour", "weekday", "hour_of_day") {
		return []Part{{Route: RouteHot, From: from, To: req.To}}
	}
	parts := make([]Part, 0, 3)
	for day := from; !day.After(req.To); day = day.AddDate(0, 0, 1) {
		route := RouteHot
		if isClosed(day) {
			route = closedRoute(req)
		}
		if len(parts) > 0 && parts[len(parts)-1].Route == route && parts[len(parts)-1].To.AddDate(0, 0, 1).Equal(day) {
			parts[len(parts)-1].To = day
			continue
		}
		parts = append(parts, Part{Route: route, From: day, To: day})
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
			if metric == api.Events || metric == api.Conversions || metric == api.ConversionRate || isRevenueMetric(metric) {
				return false
			}
			if route == RouteCube && isPageMetric(metric) {
				return false
			}
		case RouteVisitorDay:
			if metric == api.ScrollDepth || metric == api.Events || metric == api.Conversions || metric == api.ConversionRate || isRevenueMetric(metric) || isPageMetric(metric) {
				return false
			}
		}
	}
	return true
}

// Engine executes normalized queries against the storage tiers.
type Engine struct {
	trackingMode privacy.Mode
	pathPolicy   privacy.PathPolicy
	pool         *pgxpool.Pool
	dict         *dict.Dict
	registry     sites.Registry
	goals        GoalLookup
	cache        *Cache
	closeMemo    *Cache
	forcedRoute  *Route
	now          func() time.Time
}

// NewEngine constructs a query engine.
func NewEngine(pool *pgxpool.Pool, dictionary *dict.Dict, registry sites.Registry, definitions ...GoalLookup) *Engine {
	var lookup GoalLookup
	if len(definitions) > 0 {
		lookup = definitions[0]
	}
	return &Engine{pool: pool, dict: dictionary, registry: registry, goals: lookup, closeMemo: NewCache(1), now: time.Now}
}

// SetTrackingMode configures the restart-boundary policy before serving requests.
func (e *Engine) SetTrackingMode(mode privacy.Mode) { e.trackingMode = mode }

// SetPathPolicy configures immutable verified-path reads before serving.
func (e *Engine) SetPathPolicy(policy privacy.PathPolicy) { e.pathPolicy = policy }

// SetCache attaches the shared closed-range result cache. NewEngine keeps its
// existing signature so tests and embedders may opt in explicitly.
func (e *Engine) SetCache(cache *Cache) {
	if e == nil {
		return
	}
	e.cache = cache
	if cache != nil {
		e.closeMemo = cache
	}
}

// InvalidateSite forces the next request for siteID to reload close metadata.
func (e *Engine) InvalidateSite(siteID int32) {
	if e == nil || e.closeMemo == nil {
		return
	}
	e.closeMemo.InvalidateSite(siteID)
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
	imported   Sums
}

// Query runs a typed analytics query and returns finalized metric values.
func (e *Engine) Query(ctx context.Context, req api.QueryRequest) (api.QueryResponse, error) {
	if e != nil && e.trackingMode != privacy.Identified {
		if err := api.ValidateAnonymousQuery(req); err != nil {
			return api.QueryResponse{}, err
		}
	}
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
	normalized.PGZone = postgresZone(ctx, e.pool, normalized.Location)
	if err := e.resolveGoalFilters(&normalized); err != nil {
		return api.QueryResponse{}, err
	}
	if containsDimension(normalized.Dimensions, "goal") {
		if err := e.resolveGoalBreakdown(&normalized); err != nil {
			return api.QueryResponse{}, err
		}
	}
	metadata, err := e.closeMemo.closeMetadata(ctx, e, normalized.SiteID)
	if err != nil {
		return api.QueryResponse{}, err
	}
	return e.queryNormalized(ctx, req, site, normalized, now, metadata)
}

func (e *Engine) queryNormalized(ctx context.Context, req api.QueryRequest, site sites.Site, normalized Normalized, now time.Time, metadata closeMetadata) (api.QueryResponse, error) {
	if err := validateTrafficRange(normalized, site, now); err != nil {
		return api.QueryResponse{}, err
	}
	closedThrough := metadata.closedThrough(normalized.From, normalized.To)
	parts := metadata.plan(normalized)
	if e.forcedRoute != nil {
		parts = []Part{{Route: *e.forcedRoute, From: normalized.From, To: normalized.To}}
	}

	var cacheKey Key
	cacheable := e.cache != nil && e.forcedRoute == nil && cacheEligible(normalized, site, now, metadata) && partsExcludeHot(parts)
	if cacheable {
		cacheKey = CacheKey(normalized.SiteID, fmt.Sprintf("tracking:%d:path-v%d:%s:%s", e.trackingMode, privacy.PathSanitizerVersion, e.pathPolicy.Fingerprint(), normalizedCacheQuery(normalized)), closedThrough, metadata.maxCloseVersion)
		if response, ok := e.cache.Get(cacheKey); ok {
			response.Meta.Cached = true
			return response, nil
		}
	}
	if !cacheable {
		return e.executeNormalized(ctx, req, site, normalized, now, metadata, parts)
	}

	result := e.cache.resultSF.DoChan(fmt.Sprintf("%x", cacheKey), func() (any, error) {
		if response, ok := e.cache.Get(cacheKey); ok {
			return cachedQueryResult{response: response, cached: true}, nil
		}
		sharedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), queryTimeout)
		defer cancel()
		response, queryErr := e.executeNormalized(sharedCtx, req, site, normalized, now, metadata, parts)
		if queryErr != nil {
			return cachedQueryResult{}, queryErr
		}
		e.cache.Put(cacheKey, response)
		return cachedQueryResult{response: response}, nil
	})
	select {
	case <-ctx.Done():
		return api.QueryResponse{}, ctx.Err()
	case completed := <-result:
		if err := ctx.Err(); err != nil {
			return api.QueryResponse{}, err
		}
		if completed.Err != nil {
			return api.QueryResponse{}, completed.Err
		}
		cached := completed.Val.(cachedQueryResult)
		response := cloneResponse(cached.response)
		response.Meta.Cached = cached.cached
		return response, nil
	}
}

type cachedQueryResult struct {
	response api.QueryResponse
	cached   bool
}

func (e *Engine) executeNormalized(ctx context.Context, req api.QueryRequest, site sites.Site, normalized Normalized, now time.Time, metadata closeMetadata, parts []Part) (api.QueryResponse, error) {
	if e.trackingMode != privacy.Identified && api.IsVerifiedPathQuery(req) {
		return e.queryVerifiedPaths(ctx, req, site, normalized, now, metadata)
	}
	rows, sources, err := e.queryParts(ctx, normalized, parts)
	if err != nil {
		return api.QueryResponse{}, err
	}
	importsAvailable := false
	var firstNative *time.Time
	if importedQueryEligible(normalized) {
		importsAvailable, err = e.importedDaysExist(ctx, normalized)
		if err != nil {
			return api.QueryResponse{}, err
		}
		if importsAvailable {
			firstNative, err = e.firstNativeDay(ctx, normalized.SiteID)
			if err != nil {
				return api.QueryResponse{}, err
			}
		}
	}
	importedRows, importedIncluded, err := e.queryImported(ctx, normalized, firstNative, importsAvailable)
	if err != nil {
		return api.QueryResponse{}, err
	}
	rows = append(rows, importedRows...)
	if importedIncluded {
		sources = append(sources, "imported")
	}
	goalConversionTotal := int64(0)
	if len(normalized.Dimensions) == 1 && normalized.Dimensions[0] == "goal" {
		visible := rows[:0]
		for _, row := range rows {
			if len(row.dimensions) == 1 && row.dimensions[0] == goalConversionTotalRow {
				goalConversionTotal += row.sums.Converted
				continue
			}
			visible = append(visible, row)
		}
		rows = visible
	}
	totalSums, totalImported := Sums{}, Sums{}
	if len(normalized.Dimensions) == 0 || hasPageMetric(normalized.Metrics) {
		for _, row := range rows {
			totalSums.add(row.sums)
			totalImported.add(row.imported)
		}
	} else {
		totalReq := normalized
		totalReq.Dimensions = nil
		if len(normalized.Dimensions) == 1 && normalized.Dimensions[0] == "goal" {
			// Goal rows are event-derived, but their dashboard denominator is the
			// filtered session population rather than event-attributed sessions.
			totalReq.EventAttribution = false
		}
		totalRows, _, queryErr := e.queryParts(ctx, totalReq, parts)
		if queryErr != nil {
			return api.QueryResponse{}, queryErr
		}
		for _, row := range totalRows {
			totalSums.add(row.sums)
		}
		importedTotals, totalsIncluded, queryErr := e.queryImported(ctx, totalReq, firstNative, importsAvailable)
		if queryErr != nil {
			return api.QueryResponse{}, queryErr
		}
		for _, row := range importedTotals {
			totalImported.add(row.imported)
		}
		importedIncluded = importedIncluded || totalsIncluded
	}
	if len(normalized.Dimensions) == 1 && normalized.Dimensions[0] == "goal" {
		// Visits and visitors remain the whole-site denominators returned by the
		// dimensionless totals query. Conversions count each matching session
		// once even when it satisfies several saved goals.
		totalSums.Converted = goalConversionTotal
	}
	if importedIncluded && !containsStringValue(sources, "imported") {
		sources = append(sources, "imported")
	}

	if e.trackingMode != privacy.Identified {
		for i := range rows {
			for j, dimension := range normalized.Dimensions {
				rows[i].dimensions[j], _ = api.CanonicalAnonymousDimension(dimension, rows[i].dimensions[j])
			}
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
		Totals:     finalMetrics(normalized.Metrics, totalSums, totalImported, nil),
		Meta:       api.QueryMeta{SourcesUsed: sources, Cached: false, ImportedIncluded: importedIncluded},
		NextCursor: nextCursor,
	}
	response.Meta.EventAttribution = eventAttributionMeta(normalized, parts)
	for _, row := range rows {
		item := make(map[string]interface{}, len(normalized.Dimensions)+len(normalized.Metrics))
		for i, dimension := range normalized.Dimensions {
			item[dimension] = row.dimensions[i]
		}
		for metric, value := range finalMetrics(normalized.Metrics, row.sums, row.imported, normalized.Dimensions) {
			item[metric] = value
		}
		response.Rows = append(response.Rows, item)
	}
	if normalized.Compare != nil {
		comparisonFrom, comparisonTo := comparisonRange(normalized.From, normalized.To, *normalized.Compare)
		comparisonRequest := req
		comparisonRequest.Period = nil
		comparisonRequest.Compare = nil
		comparisonRequest.DateRange = &api.DateRange{
			From: openapi_types.Date{Time: comparisonFrom},
			To:   openapi_types.Date{Time: comparisonTo},
		}
		comparisonNormalized, problem := Normalize(comparisonRequest, site, now)
		if problem != nil {
			return api.QueryResponse{}, errors.New(problemDetail(problem))
		}
		if err := e.resolveGoalFilters(&comparisonNormalized); err != nil {
			return api.QueryResponse{}, err
		}
		if containsDimension(comparisonNormalized.Dimensions, "goal") {
			if err := e.resolveGoalBreakdown(&comparisonNormalized); err != nil {
				return api.QueryResponse{}, err
			}
		}
		comparisonResponse, comparisonErr := e.queryNormalized(ctx, comparisonRequest, site, comparisonNormalized, now, metadata)
		if comparisonErr != nil {
			return api.QueryResponse{}, comparisonErr
		}
		response.Comparison = &api.ComparisonResult{
			EventAttribution: comparisonResponse.Meta.EventAttribution,
			DateRange:        *comparisonRequest.DateRange,
			Rows:             comparisonResponse.Rows,
			Totals:           comparisonResponse.Totals,
		}
	}
	return response, nil
}

func validateTrafficRange(req Normalized, site sites.Site, now time.Time) error {
	if !containsAny(req.Dimensions, "weekday", "hour_of_day") {
		return nil
	}
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	today := civilDate(now.In(location))
	if req.To.After(today) {
		return errors.New("date_range.to must not be after today in the site's timezone")
	}
	if site.Retention != 0 {
		oldest := today.AddDate(0, 0, -int(site.Retention)+1)
		if req.From.Before(oldest) {
			return fmt.Errorf("Traffic breakdowns are available only within the site's %d-day raw retention window", site.Retention)
		}
	}
	return nil
}

func eventAttributionMeta(req Normalized, parts []Part) *api.QueryEventAttribution {
	if !req.EventAttribution {
		return nil
	}
	coverage := api.QueryEventAttributionCoverageExact
	legacyExcluded := false
	for _, part := range parts {
		if part.Route != RouteHot {
			coverage = api.QueryEventAttributionCoverageAvailableOnly
			legacyExcluded = true
			break
		}
	}
	return &api.QueryEventAttribution{
		Basis:          api.QueryEventAttributionBasisEvent,
		Coverage:       coverage,
		LegacyExcluded: legacyExcluded,
	}
}

func cacheEligible(req Normalized, site sites.Site, now time.Time, metadata closeMetadata) bool {
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	today := civilDate(now.In(location))
	if !req.To.Before(today) || !partsExcludeHot(metadata.plan(req)) {
		return false
	}
	if req.Compare == nil {
		return true
	}
	from, to := comparisonRange(req.From, req.To, *req.Compare)
	comparison := req
	comparison.From, comparison.To = from, to
	return to.Before(today) && partsExcludeHot(metadata.plan(comparison))
}

func partsExcludeHot(parts []Part) bool {
	for _, part := range parts {
		if part.Route == RouteHot {
			return false
		}
	}
	return len(parts) > 0
}

func normalizedCacheQuery(req Normalized) string {
	encoded, _ := json.Marshal(struct {
		From, To, Timezone string
		Metrics            []api.Metric
		Dimensions         []string
		Filters            []api.Filter
		OrderBy            []api.OrderBy
		Limit              int
		Cursor             *string
		Compare            *api.QueryRequestCompare
		IncludeImported    bool
		GoalDefinitions    any
		RevenueCurrency    string
	}{
		From: req.From.Format(time.DateOnly), To: req.To.Format(time.DateOnly), Timezone: req.Location.String(),
		Metrics: req.Metrics, Dimensions: req.Dimensions, Filters: req.Filters, OrderBy: req.OrderBy,
		Limit: req.Limit, Cursor: req.Cursor, Compare: req.Compare, IncludeImported: req.IncludeImported,
		GoalDefinitions: req.GoalDefinitions,
		RevenueCurrency: req.RevenueCurrency,
	})
	return string(encoded)
}

func comparisonRange(from, to time.Time, compare api.QueryRequestCompare) (time.Time, time.Time) {
	if compare == api.PreviousYear {
		return previousYearDay(from), previousYearDay(to)
	}
	days := daysInclusive(from, to)
	previousTo := from.AddDate(0, 0, -1)
	return previousTo.AddDate(0, 0, 1-days), previousTo
}

func previousYearDay(day time.Time) time.Time {
	year := day.Year() - 1
	lastDay := time.Date(year, day.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, day.Month(), min(day.Day(), lastDay), 0, 0, 0, 0, time.UTC)
}

func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
	metadata, err := e.closeMemo.closeMetadata(ctx, e, siteID)
	if err != nil {
		return time.Time{}, fmt.Errorf("query closed-through day: %w", err)
	}
	return metadata.closedThrough(from, to), nil
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
		current.imported.add(row.imported)
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
			left := finalMetric(req.Metrics[0], rows[i].sums, rows[i].imported, req.Dimensions)
			right := finalMetric(req.Metrics[0], rows[j].sums, rows[j].imported, req.Dimensions)
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
			return strconv.FormatFloat(finalMetric(metric, left.sums, left.imported, req.Dimensions), 'g', -1, 64), strconv.FormatFloat(finalMetric(metric, right.sums, right.imported, req.Dimensions), 'g', -1, 64), true
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

func finalMetrics(metrics []api.Metric, sums, imported Sums, dimensions []string) map[string]float64 {
	result := make(map[string]float64, len(metrics))
	for _, metric := range metrics {
		result[string(metric)] = finalMetric(metric, sums, imported, dimensions)
	}
	return result
}

func finalMetric(metric api.Metric, sums, imported Sums, dimensions []string) float64 {
	if metricSupportsImported(metric, dimensions) {
		sums.add(imported)
	}
	return Metric(metric).Final(sums)
}

func metricSupportsImported(metric api.Metric, dimensions []string) bool {
	switch metric {
	case api.Visitors, api.Visits, api.Pageviews, api.ViewsPerVisit, api.BounceRate, api.VisitDuration:
		return true
	case api.Events:
		return len(dimensions) == 1 && dimensions[0] != "time"
	default:
		return false
	}
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
	s.Exits += other.Exits
	s.EngagedN += other.EngagedN
	s.ScrollSum += other.ScrollSum
	s.ScrollN += other.ScrollN
	s.Events += other.Events
	s.Converted += other.Converted
	s.RevenueMicros += other.RevenueMicros
	s.RevenueEvents += other.RevenueEvents
}

func isPageMetric(metric api.Metric) bool {
	return metric == api.Metric(ExitRate) || metric == api.Metric(TimeOnPage)
}

func hasPageMetric(metrics []api.Metric) bool {
	for _, metric := range metrics {
		if isPageMetric(metric) {
			return true
		}
	}
	return false
}

func isRevenueMetric(metric api.Metric) bool {
	return metric == api.TotalRevenue || metric == api.AverageRevenue
}

func hasRevenueMetric(metrics []api.Metric) bool {
	for _, metric := range metrics {
		if isRevenueMetric(metric) {
			return true
		}
	}
	return false
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
