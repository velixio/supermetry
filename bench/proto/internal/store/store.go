// Package store is the Postgres side of the prototype: schema, partition
// management, COPY of events, batch upsert of sessions, salts and sites.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/bench/proto/internal/core"
)

// DefaultDSN is the local benchmark database.
const DefaultDSN = "postgres://supermetry:supermetry@127.0.0.1:5432/supermetry?sslmode=disable"

// DSN returns $SUPERMETRY_DSN or DefaultDSN.
func DSN() string {
	if v := os.Getenv("SUPERMETRY_DSN"); v != "" {
		return v
	}
	return DefaultDSN
}

// Options tune the pool.
type Options struct {
	MaxConns     int32
	SearchPath   string // tests use a throwaway schema
	JIT          *bool  // nil = server default
	RawPartition string // monthly (default) or daily; daily demonstrates O5 plateau
}

const (
	RawPartitionMonthly = "monthly"
	RawPartitionDaily   = "daily"
)

// Store wraps the pool and partition bookkeeping.
type Store struct {
	Pool         *pgxpool.Pool
	mu           sync.Mutex
	parts        map[string]bool // partitions known to exist, by name; false = covered by an older partition
	rawPartition string
}

// Open connects to PG.
func Open(ctx context.Context, dsn string, o Options) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if o.MaxConns > 0 {
		cfg.MaxConns = o.MaxConns
	} else {
		cfg.MaxConns = 16
	}
	rp := cfg.ConnConfig.RuntimeParams
	rp["application_name"] = "supermetry-proto"
	// Analytics queries: always plan with the actual range/filter values.
	rp["plan_cache_mode"] = "force_custom_plan"
	if o.SearchPath != "" {
		rp["search_path"] = o.SearchPath
	}
	if o.JIT != nil {
		rp["jit"] = map[bool]string{true: "on", false: "off"}[*o.JIT]
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if o.RawPartition == "" {
		o.RawPartition = RawPartitionMonthly
	}
	if o.RawPartition != RawPartitionMonthly && o.RawPartition != RawPartitionDaily {
		pool.Close()
		return nil, fmt.Errorf("raw partition must be %q or %q, got %q", RawPartitionMonthly, RawPartitionDaily, o.RawPartition)
	}
	return &Store{Pool: pool, parts: map[string]bool{}, rawPartition: o.RawPartition}, nil
}

// Close closes the pool.
func (s *Store) Close() { s.Pool.Close() }

// ensurePartition creates parent's partition name for [from, to) unless it
// is known to exist. A range already covered by an older partition (for
// example a monthly sessions partition from before daily ones) counts as
// existing.
func (s *Store) ensurePartition(ctx context.Context, parent, name, from, to, with string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.parts[name]; ok {
		return nil
	}
	sql := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s') %s`,
		name, parent, from, to, with)
	if _, err := s.Pool.Exec(ctx, sql); err != nil {
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42P17" { // 42P17: "would overlap partition"
			return fmt.Errorf("create partition %s: %w", name, err)
		}
		s.parts[name] = false
		return nil
	}
	s.parts[name] = true
	return nil
}

// EnsureEventPartitions creates monthly (the production default) or daily
// UTC event partitions. Daily partitions are used by the O5 retention rig so
// a seven-day size plateau is visible without simulating several months.
func (s *Store) EnsureEventPartitions(ctx context.Context, ts ...time.Time) error {
	for _, t := range ts {
		t = t.UTC()
		from := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		name := "events_p" + from.Format("2006_01")
		to := from.AddDate(0, 1, 0)
		if s.rawPartition == RawPartitionDaily {
			from = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			name = "events_d" + from.Format("20060102")
			to = from.AddDate(0, 0, 1)
		}
		if err := s.ensurePartition(ctx, "events", name,
			from.Format(time.RFC3339), to.Format(time.RFC3339), ""); err != nil {
			return err
		}
	}
	return nil
}

// EnsureMonths is retained for callers that explicitly want the production
// default partition shape.
func (s *Store) EnsureMonths(ctx context.Context, ts ...time.Time) error {
	return s.EnsureEventPartitions(ctx, ts...)
}

