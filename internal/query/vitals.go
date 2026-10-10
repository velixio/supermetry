package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
)

const (
	webVitalsEventName      = "Web Vitals"
	maxVitalsTimeseriesDays = 366
)

type normalizedVitals struct {
	siteID         int32
	retentionClass db.RetentionClass
	from           time.Time
	to             time.Time
	fromTimestamp  time.Time
	toTimestamp    time.Time
	filters        []api.Filter
	dimension      *api.VitalsDimension
}

// Vitals returns Web Vitals aggregates from raw events. Unlike the general
// query engine this report intentionally has no rollup fallback.
func (e *Engine) Vitals(ctx context.Context, req api.VitalsRequest) (api.VitalsResponse, error) {
	if e.trackingMode != privacy.Identified {
		return api.VitalsResponse{}, api.ErrAnonymousHistory
	}
	if e == nil || e.pool == nil || e.registry == nil {
		return api.VitalsResponse{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(req.SiteId)
	if !ok {
		return api.VitalsResponse{}, fmt.Errorf("unknown site %d", req.SiteId)
	}
	now := time.Now()
	if e.now != nil {
		now = e.now()
	}
	normalized, err := normalizeVitals(req, site, now)
	if err != nil {
		return api.VitalsResponse{}, err
	}

	response := api.VitalsResponse{Breakdown: []api.VitalsBreakdown{}, Timeseries: []api.VitalsTrendPoint{}}
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		overallSQL, args, err := buildVitalsSQL(normalized, nil)
		if err != nil {
			return err
		}
		row := tx.QueryRow(ctx, overallSQL, args...)
		overall, err := scanVitalsMetrics(row)
		if err != nil {
			return fmt.Errorf("query Web Vitals totals: %w", err)
		}
		response.Overall = overall

		timeseriesSQL, args, err := buildVitalsTimeseriesSQL(normalized)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, timeseriesSQL, args...)
		if err != nil {
			return fmt.Errorf("query Web Vitals timeseries: %w", err)
		}
		byDay := make(map[string]api.VitalsTrendPoint)
		for rows.Next() {
			var day time.Time
			point, err := scanVitalsTrendPoint(rows, &day)
			if err != nil {
				rows.Close()
				return err
			}
			point.Date = openapi_types.Date{Time: civilDate(day)}
			byDay[day.Format(time.DateOnly)] = point
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for day := normalized.from; !day.After(normalized.to); day = day.AddDate(0, 0, 1) {
			key := day.Format(time.DateOnly)
			point, ok := byDay[key]
			if !ok {
				point.Date = openapi_types.Date{Time: day}
			}
			response.Timeseries = append(response.Timeseries, point)
		}

		if normalized.dimension == nil {
			return nil
		}
		breakdownSQL, args, err := buildVitalsSQL(normalized, normalized.dimension)
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, breakdownSQL, args...)
		if err != nil {
			return fmt.Errorf("query Web Vitals breakdown: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value string
			metrics, err := scanVitalsMetricsWithPrefix(rows, &value)
			if err != nil {
				return err
			}
			value = e.decodeDimension(string(*normalized.dimension), value)
			response.Breakdown = append(response.Breakdown, api.VitalsBreakdown{
				Value: value,
				Lcp:   metrics.Lcp,
				Cls:   metrics.Cls,
				Inp:   metrics.Inp,
			})
		}
		return rows.Err()
	})
	if err != nil {
		return api.VitalsResponse{}, err
	}
	return response, nil
}

func normalizeVitals(req api.VitalsRequest, site sites.Site, now time.Time) (normalizedVitals, error) {
	if req.SiteId != site.ID {
		return normalizedVitals{}, errors.New("site_id does not match the requested site")
	}
	if req.DateRange.From.Time.IsZero() || req.DateRange.To.Time.IsZero() {
		return normalizedVitals{}, errors.New("date_range.from and date_range.to are required")
	}
	from := civilDate(req.DateRange.From.Time)
	to := civilDate(req.DateRange.To.Time)
	if from.After(to) {
		return normalizedVitals{}, errors.New("date_range.from must not be after date_range.to")
	}
	if req.Dimension != nil && !req.Dimension.Valid() {
		return normalizedVitals{}, fmt.Errorf("unknown dimension %q", *req.Dimension)
	}
	filters := copyValues(req.Filters)
	if err := ValidateFilters(filters); err != nil {
		return normalizedVitals{}, err
	}
	for _, filter := range filters {
		if filter.Dimension == "revenue_currency" {
			return normalizedVitals{}, fmt.Errorf("filter dimension %q is not meaningful for Web Vitals", filter.Dimension)
		}
		if _, _, err := vitalsDimensionResolver("e", nil)(filter.Dimension); err != nil {
			return normalizedVitals{}, err
		}
	}
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
	if to.After(today) {
		return normalizedVitals{}, errors.New("date_range.to must not be after today in the site's timezone")
	}
	if site.Retention > 0 {
		oldest := today.AddDate(0, 0, -int(site.Retention)+1)
		if from.Before(oldest) {
			return normalizedVitals{}, fmt.Errorf("Web Vitals are available only within the site's %d-day raw retention window", site.Retention)
		}
	}
	days := int(to.Sub(from)/(24*time.Hour)) + 1
	if days > maxVitalsTimeseriesDays {
		return normalizedVitals{}, fmt.Errorf("Web Vitals trends support at most %d days per request", maxVitalsTimeseriesDays)
	}
	fromTimestamp := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, location)
	toTimestamp := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1)
	return normalizedVitals{
		siteID:         req.SiteId,
		retentionClass: site.Retention,
		from:           from,
		to:             to,
		fromTimestamp:  fromTimestamp,
		toTimestamp:    toTimestamp,
		filters:        filters,
		dimension:      req.Dimension,
	}, nil
}

