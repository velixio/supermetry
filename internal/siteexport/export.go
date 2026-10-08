// Package siteexport streams a complete, portable ZIP export for one site.
package siteexport

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/csvsafe"
	"panel/internal/dict"
)

type Exporter struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Exporter { return &Exporter{pool: pool} }

type dimension struct {
	file string
	code int16
	kind *dict.Kind
}

func kind(value dict.Kind) *dict.Kind { return &value }

var dimensions = []dimension{
	{file: "pages.csv", code: 1, kind: kind(dict.Path)},
	{file: "entry_pages.csv", code: 7, kind: kind(dict.Path)},
	{file: "exit_pages.csv", code: 8, kind: kind(dict.Path)},
	{file: "sources.csv", code: 2, kind: kind(dict.Source)},
	{file: "countries.csv", code: 3},
	{file: "regions.csv", code: 9, kind: kind(dict.Region)},
	{file: "cities.csv", code: 10, kind: kind(dict.City)},
	{file: "browsers.csv", code: 4, kind: kind(dict.Browser)},
	{file: "operating_systems.csv", code: 5, kind: kind(dict.OS)},
	{file: "devices.csv", code: 6},
}

type attribution struct {
	file   string
	column string
	kind   dict.Kind
}

var attributions = []attribution{
	{file: "utm_sources.csv", column: "source_id", kind: dict.Source},
	{file: "utm_mediums.csv", column: "medium_id", kind: dict.Medium},
	{file: "utm_campaigns.csv", column: "campaign_id", kind: dict.Campaign},
	{file: "utm_contents.csv", column: "utm_content_id", kind: dict.UTMContent},
	{file: "utm_terms.csv", column: "utm_term_id", kind: dict.UTMTerm},
}

func (e *Exporter) Write(ctx context.Context, output io.Writer, siteID int32, from, to time.Time) (resultErr error) {
	archive := zip.NewWriter(output)
	defer func() {
		if err := archive.Close(); resultErr == nil && err != nil {
			resultErr = err
		}
	}()
	if err := e.writeDaily(ctx, archive, siteID, from, to); err != nil {
		return err
	}
	for _, item := range dimensions[:4] {
		if err := e.writeDimension(ctx, archive, item, siteID, from, to); err != nil {
			return err
		}
	}
	for _, item := range attributions {
		if err := e.writeAttribution(ctx, archive, item, siteID, from, to); err != nil {
			return err
		}
	}
	for _, item := range dimensions[4:] {
		if err := e.writeDimension(ctx, archive, item, siteID, from, to); err != nil {
			return err
		}
	}
	return e.writeCustom(ctx, archive, siteID, from, to)
}

func (e *Exporter) writeDaily(ctx context.Context, archive *zip.Writer, siteID int32, from, to time.Time) error {
	const query = `
with first_native as (
  select min(day) as first_day from (
    select min(day) as day from day_close where site_id=$1 and status='done'
    union all select min(day) from rollup_day where site_id=$1
    union all select min(local_day) from sessions where site_id=$1
    union all select min(local_day) from events where site_id=$1 and local_day is not null
  ) native
), combined as (
  select day,visitors::bigint,visits::bigint,pageviews::bigint,bounces::bigint,(duration_ms/1000)::bigint duration_s
  from rollup_day where site_id=$1 and day between $2 and $3
  union all
  select day,visitors,visits,pageviews,bounces,visit_duration_s
  from imported_day where site_id=$1 and day between $2 and $3
    and day < coalesce((select first_day from first_native), 'infinity'::date)
)
select day::text,sum(visitors)::bigint,sum(visits)::bigint,sum(pageviews)::bigint,
       sum(bounces)::bigint,sum(duration_s)::bigint
from combined group by day order by day`
	return e.writeRows(ctx, archive, "daily_totals.csv",
		[]string{"day", "visitors", "visits", "pageviews", "bounces", "visit_duration_s"}, query,
		siteID, date(from), date(to))
}

