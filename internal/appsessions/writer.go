// Package appsessions keeps one state row per Sentry session. A session sends
// several updates (init, then the final status), possibly out of order after a
// retry, so the writer upserts and keeps the worst status seen.
package appsessions

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/health"
)

const (
	defaultQueue      = 10_000
	defaultBatchSize  = 500
	defaultFlushEvery = time.Second
	shutdownTimeout   = 15 * time.Second
	defaultRetryDelay = time.Second
)

// Stats counts named events (sentryingest.Stats satisfies it).
type Stats interface {
	Add(category string, n uint64)
}

// Update is one session update. The hashes are HMACs under the app's secret;
// the raw sid and did never reach this package.
type Update struct {
	SiteID                         int32
	SessionHash, InstallHash       []byte
	Started                        time.Time
	Release, Environment, Platform string
	Status                         string
	Errors                         int
	DurationMS                     *int64
	Init                           bool
}

// Rank orders statuses by severity: ok < exited < errored < abnormal < crashed.
// An unknown status ranks as ok.
func Rank(status string) int {
	switch status {
	case "exited":
		return 1
	case "errored":
		return 2
	case "abnormal":
		return 3
	case "crashed":
		return 4
	}
	return 0
}

var statusByRank = [...]string{"ok", "exited", "errored", "abnormal", "crashed"}

// WriterConfig tunes a Writer. Zero values use the defaults.
type WriterConfig struct {
	QueueSize  int
	BatchSize  int
	FlushEvery time.Duration
}

// Writer upserts session updates in batches. Enqueue never blocks and never
// touches the database: a full queue drops the update and counts it.
type Writer struct {
	pool       *pgxpool.Pool
	stats      Stats
	ch         chan Update
	batchSize  int
	flushEvery time.Duration
	retryDelay time.Duration

	closeMu sync.RWMutex
	closed  bool
	done    chan struct{}
	stop    chan struct{}

	// write stores a merged batch; tests replace it.
	write func(ctx context.Context, batch []Update) error

	mu         sync.Mutex
	partitions map[time.Time]struct{}
}

// NewWriter builds a writer and starts its flush loop. Close stops it.
func NewWriter(pool *pgxpool.Pool, cfg WriterConfig, stats Stats) *Writer {
	w := newWriter(pool, cfg, stats)
	w.start()
	return w
}

func newWriter(pool *pgxpool.Pool, cfg WriterConfig, stats Stats) *Writer {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueue
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = defaultFlushEvery
	}
	w := &Writer{
		pool: pool, stats: stats, ch: make(chan Update, cfg.QueueSize), batchSize: cfg.BatchSize,
		flushEvery: cfg.FlushEvery, retryDelay: defaultRetryDelay,
		done: make(chan struct{}), stop: make(chan struct{}), partitions: map[time.Time]struct{}{},
	}
	w.write = w.upsert
	return w
}

func (w *Writer) start() { go w.loop() }

// Enqueue queues one update. It reports false when the queue is full (counted
// as session_queue_full) or the writer is closed.
func (w *Writer) Enqueue(u Update) bool {
	w.closeMu.RLock()
	defer w.closeMu.RUnlock()
	if w.closed {
		return false
	}
	select {
	case w.ch <- u:
		return true
	default:
		if w.stats != nil {
			w.stats.Add("session_queue_full", 1)
		}
		return false
	}
}

// Close stops accepting updates, writes what is queued and waits for the
// flush loop to end. It is safe to call more than once.
func (w *Writer) Close() {
	w.closeMu.Lock()
	if w.closed {
		w.closeMu.Unlock()
		<-w.done
		return
	}
	w.closed = true
	w.closeMu.Unlock()
	close(w.stop)
	<-w.done
}

