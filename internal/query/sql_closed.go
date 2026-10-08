package query

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"

	"panel/internal/api"
	"panel/internal/dict"
	platformgoals "panel/internal/platform/goals"
)

const queryTimeout = 10 * time.Second

type sqlBuilder struct {
	args []any
}

func (b *sqlBuilder) arg(value any) string {
	b.args = append(b.args, value)
	return "$" + strconv.Itoa(len(b.args))
}

func dateParam(value time.Time) pgtype.Date {
	return pgtype.Date{Time: civilDate(value), Valid: true}
}

func dateParams(values []time.Time) []pgtype.Date {
	result := make([]pgtype.Date, len(values))
	for index, value := range values {
		result[index] = dateParam(value)
	}
	return result
}

type dimensionDef struct {
	kind *dict.Kind
}

func dictionaryKind(kind dict.Kind) *dict.Kind { return &kind }

var dimensionDefs = map[string]dimensionDef{
	"page":            {kind: dictionaryKind(dict.Path)},
	"path":            {kind: dictionaryKind(dict.Path)},
	"source":          {kind: dictionaryKind(dict.Source)},
	"channel":         {},
	"browser":         {kind: dictionaryKind(dict.Browser)},
	"browser_version": {kind: dictionaryKind(dict.BrowserVersion)},
	"os":              {kind: dictionaryKind(dict.OS)},
	"os_version":      {kind: dictionaryKind(dict.OSVersion)},
	"screen_size":     {},
	"language":        {kind: dictionaryKind(dict.Language)},
	"hostname":        {kind: dictionaryKind(dict.Hostname)},
	"entry":           {kind: dictionaryKind(dict.Path)},
	"exit":            {kind: dictionaryKind(dict.Path)},
	"utm_source":      {kind: dictionaryKind(dict.Source)},
	"utm_medium":      {kind: dictionaryKind(dict.Medium)},
	"utm_campaign":    {kind: dictionaryKind(dict.Campaign)},
	"event":           {kind: dictionaryKind(dict.EventName)},
	"goal":            {kind: dictionaryKind(dict.EventName)},
	"region":          {kind: dictionaryKind(dict.Region)},
	"city":            {kind: dictionaryKind(dict.City)},
	"country":         {},
	"device":          {},
	"time":            {},
}

func (e *Engine) queryClosed(ctx context.Context, req Normalized, part Part) ([]resultRow, error) {
	return e.queryClosedChunks(ctx, req, part, buildClosedSQL, "closed tier")
}

type closedSQLBuilder func(Normalized, Part) (string, []any, error)