func buildVitalsCTE(req normalizedVitals, dimension *api.VitalsDimension, daily bool) (string, []any, error) {
	builder := &sqlBuilder{}
	resolver := vitalsDimensionResolver("e", builder)
	directFilters, sessionFilters, specialFilters := splitVitalsFilters(req.filters)
	siteArg := builder.arg(req.siteID)
	retentionArg := builder.arg(req.retentionClass)
	fromArg := builder.arg(dateParam(req.from))
	toArg := builder.arg(dateParam(req.to))
	fromTimestampArg := builder.arg(req.fromTimestamp)
	toTimestampArg := builder.arg(req.toTimestamp)
	conditions, err := filterConditions(builder, directFilters, resolver)
	if err != nil {
		return "", nil, err
	}
	filterCTEs, filterConditions, err := vitalsFilterRelations(builder, req, sessionFilters, specialFilters, siteArg, retentionArg)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, filterConditions...)
	dimensionExpr := ""
	if daily {
		dimensionExpr = "e.local_day"
	} else if dimension != nil {
		column, kind, err := resolver(string(*dimension))
		if err != nil {
			return "", nil, err
		}
		dimensionExpr = dimensionText(column, kind, string(*dimension))
	}

	where := "e.site_id=" + siteArg + " and e.retention_class=" + retentionArg +
		" and e.ts>=" + fromTimestampArg + "::timestamptz and e.ts<" + toTimestampArg + "::timestamptz" +
		" and e.local_day between " + fromArg + "::date and " + toArg + "::date" +
		" and e.kind=1 and e.name_id in (select id from dict where kind=" + fmt.Sprint(dict.EventName) + " and value='" + webVitalsEventName + "')" + andSQL(conditions)
	dimensionSelect := ""
	dimensionColumn := ""
	if dimensionExpr != "" {
		name := "dimension"
		if daily {
			name = "day"
		}
		dimensionSelect = dimensionExpr + " as " + name + ","
		dimensionColumn = name + ","
	}
	ctePrefix := ""
	if len(filterCTEs) > 0 {
		ctePrefix = strings.Join(filterCTEs, ",\n") + ",\n"
	}
	cte := `with ` + ctePrefix + `raw_vitals as (
  select ` + dimensionSelect + `
    e.props->>'lcp' as lcp_raw,
    e.props->>'cls' as cls_raw,
    e.props->>'inp' as inp_raw
  from events e
  where ` + where + `
), parsed_vitals as (
  select ` + dimensionColumn + `
    case when length(lcp_raw) between 1 and 5 and lcp_raw ~ '^[0-9]+$' then lcp_raw::int end as lcp,
    case when length(cls_raw) between 1 and 5 and cls_raw ~ '^[0-9]+$' then cls_raw::int end as cls,
    case when length(inp_raw) between 1 and 5 and inp_raw ~ '^[0-9]+$' then inp_raw::int end as inp
  from raw_vitals
), vitals as (
  select ` + dimensionColumn + `
    case when lcp <= 60000 then lcp end as lcp,
    case when cls <= 10000 then cls end as cls,
    case when inp <= 60000 then inp end as inp
  from parsed_vitals
)`
	return cte, builder.args, nil
}

