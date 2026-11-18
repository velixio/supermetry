package spans

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultQueueSpans = 65536
	defaultBatch      = 5000
	defaultFlush      = time.Second
	shutdownTimeout   = 20 * time.Second
)

// WriterConfig tunes a Writer. Zero values use the defaults.
type WriterConfig struct {
	QueueSpans int           // queue capacity in spans; 65536
	Batch      int           // flush size in spans; 5000
	Flush      time.Duration // flush interval; 1s
	// Location returns a site's time zone for late-hour reporting; nil or a
	// nil result means UTC.
	Location func(siteID int32) *time.Location
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// RetryDelay separates the two write attempts; 500ms.
	RetryDelay time.Duration
	// PartitionRefresh is the minimum gap between partition-list reads; 5s.
	PartitionRefresh time.Duration
}

// Writer stores spans in the spans table. Enqueue never blocks and never
// touches the database. One goroutine (Run) copies batches. The writer never
// creates partitions: a span whose day has no partition is dropped and counted
// as span_no_partition; the partitions job creates days ahead of time.
type Writer struct {
	pool   *pgxpool.Pool
	cfg    WriterConfig
	stats  Stats
	onLate func(siteID int32, localHours []time.Time)

	mu     sync.Mutex
	queue  [][]Span
	queued int
	closed bool
	wake   chan struct{}

	// copyRows writes rows; tests replace it.
	copyRows func(ctx context.Context, rows []Span) error

	seen        *seenGroups         // span_groups already written
	days        map[string]struct{} // existing partition suffixes (YYYYMMDD)
	daysChecked time.Time
}