func (e *Engine) queryClosedChunks(ctx context.Context, req Normalized, part Part, build closedSQLBuilder, source string) ([]resultRow, error) {
	ranges := splitDateRange(part.From, part.To, 8)
	var (
		mu   sync.Mutex
		rows []resultRow
	)
	group, groupCtx := errgroup.WithContext(ctx)
	for _, dateRange := range ranges {
		dateRange := dateRange
		group.Go(func() error {
			if err := globalQuerySlots.Acquire(groupCtx, 1); err != nil {
				return err
			}
			defer globalQuerySlots.Release(1)
			chunk := part
			chunk.From, chunk.To = dateRange[0], dateRange[1]
			querySQL, args, err := build(req, chunk)
			if err != nil {
				return err
			}
			chunkRows, err := e.executeAggregate(groupCtx, querySQL, args, req.Dimensions)
			if err != nil {
				return fmt.Errorf("query %s: %w", source, err)
			}
			mu.Lock()
			rows = append(rows, chunkRows...)
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return mergeRows(rows), nil
}

func buildClosedSQL(req Normalized, part Part) (string, []any, error) {
	if hasCustomDimension(req.Dimensions) || hasRevenueMetric(req.Metrics) {
		return buildCustomClosedSQL(req, part)
	}
	builder := &sqlBuilder{}
	siteArg := builder.arg(req.SiteID)
	fromArg := builder.arg(dateParam(part.From))
	toArg := builder.arg(dateParam(part.To))
	pageDimension := containsAny(req.Dimensions, "page", "path")
	pageFilters, remainingFilters := splitPageFilters(req.Filters)
	specialFilters, otherFilters := splitSpecialFilters(remainingFilters)

	from := "sessions_closed s"
	pageAlias := ""
	pageFilterOnly := false
	if pageDimension {
		conditions, err := filterConditions(builder, pageFilters, func(string) (string, *dict.Kind, error) {
			return "u.code >> 2", dictionaryKind(dict.Path), nil
		})
		if err != nil {
			return "", nil, err
		}
		from += ` cross join lateral (
  select u.code >> 2 as path_id, count(*)::int8 as pageviews,
         coalesce(sum(u.engagement),0)::int8 as engagement_ms,
         case when count(*) filter (where u.engagement>0)>0 then 1 else 0 end::int8 as engaged_n,
         coalesce(sum(u.scroll) filter (where u.scroll > 0),0)::int8 as scroll_sum,
         count(*) filter (where u.scroll > 0)::int8 as scroll_n
  from unnest(s.seq, s.seq_engage, s.seq_scroll) with ordinality as u(code, engagement, scroll, ord)
  where u.code > 0` + andSQL(conditions) + ` group by u.code >> 2
) pm`
		pageAlias = "pm"
	} else if len(pageFilters) > 0 {
		conditions, err := filterConditions(builder, pageFilters, func(string) (string, *dict.Kind, error) {
			return "u.code >> 2", dictionaryKind(dict.Path), nil
		})
		if err != nil {
			return "", nil, err
		}
		from += ` cross join lateral (
  select count(*)::int8 as pageviews,
         coalesce(sum(u.engagement),0)::int8 as engagement_ms,
         case when count(*) filter (where u.engagement>0)>0 then 1 else 0 end::int8 as engaged_n,
         coalesce(sum(u.scroll) filter (where u.scroll > 0),0)::int8 as scroll_sum,
         count(*) filter (where u.scroll > 0)::int8 as scroll_n,
         max(case when (u.code >> 2) = s.exit_path then 1 else 0 end)::int8 as exits
  from unnest(s.seq, s.seq_engage, s.seq_scroll) as u(code, engagement, scroll)
  where u.code > 0` + andSQL(conditions) + `
  having count(*) > 0
) pm`
		pageAlias = "pm"
		pageFilterOnly = true
	}

	resolver := func(dimension string) (string, *dict.Kind, error) {
		switch dimension {
		case "page", "path":
			if pageAlias == "" {
				return "", nil, fmt.Errorf("page dimension requires page sequence")
			}
			return pageAlias + ".path_id", dictionaryKind(dict.Path), nil
		case "source":
			return "s.source_id", dictionaryKind(dict.Source), nil
		case "channel":
			return channelSQL("s.source_id", "s.medium_id"), nil, nil
		case "browser":
			return "s.browser_id", dictionaryKind(dict.Browser), nil
		case "browser_version":
			return "s.browser_ver_id", dictionaryKind(dict.BrowserVersion), nil
		case "os":
			return "s.os_id", dictionaryKind(dict.OS), nil
		case "os_version":
			return "s.os_ver_id", dictionaryKind(dict.OSVersion), nil
		case "screen_size":
			return "s.screen_bucket", nil, nil
		case "language":
			return "s.language_id", dictionaryKind(dict.Language), nil
		case "hostname":
			return "s.hostname_id", dictionaryKind(dict.Hostname), nil
		case "entry":
			return "s.entry_path", dictionaryKind(dict.Path), nil
		case "exit":
			return "s.exit_path", dictionaryKind(dict.Path), nil
		case "utm_source":
			return "s.utm_source_id", dictionaryKind(dict.Source), nil
		case "utm_medium":
			return "s.medium_id", dictionaryKind(dict.Medium), nil
		case "utm_campaign":
			return "s.campaign_id", dictionaryKind(dict.Campaign), nil
		case "country":
			return "s.country", nil, nil
		case "region":
			return "s.region_id", dictionaryKind(dict.Region), nil
		case "city":
			return "s.city_id", dictionaryKind(dict.City), nil
		case "device":
			return "s.device", nil, nil
		case "time":
			return "s.day", nil, nil
		default:
			return "", nil, fmt.Errorf("dimension %q is not available from sessions_closed", dimension)
		}
	}
	dimensions, err := dimensionExpressions(req.Dimensions, resolver)
	if err != nil {
		return "", nil, err
	}
	conditions, err := filterConditions(builder, otherFilters, resolver)
	if err != nil {
		return "", nil, err
	}
	presentConditions, err := presentDimensionConditions(req.Dimensions, resolver)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, presentConditions...)
	specialConditions, err := closedSpecialConditions(builder, specialFilters, req.GoalDefinitions)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, specialConditions...)
	where := "s.site_id=" + siteArg + " and s.day between " + fromArg + "::date and " + toArg + "::date" + andSQL(conditions)

	pageviews := "s.pageviews"
	engagement := "s.engagement_ms"
	exits := "count(*) filter (where s.pageviews>0)::int8"
	engagedN := `count(*) filter (where exists (
  select 1 from unnest(s.seq_engage) engagement where engagement>0
))::int8`
	scrollSum := `coalesce((select sum(value)::int8 from unnest(s.seq_scroll) value where value > 0),0)`
	scrollN := `(select count(*)::int8 from unnest(s.seq_scroll) value where value > 0)`
	if pageAlias != "" {
		pageviews = pageAlias + ".pageviews"
		engagement = pageAlias + ".engagement_ms"
		exits = "count(*) filter (where s.exit_path=" + pageAlias + ".path_id)::int8"
		if pageFilterOnly {
			// No page grouping: a session exits "on the filtered pages" when its
			// exit page is one of the pages that matched the filter.
			exits = "coalesce(sum(" + pageAlias + ".exits),0)::int8"
		}
		engagedN = "coalesce(sum(" + pageAlias + ".engaged_n),0)::int8"
		scrollSum = pageAlias + ".scroll_sum"
		scrollN = pageAlias + ".scroll_n"
	}
	if pageAlias == "" && containsAny(req.Dimensions, "entry") {
		exits = "count(*) filter (where s.exit_path=s.entry_path)::int8"
	}
	converted := "0::int8"
	if hasEventFilter(specialFilters) {
		converted = "count(distinct (s.day, s.visitor_id))::int8"
	}
	sums := []string{
		"count(distinct (s.day, s.visitor_id))::int8",
		"count(*)::int8",
		"coalesce(sum(" + pageviews + "),0)::int8",
		"count(*) filter (where s.pageviews=1 and s.custom=0)::int8",
		"coalesce(sum(s.duration_ms),0)::int8",
		"coalesce(sum(" + engagement + "),0)::int8",
		exits,
		engagedN,
		"coalesce(sum(" + scrollSum + "),0)::int8",
		"coalesce(sum(" + scrollN + "),0)::int8",
		"coalesce(sum(s.custom),0)::int8",
		converted,
		"0::int8",
		"0::int8",
	}
	return aggregateSQL(dimensions, sums, from, where), builder.args, nil
}

func buildCustomClosedSQL(req Normalized, part Part) (string, []any, error) {
	builder := &sqlBuilder{}
	resolver := customResolver("c")
	dimensions, err := dimensionExpressions(req.Dimensions, resolver)
	if err != nil {
		return "", nil, err
	}
	conditions, err := filterConditions(builder, req.Filters, resolver)
	if err != nil {
		return "", nil, err
	}
	presentConditions, err := presentDimensionConditions(req.Dimensions, resolver)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, presentConditions...)
	where := "c.site_id=" + builder.arg(req.SiteID) + " and c.day between " + builder.arg(dateParam(part.From)) + "::date and " + builder.arg(dateParam(part.To)) + "::date" + andSQL(conditions)
	visitors := "count(distinct (c.day,c.visitor_id))::int8"
	sums := []string{
		visitors, "0::int8", "0::int8", "0::int8", "0::int8", "0::int8", "0::int8", "0::int8",
		"0::int8", "0::int8", "count(*)::int8", visitors,
		"coalesce(sum(c.revenue_micros),0)::int8",
		"count(*) filter (where c.revenue_micros>0)::int8",
	}
	return aggregateSQL(dimensions, sums, "custom_closed c", where), builder.args, nil
}

