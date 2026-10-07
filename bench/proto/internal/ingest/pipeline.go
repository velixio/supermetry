// Package ingest is the hot path: event building, the bounded queue, the
// COPY batchers, the session flusher, the HTTP API and the NDJSON loader.
package ingest

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"panel/bench/proto/internal/core"
)

// Errors returned by Submit/TrySubmit.
var (
	ErrBackpressure = errors.New("ingest queue full")
	ErrClosed       = errors.New("pipeline closed")
)

// Writer persists batches. *store.Store implements it.
type Writer interface {
	CopyEvents(ctx context.Context, evs []*core.Event) error
	UpsertSessions(ctx context.Context, ss []core.Session) error
}

// Config tunes the pipeline.
type Config struct {
	QueueSize         int           // bounded channel capacity = backpressure limit
	BatchSize         int           // events per COPY
	FlushEvery        time.Duration // max age of a partial batch
	Writers           int           // concurrent COPY batchers
	SessionFlushEvery time.Duration // dirty-session upsert interval (D8: ~10 s)
	SessionFlushAfter int64         // also flush after this many events (bulk load)
	SessionWriters    int           // parallel session upsert streams (default 4)
	Clock             func() int64  // "now" in unix µs for session eviction; nil = wall clock
	Log               *slog.Logger
}

func (c *Config) defaults() {
	if c.QueueSize <= 0 {
		c.QueueSize = 1 << 16
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 5000
	}
	if c.FlushEvery <= 0 {
		c.FlushEvery = time.Second
	}
	if c.Writers <= 0 {
		c.Writers = 2
	}
	if c.SessionFlushEvery <= 0 {
		c.SessionFlushEvery = 10 * time.Second
	}
	if c.SessionFlushAfter <= 0 {
		c.SessionFlushAfter = 250_000
	}
	if c.SessionWriters <= 0 {
		c.SessionWriters = 4
	}
	if c.Clock == nil {
		c.Clock = func() int64 { return time.Now().UnixMicro() }
	}
	if c.Log == nil {
		c.Log = slog.Default()
	}
}

// Stats are pipeline counters.
type Stats struct {
	Accepted, Rejected, Written, Dropped, Batches, SessionsUpserted, CopyErrors int64
	QueueLen, QueueCap, LiveSessions                                            int
}

// Pipeline: Assign session (sharded cache) → bounded channel → batchers →
// COPY; a flusher upserts dirty sessions periodically.
//
// Events travel as *core.Event: ownership moves to the pipeline on a
// successful send and the sender never touches the event again. This keeps
// the bounded buffer at 8 bytes per slot.
type Pipeline struct {
	cfg   Config
	w     Writer
	Cache *core.Cache

	mu     sync.RWMutex // guards closed + close(events) against concurrent sends
	closed bool
	events chan *core.Event

	writeCtx    context.Context // cancelled only when Close's deadline expires
	cancelWrite context.CancelFunc
	batchers    sync.WaitGroup
	flusherDone chan struct{}
	stopFlusher chan struct{}
	kick        chan struct{}
	lastKick    atomic.Int64 // accepted count at the last kick
	flushedUpTo atomic.Int64 // accepted count covered by the last completed session flush
	flushedMu   sync.Mutex
	flushedCh   chan struct{} // closed (and replaced) when a session flush completes

	accepted, rejected, written, dropped, batches, upserted, copyErrs atomic.Int64
}

// New starts the pipeline goroutines.
func New(cfg Config, w Writer, cache *core.Cache) *Pipeline {
	cfg.defaults()
	p := &Pipeline{
		cfg: cfg, w: w, Cache: cache,
		events:      make(chan *core.Event, cfg.QueueSize),
		flusherDone: make(chan struct{}),
		stopFlusher: make(chan struct{}),
		kick:        make(chan struct{}, 1),
		flushedCh:   make(chan struct{}),
	}
	p.writeCtx, p.cancelWrite = context.WithCancel(context.Background())
	for range cfg.Writers {
		p.batchers.Go(p.batcher)
	}
	go p.flusher()
	return p
}

