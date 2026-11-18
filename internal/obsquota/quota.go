// Package obsquota counts what each app site sends per UTC day (log records
// and metric points), enforces the daily quota from memory and flushes the
// totals to obs_ingest_daily. The request path never reads the database: the
// day's totals are primed once at start.
package obsquota

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Classes of intake.
const (
	ClassLogs    = "logs"
	ClassMetrics = "metrics"
)

// FlushInterval is how often the deltas go to the table.
const FlushInterval = 60 * time.Second

// historyDays is how long obs_ingest_daily rows are kept.
const historyDays = 90

type key struct {
	site  int32
	day   string // 2006-01-02, UTC
	class string
}

type delta struct{ accepted, rejected, bytes int64 }

type counts struct {
	accepted int64 // total for the day, including the primed value
	pending  delta // not flushed yet
}

// Tracker is safe for concurrent use.
type Tracker struct {
	pool *pgxpool.Pool
	now  func() time.Time

	mu sync.Mutex
	m  map[key]*counts

	stop chan struct{}
	done chan struct{}
}

// New builds a tracker. pool may be nil (memory only); now may be nil.
func New(pool *pgxpool.Pool, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{pool: pool, now: now, m: map[key]*counts{}}
}

func (t *Tracker) day() string { return t.now().UTC().Format(time.DateOnly) }

// entry must be called with t.mu held.
func (t *Tracker) entry(site int32, class string) *counts {
	k := key{site, t.day(), class}
	c := t.m[k]
	if c == nil {
		c = &counts{}
		t.m[k] = c
	}
	return c
}

// Prime loads today's accepted totals so a restart does not hand out a fresh
// quota. It reads the table once; the request path never does.
func (t *Tracker) Prime(ctx context.Context) error {
	if t.pool == nil {
		return nil
	}
	day := t.day()
	rows, err := t.pool.Query(ctx, `SELECT site_id, class, accepted FROM obs_ingest_daily WHERE day = $1::date`, day)
	if err != nil {
		return fmt.Errorf("prime obs quota: %w", err)
	}
	defer rows.Close()
	type primed struct {
		k        key
		accepted int64
	}
	var got []primed
	for rows.Next() {
		p := primed{k: key{day: day}}
		if err := rows.Scan(&p.k.site, &p.k.class, &p.accepted); err != nil {
			return fmt.Errorf("prime obs quota: %w", err)
		}
		got = append(got, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("prime obs quota: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range got {
		c := t.m[p.k]
		if c == nil {
			c = &counts{}
			t.m[p.k] = c
		}
		// Whatever was counted since start is already in c.accepted and in
		// pending; the stored row does not include it.
		c.accepted += p.accepted
	}
	return nil
}

// Take reserves up to n units for the site and returns how many fit under
// limit; limit 0 means unlimited. Units that did not fit count as rejected.
func (t *Tracker) Take(siteID int32, class string, n int, limit int64) (granted int) {
	if n <= 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.entry(siteID, class)
	granted = n
	if limit > 0 {
		granted = int(min(int64(n), max(limit-c.accepted, 0)))
	}
	c.accepted += int64(granted)
	c.pending.accepted += int64(granted)
	c.pending.rejected += int64(n - granted)
	return granted
}

// Release gives back n granted units that were never stored (the queue was
// full and the sender will retry).
func (t *Tracker) Release(siteID int32, class string, n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.entry(siteID, class)
	c.accepted -= int64(n)
	c.pending.accepted -= int64(n)
}

// Reject counts n units refused for a reason other than the quota (invalid,
// too old, dropped by a full queue).
func (t *Tracker) Reject(siteID int32, class string, n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entry(siteID, class).pending.rejected += int64(n)
}

// AddBytes counts received payload bytes (decompressed) for the site.
func (t *Tracker) AddBytes(siteID int32, class string, n int) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entry(siteID, class).pending.bytes += int64(n)
}

// Used reports the accepted units counted today.
func (t *Tracker) Used(siteID int32, class string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.m[key{siteID, t.day(), class}]; c != nil {
		return c.accepted
	}
	return 0
}

// Flush writes the unflushed deltas. A failed write keeps them for the next
// run. Days other than today leave memory once flushed.
func (t *Tracker) Flush(ctx context.Context) error {
	if t.pool == nil {
		return nil
	}
	type item struct {
		k key
		d delta
	}
	var batch []item
	today := t.day()
	t.mu.Lock()
	for k, c := range t.m {
		if c.pending != (delta{}) {
			batch = append(batch, item{k, c.pending})
			c.pending = delta{}
		}
		if k.day != today {
			delete(t.m, k)
		}
	}
	t.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, it := range batch {
		b.Queue(`INSERT INTO obs_ingest_daily (site_id, day, class, accepted, rejected, bytes)
			SELECT $1, $2::date, $3, GREATEST($4::bigint, 0), $5::bigint, $6::bigint
			WHERE EXISTS (SELECT 1 FROM sites WHERE id = $1)
			ON CONFLICT (site_id, day, class) DO UPDATE SET
				accepted = GREATEST(obs_ingest_daily.accepted + $4, 0),
				rejected = obs_ingest_daily.rejected + $5,
				bytes = obs_ingest_daily.bytes + $6`,
			it.k.site, it.k.day, it.k.class, it.d.accepted, it.d.rejected, it.d.bytes)
	}
	res := t.pool.SendBatch(ctx, b)
	var failed error
	for range batch {
		if _, err := res.Exec(); err != nil && failed == nil {
			failed = err
		}
	}
	if err := res.Close(); err != nil && failed == nil {
		failed = err
	}
	if failed == nil {
		return nil
	}
	// Put every delta back; a statement that did succeed is then counted
	// twice, which only makes the totals slightly high.
	t.mu.Lock()
	for _, it := range batch {
		c := t.m[it.k]
		if c == nil {
			c = &counts{}
			t.m[it.k] = c
		}
		c.pending.accepted += it.d.accepted
		c.pending.rejected += it.d.rejected
		c.pending.bytes += it.d.bytes
	}
	t.mu.Unlock()
	return fmt.Errorf("flush obs quota: %w", failed)
}

func (t *Tracker) prune(ctx context.Context) {
	cutoff := t.now().UTC().AddDate(0, 0, -historyDays).Format(time.DateOnly)
	if _, err := t.pool.Exec(ctx, `DELETE FROM obs_ingest_daily WHERE day < $1::date`, cutoff); err != nil {
		slog.Warn("obsquota: prune", "err", err)
	}
}

// Start flushes every FlushInterval until Close. Call it once.
func (t *Tracker) Start() {
	t.stop = make(chan struct{})
	t.done = make(chan struct{})
	go func() {
		defer close(t.done)
		tick := time.NewTicker(FlushInterval)
		defer tick.Stop()
		var lastPrune time.Time
		for {
			select {
			case <-t.stop:
				return
			case <-tick.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := t.Flush(ctx); err != nil {
					slog.Warn("obsquota: flush", "err", err)
				}
				if t.pool != nil && t.now().Sub(lastPrune) > 24*time.Hour {
					lastPrune = t.now()
					t.prune(ctx)
				}
				cancel()
			}
		}
	}()
}

// Close stops the flush goroutine and writes what is left. Call it before the
// pool closes.
func (t *Tracker) Close() {
	if t.stop != nil {
		close(t.stop)
		<-t.done
		t.stop = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := t.Flush(ctx); err != nil {
		slog.Warn("obsquota: final flush", "err", err)
	}
}