func customResolver(alias string) dimensionResolver {
	return func(dimension string) (string, *dict.Kind, error) {
		prefix := alias + "."
		switch {
		case dimension == "event" || dimension == "goal":
			return prefix + "name_id", dictionaryKind(dict.EventName), nil
		case dimension == "revenue_currency":
			return prefix + "revenue_currency", nil, nil
		case strings.HasPrefix(dimension, "prop:"):
			key := strings.TrimPrefix(dimension, "prop:")
			if key == "" {
				return "", nil, errors.New("property dimension requires a key")
			}
			return prefix + "props ->> '" + strings.ReplaceAll(key, "'", "''") + "'", nil, nil
		case dimension == "page" || dimension == "path":
			return prefix + "path_id", dictionaryKind(dict.Path), nil
		case dimension == "source":
			return prefix + "source_id", dictionaryKind(dict.Source), nil
		case dimension == "channel":
			return channelSQL(prefix+"source_id", prefix+"medium_id"), nil, nil
		case dimension == "utm_source":
			return prefix + "utm_source_id", dictionaryKind(dict.Source), nil
		case dimension == "utm_medium":
			return prefix + "medium_id", dictionaryKind(dict.Medium), nil
		case dimension == "utm_campaign":
			return prefix + "campaign_id", dictionaryKind(dict.Campaign), nil
		case dimension == "browser":
			return prefix + "browser_id", dictionaryKind(dict.Browser), nil
		case dimension == "browser_version":
			return prefix + "browser_ver_id", dictionaryKind(dict.BrowserVersion), nil
		case dimension == "os":
			return prefix + "os_id", dictionaryKind(dict.OS), nil
		case dimension == "os_version":
			return prefix + "os_ver_id", dictionaryKind(dict.OSVersion), nil
		case dimension == "screen_size":
			return prefix + "screen_bucket", nil, nil
		case dimension == "language":
			return prefix + "language_id", dictionaryKind(dict.Language), nil
		case dimension == "hostname":
			return prefix + "hostname_id", dictionaryKind(dict.Hostname), nil
		case dimension == "entry":
			return prefix + "entry_path", dictionaryKind(dict.Path), nil
		case dimension == "exit":
			return prefix + "exit_path", dictionaryKind(dict.Path), nil
		case dimension == "region":
			return prefix + "region_id", dictionaryKind(dict.Region), nil
		case dimension == "city":
			return prefix + "city_id", dictionaryKind(dict.City), nil
		case dimension == "country" || dimension == "device" || dimension == "time":
			column := dimension
			if dimension == "time" {
				column = "day"
			}
			return prefix + column, nil, nil
		default:
			return "", nil, fmt.Errorf("dimension %q is unavailable from custom events", dimension)
		}
	}
}

