package obsmetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultQueuePoints = 200_000
	defaultQueueBytes  = 32 << 20
	defaultBatch       = 5000
	defaultFlush       = time.Second
	shutdownTimeout    = 20 * time.Second
	touchEvery         = time.Hour
)

// WriterConfig tunes a Writer. Zero values use the defaults.
type WriterConfig struct {
	QueuePoints      int           // queue capacity in points; 200000
	QueueBytes       int           // queue capacity in estimated bytes; 32 MiB
	Batch            int           // flush size in points; 5000
	Flush            time.Duration // flush interval; 1s
	SeriesCap        int           // series per site; 10000
	Now              func() time.Time
	RetryDelay       time.Duration // between the two write attempts; 500ms
	PartitionRefresh time.Duration // minimum gap between partition-list reads; 5s
	// OnWritten, when set, is called with each batch after its rows are
	// stored (the rollup notes late points here). It runs on the writer
	// goroutine and must not block.
	OnWritten func(batch []Point)
}

// Writer stores points in metric_points and series in metric_series. Enqueue
// never blocks and never touches the database; one goroutine (Run) drains the
// queue, converts cumulative readings to deltas, registers new series and
// copies the rows. It never creates partitions: a point whose day has no
// partition is dropped and counted as metric_no_partition.
type Writer struct {
	pool  *pgxpool.Pool
	cfg   WriterConfig
	stats Stats
	reg   *Registry
	delta *DeltaCache

	mu          sync.Mutex
	queue       [][]Point
	queued      int
	queuedBytes int
	closed      bool
	wake        chan struct{}

	days        map[string]struct{}
	daysChecked time.Time
}

// LoadRegistry reads metric_series into a registry. Call it once at startup.
func LoadRegistry(ctx context.Context, pool *pgxpool.Pool, seriesCap int) (*Registry, error) {
	r := NewRegistry(seriesCap)
	rows, err := pool.Query(ctx, `select id, site_id, attrs_hash, last_seen from metric_series`)
	if err != nil {
		return nil, fmt.Errorf("load metric series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var site int32
		var hash []byte
		var seen time.Time
		if err := rows.Scan(&id, &site, &hash, &seen); err != nil {
			return nil, err
		}
		r.set(site, hash, id, seen)
	}
	return r, rows.Err()
}

// NewWriter builds a writer around a loaded registry.
func NewWriter(pool *pgxpool.Pool, reg *Registry, cfg WriterConfig, stats Stats) *Writer {
	if cfg.QueuePoints <= 0 {
		cfg.QueuePoints = defaultQueuePoints
	}
	if cfg.QueueBytes <= 0 {
		cfg.QueueBytes = defaultQueueBytes
	}
	if cfg.Batch <= 0 {
		cfg.Batch = defaultBatch
	}
	if cfg.Flush <= 0 {
		cfg.Flush = defaultFlush
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 500 * time.Millisecond
	}
	if cfg.PartitionRefresh <= 0 {
		cfg.PartitionRefresh = 5 * time.Second
	}
	if reg == nil {
		reg = NewRegistry(cfg.SeriesCap)
	}
	return &Writer{pool: pool, cfg: cfg, stats: stats, reg: reg, delta: NewDeltaCache(cfg.Now), wake: make(chan struct{}, 1)}
}

// Registry returns the writer's series registry.
func (w *Writer) Registry() *Registry { return w.reg }

func (w *Writer) count(name string, n int) {
	if w.stats != nil && n > 0 {
		w.stats.Add(name, n)
	}
}

// Enqueue admits the batch's series against the per-site cap (points of a
// series over the cap are dropped and counted metric_series_limit; the number
// dropped is returned) and queues the rest, all or nothing: when the queue is
// full or closed nothing is queued, metric_queue_full is counted and ok is
// false. The writer takes ownership of batch.
func (w *Writer) Enqueue(batch []Point) (dropped int, ok bool) {
	if len(batch) == 0 {
		return 0, true
	}
	w.mu.Lock()
	full := w.closed || w.queued+len(batch) > w.cfg.QueuePoints || w.queuedBytes >= w.cfg.QueueBytes
	w.mu.Unlock()
	if full {
		w.count("metric_queue_full", len(batch))
		return 0, false
	}
	kept := batch[:0]
	size := 0
	for i := range batch {
		if !w.reg.Admit(batch[i].SiteID, batch[i].Hash) {
			dropped++
			continue
		}
		size += batch[i].size()
		kept = append(kept, batch[i])
	}
	w.count("metric_series_limit", dropped)
	if len(kept) == 0 {
		return dropped, true
	}
	w.mu.Lock()
	if w.closed || w.queued+len(kept) > w.cfg.QueuePoints || w.queuedBytes+size > w.cfg.QueueBytes {
		w.mu.Unlock()
		w.count("metric_queue_full", len(kept))
		return 0, false
	}
	w.queue = append(w.queue, kept)
	w.queued += len(kept)
	w.queuedBytes += size
	wake := w.queued >= w.cfg.Batch
	w.mu.Unlock()
	if wake {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	return dropped, true
}

func (w *Writer) take() []Point {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []Point
	if len(w.queue) == 1 {
		out = w.queue[0]
	} else {
		out = make([]Point, 0, w.queued)
		for _, b := range w.queue {
			out = append(out, b...)
		}
	}
	w.queue, w.queued, w.queuedBytes = nil, 0, 0
	return out
}

// Run flushes on size and on an interval until ctx ends, then stops accepting
// points and drains what is left.
func (w *Writer) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.Flush)
	defer ticker.Stop()
	for {
		select {
		case <-w.wake:
			w.flushAll(ctx)
		case <-ticker.C:
			w.flushAll(ctx)
		case <-ctx.Done():
			w.Close()
			final, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			w.flushAll(final)
			return nil
		}
	}
}

