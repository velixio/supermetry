package query

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/api"
	platformgoals "panel/internal/platform/goals"
	"panel/internal/sites"
)

const (
	defaultLimit  = 100
	maxLimit      = 1000
	maxRangeDays  = 366
	maxDimensions = 3
	maxFilters    = 10
)

var validDimensions = map[string]struct{}{
	"browser":         {},
	"browser_version": {},
	"channel":         {},
	"city":            {},
	"country":         {},
	"device":          {},
	"entry":           {},
	"event":           {},
	"exit":            {},
	"goal":            {},
	"hour":            {},
	"hostname":        {},
	"language":        {},
	"os":              {},
	"os_version":      {},
	"page":            {},
	"path":            {},
	"prop":            {},
	"region":          {},
	"source":          {},
	"screen_size":     {},
	"time":            {},
	"utm_campaign":    {},
	"utm_medium":      {},
	"utm_source":      {},
}

// Normalized is a validated query with an inclusive civil-date range and
// concrete defaults. From and To encode date labels at UTC midnight; callers
// must use Location when deriving real timestamp boundaries for those dates.
type Normalized struct {
	SiteID          int32
	From            time.Time
	To              time.Time
	Location        *time.Location
	Metrics         []api.Metric
	Dimensions      []string
	Filters         []api.Filter
	OrderBy         []api.OrderBy
	Limit           int
	Cursor          *string
	Compare         *api.QueryRequestCompare
	IncludeImported bool
	GoalDefinitions map[int64]platformgoals.Goal
	// EventAttribution keeps event-scoped attribution semantics when derived
	// queries (notably totals) remove their displayed dimensions.
	EventAttribution bool
}

// Normalize validates req and resolves its date range in the requested time
// zone, defaulting to the site's time zone.
func Normalize(req api.QueryRequest, site sites.Site, now time.Time) (Normalized, *api.Problem) {
	location, problem := queryLocation(req.Timezone, site.TZ)
	if problem != nil {
		return Normalized{}, problem
	}
	if req.SiteId != site.ID {
		return Normalized{}, badQuery("site_id does not match the requested site")
	}
	if len(req.Metrics) == 0 {
		return Normalized{}, badQuery("at least one metric is required")
	}
	for _, metric := range req.Metrics {
		if !metric.Valid() && metric != api.Metric(ExitRate) && metric != api.Metric(TimeOnPage) {
			return Normalized{}, badQuery(fmt.Sprintf("unknown metric %q", metric))
		}
	}

	from, to, problem := normalizeRange(req, location, now)
	if problem != nil {
		return Normalized{}, problem
	}
	if from.After(to) {
		return Normalized{}, badQuery("date_range.from must not be after date_range.to")
	}
	allPeriod := req.Period != nil && *req.Period == api.Period("all")
	if !allPeriod && daysInclusive(from, to) > maxRangeDays {
		return Normalized{}, badQuery("date range must not exceed 366 days")
	}

	dimensions := copyValues(req.Dimensions)
	if len(dimensions) > maxDimensions {
		return Normalized{}, badQuery("at most 3 dimensions are allowed")
	}
	for _, dimension := range dimensions {
		if !knownDimension(dimension) {
			return Normalized{}, badQuery(fmt.Sprintf("unknown dimension %q", dimension))
		}
	}
	for _, metric := range req.Metrics {
		if !pageMetricSupportsDimensions(metric, dimensions) {
			return Normalized{}, badQuery(fmt.Sprintf("metric %q is unsupported for the requested dimensions", metric))
		}
	}

	filters := copyValues(req.Filters)
	if err := ValidateFilters(filters); err != nil {
		return Normalized{}, badQuery(err.Error())
	}
	for _, filter := range filters {
		if filter.Dimension == "goal_id" {
			if filter.Operator != api.Is || len(filter.Values) != 1 {
				return Normalized{}, badQuery("goal_id requires operator is and exactly one value")
			}
			id, err := strconv.ParseInt(filter.Values[0], 10, 64)
			if err != nil || id <= 0 {
				return Normalized{}, badQuery("goal_id requires one positive numeric value")
			}
		}
		if filter.Dimension == "revenue_currency" {
			if filter.Operator != api.Is || len(filter.Values) != 1 || !validCurrency(filter.Values[0]) {
				return Normalized{}, badQuery("revenue_currency requires operator is and one uppercase 3-letter value")
			}
		}
	}
	if hasRevenueMetric(req.Metrics) {
		if !hasEventOrGoalScope(dimensions, filters) {
			return Normalized{}, badQuery("revenue metrics require an event or goal filter or dimension")
		}
	} else if filterHasDimension(filters, "revenue_currency") {
		return Normalized{}, badQuery("revenue_currency requires a revenue metric")
	}

	limit := defaultLimit
	if req.Limit != nil {
		limit = *req.Limit
	}
	if limit < 1 || limit > maxLimit {
		return Normalized{}, badQuery("limit must be between 1 and 1000")
	}
	if req.Compare != nil && !req.Compare.Valid() {
		return Normalized{}, badQuery(fmt.Sprintf("unknown comparison %q", *req.Compare))
	}

	orderBy := copyValues(req.OrderBy)
	for _, order := range orderBy {
		if order.Direction != nil && !order.Direction.Valid() {
			return Normalized{}, badQuery(fmt.Sprintf("unknown order direction %q", *order.Direction))
		}
		if !containsDimension(dimensions, order.Column) && !containsMetric(req.Metrics, order.Column) {
			return Normalized{}, badQuery(fmt.Sprintf("order_by column %q is not requested", order.Column))
		}
	}

	includeImported := req.IncludeImported == nil || *req.IncludeImported
	return Normalized{
		SiteID:           req.SiteId,
		From:             from,
		To:               to,
		Location:         location,
		Metrics:          append([]api.Metric(nil), req.Metrics...),
		Dimensions:       dimensions,
		Filters:          filters,
		OrderBy:          orderBy,
		Limit:            limit,
		Cursor:           clonePointer(req.Cursor),
		Compare:          clonePointer(req.Compare),
		IncludeImported:  includeImported,
		EventAttribution: needsEventAttribution(dimensions, filters, req.Metrics),
	}, nil
}

