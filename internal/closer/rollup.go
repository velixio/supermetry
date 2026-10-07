package closer

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const rollupDaySQL = sessionMetricsCTE + `
insert into rollup_day (
  site_id, day, visitors, visits, pageviews, bounces, duration_ms,
  engagement_ms, scroll_sum, scroll_n
)
select site_id, day, count(*) filter (where flags & 1 <> 0), count(*),
       sum(pageviews), count(*) filter (where pageviews = 1 and custom = 0),
       sum(duration_ms), sum(engagement_ms), sum(scroll_sum), sum(scroll_n)
from session_metrics
group by site_id, day`

const rollupSessionDimensionsSQL = sessionMetricsCTE + `
insert into rollup_day_dim (
  site_id, day, dim, value_id, visitors, visits, pageviews, bounces,
  duration_ms, engagement_ms, scroll_sum, scroll_n
)
select site_id, $1, dim, value_id,
       count(*) filter (where flags & visitor_flag <> 0),
       count(*), sum(pageviews),
       count(*) filter (where pageviews = 1 and custom = 0),
       sum(duration_ms), sum(engagement_ms), sum(scroll_sum), sum(scroll_n)
from session_metrics s
cross join lateral (values
  (2::smallint, s.source_id, 2::smallint),
  (3::smallint,
    coalesce(ascii(substr(s.country, 1, 1)), 0) * 256
      + coalesce(ascii(substr(s.country, 2, 1)), 0), 1::smallint),
  (4::smallint, s.browser_id, 1::smallint),
  (5::smallint, s.os_id, 1::smallint),
  (6::smallint, s.device::integer, 1::smallint),
  (7::smallint, s.entry_path, 4::smallint),
  (8::smallint, s.exit_path, 8::smallint)
) dimensions(dim, value_id, visitor_flag)
where not (dim in (7, 8) and s.pageviews = 0)
group by site_id, dim, value_id`

const rollupPathSQL = `
insert into rollup_day_dim (
  site_id, day, dim, value_id, visitors, visits, pageviews, bounces,
  duration_ms, engagement_ms, scroll_sum, scroll_n
)
select site_id, $1, 1, code >> 2,
       count(*) filter (where code & 1 <> 0),
       count(*) filter (where code & 2 <> 0),
       count(*),
       count(*) filter (where code & 2 <> 0 and bounce),
       coalesce(sum(duration_ms) filter (where code & 2 <> 0), 0),
       sum(engagement_ms),
       coalesce(sum(scroll_pct) filter (where scroll_pct > 0), 0),
       count(*) filter (where scroll_pct > 0)
from (
  select s.site_id, s.pageviews = 1 and s.custom = 0 as bounce, s.duration_ms,
         sequence.code, sequence.engagement_ms, sequence.scroll_pct
  from sessions_closed s
  cross join lateral unnest(s.seq, s.seq_engage, s.seq_scroll)
    as sequence(code, engagement_ms, scroll_pct)
  where s.day = $1 and s.site_id = any($2)
) pages
where code > 0
group by site_id, code >> 2`

const refreshMonthDeleteSQL = `
with eligible as (
  select site_id
  from unnest($2::int[]) sites(site_id)
  where exists (
    select 1 from rollup_month_done done
    where done.site_id = sites.site_id and done.month = $1
  ) or not exists (
    select 1
    from generate_series($1::date, ($1::date + interval '1 month - 1 day')::date, interval '1 day') days(day)
    where not exists (
      select 1 from day_close closed
      where closed.site_id = sites.site_id and closed.day = days.day and closed.status = 'done'
    )
  )
)
delete from rollup_month_dim monthly
using eligible
where monthly.site_id = eligible.site_id and monthly.month = $1`

const refreshMonthInsertSQL = `
with eligible as (
  select site_id
  from unnest($2::int[]) sites(site_id)
  where exists (
    select 1 from rollup_month_done done
    where done.site_id = sites.site_id and done.month = $1
  ) or not exists (
    select 1
    from generate_series($1::date, ($1::date + interval '1 month - 1 day')::date, interval '1 day') days(day)
    where not exists (
      select 1 from day_close closed
      where closed.site_id = sites.site_id and closed.day = days.day and closed.status = 'done'
    )
  )
), inserted as (
  insert into rollup_month_dim (
    site_id, month, dim, value_id, visitors, visits, pageviews, bounces,
    duration_ms, engagement_ms, scroll_sum, scroll_n
  )
  select daily.site_id, $1, daily.dim, daily.value_id,
         sum(daily.visitors), sum(daily.visits), sum(daily.pageviews), sum(daily.bounces),
         sum(daily.duration_ms), sum(daily.engagement_ms), sum(daily.scroll_sum), sum(daily.scroll_n)
  from rollup_day_dim daily
  join eligible using (site_id)
  where daily.day >= $1 and daily.day < ($1::date + interval '1 month')
  group by daily.site_id, daily.dim, daily.value_id
)
insert into rollup_month_done (site_id, month)
select site_id, $1 from eligible
on conflict (site_id, month) do nothing`

func refreshMonth(ctx context.Context, tx pgx.Tx, siteIDs []int32, day time.Time) error {
	month := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, err := tx.Exec(ctx, refreshMonthDeleteSQL, month, siteIDs); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, refreshMonthInsertSQL, month, siteIDs)
	return err
}
