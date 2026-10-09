package query

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"panel/internal/api"
	"panel/internal/dict"
	platformgoals "panel/internal/platform/goals"
)

func (e *Engine) queryHot(ctx context.Context, req Normalized, part Part) ([]resultRow, error) {
	querySQL, args, err := buildHotSQL(req, part)
	if err != nil {
		return nil, err
	}
	rows, err := e.executeAggregate(ctx, querySQL, args, req.Dimensions)
	if err != nil {
		return nil, fmt.Errorf("query hot tier: %w", err)
	}
	return rows, nil
}

func buildHotSQL(req Normalized, part Part) (string, []any, error) {
	if hasCustomDimension(req.Dimensions) || hasRevenueMetric(req.Metrics) || req.EventAttribution {
		return buildCustomHotSQL(req, part)
	}
	builder := &sqlBuilder{}
	siteArg := builder.arg(req.SiteID)
	pageDimension := containsAny(req.Dimensions, "page", "path")
	pageFilters, remainingFilters := splitPageFilters(req.Filters)
	specialFilters, otherFilters := splitSpecialFilters(remainingFilters)
	from := "sessions s"
	ctes := make([]string, 0, 2)
	pageAlias := ""
	if pageDimension || len(pageFilters) > 0 {
		conditions, err := filterConditions(builder, pageFilters, func(string) (string, *dict.Kind, error) {
			return "pe.path_id", dictionaryKind(dict.Path), nil
		})
		if err != nil {
			return "", nil, err
		}
		group := "pe.site_id, pe.session_id"
		selectPath := ""
		if pageDimension {
			selectPath = "pe.path_id, "
			group += ", pe.path_id"
		}
		engagement, engagedN := "0::int8", "0::int8"
		if containsMetric(req.Metrics, string(EngagementTime)) || containsMetric(req.Metrics, string(TimeOnPage)) {
			engagement = "coalesce(sum(pe.engagement_ms) filter (where pe.kind=2),0)::int8"
		}
		if containsMetric(req.Metrics, string(TimeOnPage)) {
			engagedN = "case when count(*) filter (where pe.kind=2 and pe.engagement_ms>0)>0 then 1 else 0 end::int8"
		}
		scrollSum, scrollN := "0::int8", "0::int8"
		if containsMetric(req.Metrics, string(ScrollDepth)) {
			scrollSum = "coalesce(sum(pe.scroll_pct) filter (where pe.kind=2 and pe.scroll_pct>0),0)::int8"
			scrollN = "count(*) filter (where pe.kind=2 and pe.scroll_pct>0)::int8"
		}
		pathIDs := "null::int4[]"
		if !pageDimension && containsMetric(req.Metrics, string(ExitRate)) {
			pathIDs = "array_agg(distinct pe.path_id) filter (where pe.kind=0)"
		}
		startArg, endArg := hotEventRangeArgs(builder, part)
		ctes = append(ctes, `page_event_stats as (
  select pe.site_id, pe.session_id, `+selectPath+`
         count(*) filter (where pe.kind=0)::int8 as pageviews,
         `+engagement+` as engagement_ms,
         `+engagedN+` as engaged_n,
         `+scrollSum+` as scroll_sum,
         `+scrollN+` as scroll_n,
         `+pathIDs+` as path_ids
  from events pe
  where pe.site_id=`+siteArg+` and pe.ts>=`+startArg+` and pe.ts<`+endArg+andSQL(conditions)+`
  group by `+group+`
  having count(*) filter (where pe.kind=0)>0
)`)
		from += " join page_event_stats pm on pm.site_id=s.site_id and pm.session_id=s.session_id"
		pageAlias = "pm"
	}

	resolver := func(dimension string) (string, *dict.Kind, error) {
		switch dimension {
		case "page", "path":
			if pageAlias == "" {
				return "", nil, fmt.Errorf("page dimension requires event rows")
			}
			return "pm.path_id", dictionaryKind(dict.Path), nil
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
			return "s.local_day", nil, nil
		case "hour":
			location := "UTC"
			if req.Location != nil {
				location = req.Location.String()
			}
			return "to_char(date_trunc('hour', s.start at time zone " + builder.arg(location) + "), 'YYYY-MM-DD\"T\"HH24:00:00')", nil, nil
		default:
			return "", nil, fmt.Errorf("dimension %q is not available from hot sessions", dimension)
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
	eventStartArg, eventEndArg := "", ""
	if len(specialFilters) > 0 {
		eventStartArg, eventEndArg = hotEventRangeArgs(builder, part)
	}
	specialConditions, err := hotSpecialSemiJoinConditions(builder, specialFilters, req.GoalDefinitions, siteArg, eventStartArg, eventEndArg)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, specialConditions...)
	where := "s.site_id=" + siteArg + " and s.local_day between " + builder.arg(dateParam(part.From)) + "::date and " + builder.arg(dateParam(part.To)) + "::date" + andSQL(conditions)

	pageviews := "s.pageviews"
	engagement := "s.engagement_ms"
	exits := "count(*) filter (where s.pageviews>0)::int8"
	engagedN := "0::int8"
	scrollSum := "0::int8"
	scrollN := "0::int8"
	if pageAlias == "" && (containsMetric(req.Metrics, string(TimeOnPage)) || containsMetric(req.Metrics, string(ScrollDepth))) {
		startArg, endArg := hotEventRangeArgs(builder, part)
		engagedExpr, scrollSumExpr, scrollNExpr := "0::int8", "0::int8", "0::int8"
		if containsMetric(req.Metrics, string(TimeOnPage)) {
			engagedExpr = "case when count(*) filter (where es.kind=2 and es.engagement_ms>0)>0 then 1 else 0 end::int8"
		}
		if containsMetric(req.Metrics, string(ScrollDepth)) {
			scrollSumExpr = "coalesce(sum(es.scroll_pct) filter (where es.kind=2 and es.scroll_pct>0),0)::int8"
			scrollNExpr = "count(*) filter (where es.kind=2 and es.scroll_pct>0)::int8"
		}
		ctes = append(ctes, `event_session_stats as (
  select es.site_id, es.session_id, `+engagedExpr+` as engaged_n,
         `+scrollSumExpr+` as scroll_sum, `+scrollNExpr+` as scroll_n
  from events es
  where es.site_id=`+siteArg+` and es.ts>=`+startArg+` and es.ts<`+endArg+` and es.kind=2
  group by es.site_id, es.session_id
)`)
		from += " left join event_session_stats es on es.site_id=s.site_id and es.session_id=s.session_id"
		engagedN = "coalesce(sum(es.engaged_n),0)::int8"
		scrollSum = "coalesce(es.scroll_sum,0)"
		scrollN = "coalesce(es.scroll_n,0)"
	}
	if pageAlias != "" {
		pageviews = pageAlias + ".pageviews"
		engagement = pageAlias + ".engagement_ms"
		exits = "count(*) filter (where s.exit_path=" + pageAlias + ".path_id)::int8"
		if !pageDimension {
			// No page grouping: a session exits "on the filtered pages" when its
			// exit page is one of the pages that matched the filter.
			exits = "count(*) filter (where s.exit_path=any(" + pageAlias + ".path_ids))::int8"
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
		converted = "count(distinct (s.local_day, s.visitor_id))::int8"
	}
	sums := []string{
		"count(distinct (s.local_day, s.visitor_id))::int8",
		"count(*)::int8",
		"coalesce(sum(" + pageviews + "),0)::int8",
		"count(*) filter (where s.bounce)::int8",
		"coalesce(sum(round(extract(epoch from (s.last-s.start))*1000)),0)::int8",
		"coalesce(sum(" + engagement + "),0)::int8",
		exits,
		engagedN,
		"coalesce(sum(" + scrollSum + "),0)::int8",
		"coalesce(sum(" + scrollN + "),0)::int8",
		"coalesce(sum(s.events),0)::int8",
		converted,
		"0::int8",
		"0::int8",
	}
	querySQL := aggregateSQL(dimensions, sums, from, where)
	if len(ctes) > 0 {
		querySQL = "with " + strings.Join(ctes, ",\n") + " " + querySQL
	}
	return querySQL, builder.args, nil
}

func hotEventRangeArgs(builder *sqlBuilder, part Part) (string, string) {
	return builder.arg(part.From.AddDate(0, 0, -1)), builder.arg(part.To.AddDate(0, 0, 2))
}

func buildCustomHotSQL(req Normalized, part Part) (string, []any, error) {
	builder := &sqlBuilder{}
	baseResolver := customResolver("e")
	resolver := func(dimension string) (string, *dict.Kind, error) {
		switch dimension {
		case "browser_version":
			return "s.browser_ver_id", dictionaryKind(dict.BrowserVersion), nil
		case "os_version":
			return "s.os_ver_id", dictionaryKind(dict.OSVersion), nil
		case "screen_size":
			return "s.screen_bucket", nil, nil
		case "language":
			return "s.language_id", dictionaryKind(dict.Language), nil
		case "hostname":
			return "s.hostname_id", dictionaryKind(dict.Hostname), nil
		case "time":
			return "e.local_day", nil, nil
		case "hour":
			location := "UTC"
			if req.Location != nil {
				location = req.Location.String()
			}
			return "to_char(date_trunc('hour', e.ts at time zone " + builder.arg(location) + "), 'YYYY-MM-DD\"T\"HH24:00:00')", nil, nil
		default:
			return baseResolver(dimension)
		}
	}
	dimensions, err := dimensionExpressions(req.Dimensions, resolver)
	if err != nil {
		return "", nil, err
	}
	conditions, err := customFilterConditions(builder, req, resolver, "e")
	if err != nil {
		return "", nil, err
	}
	presentConditions, err := presentDimensionConditions(req.Dimensions, resolver)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, presentConditions...)
	where := "e.site_id=" + builder.arg(req.SiteID) + " and e.local_day between " + builder.arg(dateParam(part.From)) + "::date and " + builder.arg(dateParam(part.To)) + "::date and e.kind=1" + andSQL(conditions)
	visitors := "count(distinct (e.local_day,e.visitor_id))::int8"
	sums := []string{
		visitors, "count(distinct e.session_id)::int8", "0::int8", "0::int8", "0::int8", "0::int8", "0::int8", "0::int8",
		"0::int8", "0::int8", "count(*)::int8", visitors,
		"coalesce(sum(e.revenue_micros),0)::int8",
		"count(*) filter (where e.revenue_micros>0)::int8",
	}
	from := "events e"
	if customQueryNeedsSession(req) {
		from += " join sessions s on s.site_id=e.site_id and s.session_id=e.session_id"
	}
	return aggregateSQL(dimensions, sums, from, where), builder.args, nil
}

func hotSpecialSemiJoinConditions(builder *sqlBuilder, filters []api.Filter, definitions map[int64]platformgoals.Goal, siteArg, startArg, endArg string) ([]string, error) {
	conditions := make([]string, 0, len(filters))
	for _, filter := range filters {
		var predicate string
		var err error
		switch {
		case filter.Dimension == "goal_id":
			id, parseErr := strconv.ParseInt(filter.Values[0], 10, 64)
			if parseErr != nil {
				return nil, errors.New(`invalid filter "goal_id"`)
			}
			goal, ok := definitions[id]
			if !ok {
				return nil, errors.New(`invalid filter "goal_id"`)
			}
			predicate, err = hotGoalEventPredicate(builder, goal, "fe")
		case filter.Dimension == "event" || filter.Dimension == "goal":
			filter.Dimension = "event"
			matches, matchErr := filterConditions(builder, []api.Filter{filter}, func(string) (string, *dict.Kind, error) {
				return "fe.name_id", dictionaryKind(dict.EventName), nil
			})
			err = matchErr
			if err == nil {
				predicate = "fe.kind=1 and " + matches[0]
			}
		case strings.HasPrefix(filter.Dimension, "prop:"):
			key := strings.TrimPrefix(filter.Dimension, "prop:")
			if key == "" {
				return nil, errors.New("property filter requires a key")
			}
			keyArg := builder.arg(key)
			predicate, err = textMatch(builder, "fe.props ->> "+keyArg, filter)
			if err == nil {
				predicate = "fe.kind=1 and fe.props ? " + keyArg + " and " + predicate
			}
		}
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, "s.session_id in (select fe.session_id from events fe where fe.site_id="+siteArg+" and fe.ts>="+startArg+" and fe.ts<"+endArg+" and "+predicate+")")
	}
	return conditions, nil
}

