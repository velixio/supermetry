package query

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"panel/internal/api"
	"panel/internal/dict"
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
	if hasCustomDimension(req.Dimensions) || hasRevenueMetric(req.Metrics) {
		return buildCustomHotSQL(req, part)
	}
	builder := &sqlBuilder{}
	pageDimension := containsAny(req.Dimensions, "page", "path")
	pageFilters, remainingFilters := splitPageFilters(req.Filters)
	specialFilters, otherFilters := splitSpecialFilters(remainingFilters)
	from := "sessions s"
	pageAlias := ""
	if pageDimension || len(pageFilters) > 0 {
		conditions, err := filterConditions(builder, pageFilters, func(string) (string, *dict.Kind, error) {
			return "e.path_id", dictionaryKind(dict.Path), nil
		})
		if err != nil {
			return "", nil, err
		}
		group := ""
		selectPath := ""
		if pageDimension {
			selectPath = "e.path_id, "
			group = " group by e.path_id"
		}
		from += ` cross join lateral (
  select ` + selectPath + `count(*) filter (where e.kind=0)::int8 as pageviews,
         coalesce(sum(e.engagement_ms) filter (where e.kind=2),0)::int8 as engagement_ms,
         coalesce(sum(e.scroll_pct) filter (where e.kind=2 and e.scroll_pct>0),0)::int8 as scroll_sum,
         count(*) filter (where e.kind=2 and e.scroll_pct>0)::int8 as scroll_n
  from events e where e.site_id=s.site_id and e.session_id=s.session_id` + andSQL(conditions) + group + `
  having count(*) filter (where e.kind=0) > 0
) pm`
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
		case "browser":
			return "s.browser_id", dictionaryKind(dict.Browser), nil
		case "os":
			return "s.os_id", dictionaryKind(dict.OS), nil
		case "entry":
			return "s.entry_path", dictionaryKind(dict.Path), nil
		case "exit":
			return "s.exit_path", dictionaryKind(dict.Path), nil
		case "utm_source":
			return "s.source_id", dictionaryKind(dict.Source), nil
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
	specialConditions, err := hotSpecialConditions(builder, specialFilters)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, specialConditions...)
	where := "s.site_id=" + builder.arg(req.SiteID) + " and s.local_day between " + builder.arg(part.From) + "::date and " + builder.arg(part.To) + "::date" + andSQL(conditions)

	pageviews := "s.pageviews"
	engagement := "s.engagement_ms"
	scrollSum := `coalesce((select sum(e.scroll_pct)::int8 from events e where e.site_id=s.site_id and e.session_id=s.session_id and e.kind=2 and e.scroll_pct>0),0)`
	scrollN := `(select count(*)::int8 from events e where e.site_id=s.site_id and e.session_id=s.session_id and e.kind=2 and e.scroll_pct>0)`
	if pageAlias != "" {
		pageviews = pageAlias + ".pageviews"
		engagement = pageAlias + ".engagement_ms"
		scrollSum = pageAlias + ".scroll_sum"
		scrollN = pageAlias + ".scroll_n"
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
		"coalesce(sum(" + scrollSum + "),0)::int8",
		"coalesce(sum(" + scrollN + "),0)::int8",
		"coalesce(sum(s.events),0)::int8",
		converted,
		"0::int8",
		"0::int8",
	}
	return aggregateSQL(dimensions, sums, from, where), builder.args, nil
}

func buildCustomHotSQL(req Normalized, part Part) (string, []any, error) {
	builder := &sqlBuilder{}
	baseResolver := customResolver("e")
	resolver := func(dimension string) (string, *dict.Kind, error) {
		if dimension == "time" {
			return "e.local_day", nil, nil
		}
		if dimension == "hour" {
			location := "UTC"
			if req.Location != nil {
				location = req.Location.String()
			}
			return "to_char(date_trunc('hour', e.ts at time zone " + builder.arg(location) + "), 'YYYY-MM-DD\"T\"HH24:00:00')", nil, nil
		}
		return baseResolver(dimension)
	}
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
	where := "e.site_id=" + builder.arg(req.SiteID) + " and e.local_day between " + builder.arg(part.From) + "::date and " + builder.arg(part.To) + "::date and e.kind=1" + andSQL(conditions)
	visitors := "count(distinct (e.local_day,e.visitor_id))::int8"
	sums := []string{
		visitors, "count(distinct e.session_id)::int8", "0::int8", "0::int8", "0::int8", "0::int8",
		"0::int8", "0::int8", "count(*)::int8", visitors,
		"coalesce(sum(e.revenue_micros),0)::int8",
		"count(*) filter (where e.revenue_micros>0)::int8",
	}
	return aggregateSQL(dimensions, sums, "events e", where), builder.args, nil
}

func hotSpecialConditions(builder *sqlBuilder, filters []api.Filter) ([]string, error) {
	conditions := make([]string, 0, len(filters))
	for _, filter := range filters {
		switch {
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