func hasCustomDimension(dimensions []string) bool {
	for _, dimension := range dimensions {
		if dimension == "event" || dimension == "goal" || strings.HasPrefix(dimension, "prop:") {
			return true
		}
	}
	return false
}

type dimensionResolver func(string) (column string, kind *dict.Kind, err error)

func dimensionExpressions(dimensions []string, resolver dimensionResolver) ([]string, error) {
	result := make([]string, 0, len(dimensions))
	for _, dimension := range dimensions {
		column, kind, err := resolver(dimension)
		if err != nil {
			return nil, err
		}
		result = append(result, dimensionText(column, kind, dimension))
	}
	return result, nil
}

func dimensionText(column string, kind *dict.Kind, dimension string) string {
	if kind != nil {
		return column + "::text"
	}
	switch dimension {
	case "device":
		return "case " + column + " when 0 then 'desktop' when 1 then 'mobile' when 2 then 'tablet' else " + column + "::text end"
	case "screen_size":
		return "case " + column + " when 1 then 'mobile' when 2 then 'tablet' when 3 then 'laptop' when 4 then 'desktop' when 5 then 'large' else '' end"
	case "time":
		return "to_char(" + column + ", 'YYYY-MM-DD')"
	default:
		return column + "::text"
	}
}

func filterConditions(builder *sqlBuilder, filters []api.Filter, resolver dimensionResolver) ([]string, error) {
	conditions := make([]string, 0, len(filters))
	for _, filter := range filters {
		column, kind, err := resolver(filter.Dimension)
		if err != nil {
			return nil, err
		}
		valueExpr := dimensionText(column, kind, filter.Dimension)
		if kind != nil {
			valueExpr = "fd.value"
		}
		match, err := textMatch(builder, valueExpr, filter)
		if err != nil {
			return nil, err
		}
		if kind != nil {
			match = column + " in (select fd.id from dict fd where fd.kind=" + strconv.Itoa(int(*kind)) + " and " + match + ")"
		}
		conditions = append(conditions, match)
	}
	return conditions, nil
}

