package appingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appattach"
	"supermetry/internal/db"
	"supermetry/internal/health"
)

const (
	defaultBodyQueue       = 4096
	defaultAttachmentBytes = 64 << 20
)

// BodyWriterConfig tunes a BodyWriter. Zero values use the defaults.
type BodyWriterConfig struct {
	QueueSize  int
	BatchSize  int
	FlushEvery time.Duration
	// AttachmentBytes caps the attachment content held in memory by queued
	// and in-flight bodies. Zero uses 64 MiB.
	AttachmentBytes int64
}

// BodyWriter stores scrubbed event bodies in app_event_bodies. Enqueue never
// blocks and never touches the database: a full queue drops the body and
// counts it. One goroutine (Run) copies batches, flushing on size, on an
// interval and on shutdown.
type BodyWriter struct {
	pool       *pgxpool.Pool
	ch         chan bodyRow
	closed     bool
	closeMu    sync.RWMutex
	batchSize  int
	flushEvery time.Duration
	retryDelay time.Duration

	// queuedBytes is the attachment content of rows that are queued or being
	// written; attachmentBytes caps it.
	queuedBytes     atomic.Int64
	attachmentBytes int64

	// copyRows writes a batch; tests replace it.
	copyRows func(ctx context.Context, rows []bodyRow) error

	mu         sync.Mutex
	partitions map[time.Time]struct{}
}

type bodyRow struct {
	siteID  int32
	ts      time.Time
	eventID string
	json    []byte
	files   []appattach.File
}

// fileBytes is the attachment content a row holds in memory.
func (r bodyRow) fileBytes() int64 {
	var n int64
	for _, f := range r.files {
		n += int64(len(f.Data))
	}
	return n
}

// NewBodyWriter builds a writer with its own bounded queue.
func NewBodyWriter(pool *pgxpool.Pool, cfg BodyWriterConfig) *BodyWriter {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultBodyQueue
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = defaultFlushEvery
	}
	if cfg.AttachmentBytes <= 0 {
		cfg.AttachmentBytes = defaultAttachmentBytes
	}
	w := &BodyWriter{
		attachmentBytes: cfg.AttachmentBytes,
		pool:            pool, ch: make(chan bodyRow, cfg.QueueSize), batchSize: cfg.BatchSize,
		flushEvery: cfg.FlushEvery, retryDelay: defaultRetryDelay, partitions: map[time.Time]struct{}{},
	}
	w.copyRows = w.copy
	return w
}

// Enqueue queues one scrubbed body with its attachment content. It reports
// false when the queue is full or closed, or the body cannot be serialised;
// full and closed are counted. Attachment bytes held by queued and in-flight
// rows are capped: past the cap the body is still queued, but its attachments
// are dropped (their metadata says stored=false) and counted.
func (w *BodyWriter) Enqueue(b Body) bool {
	if b.EventID == "" {
		return false
	}
	var reserved int64
	for _, f := range b.Files {
		reserved += int64(len(f.Data))
	}
	if reserved > 0 && w.queuedBytes.Add(reserved) > w.attachmentBytes {
		w.queuedBytes.Add(-reserved)
		reserved = 0
		health.AppAttachmentDropped.Add(uint64(len(b.Files)))
		b.Files = nil
		kept := make([]AttachmentMeta, len(b.Attachments))
		copy(kept, b.Attachments)
		for i := range kept {
			kept[i].Stored = false
		}
		b.Attachments = kept
	}
	raw, err := json.Marshal(b)
	if err != nil {
		w.queuedBytes.Add(-reserved)
		return false
	}
	w.closeMu.RLock()
	defer w.closeMu.RUnlock()
	if w.closed {
		w.queuedBytes.Add(-reserved)
		health.AppBodyQueueFull.Add(1)
		return false
	}
	select {
	case w.ch <- bodyRow{siteID: b.SiteID, ts: b.TS, eventID: b.EventID, json: raw, files: b.Files}:
		return true
	default:
		w.queuedBytes.Add(-reserved)
		health.AppBodyQueueFull.Add(1)
		return false
	}
}

