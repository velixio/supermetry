// Package ghstars records the star history of the instance's own GitHub
// repository for the instance admin: a webhook for live stars and unstars, a
// daily reconcile that repairs missed deliveries, and aggregate queries.
package ghstars

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event sources and actions as stored in github_star_events.
const (
	ActionStarred   = "starred"
	ActionUnstarred = "unstarred"

	SourceWebhook   = "webhook"
	SourceBackfill  = "backfill"
	SourceReconcile = "reconcile"
)

const (
	// KeepWatchers leaves the stored watcher count unchanged when passed as Counts.Watchers.
	KeepWatchers = -1

	maxHourBuckets = 24 * 31
	maxDayBuckets  = 366 * 5
	maxEventsLimit = 200
	maxLabelLength = 200
)

var (
	// ErrNotFound reports a missing annotation.
	ErrNotFound = errors.New("ghstars: not found")
	// ErrInvalid reports a bad argument such as an empty label or an oversized range.
	ErrInvalid = errors.New("ghstars: invalid argument")
)

// Counts are the repository counters reported by GitHub.
type Counts struct {
	Stars      int `json:"stars"`
	Forks      int `json:"forks"`
	Watchers   int `json:"watchers"`
	OpenIssues int `json:"open_issues"`
}

// Event is one star or unstar by one user.
type Event struct {
	ID         int64     `json:"id"`
	DeliveryID string    `json:"-"`
	UserID     int64     `json:"user_id"`
	Login      string    `json:"login"`
	Action     string    `json:"action"`
	OccurredAt time.Time `json:"occurred_at"`
	Source     string    `json:"source"`
}

// Summary is the live state plus the recent net change.
type Summary struct {
	Counts
	UpdatedAt *time.Time `json:"updated_at"`
	Net24h    int        `json:"net_24h"`
	Net7d     int        `json:"net_7d"`
}

// Point is one chart bucket. Bucket is the start of the bucket in the store's time zone.
type Point struct {
	Bucket     time.Time `json:"bucket"`
	Gained     int       `json:"gained"`
	Lost       int       `json:"lost"`
	Net        int       `json:"net"`
	Cumulative int       `json:"cumulative"`
}

// Annotation is a manual marker on the chart.
type Annotation struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Label     string    `json:"label"`
	CreatedBy *int64    `json:"created_by"`
}

// Store reads and writes the star tables for one repository.
type Store struct {
	pool *pgxpool.Pool
	repo string
	loc  *time.Location
}

// NewStore returns a store for repo (owner/name). Aggregates are bucketed in loc.
func NewStore(pool *pgxpool.Pool, repo string, loc *time.Location) *Store {
	if loc == nil {
		loc = time.UTC
	}
	return &Store{pool: pool, repo: repo, loc: loc}
}

// Repo returns the tracked repository.
func (s *Store) Repo() string { return s.repo }

