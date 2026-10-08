package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/sites"
)

const webVitalsEventName = "Web Vitals"

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

	response := api.VitalsResponse{Breakdown: []api.VitalsBreakdown{}}
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

		if normalized.dimension == nil {
			return nil
		}
		breakdownSQL, args, err := buildVitalsSQL(normalized, normalized.dimension)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, breakdownSQL, args...)
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
		if _, _, err := vitalsDimensionResolver("e")(filter.Dimension); err != nil {
			return normalizedVitals{}, err
		}
	}
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	if site.Retention > 0 {
		localNow := now.In(location)
		today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
		oldest := today.AddDate(0, 0, -int(site.Retention)+1)
		if from.Before(oldest) {
			return normalizedVitals{}, fmt.Errorf("Web Vitals are available only within the site's %d-day raw retention window", site.Retention)
		}
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

func buildVitalsSQL(req normalizedVitals, dimension *api.VitalsDimension) (string, []any, error) {
	builder := &sqlBuilder{}
	resolver := vitalsDimensionResolver("e")
	directFilters, sessionFilters, specialFilters := splitVitalsFilters(req.filters)
	conditions, err := filterConditions(builder, directFilters, resolver)
	if err != nil {
		return "", nil, err
	}
	sessionConditions, err := filterConditions(builder, sessionFilters, vitalsSessionDimensionResolver("s"))
	if err != nil {
		return "", nil, err
	}
	if len(sessionConditions) > 0 {
		conditions = append(conditions, "exists (select 1 from sessions s where s.site_id=e.site_id and s.retention_class=e.retention_class and s.session_id=e.session_id"+andSQL(sessionConditions)+")")
	}
	specialConditions, err := vitalsSpecialConditions(builder, specialFilters)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, specialConditions...)
	dimensionExpr := ""
	if dimension != nil {
		column, kind, err := resolver(string(*dimension))
		if err != nil {
			return "", nil, err
		}
		dimensionExpr = dimensionText(column, kind, string(*dimension))
	}

	siteArg := builder.arg(req.siteID)
	retentionArg := builder.arg(req.retentionClass)
	fromArg := builder.arg(req.from)
	toArg := builder.arg(req.to)
	fromTimestampArg := builder.arg(req.fromTimestamp)
	toTimestampArg := builder.arg(req.toTimestamp)
	where := "e.site_id=" + siteArg + " and e.retention_class=" + retentionArg +
		" and e.ts>=" + fromTimestampArg + "::timestamptz and e.ts<" + toTimestampArg + "::timestamptz" +
		" and e.local_day between " + fromArg + "::date and " + toArg + "::date" +
		" and e.kind=1 and e.name_id in (select id from dict where kind=" + fmt.Sprint(dict.EventName) + " and value='" + webVitalsEventName + "')" + andSQL(conditions)
	dimensionSelect := ""
	dimensionColumn := ""
	if dimensionExpr != "" {
		dimensionSelect = dimensionExpr + " as dimension,"
		dimensionColumn = "dimension,"
	}
	cte := `with raw_vitals as (
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
		return cte + " select" + aggregates + " from vitals", builder.args, nil
	}
	return cte + ` select dimension,` + aggregates + `
from vitals
where dimension <> '' and dimension <> '0'
group by dimension
order by count(lcp)+count(cls)+count(inp) desc, dimension
limit 20`, builder.args, nil
}

func vitalsDimensionResolver(alias string) dimensionResolver {
	base := customResolver(alias)
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

func vitalsSpecialConditions(builder *sqlBuilder, filters []api.Filter) ([]string, error) {
	conditions := make([]string, 0, len(filters))
	for _, filter := range filters {
		switch {
		case filter.Dimension == "event" || filter.Dimension == "goal":
			filter.Dimension = "event"
			matches, err := filterConditions(builder, []api.Filter{filter}, customResolver("ce"))
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, "exists (select 1 from events ce where ce.site_id=e.site_id and ce.retention_class=e.retention_class and ce.session_id=e.session_id and ce.kind=1 and "+matches[0]+")")
		case strings.HasPrefix(filter.Dimension, "prop:"):
			key := strings.TrimPrefix(filter.Dimension, "prop:")
			match, err := textMatch(builder, "ce.props ->> "+builder.arg(key), filter)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, "exists (select 1 from events ce where ce.site_id=e.site_id and ce.retention_class=e.retention_class and ce.session_id=e.session_id and ce.kind=1 and ce.props ? "+builder.arg(key)+" and "+match+")")
		}
	}
	return conditions, nil
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