func (e *Exporter) writeDimension(ctx context.Context, archive *zip.Writer, item dimension, siteID int32, from, to time.Time) error {
	value := `case
when r.dim=3 then case when r.value_id=0 then '' else chr(r.value_id/256)||chr(r.value_id%256) end
when r.dim=6 then case r.value_id when 1 then 'desktop' when 2 then 'mobile' when 3 then 'tablet' else '' end
else coalesce((select value from dict where kind=$5 and id=r.value_id),'') end`
	kindID := int16(0)
	if item.kind != nil {
		kindID = int16(*item.kind)
	}
	query := fmt.Sprintf(`
with first_native as (
  select min(day) as first_day from (
    select min(day) as day from day_close where site_id=$1 and status='done'
    union all select min(day) from rollup_day where site_id=$1
    union all select min(local_day) from sessions where site_id=$1
    union all select min(local_day) from events where site_id=$1 and local_day is not null
  ) native
), combined as (
 select r.day,%s value,r.visitors::bigint,r.visits::bigint,r.pageviews::bigint,r.bounces::bigint,
        (r.duration_ms/1000)::bigint duration_s,0::bigint events
 from rollup_day_dim r where r.site_id=$1 and r.day between $2 and $3 and r.dim=$4
 union all
 select i.day,i.value,i.visitors,i.visits,i.pageviews,i.bounces,i.visit_duration_s,i.events
 from imported_day_dim i where i.site_id=$1 and i.day between $2 and $3 and i.dim=$4
   and i.day < coalesce((select first_day from first_native),'infinity'::date)
)
select day::text,value,sum(visitors)::bigint,sum(visits)::bigint,sum(pageviews)::bigint,
       sum(bounces)::bigint,sum(duration_s)::bigint,sum(events)::bigint
from combined group by day,value order by day,value`, value)
	return e.writeRows(ctx, archive, item.file,
		[]string{"day", "value", "visitors", "visits", "pageviews", "bounces", "visit_duration_s", "events"},
		query, siteID, date(from), date(to), item.code, kindID)
}

func (e *Exporter) writeAttribution(ctx context.Context, archive *zip.Writer, item attribution, siteID int32, from, to time.Time) error {
	query := fmt.Sprintf(`
select s.day::text,coalesce(d.value,''),
 count(distinct s.visitor_id),count(*),sum(s.pageviews),
 count(*) filter(where s.pageviews=1 and s.custom=0),sum(s.duration_ms)/1000,sum(s.custom)
from sessions_closed s
left join dict d on d.kind=$4 and d.id=s.%s
where s.site_id=$1 and s.day between $2 and $3 and s.%s<>0
group by s.day,d.value order by s.day,d.value`, item.column, item.column)
	return e.writeRows(ctx, archive, item.file,
		[]string{"day", "value", "visitors", "visits", "pageviews", "bounces", "visit_duration_s", "events"},
		query, siteID, date(from), date(to), int16(item.kind))
}

func (e *Exporter) writeCustom(ctx context.Context, archive *zip.Writer, siteID int32, from, to time.Time) error {
	const query = `
select c.day::text,coalesce(n.value,''),coalesce(p.value,''),coalesce(c.props::text,'{}'),
 count(*),sum(c.revenue_micros)::bigint,coalesce(c.revenue_currency::text,'')
from custom_closed c
left join dict n on n.kind=$4 and n.id=c.name_id
left join dict p on p.kind=$5 and p.id=c.path_id
where c.site_id=$1 and c.day between $2 and $3
group by c.day,n.value,p.value,c.props,c.revenue_currency
order by c.day,n.value,p.value,c.props::text`
	return e.writeRows(ctx, archive, "custom_events.csv",
		[]string{"day", "event", "page", "props", "events", "revenue_micros", "revenue_currency"}, query,
		siteID, date(from), date(to), int16(dict.EventName), int16(dict.Path))
}

func (e *Exporter) writeRows(ctx context.Context, archive *zip.Writer, name string, header []string, query string, args ...any) error {
	entry, err := archive.Create(name)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	w := csv.NewWriter(entry)
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write %s header: %w", name, err)
	}
	rows, err := e.pool.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query %s: %w", name, err)
	}
	defer rows.Close()
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		record := make([]string, len(values))
		for i, value := range values {
			record[i] = csvValue(value)
		}
		if err := w.Write(record); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s: %w", name, err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush %s: %w", name, err)
	}
	return nil
}

func csvValue(value any) string {
	switch value := value.(type) {
	case nil:
		return ""
	case string:
		return csvsafe.Cell(value)
	case []byte:
		return csvsafe.Cell(string(value))
	case int64:
		return strconv.FormatInt(value, 10)
	case int32:
		return strconv.FormatInt(int64(value), 10)
	default:
		return fmt.Sprint(value)
	}
}

func date(value time.Time) string { return value.Format(time.DateOnly) }
