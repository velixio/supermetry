package closer

const sessionMetricsCTE = `
with session_metrics as (
  select s.*,
         coalesce(x.scroll_sum, 0) as scroll_sum,
         coalesce(x.scroll_n, 0) as scroll_n
  from sessions_closed s
  left join lateral (
    select coalesce(sum(scroll), 0)::bigint as scroll_sum,
           count(*) filter (where scroll > 0)::bigint as scroll_n
    from unnest(s.seq_scroll) as values(scroll)
  ) x on true
  where s.day = $1 and s.site_id = any($2)
)`

const cubeDaySQL = sessionMetricsCTE + `
insert into cube_day (
  site_id, day, source_id, country, browser_id, os_id, device,
  v_vd, v_src, visits, pageviews, bounces, duration_ms, engagement_ms,
  scroll_sum, scroll_n
)
select site_id, day, source_id, country, browser_id, os_id, device,
       count(*) filter (where flags & 1 <> 0),
       count(*) filter (where flags & 2 <> 0),
       count(*), sum(pageviews),
       count(*) filter (where pageviews = 1 and custom = 0),
       sum(duration_ms), sum(engagement_ms), sum(scroll_sum), sum(scroll_n)
from session_metrics
group by site_id, day, source_id, country, browser_id, os_id, device
order by site_id, country, source_id`

const visitorDaySQL = `
insert into visitor_day (
  site_id, day, country, browser_id, os_id, device,
  visitors, visits, pageviews, bounces, duration_ms, engagement_ms
)
select site_id, day, country, browser_id, os_id, device,
       count(*) filter (where flags & 1 = 1), count(*), sum(pageviews),
       count(*) filter (where pageviews = 1 and custom = 0),
       sum(duration_ms), sum(engagement_ms)
from sessions_closed
where day = $1 and site_id = any($2)
group by site_id, day, country, browser_id, os_id, device
order by site_id, country, browser_id, os_id, device`