// NewWriter builds a writer with its own bounded queue. onLate (may be nil)
// receives, after a successful flush, the site-local wall-clock hours older
// than the previous local hour that the flush wrote spans into.
func NewWriter(pool *pgxpool.Pool, cfg WriterConfig, stats Stats, onLate func(siteID int32, localHours []time.Time)) *Writer {
	if cfg.QueueSpans <= 0 {
		cfg.QueueSpans = defaultQueueSpans
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
	w := &Writer{pool: pool, cfg: cfg, stats: stats, onLate: onLate, wake: make(chan struct{}, 1)}
	w.copyRows = w.copy
	return w
}

func (w *Writer) count(name string, n int) {
	if w.stats != nil && n > 0 {
		w.stats.Add(name, n)
	}
}

// Enqueue queues a whole batch or nothing: when it does not fit (or the writer
// is closed) it reports false and counts span_queue_full per span. The writer
// takes ownership of batch.
func (w *Writer) Enqueue(batch []Span) bool {
	if len(batch) == 0 {
		return true
	}
	w.mu.Lock()
	if w.closed || w.queued+len(batch) > w.cfg.QueueSpans {
		w.mu.Unlock()
		w.count("span_queue_full", len(batch))
		return false
	}
	w.queue = append(w.queue, batch)
	w.queued += len(batch)
	full := w.queued >= w.cfg.Batch
	w.mu.Unlock()
	if full {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	return true
}

// take removes and returns every queued span.
func (w *Writer) take() []Span {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []Span
	if len(w.queue) == 1 {
		out = w.queue[0]
	} else {
		out = make([]Span, 0, w.queued)
		for _, b := range w.queue {
			out = append(out, b...)
		}
	}
	w.queue = nil
	w.queued = 0
	return out
}

// Run flushes on size and on an interval until ctx ends, then closes the queue
// to new spans and drains what is left.
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

// Close stops accepting spans; Run's shutdown drains the queue.
func (w *Writer) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

func (w *Writer) flushAll(ctx context.Context) {
	spans := w.take()
	for len(spans) > 0 {
		n := min(len(spans), w.cfg.Batch)
		w.flushRetry(ctx, spans[:n])
		spans = spans[n:]
	}
}

// flushRetry writes a batch, retrying once; a second failure drops the batch
// and counts span_write_failed per span.
func (w *Writer) flushRetry(ctx context.Context, batch []Span) {
	var prepared []Span
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
		err := w.copyRows(ctx, prepared)
		if err == nil {
			w.upsertGroups(ctx, prepared)
			w.reportLate(prepared)
		}
		return err
	}
	err := attempt()
	if err != nil {
		slog.Warn("span write failed, retrying once", "spans", len(batch), "error", err)
		timer := time.NewTimer(w.cfg.RetryDelay)
		select {
		case <-timer.C:
			err = attempt()
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err != nil {
		w.count("span_write_failed", len(batch))
		slog.Error("span write failed", "spans", len(batch), "error", err)
	}
}

func dayKey(t time.Time) string { return t.UTC().Format("20060102") }

// refreshDays reads the existing daily partitions.
func (w *Writer) refreshDays(ctx context.Context) error {
	rows, err := w.pool.Query(ctx, `
		select c.relname from pg_partition_tree(to_regclass('spans')) p
		join pg_class c on c.oid = p.relid where p.isleaf`)
	if err != nil {
		return fmt.Errorf("list span partitions: %w", err)
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

// withPartition keeps spans whose day has a partition and counts the rest as
// span_no_partition. The partition list is re-read on a miss, at most once per
// PartitionRefresh.
func (w *Writer) withPartition(ctx context.Context, batch []Span) ([]Span, error) {
	if w.days == nil {
		if err := w.refreshDays(ctx); err != nil {
			return nil, err
		}
	}
	missing := false
	for i := range batch {
		if _, ok := w.days[dayKey(batch[i].Start)]; !ok {
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
		if _, ok := w.days[dayKey(batch[i].Start)]; ok {
			kept = append(kept, batch[i])
		} else {
			dropped++
		}
	}
	if dropped > 0 {
		w.count("span_no_partition", dropped)
	}
	return kept, nil
}

// prepare keeps the spans that can be written: their day has a partition and
// their site still exists. Drops are counted once, before any retry.
func (w *Writer) prepare(ctx context.Context, batch []Span) ([]Span, error) {
	batch, err := w.withPartition(ctx, batch)
	if err != nil {
		return nil, err
	}
	return w.existingSites(ctx, batch)
}

// copy writes prepared spans.
func (w *Writer) copy(ctx context.Context, batch []Span) error {
	_, err := w.pool.CopyFrom(ctx, pgx.Identifier{"spans"}, columns, pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
		return row(&batch[i])
	}))
	if err != nil {
		w.days = nil // a partition may have been dropped by retention
		return err
	}
	return nil
}

// existingSites drops spans of deleted sites so one gone site cannot fail
// everyone else's batch.
func (w *Writer) existingSites(ctx context.Context, batch []Span) ([]Span, error) {
	seen := map[int32]struct{}{}
	ids := make([]int32, 0, 4)
	for i := range batch {
		if _, ok := seen[batch[i].SiteID]; !ok {
			seen[batch[i].SiteID] = struct{}{}
			ids = append(ids, batch[i].SiteID)
		}
	}
	rows, err := w.pool.Query(ctx, `select id from sites where id = any($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("check span sites: %w", err)
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

var columns = []string{
	"site_id", "start_ts", "duration_us", "bucket", "trace_id", "span_id", "parent_span_id", "segment_id",
	"is_segment", "name", "op", "description", "group_hash", "status", "http_status", "release",
	"environment", "platform", "app_start_type", "app_start_ms", "app_start_bucket", "attrs",
}

func row(s *Span) ([]any, error) {
	var parent any
	if s.ParentID != nil {
		parent = s.ParentID[:]
	}
	var httpStatus, startBucket, startMS any
	if s.HTTPStatus != nil {
		httpStatus = *s.HTTPStatus
	}
	if s.AppStartBucket != nil {
		startBucket = *s.AppStartBucket
	}
	if s.AppStartMS != nil {
		startMS = *s.AppStartMS
	}
	var startType, attrs any
	if s.AppStartType != "" {
		startType = s.AppStartType
	}
	if len(s.Attrs) > 0 {
		raw, err := json.Marshal(s.Attrs)
		if err != nil {
			return nil, err
		}
		attrs = raw
	}
	return []any{
		s.SiteID, s.Start, s.DurationUS, Bucket(s.DurationUS), s.TraceID[:], s.SpanID[:], parent, s.SegmentID[:],
		s.IsSegment, s.Name, s.Op, s.Description, GroupHash(s.Op, s.Description), s.Status, httpStatus, s.Release,
		s.Environment, s.Platform, startType, startMS, startBucket, attrs,
	}, nil
}

// reportLate tells onLate which site-local hours, older than the previous
// local hour, received spans. Hours are wall-clock hours in the site's zone,
// so the repeated hour of a DST fall-back is one entry.
func (w *Writer) reportLate(batch []Span) {
	if w.onLate == nil {
		return
	}
	now := w.cfg.Now()
	type key struct {
		site int32
		hour [4]int
	}
	late := map[key]time.Time{}
	for i := range batch {
		loc := time.UTC
		if w.cfg.Location != nil {
			if l := w.cfg.Location(batch[i].SiteID); l != nil {
				loc = l
			}
		}
		prev := localHour(now.In(loc)).Add(-time.Hour)
		h := localHour(batch[i].Start.In(loc))
		// Compare wall clocks, not instants, so a fall-back hour is stable.
		if wallBefore(h, prev) {
			late[key{batch[i].SiteID, [4]int{h.Year(), int(h.Month()), h.Day(), h.Hour()}}] = h
		}
	}
	bySite := map[int32][]time.Time{}
	for k, h := range late {
		bySite[k.site] = append(bySite[k.site], h)
	}
	for site, hours := range bySite {
		sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })
		w.onLate(site, hours)
	}
}

func localHour(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

func wallBefore(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	if ay != by {
		return ay < by
	}
	if am != bm {
		return am < bm
	}
	if ad != bd {
		return ad < bd
	}
	return a.Hour() < b.Hour()
}