// Close stops accepting points; Run's shutdown drains the queue.
func (w *Writer) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

// Flush writes everything queued now (tests and shutdown paths).
func (w *Writer) Flush(ctx context.Context) { w.flushAll(ctx) }

func (w *Writer) flushAll(ctx context.Context) {
	pts := w.take()
	for len(pts) > 0 {
		n := min(len(pts), w.cfg.Batch)
		w.flushRetry(ctx, pts[:n])
		pts = pts[n:]
	}
}

// convert applies cumulative-to-delta conversion in queue order. Baseline
// readings produce no row.
func (w *Writer) convert(batch []Point) []Point {
	out := batch[:0:0]
	for i := range batch {
		p := batch[i]
		if p.Cumulative {
			key := append([]byte{byte(p.SiteID >> 24), byte(p.SiteID >> 16), byte(p.SiteID >> 8), byte(p.SiteID)}, p.Hash...)
			switch {
			case p.Count != nil:
				var sum float64
				if p.Sum != nil {
					sum = *p.Sum
				}
				dc, ds, db, ok := w.delta.Histogram(key, p.Start, *p.Count, sum, p.Buckets)
				if !ok {
					continue
				}
				p.Count, p.Buckets = &dc, db
				if p.Sum != nil {
					p.Sum = &ds
				}
				// Min and max describe the cumulative window, not the interval.
				p.Min, p.Max = nil, nil
			case p.Value != nil:
				d, ok := w.delta.Counter(key, p.Start, *p.Value)
				if !ok {
					continue
				}
				p.Value = &d
			}
		}
		out = append(out, p)
	}
	return out
}

