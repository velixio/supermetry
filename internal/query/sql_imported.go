package query

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/api"
	panelimports "supermetry/internal/imports"
)

func (e *Engine) queryImported(ctx context.Context, req Normalized, _ *time.Time, importsAvailable bool) ([]resultRow, bool, error) {
	if !importsAvailable || !importedQueryEligible(req) {
		return nil, false, nil
	}

	builder := &sqlBuilder{}
	siteArg := builder.arg(req.SiteID)
	fromArg := builder.arg(dateParam(req.From))
	toArg := builder.arg(dateParam(req.To))
	where := "d.site_id=" + siteArg + " and d.day between " + fromArg + "::date and " + toArg + "::date"
	dimensions := []string(nil)
	from := "imported_day d join chosen_import_days c using(site_id,day,import_id)"
	events := "0::int8"
	converted := "0::int8"
	goalDimension := false
	if len(req.Dimensions) == 1 {
		dimension := req.Dimensions[0]
		switch dimension {
		case "time":
			dimensions = []string{"to_char(d.day, 'YYYY-MM-DD')"}
		case "event":
			prefixArg := builder.arg(panelimports.EventValuePrefix)
			from = "imported_day_dim d join chosen_import_days c using(site_id,day,import_id)"
			where += " and d.dim=" + builder.arg(panelimports.DimPage) + " and left(d.value,char_length(" + prefixArg + "))=" + prefixArg
			dimensions = []string{"substr(d.value,char_length(" + prefixArg + ")+1)"}
			events = "coalesce(sum(d.events),0)::int8"
		case "goal":
			prefixArg := builder.arg(panelimports.EventValuePrefix)
			values := make([]string, 0, len(req.GoalDefinitions))
			ids := make([]int64, 0, len(req.GoalDefinitions))
			for id := range req.GoalDefinitions {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			eventValue := "substr(d.value,char_length(" + prefixArg + ")+1)"
			for _, id := range ids {
				goal := req.GoalDefinitions[id]
				// Aggregate imports contain neither properties nor session identity.
				// Event occurrences are therefore the best available approximation
				// for property-free event-goal conversions.
				if goal.Kind != "event" || goal.PropKey != "" {
					continue
				}
				predicate, err := importedTextMatchPredicate(builder, eventValue, goal.Match, goal.Value)
				if err != nil {
					return nil, false, err
				}
				values = append(values, "("+builder.arg(strconv.FormatInt(id, 10))+"::text,"+predicate+")")
			}
			if len(values) == 0 {
				return nil, false, nil
			}
			from = "imported_day_dim d join chosen_import_days c using(site_id,day,import_id) cross join lateral (values " + strings.Join(values, ",") + ") g(goal_id,matched)"
			where += " and d.dim=" + builder.arg(panelimports.DimPage) + " and left(d.value,char_length(" + prefixArg + "))=" + prefixArg + " and g.matched"
			dimensions = []string{"g.goal_id"}
			events = "coalesce(sum(d.events),0)::int8"
			converted = events
			goalDimension = true
		default:
			from = "imported_day_dim d join chosen_import_days c using(site_id,day,import_id)"
			where += " and d.dim=" + builder.arg(rollupDimensionCodes[dimension])
			if rollupDimensionCodes[dimension] == panelimports.DimPage {
				prefixArg := builder.arg(panelimports.EventValuePrefix)
				where += " and left(d.value,char_length(" + prefixArg + "))<>" + prefixArg
			}
			dimensions = []string{"d.value"}
			events = "coalesce(sum(d.events),0)::int8"
		}
	}
	sums := []string{
		"coalesce(sum(d.visitors),0)::int8",
		"coalesce(sum(d.visits),0)::int8",
		"coalesce(sum(d.pageviews),0)::int8",
		"coalesce(sum(d.bounces),0)::int8",
		"coalesce(sum(d.visit_duration_s),0)::int8 * 1000",
		"0::int8", "0::int8", "0::int8", "0::int8", "0::int8", events,
		converted, "0::int8", "0::int8",
	}
	querySQL := `with covered_import_days as materialized (
  select site_id,import_id,day from imported_day
  where site_id=` + siteArg + ` and day between ` + fromArg + `::date and ` + toArg + `::date
  union
  select site_id,import_id,day from imported_day_dim
  where site_id=` + siteArg + ` and day between ` + fromArg + `::date and ` + toArg + `::date
), native_days as materialized (
  select day from rollup_day where site_id=` + siteArg + ` and day between ` + fromArg + `::date and ` + toArg + `::date
  union select local_day from sessions where site_id=` + siteArg + ` and local_day between ` + fromArg + `::date and ` + toArg + `::date
  union select local_day from events where site_id=` + siteArg + ` and local_day between ` + fromArg + `::date and ` + toArg + `::date
    and ts >= (` + fromArg + `::date - 1)::timestamptz and ts < (` + toArg + `::date + 2)::timestamptz
  union select day from sessions_closed where site_id=` + siteArg + ` and day between ` + fromArg + `::date and ` + toArg + `::date
  union select day from custom_closed where site_id=` + siteArg + ` and day between ` + fromArg + `::date and ` + toArg + `::date
), chosen_import_days as materialized (
  select distinct on (d.site_id,d.day) d.site_id,d.day,d.import_id
  from covered_import_days d
  join imports i on i.id=d.import_id and i.site_id=d.site_id
  where d.site_id=` + siteArg + ` and d.day between ` + fromArg + `::date and ` + toArg + `::date
    and i.status='completed' and not exists (select 1 from native_days n where n.day=d.day)
  order by d.site_id,d.day,i.completed_at desc nulls last,i.id desc
)
` + aggregateSQL(dimensions, sums, from, where)
	if goalDimension {
		totalSums := make([]string, len(sums))
		for i := range totalSums {
			totalSums[i] = "0::int8"
		}
		totalSums[11] = converted
		querySQL += " union all " + aggregateSQL([]string{builder.arg(goalConversionTotalRow)}, totalSums, from, where) + " having count(*)>0"
	} else if len(dimensions) == 0 {
		querySQL += " having count(*)>0"
	}
	rows, err := e.executeAggregate(ctx, querySQL, builder.args, req.Dimensions)
	if err != nil {
		return nil, false, fmt.Errorf("query imported aggregates: %w", err)
	}
	for i := range rows {
		rows[i].imported = rows[i].sums
		rows[i].sums = Sums{}
		if goalDimension {
			rows[i].sums.Converted = rows[i].imported.Converted
			rows[i].imported.Converted = 0
		}
	}
	return rows, len(rows) > 0, nil
}

func (e *Engine) importedDaysExist(ctx context.Context, req Normalized) (bool, error) {
	var exists bool
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
select exists (
  select 1 from (
    select d.import_id,d.site_id,d.day from imported_day d
    union all
    select d.import_id,d.site_id,d.day from imported_day_dim d
  ) d
  join imports i on i.id=d.import_id and i.site_id=d.site_id
  where d.site_id=$1 and d.day between $2::date and $3::date and i.status='completed'
)`, req.SiteID, dateParam(req.From), dateParam(req.To)).Scan(&exists)
	})
	if err != nil {
		return false, fmt.Errorf("check imported aggregates: %w", err)
	}
	return exists, nil
}

func importedQueryEligible(req Normalized) bool {
	if !req.IncludeImported || len(req.Filters) != 0 || len(req.Dimensions) > 1 {
		return false
	}
	if len(req.Dimensions) == 1 {
		dimension := req.Dimensions[0]
		if dimension != "time" && dimension != "event" && dimension != "goal" {
			if _, ok := rollupDimensionCodes[dimension]; !ok {
				return false
			}
		}
	}
	for _, metric := range req.Metrics {
		if len(req.Dimensions) == 1 && req.Dimensions[0] == "event" && metric == api.Events {
			return true
		}
		if len(req.Dimensions) == 1 && req.Dimensions[0] == "goal" && (metric == api.Conversions || metric == api.ConversionRate) {
			return true
		}
		if metricSupportsImported(metric, req.Dimensions) {
			return true
		}
	}
	return false
}

func importedTextMatchPredicate(builder *sqlBuilder, expression, match, value string) (string, error) {
	switch match {
	case "exact":
		return expression + "=" + builder.arg(value), nil
	case "prefix":
		return expression + " like " + builder.arg(EscapeLike(value)+"%") + ` escape '\'`, nil
	case "wildcard":
		parts := strings.Split(value, "*")
		for i := range parts {
			parts[i] = EscapeLike(parts[i])
		}
		return expression + " like " + builder.arg(strings.Join(parts, "%")) + ` escape '\'`, nil
	default:
		return "", fmt.Errorf("unknown import goal match %q", match)
	}
}

func (e *Engine) firstNativeDay(ctx context.Context, siteID int32) (*time.Time, error) {
	var day *time.Time
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
select min(day)::date
from (
  select min(day) as day from rollup_day where site_id=$1
  union all
  select min(local_day) as day from sessions where site_id=$1
  union all
  select min(local_day) as day from events where site_id=$1 and local_day is not null
) native`, siteID).Scan(&day)
	})
	if err != nil {
		return nil, fmt.Errorf("query first native day: %w", err)
	}
	if day == nil {
		return nil, nil
	}
	normalized := civilDate(*day)
	return &normalized, nil
}