func customQueryNeedsSession(req Normalized) bool {
	for _, dimension := range req.Dimensions {
		if isSessionOnlyDimension(dimension) {
			return true
		}
	}
	for _, filter := range req.Filters {
		if isSessionOnlyDimension(filter.Dimension) {
			return true
		}
	}
	return false
}

func isSessionOnlyDimension(dimension string) bool {
	switch dimension {
	case "browser_version", "os_version", "screen_size", "language", "hostname":
		return true
	default:
		return false
	}
}

func hotSpecialConditions(builder *sqlBuilder, filters []api.Filter, goalDefinitions ...map[int64]platformgoals.Goal) ([]string, error) {
	conditions := make([]string, 0, len(filters))
	var definitions map[int64]platformgoals.Goal
	if len(goalDefinitions) > 0 {
		definitions = goalDefinitions[0]
	}
	for _, filter := range filters {
		switch {
		case filter.Dimension == "goal_id":
			condition, err := goalFilterCondition(builder, filter, tierHot, definitions)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, condition)
		case filter.Dimension == "event" || filter.Dimension == "goal":
			filter.Dimension = "event"
			matches, err := filterConditions(builder, []api.Filter{filter}, func(string) (string, *dict.Kind, error) {
				return "ce.name_id", dictionaryKind(dict.EventName), nil
			})
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, "exists (select 1 from events ce where ce.site_id=s.site_id and ce.session_id=s.session_id and ce.kind=1 and "+matches[0]+")")
		case strings.HasPrefix(filter.Dimension, "prop:"):
			key := strings.TrimPrefix(filter.Dimension, "prop:")
			if key == "" {
				return nil, errors.New("property filter requires a key")
			}
			keyArg := builder.arg(key)
			match, err := textMatch(builder, "ce.props ->> "+keyArg, filter)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, "exists (select 1 from events ce where ce.site_id=s.site_id and ce.session_id=s.session_id and ce.kind=1 and ce.props ? "+keyArg+" and "+match+")")
		}
	}
	return conditions, nil
}