func needsEventAttribution(dimensions []string, filters []api.Filter, metrics []api.Metric) bool {
	if !hasCustomDimension(dimensions) && !hasRevenueMetric(metrics) {
		return false
	}
	for _, dimension := range dimensions {
		if isAttributionDimension(dimension) {
			return true
		}
	}
	for _, filter := range filters {
		if isAttributionDimension(filter.Dimension) {
			return true
		}
	}
	return false
}

func isAttributionDimension(dimension string) bool {
	switch dimension {
	case "source", "channel", "utm_source", "utm_medium", "utm_campaign":
		return true
	default:
		return false
	}
}

func pageMetricSupportsDimensions(metric api.Metric, dimensions []string) bool {
	if metric != api.Metric(ExitRate) && metric != api.Metric(TimeOnPage) {
		return true
	}
	if len(dimensions) != 1 {
		return false
	}
	if metric == api.Metric(TimeOnPage) {
		return dimensions[0] == "page" || dimensions[0] == "path"
	}
	return containsAny(dimensions, "page", "path", "entry", "exit")
}

// ValidateFilters applies the shared dashboard-query rules to a filter set.
// Saved segments call this directly so persisted filters cannot diverge from
// filters accepted by Normalize.
func ValidateFilters(filters []api.Filter) error {
	if len(filters) > maxFilters {
		return errors.New("at most 10 filters are allowed")
	}
	for _, filter := range filters {
		if !knownFilterDimension(filter.Dimension) {
			return fmt.Errorf("unknown filter dimension %q", filter.Dimension)
		}
		if !filter.Operator.Valid() {
			return fmt.Errorf("unknown filter operator %q", filter.Operator)
		}
		if len(filter.Values) == 0 {
			return fmt.Errorf("filter %q must contain at least one value", filter.Dimension)
		}
	}
	return nil
}

func queryLocation(requested *string, siteLocation *time.Location) (*time.Location, *api.Problem) {
	if requested != nil {
		location, err := time.LoadLocation(*requested)
		if err != nil {
			return nil, badQuery(fmt.Sprintf("invalid timezone %q", *requested))
		}
		return location, nil
	}
	if siteLocation == nil {
		return nil, badQuery("site timezone is not configured")
	}
	return siteLocation, nil
}