// EnsureSessionDays creates the daily (UTC) sessions partitions for the given
// instants. Hot session rows are only needed until their local days are
// closed and compacted into sessions_closed; daily partitions let
// DropCompactedSessions retire them with DROP TABLE.
func (s *Store) EnsureSessionDays(ctx context.Context, ts ...time.Time) error {
	for _, t := range ts {
		t = t.UTC()
		from := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		if err := s.ensurePartition(ctx, "sessions", "sessions_d"+from.Format("20060102"),
			from.Format(time.RFC3339), from.AddDate(0, 0, 1).Format(time.RFC3339), "WITH (fillfactor = 90)"); err != nil {
			return err
		}
	}
	return nil
}

// AnalyzePartitions refreshes planner statistics and BRIN summaries of every
// raw partition this store created or wrote to (after a bulk load; autovacuum
// would get there later, and until BRIN ranges are summarized every range
// scan reads the whole partition).
func (s *Store) AnalyzePartitions(ctx context.Context) error {
	s.mu.Lock()
	names := make([]string, 0, len(s.parts))
	for k, exists := range s.parts {
		if exists {
			names = append(names, k)
		}
	}
	s.mu.Unlock()
	for _, n := range names {
		if _, err := s.Pool.Exec(ctx, "ANALYZE "+n); err != nil {
			return err
		}
		if _, err := s.Pool.Exec(ctx, `SELECT brin_summarize_new_values(i.indexrelid) FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_am a ON a.oid = c.relam
			WHERE i.indrelid = $1::regclass AND a.amname = 'brin'`, n); err != nil {
			return err
		}
	}
	return nil
}

// UpsertSites inserts or updates sites.
func (s *Store) UpsertSites(ctx context.Context, sites []core.Site) error {
	b := &pgx.Batch{}
	for _, st := range sites {
		b.Queue(`INSERT INTO sites (id, host, tz) VALUES ($1, $2, $3)
		         ON CONFLICT (id) DO UPDATE SET host = EXCLUDED.host, tz = EXCLUDED.tz`, st.ID, st.Host, st.TZ)
	}
	return s.Pool.SendBatch(ctx, b).Close()
}

// LoadSites returns all sites with time zones resolved.
func (s *Store) LoadSites(ctx context.Context) ([]core.Site, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, host, tz FROM sites ORDER BY id`)
	if err != nil {
		return nil, err
	}
	sites, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (core.Site, error) {
		var st core.Site
		err := r.Scan(&st.ID, &st.Host, &st.TZ)
		return st, err
	})
	if err != nil {
		return nil, err
	}
	for i := range sites {
		if sites[i].Loc, err = time.LoadLocation(sites[i].TZ); err != nil {
			return nil, err
		}
	}
	return sites, nil
}

var eventCols = []string{"site_id", "local_date", "ts", "visitor_id", "session_id", "name", "path",
	"source", "country", "browser", "os", "device", "utm_source", "utm_medium", "utm_campaign", "props"}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type eventSource struct {
	evs []*core.Event
	i   int
	row []any
}

func (e *eventSource) Next() bool { e.i++; return e.i <= len(e.evs) }
func (e *eventSource) Err() error { return nil }
func (e *eventSource) Values() ([]any, error) {
	v := e.evs[e.i-1]
	r := e.row[:0]
	var props any
	if v.Props != nil {
		props = v.Props
	}
	r = append(r, v.SiteID, v.Day.Time(), time.UnixMicro(v.TS), v.VisitorID, v.SessionID, v.Name, v.Path,
		v.Source, v.Country, v.Browser, v.OS, v.Device,
		nullStr(v.UTMSource), nullStr(v.UTMMedium), nullStr(v.UTMCampaign), props)
	e.row = r
	return r, nil
}

// CopyEvents writes a batch of events with COPY.
func (s *Store) CopyEvents(ctx context.Context, evs []*core.Event) error {
	if len(evs) == 0 {
		return nil
	}
	if err := s.EnsureEventPartitions(ctx, distinctDays(len(evs), func(i int) int64 { return evs[i].TS })...); err != nil {
		return err
	}
	_, err := s.Pool.CopyFrom(ctx, pgx.Identifier{"events"}, eventCols, &eventSource{evs: evs, row: make([]any, 0, len(eventCols))})
	return err
}

// distinctDays returns one instant per UTC day present in a batch.
func distinctDays(n int, ts func(int) int64) []time.Time {
	var out []time.Time
	seen := map[int64]bool{}
	for i := 0; i < n; i++ {
		t := ts(i)
		if d := t / 86_400_000_000; !seen[d] {
			seen[d] = true
			out = append(out, time.UnixMicro(t).UTC())
		}
	}
	return out
}

var sessionCols = []string{"site_id", "local_date", "start_ts", "session_id", "visitor_id", "duration_ms",
	"pageviews", "custom_events", "entry_path", "exit_path", "source", "country", "browser", "os", "device",
	"utm_source", "utm_medium", "utm_campaign"}

// UpsertSessions batch-upserts session snapshots (COPY into a temp stage,
// then INSERT ... ON CONFLICT DO UPDATE).
func (s *Store) UpsertSessions(ctx context.Context, ss []core.Session) error {
	if len(ss) == 0 {
		return nil
	}
	if err := s.EnsureSessionDays(ctx, distinctDays(len(ss), func(i int) int64 { return ss[i].Start })...); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE IF NOT EXISTS sessions_stage (LIKE sessions) ON COMMIT DELETE ROWS`); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"sessions_stage"}, sessionCols, &sessionSource{ss: ss, row: make([]any, 0, len(sessionCols))}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
