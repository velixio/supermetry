package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/dict"
)

const (
	defaultSessionLimit = 50
	maxSessionLimit     = 1000
	maxSessionEvents    = 1000
	// sessionTimelineSlack widens the timeline's upper ts bound past the
	// stored session.last, which the writer flushes after the events, so the
	// bound keeps partition pruning without hiding the newest hits.
	sessionTimelineSlack = 30 * time.Minute
)

type sessionCursor struct {
	startedAt time.Time
	id        int64
}

// Sessions lists bounded anonymous session summaries across the closed and hot tiers.
func (e *Engine) Sessions(ctx context.Context, siteID int32, params api.ListSessionsParams, filters []api.Filter) (api.SessionList, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return api.SessionList{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(siteID)
	if !ok {
		return api.SessionList{}, fmt.Errorf("unknown site %d", siteID)
	}
	from, to := civilDate(params.From.Time), civilDate(params.To.Time)
	if from.IsZero() || to.IsZero() {
		return api.SessionList{}, errors.New("from and to are required")
	}
	if from.After(to) {
		return api.SessionList{}, errors.New("from must not be after to")
	}
	if daysInclusive(from, to) > maxRangeDays {
		return api.SessionList{}, fmt.Errorf("date range must not exceed %d days", maxRangeDays)
	}
	if err := ValidateFilters(filters); err != nil {
		return api.SessionList{}, err
	}
	limit := defaultSessionLimit
	if params.Limit != nil {
		limit = int(*params.Limit)
	}
	if limit < 1 || limit > maxSessionLimit {
		return api.SessionList{}, fmt.Errorf("limit must be between 1 and %d", maxSessionLimit)
	}
	var cursor *sessionCursor
	if params.Cursor != nil {
		parsed, err := decodeSessionCursor(string(*params.Cursor))
		if err != nil {
			return api.SessionList{}, errors.New("invalid cursor")
		}
		cursor = &parsed
	}

	querySQL, args, err := buildSessionsSQL(siteID, site.Retention, site.TZ, site.ProfilesEnabled, from, to, filters, cursor, limit+1)
	if err != nil {
		return api.SessionList{}, err
	}
	response := api.SessionList{Data: make([]api.SessionSummary, 0, limit)}
	if err := globalQuerySlots.Acquire(ctx, 1); err != nil {
		return api.SessionList{}, fmt.Errorf("query sessions: %w", err)
	}
	defer globalQuerySlots.Release(1)
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, queryErr := tx.Query(ctx, querySQL, args...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			item, scanErr := scanSessionSummary(rows)
			if scanErr != nil {
				return scanErr
			}
			response.Data = append(response.Data, item)
		}
		return rows.Err()
	})
	if err != nil {
		return api.SessionList{}, fmt.Errorf("query sessions: %w", err)
	}
	if len(response.Data) > limit {
		last := response.Data[limit-1]
		response.Data = response.Data[:limit]
		cursor := encodeSessionCursor(last.StartedAt, mustSessionID(last.Id))
		response.NextCursor = &cursor
	}
	return response, nil
}

// Session returns one hot-tier session and its chronological pageview/custom-event timeline.
func (e *Engine) Session(ctx context.Context, siteID int32, sessionID int64) (api.SessionDetail, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return api.SessionDetail{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(siteID)
	if !ok {
		return api.SessionDetail{}, fmt.Errorf("unknown site %d", siteID)
	}
	if sessionID <= 0 {
		return api.SessionDetail{}, api.ErrSessionNotFound
	}
	if err := globalQuerySlots.Acquire(ctx, 1); err != nil {
		return api.SessionDetail{}, fmt.Errorf("query session: %w", err)
	}
	defer globalQuerySlots.Release(1)

	detail := api.SessionDetail{Timeline: make([]api.SessionTimelineItem, 0)}
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		var start, last time.Time
		var localDay time.Time
		row := tx.QueryRow(ctx, sessionSummarySQL("sessions", site.ProfilesEnabled)+`
where s.site_id=$1 and s.retention_class=$2 and s.session_id=$3
order by s.start desc limit 1`, siteID, site.Retention, sessionID)
		item, scanErr := scanSessionSummary(row, &start, &last, &localDay)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			var closed bool
			if err := tx.QueryRow(ctx, `select exists(select 1 from sessions_closed where site_id=$1 and session_id=$2)`, siteID, sessionID).Scan(&closed); err != nil {
				return err
			}
			if closed {
				return api.ErrSessionRawExpired
			}
			return api.ErrSessionNotFound
		}
		if scanErr != nil {
			return scanErr
		}
		detail.Session = item

		rows, err := tx.Query(ctx, `
select e.ts, e.kind,
       case when e.kind=0 then 'pageview' else coalesce((select d.value from dict d where d.kind=$4 and d.id=e.name_id),'') end,
       coalesce((select d.value from dict d where d.kind=$5 and d.id=e.path_id),''), e.props
from events e
where e.site_id=$1 and e.retention_class=$2 and e.session_id=$3 and e.kind in (0,1)
  and e.local_day=$6 and e.ts>=$7 and e.ts<$8
order by e.ts, e.kind, e.name_id
limit `+strconv.Itoa(maxSessionEvents), siteID, site.Retention, sessionID, dict.EventName, dict.Path,
			localDay, start, last.Add(sessionTimelineSlack))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item api.SessionTimelineItem
			var kind int16
			var rawProps []byte
			if err := rows.Scan(&item.Ts, &kind, &item.Name, &item.Page, &rawProps); err != nil {
				return err
			}
			if kind == 0 {
				item.Kind = api.SessionPageview
			} else {
				item.Kind = api.SessionEvent
			}
			if len(rawProps) > 0 {
				props := make(map[string]any)
				if err := json.Unmarshal(rawProps, &props); err != nil {
					return err
				}
				item.Props = &props
			}
			detail.Timeline = append(detail.Timeline, item)
		}
		return rows.Err()
	})
	if err != nil {
		return api.SessionDetail{}, fmt.Errorf("query session: %w", err)
	}
	return detail, nil
}

