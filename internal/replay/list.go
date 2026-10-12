package replay

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/dict"
)

// ListFilter is the validated, user-scoped replay-list query.
type ListFilter struct {
	SiteID      int32
	UserID      int64
	Retention   int16
	From        time.Time
	To          time.Time
	Limit       int
	Query       string
	MinDuration *int64
	MaxDuration *int64
	HasEvents   *bool
	Saved       *bool
	Cursor      *ListCursor
	TotalCap    int
}

// ListCursor is the stable descending keyset position for a replay page.
type ListCursor struct {
	StartedAt time.Time
	SessionID int64
}

// ListItem is one replay row enriched with its session summary and current user's saved marker.
type ListItem struct {
	SessionID int64     `json:"session_id,string"`
	StartedAt time.Time `json:"started_at"`
	DurationS int64     `json:"duration_s"`
	Pages     int32     `json:"pages"`
	Bytes     int64     `json:"bytes"`
	Trigger   string    `json:"trigger"`
	ProfileID *int64    `json:"profile_id"`
	Browser   string    `json:"browser"`
	OS        string    `json:"os"`
	Device    string    `json:"device"`
	Country   string    `json:"country"`
	City      string    `json:"city"`
	Actions   int64     `json:"actions"`
	Saved     bool      `json:"saved"`
}

// ListResult contains a page plus the capped total for the active filters.
type ListResult struct {
	Items       []ListItem
	Next        *ListCursor
	Total       int
	TotalCapped bool
}

var replayListFromSQL = `
from replay_sessions r
left join lateral (
  select candidate.country,candidate.city_id,candidate.device,candidate.browser_id,candidate.os_id,
         candidate.entry_path,candidate.exit_path,candidate.custom_events,candidate.actions
  from (
    select s.country,s.city_id,s.device,s.browser_id,s.os_id,s.entry_path,s.exit_path,
           s.custom::bigint as custom_events,s.pageviews::bigint+s.custom::bigint as actions,0 as priority
    from sessions_closed s
    where s.site_id=r.site_id and s.session_id=r.session_id
      and s.day between r.day-1 and r.day+1
      and s.started_at>=r.started_at-interval '1 day' and s.started_at<r.started_at+interval '1 day'
      and exists (select 1 from day_close dc where dc.site_id=s.site_id and dc.day=s.day and dc.status='done')
    union all
    select s.country,s.city_id,s.device,s.browser_id,s.os_id,s.entry_path,s.exit_path,
           s.events::bigint as custom_events,s.pageviews::bigint+s.events::bigint as actions,1 as priority
    from sessions s
    where s.site_id=r.site_id and s.retention_class=$4 and s.session_id=r.session_id
      and s.start>=r.started_at-interval '1 day' and s.start<r.started_at+interval '1 day'
      and not exists (select 1 from day_close dc where dc.site_id=s.site_id and dc.day=s.local_day and dc.status='done')
  ) candidate
  order by candidate.priority
  limit 1
) details on true
left join dict browser on browser.kind=` + fmt.Sprint(dict.Browser) + ` and browser.id=details.browser_id
left join dict os on os.kind=` + fmt.Sprint(dict.OS) + ` and os.id=details.os_id
left join dict city on city.kind=` + fmt.Sprint(dict.City) + ` and city.id=details.city_id
left join dict entry_path on entry_path.kind=` + fmt.Sprint(dict.Path) + ` and entry_path.id=details.entry_path
left join dict exit_path on exit_path.kind=` + fmt.Sprint(dict.Path) + ` and exit_path.id=details.exit_path
left join replay_saved saved on saved.site_id=r.site_id and saved.user_id=$5 and saved.session_id=r.session_id`