INSERT INTO sessions SELECT * FROM sessions_stage
ON CONFLICT (site_id, start_ts, session_id) DO UPDATE SET
  duration_ms = EXCLUDED.duration_ms, pageviews = EXCLUDED.pageviews,
  custom_events = EXCLUDED.custom_events, entry_path = EXCLUDED.entry_path, exit_path = EXCLUDED.exit_path`)
		return err
	})
}

type sessionSource struct {
	ss  []core.Session
	i   int
	row []any
}

func (s *sessionSource) Next() bool { s.i++; return s.i <= len(s.ss) }
func (s *sessionSource) Err() error { return nil }
func (s *sessionSource) Values() ([]any, error) {
	v := &s.ss[s.i-1]
	s.row = append(s.row[:0], v.SiteID, v.Day().Time(), time.UnixMicro(v.Start), v.ID(), v.VisitorID, int32(v.DurationMs()),
		v.Pageviews, v.Custom, v.Entry(), v.Exit(), v.Source(), v.Country(), v.Browser(), v.OS(), v.Device(),
		nullStr(v.UTMSource()), nullStr(v.UTMMedium()), nullStr(v.UTMCampaign()))
	return s.row, nil
}

// SaveSalt persists a salt (first writer wins).
func (s *Store) SaveSalt(ctx context.Context, k core.SaltKey, salt []byte) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO salts (site_id, day, salt) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		k.Site, k.Day.Time(), salt)
	return err
}

// LoadSalts returns salts for days >= from.
func (s *Store) LoadSalts(ctx context.Context, from core.Day) (map[core.SaltKey][]byte, error) {
	rows, err := s.Pool.Query(ctx, `SELECT site_id, day, salt FROM salts WHERE day >= $1`, from.Time())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[core.SaltKey][]byte{}
	for rows.Next() {
		var site int32
		var day time.Time
		var salt []byte
		if err := rows.Scan(&site, &day, &salt); err != nil {
			return nil, err
		}
		out[core.SaltKey{Site: site, Day: core.Day(day.Unix() / 86400)}] = salt
	}
	return out, rows.Err()
}

// PurgeSalts deletes salts created more than maxAge ago (P2: within 48 h).
func (s *Store) PurgeSalts(ctx context.Context, maxAge time.Duration) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM salts WHERE created_at < now() - $1::interval`, fmt.Sprintf("%d seconds", int(maxAge.Seconds())))
	return tag.RowsAffected(), err
}
