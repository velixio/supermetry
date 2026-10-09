package closer

const sessionSequencesSQL = `
pageviews as (
  select e.site_id, e.session_id, s.visitor_id, e.ts, e.path_id,
         lead(e.ts) over (partition by e.site_id, e.session_id order by e.ts, e.path_id) as next_ts,
         row_number() over (partition by e.site_id, e.session_id, e.path_id order by e.ts) as in_session,
         row_number() over (partition by e.site_id, s.visitor_id, e.path_id order by e.ts, e.session_id) as in_visitor
  from events e
  join candidate_sessions s using (site_id, session_id)
  where e.ts >= $2 and e.ts < $3 and e.kind = 0
), page_sequence as (
  select p.site_id, p.session_id, p.ts,
         (p.path_id << 2)
           | case when p.in_session = 1 then 2 else 0 end
           | case when p.in_visitor = 1 then 1 else 0 end as code,
         coalesce(sum(g.engagement_ms), 0)::integer as engagement_ms,
         coalesce(max(g.scroll_pct), 0)::smallint as scroll_pct
  from pageviews p
  left join events g
    on g.site_id = p.site_id and g.session_id = p.session_id and g.kind = 2
   and g.ts >= $2 and g.ts < $3
   and g.ts >= p.ts and (p.next_ts is null or g.ts < p.next_ts)
  group by p.site_id, p.session_id, p.ts, p.path_id, p.in_session, p.in_visitor
), custom_sequence as (
  select e.site_id, e.session_id, e.ts, -e.name_id as code,
         0::integer as engagement_ms, 0::smallint as scroll_pct
  from events e
  join candidate_sessions s using (site_id, session_id)
  where e.ts >= $2 and e.ts < $3 and e.kind = 1
), sequence as (
  select * from page_sequence
  union all
  select * from custom_sequence
), sequences as (
  select site_id, session_id,
         array_agg(code order by ts, code desc) as seq,
         array_agg(engagement_ms order by ts, code desc) as seq_engage,
         array_agg(scroll_pct order by ts, code desc) as seq_scroll,
         array_agg(ts order by ts, code desc) as seq_at
  from sequence
  group by site_id, session_id
)`

const closeSessionsSQL = `
insert into sessions_closed (
  site_id, day, visitor_id, session_id, started_at, profile_id, source_id, utm_source_id, medium_id, campaign_id,
  utm_content_id, utm_term_id, country, region_id, city_id, browser_id, os_id,
  browser_ver_id, os_ver_id, language_id, hostname_id, screen_bucket, device,
  entry_path, exit_path, pageviews, custom, duration_ms, engagement_ms,
  vd_n, flags, seq, seq_engage, seq_scroll, seq_at
)
with candidate_sessions as (
  select s.*
  from sessions s
  where s.local_day = $1 and s.start >= $2 and s.start < $3 and s.site_id = any($4)
), ` + sessionSequencesSQL + `, first_attribution as (
  select distinct on (e.site_id, e.session_id)
         e.site_id, e.session_id, e.utm_content_id, e.utm_term_id
  from events e
  join candidate_sessions s using (site_id, session_id)
  where e.ts >= $2 and e.ts < $3
  order by e.site_id, e.session_id, e.ts
), first_profile as (
  select distinct on (e.site_id, e.session_id)
         e.site_id, e.session_id, e.profile_id
  from events e
  join candidate_sessions s using (site_id, session_id)
  where e.ts >= $2 and e.ts < $3 and e.profile_id is not null
  order by e.site_id, e.session_id, e.ts
), session_rows as (
	 select s.site_id, s.visitor_id, s.session_id, s.start,
	        profile.profile_id,
	        s.source_id, s.utm_source_id, s.medium_id, s.campaign_id,
	        coalesce(attribution.utm_content_id, 0) as utm_content_id,
	        coalesce(attribution.utm_term_id, 0) as utm_term_id,
	         first_value(s.country) over visitor_order as country,
	         first_value(s.region_id) over visitor_order as region_id,
	         first_value(s.city_id) over visitor_order as city_id,
	         first_value(s.browser_id) over visitor_order as browser_id,
         first_value(s.os_id) over visitor_order as os_id,
		 first_value(s.browser_ver_id) over visitor_order as browser_ver_id,
		 first_value(s.os_ver_id) over visitor_order as os_ver_id,
		 s.language_id, s.hostname_id, s.screen_bucket,
         first_value(s.device) over visitor_order as device,
         s.entry_path, s.exit_path, s.pageviews, s.events as custom,
         least(2147483647::numeric,
               greatest(0::numeric, round(extract(epoch from (s.last - s.start)) * 1000)))::integer as duration_ms,
         s.engagement_ms,
         least(32767, count(*) over visitor_day)::smallint as vd_n,
         (case when row_number() over visitor_order = 1 then 1 else 0 end
          | case when row_number() over (
              partition by s.site_id, s.visitor_id, s.source_id order by s.start, s.session_id
            ) = 1 then 2 else 0 end
          | case when row_number() over (
              partition by s.site_id, s.visitor_id, s.entry_path order by s.start, s.session_id
            ) = 1 then 4 else 0 end
          | case when row_number() over (
              partition by s.site_id, s.visitor_id, s.exit_path order by s.start, s.session_id
            ) = 1 then 8 else 0 end
          | case when row_number() over (
              partition by s.site_id, s.visitor_id, s.utm_source_id order by s.start, s.session_id
            ) = 1 then 16 else 0 end
          | case when row_number() over (
              partition by s.site_id, s.visitor_id, s.screen_bucket order by s.start, s.session_id
            ) = 1 then 32 else 0 end
          | case when row_number() over (
              partition by s.site_id, s.visitor_id, s.language_id order by s.start, s.session_id
            ) = 1 then 64 else 0 end
          | case when row_number() over (
              partition by s.site_id, s.visitor_id, s.hostname_id order by s.start, s.session_id
            ) = 1 then 128 else 0 end)::smallint as flags
  from candidate_sessions s
  left join first_attribution attribution using (site_id, session_id)
  left join first_profile profile using (site_id, session_id)
  window visitor_day as (partition by s.site_id, s.visitor_id),
         visitor_order as (partition by s.site_id, s.visitor_id order by s.start, s.session_id)
)
select s.site_id, $1, s.visitor_id, s.session_id, s.start, s.profile_id,
	       s.source_id, s.utm_source_id, s.medium_id, s.campaign_id, s.utm_content_id, s.utm_term_id,
       s.country, s.region_id, s.city_id, s.browser_id, s.os_id,
       s.browser_ver_id, s.os_ver_id, s.language_id, s.hostname_id, s.screen_bucket,
       s.device, s.entry_path, s.exit_path,
       s.pageviews, s.custom, s.duration_ms, s.engagement_ms,
       s.vd_n, s.flags, q.seq, q.seq_engage, q.seq_scroll, q.seq_at
from session_rows s
join sequences q using (site_id, session_id)
order by s.site_id, s.country, s.visitor_id, s.start, s.session_id`

