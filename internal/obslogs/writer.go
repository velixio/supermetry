package obslogs

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultMaxRecords = 100_000
	defaultMaxBytes   = 64 << 20
	defaultBatch      = 2_000
	defaultFlush      = time.Second
	shutdownTimeout   = 20 * time.Second
	retryDelay        = 500 * time.Millisecond
	partitionRefresh  = 5 * time.Second
)

// WriterConfig tunes a Writer. Zero values use the defaults.
type WriterConfig struct {
	MaxRecords int           // queue capacity in records; 100,000
	MaxBytes   int64         // queue capacity in Record.Size bytes; 64 MiB
	Batch      int           // flush size in records; 2,000
	Flush      time.Duration // flush interval; 1s
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

type queued struct {
	rec  Record
	size int
}

// Writer stores records in the logs table. Enqueue never blocks and never
// touches the database; one goroutine copies batches. The writer never creates
// partitions: a record whose day has no partition (beyond the lookahead, or
// older than retention) is dropped and counted as log_out_of_range.
type Writer struct {
	pool  *pgxpool.Pool
	cfg   WriterConfig
	stats Stats

	mu     sync.Mutex
	queue  []queued
	bytes  int64
	closed bool
	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}

	// copyRows writes rows; tests may replace it.
	copyRows func(ctx context.Context, rows []queued) error

	days        map[string]struct{} // existing partition suffixes (YYYYMMDD); flusher only
	daysChecked time.Time
}