func (w *Writer) loop() {
	defer close(w.done)
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	batch := make([]Update, 0, w.batchSize)
	flush := func(ctx context.Context) {
		if len(batch) > 0 {
			w.flushRetry(ctx, batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case u := <-w.ch:
			batch = append(batch, u)
			if len(batch) >= w.batchSize {
				flush(context.Background())
			}
		case <-ticker.C:
			flush(context.Background())
		case <-w.stop:
			final, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			for {
				select {
				case u := <-w.ch:
					batch = append(batch, u)
					if len(batch) >= w.batchSize {
						flush(final)
					}
					continue
				default:
				}
				break
			}
			flush(final)
			return
		}
	}
}

// flushRetry writes a batch and retries once after a failure; a second
// failure drops the batch and counts it. Replaying is safe: the upsert is
// idempotent.
func (w *Writer) flushRetry(ctx context.Context, batch []Update) {
	merged := merge(batch)
	err := w.write(ctx, merged)
	if err != nil {
		slog.Warn("app session write failed, retrying once", "sessions", len(merged), "error", err)
		timer := time.NewTimer(w.retryDelay)
		select {
		case <-timer.C:
			err = w.write(ctx, merged)
		case <-ctx.Done():
			timer.Stop()
		}
	}
	if err != nil {
		health.AppIngestWriteFailed.Add(uint64(len(merged)))
		if w.stats != nil {
			w.stats.Add("session_write_failed", uint64(len(merged)))
		}
		slog.Error("app session write failed", "sessions", len(merged), "error", err)
	}
}

type key struct {
	site    int32
	session string
	started int64
}

// merge folds updates of the same session into one row (the same rules as the
// upsert), because one INSERT cannot touch a row twice.
func merge(batch []Update) []Update {
	idx := make(map[key]int, len(batch))
	out := make([]Update, 0, len(batch))
	for _, u := range batch {
		k := key{u.SiteID, string(u.SessionHash), u.Started.UnixMicro()}
		i, ok := idx[k]
		if !ok {
			idx[k] = len(out)
			out = append(out, u)
			continue
		}
		cur := &out[i]
		if Rank(u.Status) > Rank(cur.Status) {
			cur.Status = u.Status
		}
		cur.Errors = max(cur.Errors, u.Errors)
		if u.DurationMS != nil {
			cur.DurationMS = u.DurationMS
		}
		if cur.InstallHash == nil {
			cur.InstallHash = u.InstallHash
		}
		if cur.Release == "" {
			cur.Release = u.Release
		}
		if cur.Environment == "" {
			cur.Environment = u.Environment
		}
		if cur.Platform == "" {
			cur.Platform = u.Platform
		}
	}
	return out
}

// upsertSQL writes a batch with one statement. Rows of deleted sites are
// skipped by the join so one gone site cannot fail the batch. Rank is an
// inline case expression, not a SQL function.
const upsertSQL = `
insert into app_sessions (site_id, started, session_hash, install_hash, release, environment, platform, status, errors, duration_ms, updated_at)
select u.site_id, u.started, u.session_hash, u.install_hash, u.release, u.environment, u.platform, u.status, u.errors, u.duration_ms, $11
from unnest($1::int[], $2::timestamptz[], $3::bytea[], $4::bytea[], $5::text[], $6::text[], $7::text[], $8::text[], $9::int[], $10::bigint[])
  as u(site_id, started, session_hash, install_hash, release, environment, platform, status, errors, duration_ms)
join sites s on s.id = u.site_id
on conflict (site_id, session_hash, started) do update set
  status = case
    when (case excluded.status when 'exited' then 1 when 'errored' then 2 when 'abnormal' then 3 when 'crashed' then 4 else 0 end)
       > (case app_sessions.status when 'exited' then 1 when 'errored' then 2 when 'abnormal' then 3 when 'crashed' then 4 else 0 end)
    then excluded.status else app_sessions.status end,
  errors = greatest(app_sessions.errors, excluded.errors),
  duration_ms = coalesce(excluded.duration_ms, app_sessions.duration_ms),
  install_hash = coalesce(app_sessions.install_hash, excluded.install_hash),
  release = case when app_sessions.release = '' then excluded.release else app_sessions.release end,
  environment = case when app_sessions.environment = '' then excluded.environment else app_sessions.environment end,
  platform = case when app_sessions.platform = '' then excluded.platform else app_sessions.platform end,
  updated_at = excluded.updated_at`

func (w *Writer) upsert(ctx context.Context, batch []Update) error {
	if len(batch) == 0 {
		return nil
	}
	if err := w.ensurePartitions(ctx, batch); err != nil {
		return err
	}
	n := len(batch)
	sites := make([]int32, n)
	started := make([]time.Time, n)
	sessions := make([][]byte, n)
	installs := make([][]byte, n)
	releases := make([]string, n)
	envs := make([]string, n)
	platforms := make([]string, n)
	statuses := make([]string, n)
	errs := make([]int32, n)
	durs := make([]*int64, n)
	for i, u := range batch {
		sites[i], started[i], sessions[i], installs[i] = u.SiteID, u.Started, u.SessionHash, u.InstallHash
		releases[i], envs[i], platforms[i] = u.Release, u.Environment, u.Platform
		statuses[i] = statusByRank[Rank(u.Status)]
		errs[i] = int32(min(max(u.Errors, 0), 1<<30))
		durs[i] = u.DurationMS
	}
	_, err := w.pool.Exec(ctx, upsertSQL, sites, started, sessions, installs, releases, envs, platforms, statuses, errs, durs, time.Now().UTC())
	if err != nil {
		w.mu.Lock()
		clear(w.partitions) // a partition may have been dropped by retention
		w.mu.Unlock()
		return fmt.Errorf("upsert app sessions: %w", err)
	}
	return nil
}

// ensurePartitions creates the monthly partitions the batch needs; a late
// update can land in a month the regular lookahead never created.
func (w *Writer) ensurePartitions(ctx context.Context, batch []Update) error {
	months := map[time.Time]struct{}{}
	for _, u := range batch {
		t := u.Started.UTC()
		months[time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)] = struct{}{}
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
	return nil
}