func (w *BodyWriter) close() {
	w.closeMu.Lock()
	w.closed = true
	w.closeMu.Unlock()
}

// Run flushes until ctx ends, then drains the queue and flushes the rest.
func (w *BodyWriter) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	batch := make([]bodyRow, 0, w.batchSize)
	flush := func(c context.Context) {
		if len(batch) > 0 {
			w.flushRetry(c, batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case r := <-w.ch:
			batch = append(batch, r)
			if len(batch) >= w.batchSize {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			w.close()
			final, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			for {
				select {
				case r := <-w.ch:
					batch = append(batch, r)
					if len(batch) >= w.batchSize {
						flush(final)
					}
					continue
				default:
				}
				break
			}
			flush(final)
			return nil
		}
	}
}

// flushRetry writes a batch, retrying once after a failure; a second failure
// drops the batch and counts it.
func (w *BodyWriter) flushRetry(ctx context.Context, batch []bodyRow) {
	defer func() {
		var n int64
		for _, r := range batch {
			n += r.fileBytes()
		}
		w.queuedBytes.Add(-n)
	}()
	err := w.copyRows(ctx, batch)
	if err != nil {
		slog.Warn("app body write failed, retrying once", "bodies", len(batch), "error", err)
		timer := time.NewTimer(w.retryDelay)
		select {
		case <-timer.C:
			err = w.copyRows(ctx, batch)
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err != nil {
		health.AppBodyWriteFailed.Add(uint64(len(batch)))
		slog.Error("app body write failed", "bodies", len(batch), "error", err)
	}
}

// copy writes the rows of sites that still exist; bodies of deleted sites are
// skipped so one gone site cannot fail everyone else's batch.
func (w *BodyWriter) copy(ctx context.Context, batch []bodyRow) error {
	seen := map[int32]struct{}{}
	ids := make([]int32, 0, 4)
	months := map[time.Time]struct{}{}
	for _, r := range batch {
		if _, ok := seen[r.siteID]; !ok {
			seen[r.siteID] = struct{}{}
			ids = append(ids, r.siteID)
		}
		t := r.ts.UTC()
		months[time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)] = struct{}{}
	}
	existing := map[int32]struct{}{}
	rows, err := w.pool.Query(ctx, `select id from sites where id = any($1)`, ids)
	if err != nil {
		return fmt.Errorf("check body sites: %w", err)
	}
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for month := range months {
		w.mu.Lock()
		_, done := w.partitions[month]
		w.mu.Unlock()
		if done {
			continue
		}
		if err := db.EnsureAppEventPartitions(ctx, w.pool, month, month.AddDate(0, 1, 0)); err != nil {
			return err
		}
		w.mu.Lock()
		w.partitions[month] = struct{}{}
		w.mu.Unlock()
	}
	out := make([][]any, 0, len(batch))
	var files [][]any
	for _, r := range batch {
		if _, ok := existing[r.siteID]; !ok {
			continue
		}
		out = append(out, []any{r.siteID, r.ts, r.eventID, r.json, int32(len(r.json))})
		for _, f := range r.files {
			files = append(files, []any{r.siteID, r.ts, r.eventID, f.Idx, f.Name, f.ContentType, int32(len(f.Data)), f.Data})
		}
	}
	if len(out) == 0 {
		return nil
	}
	// One transaction, so a retry after a failure cannot duplicate bodies
	// whose attachments failed.
	err = pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"app_event_bodies"},
			[]string{"site_id", "ts", "event_id", "body", "size"}, pgx.CopyFromRows(out)); err != nil {
			return err
		}
		if len(files) == 0 {
			return nil
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"app_event_attachments"},
			[]string{"site_id", "ts", "event_id", "idx", "name", "content_type", "size", "data"}, pgx.CopyFromRows(files))
		return err
	})
	if err != nil {
		w.mu.Lock()
		clear(w.partitions) // a partition may have been dropped by retention
		w.mu.Unlock()
	}
	return err
}
