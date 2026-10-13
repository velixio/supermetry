package sitesvc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/query"
	"supermetry/internal/realtime"
	"supermetry/internal/session"
	coresites "supermetry/internal/sites"
)

const (
	OverviewMaxLimit = 200
	overviewCacheTTL = 5 * time.Second
)

type OverviewPeriod string

const (
	Overview24Hours OverviewPeriod = "24h"
	Overview7Days   OverviewPeriod = "7d"
	Overview30Days  OverviewPeriod = "30d"
)

type OverviewOrganization struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type OverviewPoint struct {
	Start    time.Time `json:"start"`
	Visitors int64     `json:"visitors"`
}

type OverviewSite struct {
	ID               int32                   `json:"id"`
	OrgID            int64                   `json:"org_id"`
	PublicID         string                  `json:"public_id"`
	Name             string                  `json:"name"`
	Domain           string                  `json:"domain"`
	Timezone         string                  `json:"timezone"`
	IconURL          string                  `json:"icon_url,omitempty"`
	Organization     OverviewOrganization    `json:"organization"`
	Role             authz.Role              `json:"role"`
	CreatedAt        time.Time               `json:"created_at"`
	Visitors         int64                   `json:"visitors"`
	PreviousVisitors int64                   `json:"previous_visitors"`
	ChangePercent    *float64                `json:"change_percent"`
	Sparkline        []OverviewPoint         `json:"sparkline"`
	LiveVisitors     int                     `json:"live_visitors"`
	LastEventAt      *time.Time              `json:"last_event_at"`
	InstallStatus    coresites.InstallStatus `json:"install_status"`
	TrackingSnippet  string                  `json:"tracking_snippet"`
}

type overviewCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int32     `json:"id"`
}

type overviewCacheEntry struct {
	expires time.Time
	values  map[int32]overviewFigures
}

type overviewFigures struct {
	visitors, previous int64
	points             []OverviewPoint
}

type Overview struct {
	store    *store.Store
	sessions session.Store
	now      func() time.Time
	mu       sync.Mutex
	cache    map[string]overviewCacheEntry
}

func NewOverview(data *store.Store, sessions session.Store) *Overview {
	return &Overview{store: data, sessions: sessions, now: time.Now, cache: make(map[string]overviewCacheEntry)}
}

func (s *Overview) SetClock(now func() time.Time) { s.now = now }

func ParseOverviewPeriod(value string) (OverviewPeriod, error) {
	period := OverviewPeriod(value)
	switch period {
	case Overview24Hours, Overview7Days, Overview30Days:
		return period, nil
	default:
		return "", fmt.Errorf("%w: period must be 24h, 7d, or 30d", ErrInvalid)
	}
}

func (s *Overview) List(ctx context.Context, userID int64, period OverviewPeriod, cursor string, limit int) ([]OverviewSite, string, error) {
	if s == nil || s.store == nil || userID <= 0 {
		return nil, "", ErrForbidden
	}
	if _, err := ParseOverviewPeriod(string(period)); err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > OverviewMaxLimit {
		return nil, "", fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, OverviewMaxLimit)
	}
	after, hasAfter, err := decodeOverviewCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	items, err := s.readableSites(ctx, userID, after, hasAfter, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		last := items[limit-1]
		next = encodeOverviewCursor(overviewCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		items = items[:limit]
	}
	now := s.now().UTC()
	figures, err := s.figures(ctx, items, period, now)
	if err != nil {
		return nil, "", err
	}
	for index := range items {
		figure := figures[items[index].ID]
		items[index].Visitors = figure.visitors
		items[index].PreviousVisitors = figure.previous
		items[index].Sparkline = append([]OverviewPoint(nil), figure.points...)
		if figure.previous != 0 {
			change := (float64(figure.visitors-figure.previous) / float64(figure.previous)) * 100
			items[index].ChangePercent = &change
		}
		items[index].LiveVisitors = realtime.RichSnapshot(s.sessions, items[index].ID, now).ActiveVisitors
		items[index].InstallStatus = coresites.InstallStatusAt(items[index].LastEventAt, now)
	}
	return items, next, nil
}

