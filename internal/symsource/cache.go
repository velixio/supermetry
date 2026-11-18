package symsource

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Row states of app_remote_symbols.
const (
	stateOK      = "ok"
	stateMissing = "missing"
	stateError   = "error"
)

// touchEvery bounds how often a cache hit rewrites last_used_at: the eviction
// order only needs coarse recency, and a hot module must not cost a write per
// crash.
const touchEvery = time.Hour

// row is one app_remote_symbols entry.
type row struct {
	State      string
	CacheKey   string
	FetchedAt  time.Time
	LastUsedAt time.Time
}

// cacheTable is the Postgres side of the remote symbol cache: positive entries
// point at a cache blob, negative ones (missing, error) remember a failed
// lookup so it is not repeated before its retry time.
type cacheTable struct {
	pool   *pgxpool.Pool
	source string
}

func (t cacheTable) get(ctx context.Context, debugFile, debugID string) (row, bool, error) {
	var r row
	var key *string
	err := t.pool.QueryRow(ctx, `
		select state, cache_key, fetched_at, last_used_at
		from app_remote_symbols
		where source = $1 and debug_file = $2 and debug_id = $3`,
		t.source, debugFile, debugID).Scan(&r.State, &key, &r.FetchedAt, &r.LastUsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return row{}, false, nil
	}
	if err != nil {
		return row{}, false, err
	}
	if key != nil {
		r.CacheKey = *key
	}
	return r, true, nil
}

// put records the outcome of a fetch, replacing any earlier row.
func (t cacheTable) put(ctx context.Context, debugFile, debugID, state, cacheKey string, size int64, now time.Time) error {
	var key *string
	if cacheKey != "" {
		key = &cacheKey
	}
	_, err := t.pool.Exec(ctx, `
		insert into app_remote_symbols(source, debug_file, debug_id, state, cache_key, size, fetched_at, last_used_at)
		values ($1, $2, $3, $4, $5, $6, $7, $7)
		on conflict (source, debug_file, debug_id) do update
		set state = excluded.state, cache_key = excluded.cache_key, size = excluded.size,
		    fetched_at = excluded.fetched_at, last_used_at = excluded.last_used_at`,
		t.source, debugFile, debugID, state, key, size, now)
	return err
}

// touch marks a positive entry as used now, at most once per touchEvery.
func (t cacheTable) touch(ctx context.Context, debugFile, debugID string, r row, now time.Time) error {
	if now.Sub(r.LastUsedAt) < touchEvery {
		return nil
	}
	_, err := t.pool.Exec(ctx, `
		update app_remote_symbols set last_used_at = $4
		where source = $1 and debug_file = $2 and debug_id = $3 and state = 'ok'`,
		t.source, debugFile, debugID, now)
	return err
}
