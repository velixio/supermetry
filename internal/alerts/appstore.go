package alerts

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/apprelease"
	"supermetry/internal/approllup"
)

// firedRetention bounds how long dedup keys are kept; every key is only
// consulted within a day or an hour of its event.
const firedRetention = 30 * 24 * time.Hour

// AppStore reads the app alert candidates from Postgres and keeps the dedup
// keys. It implements AppSource and AppDedup.
type AppStore struct{ pool *pgxpool.Pool }

// NewAppStore returns the Postgres-backed source.
func NewAppStore(pool *pgxpool.Pool) *AppStore { return &AppStore{pool: pool} }

var (
	_ AppSource = (*AppStore)(nil)
	_ AppDedup  = (*AppStore)(nil)
)

func (s *AppStore) Site(ctx context.Context, siteID int32) (AppSite, error) {
	var site AppSite
	err := s.pool.QueryRow(ctx, `select s.name, o.slug, s.slug from sites s join orgs o on o.id = s.org_id where s.id = $1`, siteID).Scan(&site.Name, &site.OrgSlug, &site.SiteSlug)
	if err != nil {
		return AppSite{}, fmt.Errorf("read app site: %w", err)
	}
	return site, nil
}

func (s *AppStore) NewIssues(ctx context.Context, siteID int32, since time.Time, kinds []string, limit int) ([]AppIssue, error) {
	rows, err := s.pool.Query(ctx, `
select id, title, kind, first_release, event_count, first_seen from app_issues
where site_id = $1 and first_seen >= $2 and (coalesce(cardinality($3::text[]), 0) = 0 or kind = any($3))
order by first_seen, id limit $4`, siteID, since, kinds, limit)
	if err != nil {
		return nil, fmt.Errorf("read new app issues: %w", err)
	}
	defer rows.Close()
	var out []AppIssue
	for rows.Next() {
		var i AppIssue
		if err := rows.Scan(&i.ID, &i.Title, &i.Kind, &i.Release, &i.Events, &i.FirstSeen); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *AppStore) RegressedIssues(ctx context.Context, siteID int32, since time.Time, limit int) ([]AppIssue, error) {
	rows, err := s.pool.Query(ctx, `
select id, title, kind, last_release, event_count, first_seen, regressed_at from app_issues
where site_id = $1 and regressed_at >= $2 order by regressed_at, id limit $3`, siteID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("read regressed app issues: %w", err)
	}
	defer rows.Close()
	var out []AppIssue
	for rows.Next() {
		var i AppIssue
		if err := rows.Scan(&i.ID, &i.Title, &i.Kind, &i.Release, &i.Events, &i.FirstSeen, &i.RegressedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// LatestRelease applies the same definitions as the releases table (installs
// are distinct install hashes with any event; a crashed session needs a ping)
// to the events since the given time and returns the newest release by the
// apprelease ordering among those with at least minInstalls installs.
func (s *AppStore) LatestRelease(ctx context.Context, siteID int32, since time.Time, minInstalls int64) (*AppRelease, error) {
	crash := approllup.CrashPredicate("e.")
	rows, err := s.pool.Query(ctx, `
with ev as (
  select kind, level, install_hash, session_hash, release from app_events where site_id = $1 and ts >= $2 and release <> ''
), pings as (
  select distinct session_hash from ev where kind = 'session' and session_hash is not null
)
select e.release,
  count(distinct e.install_hash),
  count(distinct e.install_hash) filter (where `+crash+`),
  count(distinct e.session_hash) filter (where e.kind = 'session'),
  count(distinct e.session_hash) filter (where `+crash+` and p.session_hash is not null)
from ev e left join pings p on p.session_hash = e.session_hash
group by e.release`, siteID, since)
	if err != nil {
		return nil, fmt.Errorf("read app release health: %w", err)
	}
	defer rows.Close()
	byName := map[string]*AppRelease{}
	var names []string
	for rows.Next() {
		var name string
		var installs, crashedInstalls, sessions, crashedSessions int64
		if err := rows.Scan(&name, &installs, &crashedInstalls, &sessions, &crashedSessions); err != nil {
			return nil, err
		}
		if installs < minInstalls {
			continue
		}
		r := &AppRelease{Release: name, Installs: installs}
		v := 1 - float64(crashedInstalls)/float64(installs)
		r.CrashFreeUsers = &v
		if sessions > 0 {
			w := 1 - float64(crashedSessions)/float64(sessions)
			r.CrashFreeSessions = &w
		}
		byName[name] = r
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	spans, err := s.pool.Query(ctx, `select release, min(ts) from app_events where site_id = $1 and ts >= $3 and release = any($2) group by release`,
		siteID, names, since.AddDate(0, 0, -366))
	if err != nil {
		return nil, fmt.Errorf("read release first seen: %w", err)
	}
	defer spans.Close()
	firstSeen := map[string]time.Time{}
	for spans.Next() {
		var name string
		var ts time.Time
		if err := spans.Scan(&name, &ts); err != nil {
			return nil, err
		}
		firstSeen[name] = ts
	}
	if err := spans.Err(); err != nil {
		return nil, err
	}
	return byName[apprelease.SortNewestFirst(names, firstSeen)[0]], nil
}

func (s *AppStore) IssueHours(ctx context.Context, siteID int32, hour time.Time, minEvents int64) ([]IssueHour, error) {
	same := make([]time.Time, 0, spikeBaselineDays)
	for k := 1; k <= spikeBaselineDays; k++ {
		same = append(same, hour.AddDate(0, 0, -k))
	}
	rows, err := s.pool.Query(ctx, `
select h.issue_id, i.title, i.kind, i.last_release, h.events,
  coalesce((select sum(b.events) from app_issue_hourly b where b.site_id = h.site_id and b.issue_id = h.issue_id and b.hour = any($3)), 0)::bigint
from app_issue_hourly h join app_issues i on i.id = h.issue_id
where h.site_id = $1 and h.hour = $2 and h.events >= $4
order by h.events desc, h.issue_id`, siteID, hour, same, minEvents)
	if err != nil {
		return nil, fmt.Errorf("read app issue hours: %w", err)
	}
	defer rows.Close()
	var out []IssueHour
	for rows.Next() {
		var h IssueHour
		if err := rows.Scan(&h.ID, &h.Title, &h.Kind, &h.Release, &h.Events, &h.BaselineSum); err != nil {
			return nil, err
		}
		h.AppIssue.Events = h.Events
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *AppStore) AppFired(ctx context.Context, alertID int64, keys []string) (map[string]time.Time, error) {
	rows, err := s.pool.Query(ctx, `select dedup_key, fired_at from app_alert_fired where alert_id = $1 and dedup_key = any($2)`, alertID, keys)
	if err != nil {
		return nil, fmt.Errorf("read app alert dedup: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var key string
		var at time.Time
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		out[key] = at
	}
	return out, rows.Err()
}

func (s *AppStore) RecordAppFired(ctx context.Context, alertID int64, key string, at time.Time) error {
	if _, err := s.pool.Exec(ctx, `insert into app_alert_fired(alert_id, dedup_key, fired_at) values ($1, $2, $3)
on conflict (alert_id, dedup_key) do update set fired_at = excluded.fired_at`, alertID, key, at); err != nil {
		return fmt.Errorf("record app alert dedup: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `delete from app_alert_fired where alert_id = $1 and fired_at < $2`, alertID, at.Add(-firedRetention)); err != nil {
		return fmt.Errorf("prune app alert dedup: %w", err)
	}
	return nil
}

func (s *AppStore) ForgetAppFired(ctx context.Context, alertID int64, key string) error {
	if _, err := s.pool.Exec(ctx, `delete from app_alert_fired where alert_id = $1 and dedup_key = $2`, alertID, key); err != nil {
		return fmt.Errorf("forget app alert dedup: %w", err)
	}
	return nil
}