func (s *Overview) readableSites(ctx context.Context, userID int64, after overviewCursor, hasAfter bool, limit int) ([]OverviewSite, error) {
	query := `
select s.id,s.org_id,s.public_id,s.name,s.allowed_hosts,s.timezone,
       coalesce(s.settings->>'icon_url',''),o.id,o.name,o.slug,m.role,s.created_at,
       le.last_event_at
from memberships m
join orgs o on o.id=m.org_id and o.deleted_at is null
join sites s on s.org_id=m.org_id and s.deleted_at is null
left join site_last_event le on le.site_id=s.id
where m.user_id=$1 and (m.site_ids is null or s.id=any(m.site_ids))`
	args := []any{userID}
	if hasAfter {
		query += ` and (s.created_at,s.id)>($2,$3) order by s.created_at,s.id limit $4`
		args = append(args, after.CreatedAt, after.ID, limit)
	} else {
		query += ` order by s.created_at,s.id limit $2`
		args = append(args, limit)
	}
	rows, err := s.store.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list readable sites: %w", err)
	}
	defer rows.Close()
	items := make([]OverviewSite, 0, limit)
	for rows.Next() {
		var item OverviewSite
		var hosts []string
		if err := rows.Scan(&item.ID, &item.OrgID, &item.PublicID, &item.Name, &hosts, &item.Timezone,
			&item.IconURL, &item.Organization.ID, &item.Organization.Name, &item.Organization.Slug,
			&item.Role, &item.CreatedAt, &item.LastEventAt); err != nil {
			return nil, err
		}
		if len(hosts) > 0 {
			item.Domain = strings.TrimPrefix(hosts[0], "*.")
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Overview) figures(ctx context.Context, sites []OverviewSite, period OverviewPeriod, now time.Time) (map[int32]overviewFigures, error) {
	ids := make([]int32, len(sites))
	for index := range sites {
		ids[index] = sites[index].ID
	}
	keyBytes, _ := json.Marshal(struct {
		IDs    []int32
		Period OverviewPeriod
	}{ids, period})
	key := string(keyBytes)
	s.mu.Lock()
	if cached, ok := s.cache[key]; ok && now.Before(cached.expires) {
		values := cloneOverviewFigures(cached.values)
		s.mu.Unlock()
		return values, nil
	}
	s.mu.Unlock()

	values := make(map[int32]overviewFigures, len(sites))
	for _, site := range sites {
		location, err := time.LoadLocation(site.Timezone)
		if err != nil {
			location = time.UTC
		}
		points := 24
		if period == Overview7Days {
			points = 7
		} else if period == Overview30Days {
			points = 30
		}
		localNow := now.In(location)
		start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), localNow.Hour(), 0, 0, 0, location).Add(time.Duration(-(points - 1)) * time.Hour)
		if period != Overview24Hours {
			start = time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -(points - 1))
		}
		figure := overviewFigures{points: make([]OverviewPoint, points)}
		for i := range figure.points {
			at := start.Add(time.Duration(i) * time.Hour)
			if period != Overview24Hours {
				at = start.AddDate(0, 0, i)
			}
			figure.points[i].Start = at
		}
		values[site.ID] = figure
	}
	if len(ids) != 0 {
		var err error
		if period == Overview24Hours {
			err = s.loadHourlyFigures(ctx, sites, values, now)
		} else {
			err = s.loadDailyFigures(ctx, sites, values, now, period)
		}
		if err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	if len(s.cache) >= 512 {
		for cachedKey, cached := range s.cache {
			if !now.Before(cached.expires) {
				delete(s.cache, cachedKey)
			}
		}
		if len(s.cache) >= 512 {
			for cachedKey := range s.cache {
				delete(s.cache, cachedKey)
				break
			}
		}
	}
	s.cache[key] = overviewCacheEntry{expires: now.Add(overviewCacheTTL), values: cloneOverviewFigures(values)}
	s.mu.Unlock()
	return values, nil
}