func normalizeRange(req api.QueryRequest, location *time.Location, now time.Time) (time.Time, time.Time, *api.Problem) {
	if req.DateRange != nil && req.Period != nil {
		return time.Time{}, time.Time{}, badQuery("provide either date_range or period, not both")
	}
	if req.DateRange != nil {
		from := civilDate(req.DateRange.From.Time)
		to := civilDate(req.DateRange.To.Time)
		if from.IsZero() || to.IsZero() {
			return time.Time{}, time.Time{}, badQuery("date_range.from and date_range.to are required")
		}
		return from, to, nil
	}
	if req.Period == nil {
		return time.Time{}, time.Time{}, badQuery("date_range or period is required")
	}
	period := *req.Period
	statsPeriod := period == api.Period("day") || period == api.Period("6mo") || period == api.Period("all")
	if (!period.Valid() && !statsPeriod) || period == api.Custom {
		return time.Time{}, time.Time{}, badQuery(fmt.Sprintf("unknown period %q", *req.Period))
	}

	today := civilDate(now.In(location))
	switch period {
	case api.Today, api.Period("day"):
		return today, today, nil
	case api.Yesterday:
		yesterday := today.AddDate(0, 0, -1)
		return yesterday, yesterday, nil
	case api.N7d:
		return today.AddDate(0, 0, -6), today, nil
	case api.N30d:
		return today.AddDate(0, 0, -29), today, nil
	case api.Month:
		return time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC), today, nil
	case api.LastMonth:
		startThisMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		return startThisMonth.AddDate(0, -1, 0), startThisMonth.AddDate(0, 0, -1), nil
	case api.Period("6mo"):
		return today.AddDate(0, -6, 1), today, nil
	case api.N12mo:
		return today.AddDate(-1, 0, 1), today, nil
	case api.Year:
		return time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, time.UTC), today, nil
	case api.Period("all"):
		return time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC), today, nil
	default:
		return time.Time{}, time.Time{}, badQuery(fmt.Sprintf("unknown period %q", *req.Period))
	}
}

func civilDate(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func daysInclusive(from, to time.Time) int {
	fromCivil := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	toCivil := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	return int(toCivil.Sub(fromCivil)/(24*time.Hour)) + 1
}

func knownDimension(dimension string) bool {
	if _, ok := validDimensions[dimension]; ok {
		return true
	}
	return strings.HasPrefix(dimension, "prop:")
}

func knownFilterDimension(dimension string) bool {
	return dimension == "goal_id" || dimension == "revenue_currency" || knownDimension(dimension)
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func hasEventOrGoalScope(dimensions []string, filters []api.Filter) bool {
	if containsAny(dimensions, "event", "goal") {
		return true
	}
	return filterHasDimension(filters, "event") || filterHasDimension(filters, "goal") || filterHasDimension(filters, "goal_id")
}

func containsDimension(dimensions []string, value string) bool {
	for _, dimension := range dimensions {
		if dimension == value {
			return true
		}
	}
	return false
}

func containsMetric(metrics []api.Metric, value string) bool {
	for _, metric := range metrics {
		if string(metric) == value {
			return true
		}
	}
	return false
}

func copyValues[T any](values *[]T) []T {
	if values == nil {
		return nil
	}
	return append([]T(nil), (*values)...)
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func badQuery(detail string) *api.Problem {
	return &api.Problem{
		Type:   "https://panel.dev/problems/invalid-query",
		Title:  "Invalid query",
		Status: 400,
		Detail: &detail,
	}
}

// EscapeLike escapes PostgreSQL LIKE metacharacters for use with
// LIKE ... ESCAPE '\'.
func EscapeLike(value string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
}

// WithStatementTimeout runs fn in a transaction with a transaction-local
// PostgreSQL statement timeout. Any error rolls the transaction back.
func WithStatementTimeout(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration, fn func(pgx.Tx) error) error {
	if timeout <= 0 {
		return errors.New("query timeout must be positive")
	}
	timeoutMS := max(timeout.Milliseconds(), 1)
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local statement_timeout = '"+strconv.FormatInt(timeoutMS, 10)+"ms'"); err != nil {
			return fmt.Errorf("set query statement timeout: %w", err)
		}
		return fn(tx)
	})
}

// QueryProblem maps a PostgreSQL or context query timeout to the public 503
// problem. Other errors are left for the API's general error handling.
func QueryProblem(err error) *api.Problem {
	if !isQueryTimeout(err) {
		return nil
	}
	detail := "the query exceeded its execution time limit"
	return &api.Problem{
		Type:   "https://panel.dev/problems/query-timeout",
		Title:  "Query timed out",
		Status: 503,
		Detail: &detail,
	}
}

func isQueryTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "57014"
}
