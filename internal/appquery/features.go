package appquery

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxFeatureSetsPerSite = 100
	maxFeatureNameLen     = 80
	maxFeatureEvents      = 20
)

// ErrDuplicate reports a feature set whose name already exists for the site.
var ErrDuplicate = errors.New("already exists")

// FeatureSet is a named group of product event names counted as one feature.
type FeatureSet struct {
	ID         int64
	Name       string
	EventNames []string
	CreatedAt  time.Time
}

// DayCount is a count for one local day.
type DayCount struct {
	Day   time.Time
	Count int64
}

// FeatureAdoption is how many of the active installs used a feature set.
type FeatureAdoption struct {
	Users       int64      // distinct installs with any event of the set
	ActiveUsers int64      // distinct installs with any product activity
	Share       *float64   // Users / ActiveUsers; nil without active installs
	Series      []DayCount // distinct installs using the set per local day
}

func validFeatureSet(name string, events []string) (string, []string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxFeatureNameLen {
		return "", nil, invalid("name must be 1 to %d characters", maxFeatureNameLen)
	}
	if len(events) < 1 || len(events) > maxFeatureEvents {
		return "", nil, invalid("a feature set needs 1 to %d event names", maxFeatureEvents)
	}
	seen := make(map[string]struct{}, len(events))
	out := make([]string, 0, len(events))
	for _, e := range events {
		e = strings.TrimSpace(e)
		if e == "" || utf8.RuneCountInString(e) > 200 {
			return "", nil, invalid("event names must be 1 to 200 characters")
		}
		if _, dup := seen[e]; !dup {
			seen[e] = struct{}{}
			out = append(out, e)
		}
	}
	return name, out, nil
}

// ListFeatureSets returns the site's feature sets by name.
func (q *Querier) ListFeatureSets(ctx context.Context, scope Scope) ([]FeatureSet, error) {
	rows, err := q.pool.Query(ctx, `select id,name,event_names,created_at from app_feature_sets where site_id=$1 order by name`, scope.SiteID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FeatureSet, error) {
		var s FeatureSet
		err := r.Scan(&s.ID, &s.Name, &s.EventNames, &s.CreatedAt)
		return s, err
	})
}

// CreateFeatureSet stores a feature set; a repeated name returns ErrDuplicate.
func (q *Querier) CreateFeatureSet(ctx context.Context, scope Scope, name string, events []string) (FeatureSet, error) {
	name, events, err := validFeatureSet(name, events)
	if err != nil {
		return FeatureSet{}, err
	}
	var count int
	if err := q.pool.QueryRow(ctx, `select count(*) from app_feature_sets where site_id=$1`, scope.SiteID).Scan(&count); err != nil {
		return FeatureSet{}, err
	}
	if count >= maxFeatureSetsPerSite {
		return FeatureSet{}, invalid("at most %d feature sets per project", maxFeatureSetsPerSite)
	}
	set := FeatureSet{Name: name, EventNames: events}
	err = q.pool.QueryRow(ctx, `insert into app_feature_sets(site_id,name,event_names) values ($1,$2,$3) returning id,created_at`,
		scope.SiteID, name, events).Scan(&set.ID, &set.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return FeatureSet{}, ErrDuplicate
	}
	return set, err
}

// DeleteFeatureSet removes a set of this site; another site's id is ErrNotFound.
func (q *Querier) DeleteFeatureSet(ctx context.Context, scope Scope, id int64) error {
	tag, err := q.pool.Exec(ctx, `delete from app_feature_sets where id=$1 and site_id=$2`, id, scope.SiteID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FeatureAdoption counts the installs that fired any event of the set in the
// range. The denominator is every install with a product event in the range.
func (q *Querier) FeatureAdoption(ctx context.Context, scope Scope, f Filter, setID int64) (FeatureAdoption, error) {
	var names []string
	err := q.pool.QueryRow(ctx, `select event_names from app_feature_sets where id=$1 and site_id=$2`, setID, scope.SiteID).Scan(&names)
	if errors.Is(err, pgx.ErrNoRows) {
		return FeatureAdoption{}, ErrNotFound
	}
	if err != nil {
		return FeatureAdoption{}, err
	}
	w, err := q.window(scope, f)
	if err != nil {
		return FeatureAdoption{}, err
	}
	tz := scope.loc().String()
	rows, err := q.pool.Query(ctx, `
with scoped as (
  select e.visitor_id, (e.ts at time zone $4)::date as day,
         (e.kind = 1 and n.value = any($5::text[])) as hit
  from events e
  left join dict n on n.kind = 7 and n.id = e.name_id and e.kind = 1
  where e.site_id = $1 and e.ts >= $2 and e.ts < $3
)
select day, count(distinct visitor_id) filter (where hit)::int8, 0::int8 from scoped group by day
union all
select null, count(distinct visitor_id) filter (where hit)::int8, count(distinct visitor_id)::int8 from scoped`,
		scope.SiteID, w.start, w.end, tz, names)
	if err != nil {
		return FeatureAdoption{}, err
	}
	defer rows.Close()
	perDay := map[string]int64{}
	out := FeatureAdoption{}
	for rows.Next() {
		var day *time.Time
		var users, active int64
		if err := rows.Scan(&day, &users, &active); err != nil {
			return FeatureAdoption{}, err
		}
		if day == nil {
			out.Users, out.ActiveUsers = users, active
		} else {
			perDay[day.Format(time.DateOnly)] = users
		}
	}
	if err := rows.Err(); err != nil {
		return FeatureAdoption{}, err
	}
	for i, d := range w.days {
		out.Series = append(out.Series, DayCount{Day: w.dayStarts[i], Count: perDay[d]})
	}
	if out.ActiveUsers > 0 {
		share := float64(out.Users) / float64(out.ActiveUsers)
		out.Share = &share
	}
	return out, nil
}