func presentDimensionConditions(dimensions []string, resolver dimensionResolver) ([]string, error) {
	conditions := make([]string, 0, len(dimensions))
	for _, dimension := range dimensions {
		if dimension != "region" && dimension != "city" && dimension != "utm_source" && dimension != "utm_medium" && dimension != "utm_campaign" {
			continue
		}
		column, _, err := resolver(dimension)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, column+"<>0")
	}
	return conditions, nil
}

func textMatch(builder *sqlBuilder, expression string, filter api.Filter) (string, error) {
	switch filter.Operator {
	case api.Is:
		return expression + " = any(" + builder.arg(filter.Values) + "::text[])", nil
	case api.IsNot:
		return "not (" + expression + " = any(" + builder.arg(filter.Values) + "::text[]))", nil
	case api.Contains, api.StartsWith:
		matches := make([]string, 0, len(filter.Values))
		for _, value := range filter.Values {
			pattern := EscapeLike(value) + "%"
			if filter.Operator == api.Contains {
				pattern = "%" + pattern
			}
			matches = append(matches, expression+" like "+builder.arg(pattern)+` escape '\'`)
		}
		return "(" + strings.Join(matches, " or ") + ")", nil
	default:
		return "", fmt.Errorf("unsupported filter operator %q", filter.Operator)
	}
}

func splitPageFilters(filters []api.Filter) (page, other []api.Filter) {
	for _, filter := range filters {
		if filter.Dimension == "page" || filter.Dimension == "path" {
			page = append(page, filter)
		} else {
			other = append(other, filter)
		}
	}
	return page, other
}

func splitSpecialFilters(filters []api.Filter) (special, other []api.Filter) {
	for _, filter := range filters {
		if filter.Dimension == "event" || filter.Dimension == "goal" || filter.Dimension == "goal_id" || strings.HasPrefix(filter.Dimension, "prop:") {
			special = append(special, filter)
		} else {
			other = append(other, filter)
		}
	}
	return special, other
}

func closedSpecialConditions(builder *sqlBuilder, filters []api.Filter, goalDefinitions ...map[int64]platformgoals.Goal) ([]string, error) {
	conditions := make([]string, 0, len(filters))
	var definitions map[int64]platformgoals.Goal
	if len(goalDefinitions) > 0 {
		definitions = goalDefinitions[0]
	}
	for _, filter := range filters {
		switch {
		case filter.Dimension == "goal_id":
			condition, err := goalFilterCondition(builder, filter, tierClosed, definitions)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, condition)
		case filter.Dimension == "event" || filter.Dimension == "goal":
			filter.Dimension = "event"
			matches, err := filterConditions(builder, []api.Filter{filter}, func(string) (string, *dict.Kind, error) {
				return "-u.code", dictionaryKind(dict.EventName), nil
			})
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, "exists (select 1 from unnest(s.seq) u(code) where u.code < 0 and "+matches[0]+")")
		case strings.HasPrefix(filter.Dimension, "prop:"):
			key := strings.TrimPrefix(filter.Dimension, "prop:")
			if key == "" {
				return nil, errors.New("property filter requires a key")
			}
			match, err := textMatch(builder, "c.props ->> "+builder.arg(key), filter)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, "exists (select 1 from custom_closed c where c.site_id=s.site_id and c.day=s.day and c.visitor_id=s.visitor_id and c.props ? "+builder.arg(key)+" and "+match+")")
		}
	}
	return conditions, nil
}