// List returns the replay page for one authenticated user. Filters are applied identically to
// the page and capped total; cursor position only affects the page.
func List(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) (ListResult, error) {
	where, args := replayListWhere(filter, false)
	var count int
	countSQL := `select count(*) from (select 1 ` + replayListFromSQL + where + fmt.Sprintf(" limit $%d", len(args)+1) + `) counted`
	if err := pool.QueryRow(ctx, countSQL, append(args, filter.TotalCap+1)...).Scan(&count); err != nil {
		return ListResult{}, err
	}
	total, capped := count, false
	if count > filter.TotalCap {
		total, capped = filter.TotalCap, true
	}

	where, args = replayListWhere(filter, true)
	query := `select r.session_id,r.started_at,greatest(0,extract(epoch from (r.last_seen_at-r.started_at)))::bigint,
       r.pages,r.bytes,coalesce(r.trigger,''),r.profile_id,
       coalesce(browser.value,''),coalesce(os.value,''),
       case details.device when 0 then 'desktop' when 1 then 'mobile' when 2 then 'tablet' else '' end,
       coalesce(trim(details.country),''),coalesce(city.value,''),coalesce(details.actions,0),
       saved.session_id is not null
` + replayListFromSQL + where + ` order by r.started_at desc,r.session_id desc` + fmt.Sprintf(" limit $%d", len(args)+1)
	rows, err := pool.Query(ctx, query, append(args, filter.Limit+1)...)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	items := make([]ListItem, 0, filter.Limit+1)
	for rows.Next() {
		var item ListItem
		if err := rows.Scan(&item.SessionID, &item.StartedAt, &item.DurationS, &item.Pages, &item.Bytes, &item.Trigger,
			&item.ProfileID, &item.Browser, &item.OS, &item.Device, &item.Country, &item.City, &item.Actions, &item.Saved); err != nil {
			return ListResult{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, err
	}
	var next *ListCursor
	if len(items) > filter.Limit {
		last := items[filter.Limit-1]
		next = &ListCursor{StartedAt: last.StartedAt, SessionID: last.SessionID}
		items = items[:filter.Limit]
	}
	return ListResult{Items: items, Next: next, Total: total, TotalCapped: capped}, nil
}

func replayListWhere(filter ListFilter, cursor bool) (string, []any) {
	args := []any{filter.SiteID, filter.From, filter.To, filter.Retention, filter.UserID}
	where := ` where r.site_id=$1 and r.day between $2::date and $3::date
  and r.started_at>=$2::date-interval '1 day' and r.started_at<$3::date+interval '2 days'`
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	duration := `greatest(0,extract(epoch from (r.last_seen_at-r.started_at)))::bigint`
	if filter.MinDuration != nil {
		where += " and " + duration + ">=" + add(*filter.MinDuration)
	}
	if filter.MaxDuration != nil {
		where += " and " + duration + "<=" + add(*filter.MaxDuration)
	}
	if filter.HasEvents != nil {
		operator := ">"
		if !*filter.HasEvents {
			operator = "="
		}
		where += " and coalesce(details.custom_events,0)" + operator + "0"
	}
	if filter.Saved != nil {
		operator := "is not null"
		if !*filter.Saved {
			operator = "is null"
		}
		where += " and saved.session_id " + operator
	}
	if filter.Query != "" {
		pattern := add("%" + escapeReplayLike(strings.ToLower(filter.Query)) + "%")
		where += ` and (
    lower(coalesce(entry_path.value,'')) like ` + pattern + ` escape '\'
    or lower(coalesce(exit_path.value,'')) like ` + pattern + ` escape '\'
    or lower(coalesce(trim(details.country),'')) like ` + pattern + ` escape '\'
    or lower(coalesce(browser.value,'')) like ` + pattern + ` escape '\'
    or lower(coalesce(r.trigger,'')) like ` + pattern + ` escape '\'
    or exists (
      select 1 from events event
      join dict event_name on event_name.kind=` + fmt.Sprint(dict.EventName) + ` and event_name.id=event.name_id
      where event.site_id=r.site_id and event.retention_class=$4 and event.session_id=r.session_id and event.kind=1
        and event.ts>=r.started_at-interval '1 day' and event.ts<r.started_at+interval '1 day'
        and lower(event_name.value) like ` + pattern + ` escape '\'
    )
    or exists (
      select 1 from custom_closed event
      join dict event_name on event_name.kind=` + fmt.Sprint(dict.EventName) + ` and event_name.id=event.name_id
      where event.site_id=r.site_id and event.session_id=r.session_id and event.day between r.day-1 and r.day+1
        and lower(event_name.value) like ` + pattern + ` escape '\'
    )
  )`
	}
	if cursor && filter.Cursor != nil {
		startedArg := add(filter.Cursor.StartedAt)
		sessionArg := add(filter.Cursor.SessionID)
		where += " and (r.started_at,r.session_id)<(" + startedArg + "," + sessionArg + ")"
	}
	return where, args
}

func escapeReplayLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