// flushRetry converts once, then writes with one retry; a second failure drops
// the batch and counts metric_write_failed per point.
func (w *Writer) flushRetry(ctx context.Context, batch []Point) {
	pts := w.convert(batch)
	if len(pts) == 0 {
		return
	}
	var prepared []Point
	ready := false
	attempt := func() error {
		if !ready {
			p, err := w.prepare(ctx, pts)
			if err != nil {
				return err
			}
			prepared, ready = p, true
		}
		if len(prepared) == 0 {
			return nil
		}
		return w.write(ctx, prepared)
	}
	err := attempt()
	if err != nil {
		slog.Warn("metric write failed, retrying once", "points", len(pts), "error", err)
		timer := time.NewTimer(w.cfg.RetryDelay)
		select {
		case <-timer.C:
			err = attempt()
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err != nil {
		w.count("metric_write_failed", len(pts))
		slog.Error("metric write failed", "points", len(pts), "error", err)
	}
}

func dayKey(t time.Time) string { return t.UTC().Format("20060102") }

func (w *Writer) refreshDays(ctx context.Context) error {
	rows, err := w.pool.Query(ctx, `
		select c.relname from pg_partition_tree(to_regclass('metric_points')) p
		join pg_class c on c.oid = p.relid where p.isleaf`)
	if err != nil {
		return fmt.Errorf("list metric partitions: %w", err)
	}
	defer rows.Close()
	days := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if len(name) > 8 {
			days[name[len(name)-8:]] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	w.days = days
	w.daysChecked = w.cfg.Now()
	return nil
}

// prepare keeps the points whose day has a partition and whose site still
// exists; drops are counted once, before any retry.
func (w *Writer) prepare(ctx context.Context, batch []Point) ([]Point, error) {
	if w.days == nil {
		if err := w.refreshDays(ctx); err != nil {
			return nil, err
		}
	}
	missing := false
	for i := range batch {
		if _, ok := w.days[dayKey(batch[i].TS)]; !ok {
			missing = true
			break
		}
	}
	if missing && w.cfg.Now().Sub(w.daysChecked) >= w.cfg.PartitionRefresh {
		if err := w.refreshDays(ctx); err != nil {
			return nil, err
		}
	}
	kept := batch[:0:0]
	dropped := 0
	for i := range batch {
		if _, ok := w.days[dayKey(batch[i].TS)]; ok {
			kept = append(kept, batch[i])
		} else {
			dropped++
		}
	}
	w.count("metric_no_partition", dropped)
	return w.existingSites(ctx, kept)
}

func (w *Writer) existingSites(ctx context.Context, batch []Point) ([]Point, error) {
	seen := map[int32]struct{}{}
	ids := make([]int32, 0, 4)
	for i := range batch {
		if _, ok := seen[batch[i].SiteID]; !ok {
			seen[batch[i].SiteID] = struct{}{}
			ids = append(ids, batch[i].SiteID)
		}
	}
	if len(ids) == 0 {
		return batch, nil
	}
	rows, err := w.pool.Query(ctx, `select id from sites where id = any($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("check metric sites: %w", err)
	}
	live := map[int32]struct{}{}
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		live[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(live) == len(ids) {
		return batch, nil
	}
	kept := batch[:0:0]
	for i := range batch {
		if _, ok := live[batch[i].SiteID]; ok {
			kept = append(kept, batch[i])
		}
	}
	return kept, nil
}

const upsertSeries = `
insert into metric_series (site_id, name, kind, unit, bounds, attrs, attrs_hash, first_seen, last_seen)
values ($1, $2, $3, $4, $5, $6, $7, $8, $8)
on conflict (site_id, attrs_hash) do update set last_seen = excluded.last_seen
returning id`

// resolve registers unknown series and refreshes last_seen of known ones at
// most once an hour each. It runs before the points are copied.
func (w *Writer) resolve(ctx context.Context, batch []Point) error {
	now := w.cfg.Now()
	var unknown []*Point
	seen := map[regKey]struct{}{}
	var touch []int64
	for i := range batch {
		p := &batch[i]
		k, ok := makeKey(p.SiteID, p.Hash)
		if !ok {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if _, known := w.reg.ID(p.SiteID, p.Hash); known {
			if id := w.reg.dueTouch(p.SiteID, p.Hash, now.Add(-touchEvery), now); id != 0 {
				touch = append(touch, id)
			}
		} else {
			unknown = append(unknown, p)
		}
	}
	if len(unknown) > 0 {
		b := &pgx.Batch{}
		for _, p := range unknown {
			attrs, err := json.Marshal(p.SeriesKey.Attrs)
			if err != nil {
				return err
			}
			var bounds any
			if p.SeriesKey.Bounds != nil {
				bounds = p.SeriesKey.Bounds
			}
			b.Queue(upsertSeries, p.SiteID, p.SeriesKey.Name, p.SeriesKey.Kind, p.SeriesKey.Unit, bounds, attrs, p.Hash, now)
		}
		res := w.pool.SendBatch(ctx, b)
		ids := make([]int64, len(unknown))
		for i := range unknown {
			if err := res.QueryRow().Scan(&ids[i]); err != nil {
				_ = res.Close()
				return fmt.Errorf("register metric series: %w", err)
			}
		}
		if err := res.Close(); err != nil {
			return err
		}
		for i, p := range unknown {
			w.reg.set(p.SiteID, p.Hash, ids[i], now)
		}
	}
	if len(touch) > 0 {
		if _, err := w.pool.Exec(ctx, `update metric_series set last_seen = $1 where id = any($2)`, now, touch); err != nil {
			return fmt.Errorf("touch metric series: %w", err)
		}
	}
	return nil
}

var columns = []string{"site_id", "series_id", "ts", "value", "count", "sum", "min", "max", "bucket_counts"}

func (w *Writer) write(ctx context.Context, batch []Point) error {
	if err := w.resolve(ctx, batch); err != nil {
		return err
	}
	ids := make([]int64, len(batch))
	for i := range batch {
		id, ok := w.reg.ID(batch[i].SiteID, batch[i].Hash)
		if !ok {
			return fmt.Errorf("metric series %x has no id", batch[i].Hash[:4])
		}
		ids[i] = id
	}
	_, err := w.pool.CopyFrom(ctx, pgx.Identifier{"metric_points"}, columns, pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
		p := &batch[i]
		var buckets any
		if p.Buckets != nil {
			buckets = p.Buckets
		}
		return []any{p.SiteID, ids[i], p.TS, p.Value, p.Count, p.Sum, p.Min, p.Max, buckets}, nil
	}))
	if err != nil {
		w.days = nil // a partition may have been dropped by retention
		return err
	}
	if w.cfg.OnWritten != nil {
		w.cfg.OnWritten(batch)
	}
	return nil
}