func hasEventFilter(filters []api.Filter) bool {
	for _, filter := range filters {
		if filter.Dimension == "event" || filter.Dimension == "goal" || filter.Dimension == "goal_id" {
			return true
		}
	}
	return false
}

func aggregateSQL(dimensions, sums []string, from, where string) string {
	selects := append([]string(nil), dimensions...)
	selects = append(selects, sums...)
	querySQL := "select " + strings.Join(selects, ", ") + " from " + from + " where " + where
	if len(dimensions) > 0 {
		groups := make([]string, len(dimensions))
		for i := range dimensions {
			groups[i] = strconv.Itoa(i + 1)
		}
		querySQL += " group by " + strings.Join(groups, ", ")
	}
	return querySQL
}

func andSQL(conditions []string) string {
	if len(conditions) == 0 {
		return ""
	}
	return " and " + strings.Join(conditions, " and ")
}

func containsAny(values []string, candidates ...string) bool {
	for _, value := range values {
		for _, candidate := range candidates {
			if value == candidate {
				return true
			}
		}
	}
	return false
}

func splitDateRange(from, to time.Time, maximum int) [][2]time.Time {
	days := daysInclusive(from, to)
	count := max(1, min(maximum, days))
	result := make([][2]time.Time, 0, count)
	for i := range count {
		start := from.AddDate(0, 0, days*i/count)
		end := from.AddDate(0, 0, days*(i+1)/count-1)
		result = append(result, [2]time.Time{start, end})
	}
	return result
}

func (e *Engine) executeAggregate(ctx context.Context, querySQL string, args []any, dimensions []string) ([]resultRow, error) {
	var result []resultRow
	dimensionCount := len(dimensions)
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, querySQL, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				return err
			}
			if len(values) != dimensionCount+14 {
				return fmt.Errorf("aggregate returned %d columns, want %d", len(values), dimensionCount+14)
			}
			row := resultRow{dimensions: make([]string, dimensionCount)}
			for i := range dimensionCount {
				row.dimensions[i] = e.decodeDimension(dimensions[i], fmt.Sprint(values[i]))
			}
			metrics := values[dimensionCount:]
			row.sums = Sums{
				Visitors:      asInt64(metrics[0]),
				Visits:        asInt64(metrics[1]),
				Pageviews:     asInt64(metrics[2]),
				Bounces:       asInt64(metrics[3]),
				DurationMS:    asInt64(metrics[4]),
				EngagementMS:  asInt64(metrics[5]),
				Exits:         asInt64(metrics[6]),
				EngagedN:      asInt64(metrics[7]),
				ScrollSum:     asInt64(metrics[8]),
				ScrollN:       asInt64(metrics[9]),
				Events:        asInt64(metrics[10]),
				Converted:     asInt64(metrics[11]),
				RevenueMicros: asInt64(metrics[12]),
				RevenueEvents: asInt64(metrics[13]),
			}
			result = append(result, row)
		}
		return rows.Err()
	})
	return result, err
}

func (e *Engine) decodeDimension(dimension, value string) string {
	definition, ok := dimensionDefs[dimension]
	if !ok || definition.kind == nil || e.dict == nil {
		return value
	}
	id, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return value
	}
	if id == 0 {
		return ""
	}
	if decoded, found := e.dict.Value(*definition.kind, int32(id)); found {
		return decoded
	}
	return value
}

func asInt64(value any) int64 {
	switch value := value.(type) {
	case int64:
		return value
	case int32:
		return int64(value)
	case int16:
		return int64(value)
	case int:
		return int64(value)
	default:
		return 0
	}
}