// TrySubmit is the HTTP hot path: never blocks. On a full queue it returns
// ErrBackpressure and leaves the session cache untouched.
func (p *Pipeline) TrySubmit(ev *core.Event) error {
	if p.sessionBacklog() > 8*p.cfg.SessionFlushAfter { // session upserts can't keep up
		p.rejected.Add(1)
		p.kickFlusher()
		return ErrBackpressure
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	ok := p.Cache.Assign(ev, func(e *core.Event) bool {
		select {
		case p.events <- e:
			return true
		default:
			return false
		}
	})
	if !ok {
		p.rejected.Add(1)
		return ErrBackpressure
	}
	p.countAccepted(1)
	return nil
}

// Submit is the bulk-load path: it waits for queue space (or ctx).
func (p *Pipeline) Submit(ctx context.Context, ev *core.Event) error {
	for p.sessionBacklog() > 2*p.cfg.SessionFlushAfter { // wait for the session flusher
		p.kickFlusher()
		select {
		case <-p.flushedSignal():
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	var cerr error
	ok := p.Cache.Assign(ev, func(e *core.Event) bool {
		select {
		case p.events <- e:
			return true
		case <-ctx.Done():
			cerr = ctx.Err()
			return false
		}
	})
	if !ok {
		return cerr
	}
	p.countAccepted(1)
	return nil
}

func (p *Pipeline) countAccepted(n int64) {
	a := p.accepted.Add(n)
	if a-p.lastKick.Load() >= p.cfg.SessionFlushAfter {
		p.lastKick.Store(a)
		p.kickFlusher()
	}
}

func (p *Pipeline) kickFlusher() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// sessionBacklog is the number of accepted events whose session changes
// have not been persisted by a completed flush yet.
func (p *Pipeline) sessionBacklog() int64 { return p.accepted.Load() - p.flushedUpTo.Load() }

func (p *Pipeline) flushedSignal() <-chan struct{} {
	p.flushedMu.Lock()
	defer p.flushedMu.Unlock()
	return p.flushedCh
}

func (p *Pipeline) batcher() {
	batch := make([]*core.Event, 0, p.cfg.BatchSize)
	timer := time.NewTimer(p.cfg.FlushEvery)
	defer timer.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		p.write(batch)
		clear(batch)
		batch = batch[:0]
	}
	for {
		select {
		case ev, ok := <-p.events:
			if !ok {
				flush()
				return
			}
			batch = append(batch, ev)
			if len(batch) >= p.cfg.BatchSize {
				flush()
				timer.Reset(p.cfg.FlushEvery)
			}
		case <-timer.C:
			flush()
			timer.Reset(p.cfg.FlushEvery)
		}
	}
}

// write retries with backoff until success; it gives up (and counts the
// loss) only when Close's deadline cancels writeCtx. While it retries the
// queue fills and TrySubmit answers 503.
func (p *Pipeline) write(batch []*core.Event) {
	backoff := 50 * time.Millisecond
	for {
		err := p.w.CopyEvents(p.writeCtx, batch)
		if err == nil {
			p.written.Add(int64(len(batch)))
			p.batches.Add(1)
			return
		}
		p.copyErrs.Add(1)
		if p.writeCtx.Err() != nil {
			p.dropped.Add(int64(len(batch)))
			p.cfg.Log.Error("dropping event batch at shutdown", "n", len(batch), "err", err)
			return
		}
		p.cfg.Log.Warn("COPY failed, retrying", "n", len(batch), "err", err, "backoff", backoff)
		t := time.NewTimer(backoff)
		select {
		case <-t.C:
		case <-p.writeCtx.Done():
			t.Stop()
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func (p *Pipeline) flusher() {
	defer close(p.flusherDone)
	t := time.NewTicker(p.cfg.SessionFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-p.kick:
		case <-p.stopFlusher:
			return
		}
		p.flushSessions(true)
	}
}

// flushSessions upserts every dirty session: SessionWriters goroutines each
// own a slice of the cache shards and upsert in chunks of ≤ 20k rows, so
// memory stays bounded however large the backlog is.
func (p *Pipeline) flushSessions(evict bool) {
	upTo := p.accepted.Load() // changes up to here are collected below
	now := p.cfg.Clock()
	var wg sync.WaitGroup
	for g := range p.cfg.SessionWriters {
		wg.Go(func() {
			var buf []core.Session
			for i := g; i < core.NumShards; i += p.cfg.SessionWriters {
				buf = p.Cache.CollectShard(i, now, evict, buf)
				if len(buf) >= 20_000 || i+p.cfg.SessionWriters >= core.NumShards {
					p.upsert(buf)
					clear(buf)
					buf = buf[:0]
				}
			}
		})
	}
	wg.Wait()
	p.flushedUpTo.Store(upTo)
	p.flushedMu.Lock()
	close(p.flushedCh)
	p.flushedCh = make(chan struct{})
	p.flushedMu.Unlock()
}

func (p *Pipeline) upsert(ss []core.Session) {
	if len(ss) == 0 {
		return
	}
	backoff := 100 * time.Millisecond
	for {
		err := p.w.UpsertSessions(p.writeCtx, ss)
		if err == nil {
			p.upserted.Add(int64(len(ss)))
			return
		}
		if p.writeCtx.Err() != nil {
			p.cfg.Log.Error("dropping session batch at shutdown", "n", len(ss), "err", err)
			return
		}
		p.cfg.Log.Warn("session upsert failed, retrying", "n", len(ss), "err", err)
		t := time.NewTimer(backoff)
		select {
		case <-t.C:
		case <-p.writeCtx.Done():
			t.Stop()
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

// Close stops intake, drains the queue, flushes the last batches and all
// dirty sessions. If ctx expires first, in-flight writes are cancelled and
// what is left is counted as dropped.
func (p *Pipeline) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.events) // we are the only sender-side owner; senders hold RLock
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.batchers.Wait()
		close(p.stopFlusher)
		<-p.flusherDone
		p.flushSessions(false) // everything still dirty
		close(done)
	}()
	select {
	case <-done:
		p.cancelWrite()
		return nil
	case <-ctx.Done():
		p.cancelWrite()
		<-done
		return ctx.Err()
	}
}

// Stats snapshots the counters.
func (p *Pipeline) Stats() Stats {
	return Stats{
		Accepted: p.accepted.Load(), Rejected: p.rejected.Load(), Written: p.written.Load(),
		Dropped: p.dropped.Load(), Batches: p.batches.Load(), SessionsUpserted: p.upserted.Load(),
		CopyErrors: p.copyErrs.Load(), QueueLen: len(p.events), QueueCap: cap(p.events),
		LiveSessions: p.Cache.Len(),
	}
}