// NewWriter builds a writer and starts its flush goroutine. Close stops it.
func NewWriter(pool *pgxpool.Pool, cfg WriterConfig, stats Stats) *Writer {
	if cfg.MaxRecords <= 0 {
		cfg.MaxRecords = defaultMaxRecords
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxBytes
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
	w := &Writer{
		pool: pool, cfg: cfg, stats: stats,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	w.copyRows = w.copy
	go w.run()
	return w
}

func (w *Writer) count(name string, n int) {
	if w.stats != nil && n > 0 {
		w.stats.Add(name, n)
	}
}

// Enqueue accepts the longest prefix of rs that fits the record and byte
// budgets and reports how many it took. The rest is counted as log_queue_full;
// callers answer 503 when accepted < len(rs). The writer keeps the records.
func (w *Writer) Enqueue(rs []Record) (accepted int) {
	if len(rs) == 0 {
		return 0
	}
	sizes := make([]int, len(rs))
	for i := range rs {
		sizes[i] = rs[i].Size()
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		w.count("log_queue_full", len(rs))
		return 0
	}
	bytes := w.bytes
	for accepted < len(rs) {
		if len(w.queue)+accepted+1 > w.cfg.MaxRecords || bytes+int64(sizes[accepted]) > w.cfg.MaxBytes {
			break
		}
		bytes += int64(sizes[accepted])
		accepted++
	}
	for i := 0; i < accepted; i++ {
		w.queue = append(w.queue, queued{rec: rs[i], size: sizes[i]})
	}
	w.bytes = bytes
	full := len(w.queue) >= w.cfg.Batch
	w.mu.Unlock()
	w.count("log_queue_full", len(rs)-accepted)
	if full {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	return accepted
}

func (w *Writer) take() []queued {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.queue
	w.queue = nil
	w.bytes = 0
	return out
}

func (w *Writer) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.cfg.Flush)
	defer ticker.Stop()
	for {
		select {
		case <-w.wake:
			w.flushAll(context.Background())
		case <-ticker.C:
			w.flushAll(context.Background())
		case <-w.stop:
			return
		}
	}
}

// Close stops accepting records, writes what is queued and stops the
// goroutine. Call it before the pool closes. It is safe to call twice.
func (w *Writer) Close() {
	w.mu.Lock()
	already := w.closed
	w.closed = true
	w.mu.Unlock()
	if already {
		<-w.done
		return
	}
	close(w.stop)
	<-w.done
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	w.flushAll(ctx)
}

func (w *Writer) flushAll(ctx context.Context) {
	batch := w.take()
	for len(batch) > 0 {
		n := min(len(batch), w.cfg.Batch)
		w.flushRetry(ctx, batch[:n])
		batch = batch[n:]
	}
}

// flushRetry writes a batch, retrying once; a second failure drops the batch
// and counts log_write_failed per record.
func (w *Writer) flushRetry(ctx context.Context, batch []queued) {
	var prepared []queued
	ready := false
	attempt := func() error {
		if !ready {
			p, err := w.prepare(ctx, batch)
			if err != nil {
				return err
			}
			prepared, ready = p, true
		}
		if len(prepared) == 0 {
			return nil
		}
		return w.copyRows(ctx, prepared)
	}
	err := attempt()
	if err != nil {
		slog.Warn("log write failed, retrying once", "records", len(batch), "error", err)
		timer := time.NewTimer(retryDelay)
		select {
		case <-timer.C:
			err = attempt()
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err != nil {
		w.count("log_write_failed", len(batch))
		slog.Error("log write failed", "records", len(batch), "error", err)
	}
}

func dayKey(t time.Time) string { return t.UTC().Format("20060102") }

func (w *Writer) refreshDays(ctx context.Context) error {
	rows, err := w.pool.Query(ctx, `
		select c.relname from pg_partition_tree(to_regclass('logs')) p
		join pg_class c on c.oid = p.relid where p.isleaf`)
	if err != nil {
		return fmt.Errorf("list log partitions: %w", err)
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

// prepare keeps the records that can be written: their day has a partition and
// their site still exists. Drops are counted once, before any retry.
func (w *Writer) prepare(ctx context.Context, batch []queued) ([]queued, error) {
	if w.days == nil {
		if err := w.refreshDays(ctx); err != nil {
			return nil, err
		}
	}
	missing := false
	for i := range batch {
		if _, ok := w.days[dayKey(batch[i].rec.TS)]; !ok {
			missing = true
			break
		}
	}
	if missing && w.cfg.Now().Sub(w.daysChecked) >= partitionRefresh {
		if err := w.refreshDays(ctx); err != nil {
			return nil, err
		}
	}
	kept := batch[:0:0]
	dropped := 0
	for i := range batch {
		if _, ok := w.days[dayKey(batch[i].rec.TS)]; ok {
			kept = append(kept, batch[i])
		} else {
			dropped++
		}
	}
	w.count("log_out_of_range", dropped)
	return w.existingSites(ctx, kept)
}

// existingSites drops records of deleted sites (the foreign key would fail the
// whole batch) so one gone site cannot block everyone else's records.
func (w *Writer) existingSites(ctx context.Context, batch []queued) ([]queued, error) {
	seen := map[int32]struct{}{}
	ids := make([]int32, 0, 4)
	for i := range batch {
		if _, ok := seen[batch[i].rec.SiteID]; !ok {
			seen[batch[i].rec.SiteID] = struct{}{}
			ids = append(ids, batch[i].rec.SiteID)
		}
	}
	if len(ids) == 0 {
		return batch, nil
	}
	rows, err := w.pool.Query(ctx, `select id from sites where id = any($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("check log sites: %w", err)
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
		if _, ok := live[batch[i].rec.SiteID]; ok {
			kept = append(kept, batch[i])
		}
	}
	return kept, nil
}

var columns = []string{
	"site_id", "ts", "rid", "observed_at", "severity", "service", "environment", "release",
	"trace_id", "span_id", "user_hash", "body", "truncated", "attributes", "source",
}

func (w *Writer) copy(ctx context.Context, batch []queued) error {
	_, err := w.pool.CopyFrom(ctx, pgx.Identifier{"logs"}, columns, pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
		return row(&batch[i].rec), nil
	}))
	if err != nil {
		w.days = nil // a partition may have been dropped by retention
	}
	return err
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func row(r *Record) []any {
	body := r.Body
	if strings.IndexByte(body, 0) >= 0 { // text columns cannot hold NUL
		body = strings.ReplaceAll(body, "\x00", "")
	}
	return []any{
		r.SiteID, r.TS, rand.Int64(), r.Observed, r.Severity, r.Service, r.Environment, r.Release,
		nullBytes(r.TraceID), nullBytes(r.SpanID), nullBytes(r.UserHash), body, r.Truncated,
		encodeAttributes(r.Attributes), r.Source,
	}
}