func buildVitalsSQL(req normalizedVitals, dimension *api.VitalsDimension) (string, []any, error) {
	cte, args, err := buildVitalsCTE(req, dimension, false)
	if err != nil {
		return "", nil, err
	}
	aggregates := `
  percentile_cont(0.75) within group (order by lcp) filter (where lcp is not null)::float8,
  count(lcp)::int8,
  coalesce(avg((lcp <= 2500)::int) filter (where lcp is not null),0)::float8,
  coalesce(avg((lcp > 2500 and lcp <= 4000)::int) filter (where lcp is not null),0)::float8,
  coalesce(avg((lcp > 4000)::int) filter (where lcp is not null),0)::float8,
  percentile_cont(0.75) within group (order by cls) filter (where cls is not null)::float8,
  count(cls)::int8,
  coalesce(avg((cls <= 100)::int) filter (where cls is not null),0)::float8,
  coalesce(avg((cls > 100 and cls <= 250)::int) filter (where cls is not null),0)::float8,
  coalesce(avg((cls > 250)::int) filter (where cls is not null),0)::float8,
  percentile_cont(0.75) within group (order by inp) filter (where inp is not null)::float8,
  count(inp)::int8,
  coalesce(avg((inp <= 200)::int) filter (where inp is not null),0)::float8,
  coalesce(avg((inp > 200 and inp <= 500)::int) filter (where inp is not null),0)::float8,
  coalesce(avg((inp > 500)::int) filter (where inp is not null),0)::float8`
	if dimension == nil {
		return cte + " select" + aggregates + " from vitals", args, nil
	}
	return cte + ` select dimension,` + aggregates + `
from vitals
where dimension <> '' and dimension <> '0'
group by dimension
order by count(lcp)+count(cls)+count(inp) desc, dimension
limit 20`, args, nil
}

func buildVitalsTimeseriesSQL(req normalizedVitals) (string, []any, error) {
	cte, args, err := buildVitalsCTE(req, nil, true)
	if err != nil {
		return "", nil, err
	}
	return cte + ` select day,
  percentile_cont(0.5) within group (order by lcp) filter (where lcp is not null)::float8,
  percentile_cont(0.75) within group (order by lcp) filter (where lcp is not null)::float8,
  percentile_cont(0.95) within group (order by lcp) filter (where lcp is not null)::float8,
  count(lcp)::int8,
  percentile_cont(0.5) within group (order by cls) filter (where cls is not null)::float8,
  percentile_cont(0.75) within group (order by cls) filter (where cls is not null)::float8,
  percentile_cont(0.95) within group (order by cls) filter (where cls is not null)::float8,
  count(cls)::int8,
  percentile_cont(0.5) within group (order by inp) filter (where inp is not null)::float8,
  percentile_cont(0.75) within group (order by inp) filter (where inp is not null)::float8,
  percentile_cont(0.95) within group (order by inp) filter (where inp is not null)::float8,
  count(inp)::int8
from vitals
group by day
order by day`, args, nil
}

func vitalsDimensionResolver(alias string, builder *sqlBuilder) dimensionResolver {
	base := customResolver(alias, builder)
	return func(dimension string) (string, *dict.Kind, error) {
		prefix := alias + "."
		switch dimension {
		case "entry", "exit":
			column := "entry_path"
			if dimension == "exit" {
				column = "exit_path"
			}
			return prefix + column, dictionaryKind(dict.Path), nil
		case "channel":
			return channelSQL(prefix+"source_id", prefix+"medium_id"), nil, nil
		case "utm_source":
			return prefix + "utm_source_id", dictionaryKind(dict.Source), nil
		case "utm_medium":
			return prefix + "medium_id", dictionaryKind(dict.Medium), nil
		case "utm_campaign":
			return prefix + "campaign_id", dictionaryKind(dict.Campaign), nil
		case "time":
			return prefix + "local_day", nil, nil
		default:
			return base(dimension)
		}
	}
}

func vitalsSessionDimensionResolver(alias string) dimensionResolver {
	return func(dimension string) (string, *dict.Kind, error) {
		switch dimension {
		case "entry":
			return alias + ".entry_path", dictionaryKind(dict.Path), nil
		case "exit":
			return alias + ".exit_path", dictionaryKind(dict.Path), nil
		default:
			return "", nil, fmt.Errorf("filter dimension %q is not available from Web Vitals sessions", dimension)
		}
	}
}

func splitVitalsFilters(filters []api.Filter) (direct, session, special []api.Filter) {
	for _, filter := range filters {
		switch {
		case filter.Dimension == "entry" || filter.Dimension == "exit":
			session = append(session, filter)
		case filter.Dimension == "event" || filter.Dimension == "goal" || strings.HasPrefix(filter.Dimension, "prop:"):
			special = append(special, filter)
		default:
			direct = append(direct, filter)
		}
	}
	return direct, session, special
}

