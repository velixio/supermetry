package query

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// zoneResolver maps a Go time zone to a name PostgreSQL accepts. A database
// built without every legacy alias (Asia/Calcutta, Asia/Katmandu, ...) rejects
// names that Go resolves, so for an unknown name it finds a known zone with
// identical offsets over a multi-year window and uses that instead.
type zoneResolver struct {
	load  func(context.Context) ([]string, error)
	mu    sync.Mutex
	known map[string]struct{}
	names []string
	cache map[string]string
}

var zoneResolvers sync.Map

func postgresZone(ctx context.Context, pool *pgxpool.Pool, location *time.Location) string {
	if location == nil {
		return "UTC"
	}
	resolver, _ := zoneResolvers.LoadOrStore(pool, &zoneResolver{load: func(ctx context.Context) ([]string, error) {
		rows, err := pool.Query(ctx, `select name from pg_timezone_names`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			names = append(names, name)
		}
		return names, rows.Err()
	}})
	return resolver.(*zoneResolver).resolve(ctx, location)
}

func (r *zoneResolver) resolve(ctx context.Context, location *time.Location) string {
	name := location.String()
	if name == "" || name == "Local" || name == "UTC" {
		return "UTC"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.cache[name]; ok {
		return cached
	}
	if r.known == nil {
		names, err := r.load(ctx)
		if err != nil || len(names) == 0 {
			return name
		}
		r.known = make(map[string]struct{}, len(names))
		for _, candidate := range names {
			r.known[candidate] = struct{}{}
			if !strings.HasPrefix(candidate, "posix/") && !strings.HasPrefix(candidate, "right/") {
				r.names = append(r.names, candidate)
			}
		}
		sort.Strings(r.names)
		r.cache = make(map[string]string)
	}
	resolved := name
	if _, ok := r.known[name]; !ok {
		resolved = r.equivalent(name, location)
	}
	r.cache[name] = resolved
	return resolved
}

// equivalent returns a known zone whose offsets match location at every sample
// instant, preferring zones in the same region, or name when none matches.
func (r *zoneResolver) equivalent(name string, location *time.Location) string {
	region, _, _ := strings.Cut(name, "/")
	candidates := make([]string, 0, len(r.names))
	for _, candidate := range r.names {
		if strings.HasPrefix(candidate, region+"/") {
			candidates = append(candidates, candidate)
		}
	}
	for _, candidate := range r.names {
		if !strings.HasPrefix(candidate, region+"/") {
			candidates = append(candidates, candidate)
		}
	}
	start := time.Now().UTC().AddDate(-3, 0, 0)
	for _, candidate := range candidates {
		other, err := time.LoadLocation(candidate)
		if err != nil {
			continue
		}
		if sameOffsets(location, other, start) {
			return candidate
		}
	}
	return name
}

func sameOffsets(a, b *time.Location, start time.Time) bool {
	for at := start; at.Before(start.AddDate(4, 0, 0)); at = at.Add(7*24*time.Hour + 5*time.Hour) {
		_, offsetA := at.In(a).Zone()
		_, offsetB := at.In(b).Zone()
		if offsetA != offsetB {
			return false
		}
	}
	return true
}

// PostgresZoneName returns a name for location that the database behind pool
// accepts in AT TIME ZONE, for callers outside this package that bucket in SQL.
func PostgresZoneName(ctx context.Context, pool *pgxpool.Pool, location *time.Location) string {
	return postgresZone(ctx, pool, location)
}

// postgresLocation returns a location with the same offsets as location whose
// name PostgreSQL accepts, for code that derives the zone name from the location.
func postgresLocation(ctx context.Context, pool *pgxpool.Pool, location *time.Location) *time.Location {
	if location == nil {
		return time.UTC
	}
	name := postgresZone(ctx, pool, location)
	if name == location.String() {
		return location
	}
	if resolved, err := time.LoadLocation(name); err == nil {
		return resolved
	}
	return location
}
