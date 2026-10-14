package links

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultFlushEvery = 5 * time.Second
	defaultMaxKeys    = 20_000
	flushThreshold    = 5_000
)

type clickKey struct {
	linkID   int64
	day      string
	country  string
	referrer string
	device   string
}

type impressionKey struct {
	siteID   int32
	day      string
	campaign string
	country  string
}

// Recorder aggregates clicks and impressions in memory and persists them in
// batches. Request goroutines only take a mutex around a map increment and
// never wait for the database. The key space is bounded: when it is full, new
// keys are dropped and counted rather than growing memory or blocking.
type Recorder struct {
	pool       *pgxpool.Pool
	flushEvery time.Duration
	maxKeys    int
	wake       chan struct{}
	// Views, when set, mirrors recorded impressions for in-memory badges.
	Views *SiteViews

	mu          sync.Mutex
	clicks      map[clickKey]int64
	impressions map[impressionKey]int64
	pending     map[int64]int64 // unflushed clicks per link, for click limits

	dropped atomic.Int64
	flushMu sync.Mutex
}

// RecorderOptions tunes batching; zero values select the defaults.
type RecorderOptions struct {
	FlushEvery time.Duration
	MaxKeys    int
}

// NewRecorder constructs a recorder. A nil pool keeps data in memory only,
// which is useful for tests that inspect the buffers.
func NewRecorder(pool *pgxpool.Pool, options RecorderOptions) *Recorder {
	if options.FlushEvery <= 0 {
		options.FlushEvery = defaultFlushEvery
	}
	if options.MaxKeys <= 0 {
		options.MaxKeys = defaultMaxKeys
	}
	return &Recorder{
		pool: pool, flushEvery: options.FlushEvery, maxKeys: options.MaxKeys,
		wake:        make(chan struct{}, 1),
		clicks:      make(map[clickKey]int64),
		impressions: make(map[impressionKey]int64),
		pending:     make(map[int64]int64),
	}
}

// Dropped returns how many events were discarded because the buffer was full.
func (r *Recorder) Dropped() int64 { return r.dropped.Load() }

// Pending returns clicks recorded for a link but not yet flushed.
func (r *Recorder) Pending(linkID int64) int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[linkID]
}

// Click records one human click. It never blocks on the database.
func (r *Recorder) Click(linkID int64, day, country, referrer, device string) {
	key := clickKey{linkID, day, country, referrer, device}
	r.mu.Lock()
	if _, ok := r.clicks[key]; !ok && len(r.clicks)+len(r.impressions) >= r.maxKeys {
		r.mu.Unlock()
		r.dropped.Add(1)
		return
	}
	r.clicks[key]++
	r.pending[linkID]++
	full := len(r.clicks)+len(r.impressions) >= flushThreshold
	r.mu.Unlock()
	if full {
		r.nudge()
	}
}

// Impression records one pixel load. It never blocks on the database.
func (r *Recorder) Impression(siteID int32, day, campaign, country string) {
	key := impressionKey{siteID, day, campaign, country}
	r.mu.Lock()
	if _, ok := r.impressions[key]; !ok && len(r.clicks)+len(r.impressions) >= r.maxKeys {
		r.mu.Unlock()
		r.dropped.Add(1)
		return
	}
	r.impressions[key]++
	if r.Views != nil {
		r.Views.Add(siteID, day, 1)
	}
	full := len(r.clicks)+len(r.impressions) >= flushThreshold
	r.mu.Unlock()
	if full {
		r.nudge()
	}
}

func (r *Recorder) nudge() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run flushes on an interval, on size pressure and once more on shutdown.
func (r *Recorder) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return r.Flush(flushCtx)
		case <-ticker.C:
		case <-r.wake:
		}
		if err := r.Flush(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("link statistics flush failed; will retry", "error", err)
		}
	}
}

// snapshot returns copies of the buffered aggregates for tests.
func (r *Recorder) snapshot() (map[clickKey]int64, map[impressionKey]int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clicks := make(map[clickKey]int64, len(r.clicks))
	for k, v := range r.clicks {
		clicks[k] = v
	}
	impressions := make(map[impressionKey]int64, len(r.impressions))
	for k, v := range r.impressions {
		impressions[k] = v
	}
	return clicks, impressions
}

// Flush writes the buffered aggregates in one transaction. On failure the
// counts are merged back so the next flush retries them. Rows whose link or
// site was deleted meanwhile are skipped by the insert's existence guard.
func (r *Recorder) Flush(ctx context.Context) error {
	if r.pool == nil {
		return nil
	}
	r.flushMu.Lock()
	defer r.flushMu.Unlock()

	r.mu.Lock()
	clicks, impressions := r.clicks, r.impressions
	if len(clicks) == 0 && len(impressions) == 0 {
		r.mu.Unlock()
		return nil
	}
	r.clicks = make(map[clickKey]int64)
	r.impressions = make(map[impressionKey]int64)
	r.mu.Unlock()

	perLink := make(map[int64]int64)
	batch := &pgx.Batch{}
	for key, n := range clicks {
		perLink[key.linkID] += n
		batch.Queue(`
			insert into link_clicks_daily(link_id, day, country, referrer, device, clicks)
			select $1, $2::date, $3, $4, $5, $6 where exists (select 1 from links where id=$1)
			on conflict (link_id, day, country, referrer, device)
			do update set clicks = link_clicks_daily.clicks + excluded.clicks`,
			key.linkID, key.day, key.country, key.referrer, key.device, n)
	}
	for id, n := range perLink {
		batch.Queue(`update links set clicks = clicks + $2 where id=$1`, id, n)
	}
	for key, n := range impressions {
		batch.Queue(`
			insert into pixel_impressions_daily(site_id, day, campaign, country, impressions)
			select $1, $2::date, $3, $4, $5 where exists (select 1 from sites where id=$1)
			on conflict (site_id, day, campaign, country)
			do update set impressions = pixel_impressions_daily.impressions + excluded.impressions`,
			key.siteID, key.day, key.campaign, key.country, n)
	}

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		results := tx.SendBatch(ctx, batch)
		for range batch.Len() {
			if _, err := results.Exec(); err != nil {
				_ = results.Close()
				return err
			}
		}
		return results.Close()
	})
	if err != nil {
		r.restore(clicks, impressions)
		return err
	}
	r.mu.Lock()
	for id, n := range perLink {
		if r.pending[id] -= n; r.pending[id] <= 0 {
			delete(r.pending, id)
		}
	}
	r.mu.Unlock()
	return nil
}

// restore merges counts from a failed flush back into the buffers, dropping
// whatever no longer fits within the key bound.
func (r *Recorder) restore(clicks map[clickKey]int64, impressions map[impressionKey]int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, n := range clicks {
		if _, ok := r.clicks[key]; !ok && len(r.clicks)+len(r.impressions) >= r.maxKeys {
			r.dropped.Add(n)
			r.pending[key.linkID] -= n
			continue
		}
		r.clicks[key] += n
	}
	for key, n := range impressions {
		if _, ok := r.impressions[key]; !ok && len(r.clicks)+len(r.impressions) >= r.maxKeys {
			r.dropped.Add(n)
			continue
		}
		r.impressions[key] += n
	}
}