func vitalsFilterRelations(builder *sqlBuilder, req normalizedVitals, sessionFilters, specialFilters []api.Filter, siteArg, retentionArg string) ([]string, []string, error) {
	ctes := make([]string, 0, len(sessionFilters)+len(specialFilters))
	conditions := make([]string, 0, len(sessionFilters)+len(specialFilters))
	filterFrom := req.from.AddDate(0, 0, -1)
	filterTo := req.to.AddDate(0, 0, 1)
	filterFromTimestamp := req.fromTimestamp.AddDate(0, 0, -1)
	filterToTimestamp := req.toTimestamp.AddDate(0, 0, 1)

	if len(sessionFilters) > 0 {
		matches, err := filterConditions(builder, sessionFilters, vitalsSessionDimensionResolver("s"))
		if err != nil {
			return nil, nil, err
		}
		name := "matched_filter_0"
		ctes = append(ctes, name+` as materialized (
  select distinct s.site_id,s.retention_class,s.session_id
  from sessions s
  where s.site_id=`+siteArg+` and s.retention_class=`+retentionArg+`
    and s.local_day between `+builder.arg(dateParam(filterFrom))+`::date and `+builder.arg(dateParam(filterTo))+`::date
    and s.start>=`+builder.arg(filterFromTimestamp)+`::timestamptz and s.start<`+builder.arg(filterToTimestamp)+`::timestamptz`+andSQL(matches)+`
)`)
		conditions = append(conditions, "(e.site_id,e.retention_class,e.session_id) in (select site_id,retention_class,session_id from "+name+")")
	}

	for _, filter := range specialFilters {
		alias := "ce"
		var predicate string
		switch {
		case filter.Dimension == "event" || filter.Dimension == "goal":
			filter.Dimension = "event"
			matches, err := filterConditions(builder, []api.Filter{filter}, customResolver(alias, builder))
			if err != nil {
				return nil, nil, err
			}
			predicate = alias + ".kind=1 and " + matches[0]
		case strings.HasPrefix(filter.Dimension, "prop:"):
			key := strings.TrimPrefix(filter.Dimension, "prop:")
			match, err := textMatch(builder, alias+".props ->> "+builder.arg(key), filter)
			if err != nil {
				return nil, nil, err
			}
			predicate = alias + ".kind=1 and " + alias + ".props ? " + builder.arg(key) + " and " + match
		}
		name := fmt.Sprintf("matched_filter_%d", len(ctes))
		ctes = append(ctes, name+` as materialized (
  select distinct ce.site_id,ce.retention_class,ce.session_id
  from events ce
  where ce.site_id=`+siteArg+` and ce.retention_class=`+retentionArg+`
    and ce.local_day between `+builder.arg(dateParam(filterFrom))+`::date and `+builder.arg(dateParam(filterTo))+`::date
    and ce.ts>=`+builder.arg(filterFromTimestamp)+`::timestamptz and ce.ts<`+builder.arg(filterToTimestamp)+`::timestamptz
    and `+predicate+`
)`)
		conditions = append(conditions, "(e.site_id,e.retention_class,e.session_id) in (select site_id,retention_class,session_id from "+name+")")
	}
	return ctes, conditions, nil
}

type vitalsScanner interface {
	Scan(dest ...any) error
}

func scanVitalsMetrics(row vitalsScanner) (api.VitalsMetrics, error) {
	return scanVitalsMetricsWithPrefix(row)
}

func scanVitalsMetricsWithPrefix(row vitalsScanner, prefix ...any) (api.VitalsMetrics, error) {
	metrics := api.VitalsMetrics{}
	dest := append(prefix,
		&metrics.Lcp.P75, &metrics.Lcp.Samples, &metrics.Lcp.Good, &metrics.Lcp.NeedsImprovement, &metrics.Lcp.Poor,
		&metrics.Cls.P75, &metrics.Cls.Samples, &metrics.Cls.Good, &metrics.Cls.NeedsImprovement, &metrics.Cls.Poor,
		&metrics.Inp.P75, &metrics.Inp.Samples, &metrics.Inp.Good, &metrics.Inp.NeedsImprovement, &metrics.Inp.Poor,
	)
	if err := row.Scan(dest...); err != nil {
		return api.VitalsMetrics{}, fmt.Errorf("scan Web Vitals aggregate: %w", err)
	}
	return metrics, nil
}

func scanVitalsTrendPoint(row vitalsScanner, prefix ...any) (api.VitalsTrendPoint, error) {
	point := api.VitalsTrendPoint{}
	dest := append(prefix,
		&point.Lcp.P50, &point.Lcp.P75, &point.Lcp.P95, &point.Lcp.Samples,
		&point.Cls.P50, &point.Cls.P75, &point.Cls.P95, &point.Cls.Samples,
		&point.Inp.P50, &point.Inp.P75, &point.Inp.P95, &point.Inp.Samples,
	)
	if err := row.Scan(dest...); err != nil {
		return api.VitalsTrendPoint{}, fmt.Errorf("scan Web Vitals timeseries: %w", err)
	}
	return point, nil
}