func buildSessionsSQL(siteID int32, retention db.RetentionClass, location *time.Location, profilesEnabled bool, from, to time.Time, filters []api.Filter, cursor *sessionCursor, limit int) (string, []any, error) {
	if location == nil {
		location = time.UTC
	}
	startTS := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, location)
	endTS := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1)
	builder := &sqlBuilder{}
	siteArg, retentionArg := builder.arg(siteID), builder.arg(retention)
	fromArg, toArg := builder.arg(from), builder.arg(to)
	startArg, endArg := builder.arg(startTS), builder.arg(endTS)
	closedConditions, err := sessionExploreConditions(builder, filters, true)
	if err != nil {
		return "", nil, err
	}
	hotConditions, err := sessionExploreConditions(builder, filters, false)
	if err != nil {
		return "", nil, err
	}
	if cursor != nil {
		startedArg, idArg := builder.arg(cursor.startedAt), builder.arg(cursor.id)
		closedConditions = append(closedConditions, "(s.started_at,s.session_id) < ("+startedArg+","+idArg+")")
		hotConditions = append(hotConditions, "(s.start,s.session_id) < ("+startedArg+","+idArg+")")
	}
	profilesArg := builder.arg(profilesEnabled)
	limitArg := builder.arg(limit)
	querySQL := `with combined as (
  (
  select s.session_id,s.started_at,s.duration_ms::bigint as duration_ms,s.pageviews,s.custom as events,
         s.entry_path,s.exit_path,s.source_id,s.country,s.city_id,s.device,s.browser_id,s.os_id,s.profile_id,
         false as hot,null::timestamptz as last,null::date as local_day
  from sessions_closed s
  where s.site_id=` + siteArg + ` and s.day between ` + fromArg + `::date and ` + toArg + `::date
    and s.started_at is not null
    and exists (select 1 from day_close dc where dc.site_id=s.site_id and dc.day=s.day and dc.status='done')` + andSQL(closedConditions) + `
  order by s.started_at desc,s.session_id desc
  limit ` + limitArg + `
  )
  union all
  (
  select s.session_id,s.start,greatest(0,round(extract(epoch from (s.last-s.start))*1000))::bigint,
         s.pageviews,s.events,s.entry_path,s.exit_path,s.source_id,s.country,s.city_id,s.device,s.browser_id,s.os_id,
         null::bigint,true,s.last,s.local_day
  from sessions s
  where s.site_id=` + siteArg + ` and s.retention_class=` + retentionArg + `
    and s.local_day between ` + fromArg + `::date and ` + toArg + `::date
    and s.start>=` + startArg + ` and s.start<` + endArg + `
    and not exists (select 1 from day_close dc where dc.site_id=s.site_id and dc.day=s.local_day and dc.status='done')` + andSQL(hotConditions) + `
  order by s.start desc,s.session_id desc
  limit ` + limitArg + `
  )
)
select c.session_id,c.started_at,c.duration_ms,c.pageviews,c.events,
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.Path)) + ` and id=c.entry_path),''),
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.Path)) + ` and id=c.exit_path),''),
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.Source)) + ` and id=c.source_id),''),
       c.country,
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.City)) + ` and id=c.city_id),''),
       case c.device when 0 then 'desktop' when 1 then 'mobile' when 2 then 'tablet' else 'unknown' end,
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.Browser)) + ` and id=c.browser_id),''),
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.OS)) + ` and id=c.os_id),''),
       p.external_id
from (select * from combined order by started_at desc,session_id desc limit ` + limitArg + `) c
left join lateral (
  select case when not c.hot then c.profile_id else (
    select e.profile_id from events e
    where e.site_id=` + siteArg + ` and e.retention_class=` + retentionArg + ` and e.session_id=c.session_id
      and e.local_day=c.local_day and e.ts>=c.started_at and e.ts<c.last+interval '30 minutes'
      and e.profile_id is not null
    order by e.ts limit 1) end as profile_id
  where ` + profilesArg + `
) lp on true
left join profiles p on p.site_id=` + siteArg + ` and p.id=lp.profile_id
order by c.started_at desc,c.session_id desc`
	return querySQL, builder.args, nil
}

