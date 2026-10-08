package query

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (e *Engine) queryImported(ctx context.Context, req Normalized, firstNative *time.Time, importsAvailable bool) ([]resultRow, bool, error) {
	if !importsAvailable || !importedQueryEligible(req) {
		return nil, false, nil
	}
	to := req.To
	if firstNative != nil && !firstNative.After(to) {
		to = firstNative.AddDate(0, 0, -1)
	}
	if to.Before(req.From) {
		return nil, false, nil
	}

	builder := &sqlBuilder{}
	siteArg := builder.arg(req.SiteID)
	fromArg := builder.arg(req.From)
	toArg := builder.arg(to)
	where := "d.site_id=" + siteArg + " and d.day between " + fromArg + "::date and " + toArg + "::date and i.site_id=d.site_id and i.status='completed'"
	dimensions := []string(nil)
	from := "imported_day d join imports i on i.id=d.import_id"
	events := "0::int8"
	if len(req.Dimensions) == 1 {
		dimension := req.Dimensions[0]
		if dimension == "time" {
			dimensions = []string{"to_char(d.day, 'YYYY-MM-DD')"}
		} else {
			from = "imported_day_dim d join imports i on i.id=d.import_id"
			where += " and d.dim=" + builder.arg(rollupDimensionCodes[dimension])
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
		"0::int8", "0::int8", "0::int8",
	}
	querySQL := aggregateSQL(dimensions, sums, from, where)
	if len(dimensions) == 0 {
		querySQL += " having count(*)>0"
	}
	rows, err := e.executeAggregate(ctx, querySQL, builder.args, req.Dimensions)
	if err != nil {
		return nil, false, fmt.Errorf("query imported aggregates: %w", err)
	}
	for i := range rows {
		rows[i].imported = rows[i].sums
		rows[i].sums = Sums{}
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
)`, req.SiteID, req.From, req.To).Scan(&exists)
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
		if dimension != "time" {
			if _, ok := rollupDimensionCodes[dimension]; !ok {
				return false
			}
		}
	}
	for _, metric := range req.Metrics {
		if metricSupportsImported(metric, req.Dimensions) {
			return true
		}
	}
	return false
}

func (e *Engine) firstNativeDay(ctx context.Context, siteID int32) (*time.Time, error) {
	var day *time.Time
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
select min(day)::date
from (
  select min(day) as day from day_close where site_id=$1 and status='done'
  union all
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