const closeCustomSQL = `
insert into custom_closed (
  site_id, day, visitor_id, session_id, name_id, path_id, source_id, utm_source_id, medium_id,
  campaign_id, utm_content_id, utm_term_id, country, region_id, city_id, browser_id, os_id,
  browser_ver_id, os_ver_id, language_id, hostname_id, screen_bucket,
  device, entry_path, exit_path, props, revenue_micros, revenue_currency,
  event_source_id, event_utm_source_id, event_utm_medium_id,
  event_utm_campaign_id, event_utm_content_id, event_utm_term_id
)
select c.site_id, $1, c.visitor_id, e.session_id, e.name_id, e.path_id,
       c.source_id, c.utm_source_id, c.medium_id, c.campaign_id, c.utm_content_id, c.utm_term_id,
       c.country, c.region_id, c.city_id, c.browser_id, c.os_id,
       c.browser_ver_id, c.os_ver_id, c.language_id, c.hostname_id, c.screen_bucket,
       c.device, c.entry_path, c.exit_path, e.props,
       e.revenue_micros, e.revenue_currency,
       e.source_id, e.utm_source_id, e.medium_id, e.campaign_id, e.utm_content_id, e.utm_term_id
from events e
join sessions_closed c
  on c.site_id = e.site_id and c.session_id = e.session_id and c.day = $1
where e.ts >= $2 and e.ts < $3 and e.site_id = any($4) and e.kind = 1
order by c.site_id, e.ts, e.session_id`

const recloseCustomSQL = `
insert into custom_closed (
  site_id, day, visitor_id, session_id, name_id, path_id, source_id, utm_source_id, medium_id,
  campaign_id, utm_content_id, utm_term_id, country, region_id, city_id, browser_id, os_id,
  browser_ver_id, os_ver_id, language_id, hostname_id, screen_bucket,
  device, entry_path, exit_path, props, revenue_micros, revenue_currency,
  event_source_id, event_utm_source_id, event_utm_medium_id,
  event_utm_campaign_id, event_utm_content_id, event_utm_term_id
)
select c.site_id, $1, c.visitor_id, e.session_id, e.name_id, e.path_id,
       c.source_id, c.utm_source_id, c.medium_id, c.campaign_id, c.utm_content_id, c.utm_term_id,
       c.country, c.region_id, c.city_id, c.browser_id, c.os_id,
       c.browser_ver_id, c.os_ver_id, c.language_id, c.hostname_id, c.screen_bucket,
       c.device, c.entry_path, c.exit_path, e.props,
       e.revenue_micros, e.revenue_currency,
       e.source_id, e.utm_source_id, e.medium_id, e.campaign_id, e.utm_content_id, e.utm_term_id
from events e
join sessions_closed c
  on c.site_id=e.site_id and c.session_id=e.session_id and c.day=$1
where e.ts >= $3 and e.ts < $4 and e.site_id=any($2) and e.kind=1
order by c.site_id,e.ts,e.session_id`