func sessionExploreConditions(builder *sqlBuilder, filters []api.Filter, closed bool) ([]string, error) {
	pageFilters, remaining := splitPageFilters(filters)
	special, sessionFilters := splitSpecialFilters(remaining)
	conditions, err := filterConditions(builder, sessionFilters, reportSessionResolver("s"))
	if err != nil {
		return nil, err
	}
	for _, filter := range pageFilters {
		var column string
		if closed {
			column = "u.code >> 2"
		} else {
			column = "pe.path_id"
		}
		matches, err := filterConditions(builder, []api.Filter{filter}, func(string) (string, *dict.Kind, error) {
			return column, dictionaryKind(dict.Path), nil
		})
		if err != nil {
			return nil, err
		}
		if closed {
			conditions = append(conditions, "exists (select 1 from unnest(s.seq) u(code) where u.code>0 and "+matches[0]+")")
		} else {
			conditions = append(conditions, "exists (select 1 from events pe where pe.site_id=s.site_id and pe.retention_class=s.retention_class and pe.local_day=s.local_day and pe.session_id=s.session_id and pe.kind=0 and "+matches[0]+")")
		}
	}
	var specialConditions []string
	if closed {
		specialConditions, err = closedSpecialConditions(builder, special)
	} else {
		specialConditions, err = hotSpecialConditions(builder, special)
		specialConditions = constrainReportEventConditions(specialConditions)
	}
	if err != nil {
		return nil, err
	}
	return append(conditions, specialConditions...), nil
}

func sessionSummarySQL(table string, profilesEnabled bool) string {
	profileExpr := "false"
	if profilesEnabled {
		profileExpr = "true"
	}
	return `select s.session_id,s.start,
       greatest(0,round(extract(epoch from (s.last-s.start))*1000))::bigint,s.pageviews,s.events,
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.Path)) + ` and id=s.entry_path),''),
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.Path)) + ` and id=s.exit_path),''),
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.Source)) + ` and id=s.source_id),''),
       s.country,coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.City)) + ` and id=s.city_id),''),
       case s.device when 0 then 'desktop' when 1 then 'mobile' when 2 then 'tablet' else 'unknown' end,
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.Browser)) + ` and id=s.browser_id),''),
       coalesce((select value from dict where kind=` + strconv.Itoa(int(dict.OS)) + ` and id=s.os_id),''),
       case when ` + profileExpr + ` then (select p.external_id from events e join profiles p on p.site_id=e.site_id and p.id=e.profile_id
         where e.site_id=s.site_id and e.retention_class=s.retention_class and e.local_day=s.local_day and e.session_id=s.session_id
           and e.ts>=s.start and e.ts<s.last+interval '30 minutes' and e.profile_id is not null order by e.ts limit 1) end,
       s.start,s.last,s.local_day
from ` + table + ` s`
}

type sessionSummaryScanner interface {
	Scan(...any) error
}

// scanSessionSummary scans the summary columns followed by any extra
// destinations the caller selected after them.
func scanSessionSummary(row sessionSummaryScanner, extra ...any) (api.SessionSummary, error) {
	var item api.SessionSummary
	var id int64
	dest := append([]any{&id, &item.StartedAt, &item.DurationMs, &item.Pageviews, &item.Events,
		&item.EntryPage, &item.ExitPage, &item.Source, &item.Country, &item.City,
		&item.Device, &item.Browser, &item.Os, &item.ExternalId}, extra...)
	err := row.Scan(dest...)
	item.Id = strconv.FormatInt(id, 10)
	return item, err
}

func encodeSessionCursor(startedAt time.Time, id int64) string {
	raw := strconv.FormatInt(startedAt.UnixNano(), 10) + ":" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeSessionCursor(value string) (sessionCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return sessionCursor{}, err
	}
	startedText, idText, ok := strings.Cut(string(raw), ":")
	if !ok {
		return sessionCursor{}, errors.New("invalid cursor")
	}
	started, err := strconv.ParseInt(startedText, 10, 64)
	if err != nil {
		return sessionCursor{}, err
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return sessionCursor{}, errors.New("invalid cursor")
	}
	return sessionCursor{startedAt: time.Unix(0, started).UTC(), id: id}, nil
}

func mustSessionID(value string) int64 {
	id, _ := strconv.ParseInt(value, 10, 64)
	return id
}