func (s *Overview) loadHourlyFigures(ctx context.Context, sites []OverviewSite, values map[int32]overviewFigures, now time.Time) error {
	ids := overviewIDs(sites)
	rows, err := s.store.Pool.Query(ctx, `
select v.site_id,
       date_trunc('hour',v.minute at time zone st.timezone) at time zone st.timezone as bucket,
       sum(v.visitors)::bigint
from visitors_per_minute v join unnest($1::int[], $3::text[]) as st(id,timezone) on st.id=v.site_id
where v.site_id=any($1) and v.minute >= $2::timestamptz - interval '48 hours' and v.minute < $2::timestamptz + interval '1 hour'
group by v.site_id,bucket order by v.site_id,bucket`, ids, now, s.databaseZones(ctx, sites))
	if err != nil {
		return fmt.Errorf("query hourly overview: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var siteID int32
		var bucket time.Time
		var visitors int64
		if err := rows.Scan(&siteID, &bucket, &visitors); err != nil {
			return err
		}
		figure := values[siteID]
		if len(figure.points) == 0 {
			continue
		}
		start := figure.points[0].Start
		end := figure.points[len(figure.points)-1].Start.Add(time.Hour)
		if !bucket.Before(start) && bucket.Before(end) {
			index := int(bucket.Sub(start) / time.Hour)
			figure.points[index].Visitors += visitors
			figure.visitors += visitors
		} else if !bucket.Before(start.Add(-24*time.Hour)) && bucket.Before(start) {
			figure.previous += visitors
		}
		values[siteID] = figure
	}
	return rows.Err()
}

func (s *Overview) loadDailyFigures(ctx context.Context, sites []OverviewSite, values map[int32]overviewFigures, now time.Time, period OverviewPeriod) error {
	ids := overviewIDs(sites)
	rows, err := s.store.Pool.Query(ctx, `
with daily as (
  select r.site_id,r.day,r.visitors::bigint from rollup_day r where r.site_id=any($1) and r.day >= ($2::date - 61)
  union all
  select v.site_id,(v.minute at time zone st.timezone)::date,sum(v.visitors)::bigint
  from visitors_per_minute v join unnest($1::int[], $3::text[]) as st(id,timezone) on st.id=v.site_id
  where v.site_id=any($1) and v.minute >= $2::timestamptz - interval '61 days'
    and not exists (select 1 from day_close dc where dc.site_id=v.site_id and dc.day=(v.minute at time zone st.timezone)::date and dc.status='done')
  group by v.site_id,(v.minute at time zone st.timezone)::date
)
select site_id,day,sum(visitors)::bigint from daily group by site_id,day order by site_id,day`, ids, now, s.databaseZones(ctx, sites))
	if err != nil {
		return fmt.Errorf("query daily overview: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var siteID int32
		var day time.Time
		var visitors int64
		if err := rows.Scan(&siteID, &day, &visitors); err != nil {
			return err
		}
		figure := values[siteID]
		if len(figure.points) == 0 {
			continue
		}
		dayKey := day.Format(time.DateOnly)
		matched := false
		for index := range figure.points {
			if figure.points[index].Start.Format(time.DateOnly) == dayKey {
				figure.points[index].Visitors += visitors
				figure.visitors += visitors
				matched = true
				break
			}
		}
		if !matched {
			periodDays := 7
			if period == Overview30Days {
				periodDays = 30
			}
			previousStart := figure.points[0].Start.AddDate(0, 0, -periodDays)
			if !day.Before(previousStart) && day.Before(figure.points[0].Start) {
				figure.previous += visitors
			}
		}
		values[siteID] = figure
	}
	return rows.Err()
}

// databaseZones returns each site's time zone as a name the database accepts,
// aligned with overviewIDs. Go resolves legacy aliases such as Asia/Calcutta
// that a PostgreSQL build without them rejects, which would fail the query.
func (s *Overview) databaseZones(ctx context.Context, sites []OverviewSite) []string {
	zones := make([]string, len(sites))
	for index := range sites {
		location, err := time.LoadLocation(sites[index].Timezone)
		if err != nil {
			location = time.UTC
		}
		zones[index] = query.PostgresZoneName(ctx, s.store.Pool, location)
	}
	return zones
}

func overviewIDs(sites []OverviewSite) []int32 {
	ids := make([]int32, len(sites))
	for index := range sites {
		ids[index] = sites[index].ID
	}
	return ids
}

func cloneOverviewFigures(source map[int32]overviewFigures) map[int32]overviewFigures {
	clone := make(map[int32]overviewFigures, len(source))
	for id, figure := range source {
		figure.points = append([]OverviewPoint(nil), figure.points...)
		clone[id] = figure
	}
	return clone
}

func encodeOverviewCursor(cursor overviewCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeOverviewCursor(value string) (overviewCursor, bool, error) {
	if value == "" {
		return overviewCursor{}, false, nil
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return overviewCursor{}, false, fmt.Errorf("%w: cursor is invalid", ErrInvalid)
	}
	var cursor overviewCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID <= 0 {
		return overviewCursor{}, false, fmt.Errorf("%w: cursor is invalid", ErrInvalid)
	}
	return cursor, true, nil
}