// RecordEvent stores a webhook event and updates the live counters in one
// transaction. It reports false, writing nothing, when the delivery id was
// already recorded.
func (s *Store) RecordEvent(ctx context.Context, e Event, counts Counts, now time.Time) (bool, error) {
	var recorded bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			insert into github_star_events(delivery_id,repo,user_id,login,action,occurred_at,source)
			values (nullif($1,''),$2,$3,$4,$5,$6,$7)
			on conflict (delivery_id) do nothing`,
			e.DeliveryID, s.repo, e.UserID, e.Login, e.Action, e.OccurredAt, e.Source)
		if err != nil {
			return fmt.Errorf("insert star event: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		recorded = true
		return saveState(ctx, tx, s.repo, counts, now)
	})
	return recorded, err
}

// InsertEvents bulk-inserts events without delivery ids (backfill and reconcile).
func (s *Store) InsertEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	rows := make([][]any, len(events))
	for i, e := range events {
		rows[i] = []any{s.repo, e.UserID, e.Login, e.Action, e.OccurredAt, e.Source}
	}
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"github_star_events"},
		[]string{"repo", "user_id", "login", "action", "occurred_at", "source"}, pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("copy star events: %w", err)
	}
	return nil
}

// SaveState sets the live counters.
func (s *Store) SaveState(ctx context.Context, counts Counts, now time.Time) error {
	return saveState(ctx, s.pool, s.repo, counts, now)
}

type dbExec interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func saveState(ctx context.Context, db dbExec, repo string, c Counts, now time.Time) error {
	_, err := db.Exec(ctx, `
		insert into github_star_state(repo,stars,forks,watchers,open_issues,updated_at)
		values ($1,$2,$3,greatest($4::int,0),$5,$6)
		on conflict (repo) do update set stars=excluded.stars, forks=excluded.forks,
			watchers=case when $4::int < 0 then github_star_state.watchers else $4::int end,
			open_issues=excluded.open_issues, updated_at=excluded.updated_at`,
		repo, c.Stars, c.Forks, c.Watchers, c.OpenIssues, now)
	if err != nil {
		return fmt.Errorf("save star state: %w", err)
	}
	return nil
}

// SaveSnapshot upserts the snapshot for a calendar day.
func (s *Store) SaveSnapshot(ctx context.Context, day time.Time, c Counts) error {
	_, err := s.pool.Exec(ctx, `
		insert into github_star_snapshots(repo,day,stars,forks,watchers,open_issues)
		values ($1,$2::date,$3,$4,$5,$6)
		on conflict (repo,day) do update set stars=excluded.stars, forks=excluded.forks,
			watchers=excluded.watchers, open_issues=excluded.open_issues`,
		s.repo, day.Format(time.DateOnly), c.Stars, c.Forks, c.Watchers, c.OpenIssues)
	if err != nil {
		return fmt.Errorf("save star snapshot: %w", err)
	}
	return nil
}

// Stars returns the stored live star count and whether a state row exists.
func (s *Store) Stars(ctx context.Context) (int, bool, error) {
	var stars int
	err := s.pool.QueryRow(ctx, `select stars from github_star_state where repo=$1`, s.repo).Scan(&stars)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return stars, err == nil, err
}

// EventCount returns how many events are stored.
func (s *Store) EventCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `select count(*) from github_star_events where repo=$1`, s.repo).Scan(&n)
	return n, err
}

// CurrentStargazers returns user id to login for every user whose latest event is a star.
func (s *Store) CurrentStargazers(ctx context.Context) (map[int64]string, error) {
	rows, err := s.pool.Query(ctx, `
		select user_id, login from (
			select distinct on (user_id) user_id, login, action
			from github_star_events where repo=$1
			order by user_id, occurred_at desc, id desc) latest
		where action='starred'`, s.repo)
	if err != nil {
		return nil, fmt.Errorf("query current stargazers: %w", err)
	}
	defer rows.Close()
	current := make(map[int64]string)
	for rows.Next() {
		var id int64
		var login string
		if err := rows.Scan(&id, &login); err != nil {
			return nil, err
		}
		current[id] = login
	}
	return current, rows.Err()
}

// Summary returns the live counters and the net change over the last 24 hours and 7 days.
func (s *Store) Summary(ctx context.Context, now time.Time) (Summary, error) {
	var out Summary
	var updated *time.Time
	err := s.pool.QueryRow(ctx, `select stars,forks,watchers,open_issues,updated_at from github_star_state where repo=$1`, s.repo).
		Scan(&out.Stars, &out.Forks, &out.Watchers, &out.OpenIssues, &updated)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, fmt.Errorf("read star state: %w", err)
	}
	out.UpdatedAt = updated
	err = s.pool.QueryRow(ctx, `
		select coalesce(sum(case action when 'starred' then 1 else -1 end) filter (where occurred_at > $2),0),
		       coalesce(sum(case action when 'starred' then 1 else -1 end),0)
		from github_star_events where repo=$1 and occurred_at > $3 and occurred_at <= $4`,
		s.repo, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour), now).Scan(&out.Net24h, &out.Net7d)
	if err != nil {
		return Summary{}, fmt.Errorf("read star change: %w", err)
	}
	return out, nil
}

const seriesSQL = `
select g.bucket, coalesce(c.gained,0), coalesce(c.lost,0)
from generate_series(
        date_trunc($1::text, $2::timestamptz at time zone $3::text),
        date_trunc($1::text, ($4::timestamptz - interval '1 microsecond') at time zone $3::text),
        $5::interval) as g(bucket)
left join (
    select date_trunc($1::text, occurred_at at time zone $3::text) as bucket,
           count(*) filter (where action='starred') as gained,
           count(*) filter (where action='unstarred') as lost
    from github_star_events
    where repo=$6 and occurred_at >= $2 and occurred_at < $4
    group by 1) c using (bucket)
order by g.bucket`

// Series returns gained, lost, net and cumulative stars per bucket for
// [from, to), bucketed in the store's time zone. granularity is "hour" or "day".
// Buckets are local wall-clock hours or days, so a daylight-saving change can
// repeat or skip an hour in hour mode.
func (s *Store) Series(ctx context.Context, granularity string, from, to time.Time) ([]Point, error) {
	step, limit := "", 0
	switch granularity {
	case "hour":
		step, limit = "1 hour", maxHourBuckets
	case "day":
		step, limit = "1 day", maxDayBuckets
	default:
		return nil, fmt.Errorf("%w: granularity must be hour or day", ErrInvalid)
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: to must be after from", ErrInvalid)
	}
	approx := int(to.Sub(from)/time.Hour) + 2
	if granularity == "day" {
		approx = approx/24 + 2
	}
	if approx > limit {
		return nil, fmt.Errorf("%w: range is too long for %s buckets", ErrInvalid, granularity)
	}
	var baseline int
	err := s.pool.QueryRow(ctx, `
		select coalesce(sum(case action when 'starred' then 1 else -1 end),0)
		from github_star_events where repo=$1 and occurred_at < $2`, s.repo, from).Scan(&baseline)
	if err != nil {
		return nil, fmt.Errorf("read star baseline: %w", err)
	}
	rows, err := s.pool.Query(ctx, seriesSQL, granularity, from, s.loc.String(), to, step, s.repo)
	if err != nil {
		return nil, fmt.Errorf("query star series: %w", err)
	}
	defer rows.Close()
	points := []Point{}
	cumulative := baseline
	for rows.Next() {
		var local time.Time
		var p Point
		if err := rows.Scan(&local, &p.Gained, &p.Lost); err != nil {
			return nil, err
		}
		p.Bucket = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), 0, 0, s.loc)
		p.Net = p.Gained - p.Lost
		cumulative += p.Net
		p.Cumulative = cumulative
		points = append(points, p)
	}
	return points, rows.Err()
}

// Events returns the user-attributed log, newest first. cursor is empty for the
// first page; the returned next cursor is empty on the last page.
func (s *Store) Events(ctx context.Context, cursor string, limit int) ([]Event, string, error) {
	if limit < 1 || limit > maxEventsLimit {
		return nil, "", fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, maxEventsLimit)
	}
	var at time.Time
	var id int64
	if cursor == "" {
		at, id = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), 1<<62
	} else {
		micros, rest, ok := strings.Cut(cursor, "_")
		m, err1 := strconv.ParseInt(micros, 10, 64)
		i, err2 := strconv.ParseInt(rest, 10, 64)
		if !ok || err1 != nil || err2 != nil {
			return nil, "", fmt.Errorf("%w: cursor is malformed", ErrInvalid)
		}
		at, id = time.UnixMicro(m), i
	}
	rows, err := s.pool.Query(ctx, `
		select id,user_id,login,action,occurred_at,source from github_star_events
		where repo=$1 and (occurred_at,id) < ($2,$3)
		order by occurred_at desc, id desc limit $4`, s.repo, at, id, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("query star events: %w", err)
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.UserID, &e.Login, &e.Action, &e.OccurredAt, &e.Source); err != nil {
			return nil, "", err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(events) > limit {
		events = events[:limit]
		last := events[limit-1]
		next = strconv.FormatInt(last.OccurredAt.UnixMicro(), 10) + "_" + strconv.FormatInt(last.ID, 10)
	}
	return events, next, nil
}

// Annotations lists the chart markers, oldest first.
func (s *Store) Annotations(ctx context.Context) ([]Annotation, error) {
	rows, err := s.pool.Query(ctx, `select id,at,label,created_by from github_star_annotations where repo=$1 order by at, id`, s.repo)
	if err != nil {
		return nil, fmt.Errorf("query star annotations: %w", err)
	}
	defer rows.Close()
	items := []Annotation{}
	for rows.Next() {
		var a Annotation
		if err := rows.Scan(&a.ID, &a.At, &a.Label, &a.CreatedBy); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// AddAnnotation stores a marker.
func (s *Store) AddAnnotation(ctx context.Context, at time.Time, label string, createdBy int64) (Annotation, error) {
	label = strings.TrimSpace(label)
	if n := len([]rune(label)); n < 1 || n > maxLabelLength {
		return Annotation{}, fmt.Errorf("%w: label must be 1 to %d characters", ErrInvalid, maxLabelLength)
	}
	a := Annotation{At: at, Label: label}
	err := s.pool.QueryRow(ctx, `
		insert into github_star_annotations(repo,at,label,created_by) values ($1,$2,$3,nullif($4,0))
		returning id, created_by`, s.repo, at, label, createdBy).Scan(&a.ID, &a.CreatedBy)
	if err != nil {
		return Annotation{}, fmt.Errorf("insert star annotation: %w", err)
	}
	return a, nil
}

// DeleteAnnotation removes a marker.
func (s *Store) DeleteAnnotation(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `delete from github_star_annotations where repo=$1 and id=$2`, s.repo, id)
	if err != nil {
		return fmt.Errorf("delete star annotation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// EraseUser deletes every event of a login (privacy erasure) and writes an
// instance audit entry that records the count but not the login. Snapshots and
// the live counters are left as they are.
func (s *Store) EraseUser(ctx context.Context, login string, actorID int64) (int64, error) {
	var deleted int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from github_star_events where repo=$1 and lower(login)=lower($2)`, s.repo, login)
		if err != nil {
			return fmt.Errorf("erase star events: %w", err)
		}
		deleted = tag.RowsAffected()
		_, err = tx.Exec(ctx, `
			insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata)
			values (null,nullif($1::bigint,0),'instance.github_stars.user_erased','instance',1,jsonb_build_object('events',$2::bigint))`,
			actorID, deleted)
		if err != nil {
			return fmt.Errorf("audit star erasure: %w", err)
		}
		return nil
	})
	return deleted, err
}

// Location returns the time zone aggregates are bucketed in.
func (s *Store) Location() *time.Location { return s.loc }
