// Package writer persists accepted analytics events and changed sessions in
// bounded batches. Its queue accounts for all uncommitted events, including a
// batch currently being retried, so database outages apply prompt backpressure.
package writer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/session"
)

const (
	defaultQueueSize = 1 << 16
	batchSize        = 5_000
	sessionBatchSize = 20_000
	shutdownTimeout  = 10 * time.Second
	initialBackoff   = 100 * time.Millisecond
	maximumBackoff   = 5 * time.Second
)

// Dropped counts events rejected because the writer has no remaining
// capacity. Written counts events committed to PostgreSQL.
var Dropped, Written atomic.Int64

// Event is the durable representation of an accepted analytics hit. It must
// never contain an IP address or a full user-agent string.
type Event struct {
	session.Hit
	SessionID  int64
	Props      map[string]string
	Screen     int16
	ReferrerID int32
}

// ClosedDays reports whether a site-local day has already been compacted.
type ClosedDays interface {
	IsClosed(site int32, day time.Time) bool
}

// Writer owns the bounded event queue and the database batch loops.
type Writer struct {
	ch    chan Event
	slots chan struct{}
	sess  session.Store

	flushEvery   time.Duration
	sessionFlush time.Duration
	closed       ClosedDays

	poolMu sync.RWMutex
	pool   *pgxpool.Pool

	stateMu   sync.RWMutex
	accepting bool
	started   atomic.Bool
}

// New constructs a writer. Run must be called exactly once to consume queued
// work. A negative queue size uses the production default; zero deliberately
// creates an always-full writer, which is useful for disabling intake.
func New(pool *pgxpool.Pool, sess session.Store, size int, flushEvery, sessionFlush time.Duration, closed ClosedDays) *Writer {
	if size < 0 {
		size = defaultQueueSize
	}
	if flushEvery <= 0 {
		flushEvery = time.Second
	}
	if sessionFlush <= 0 {
		sessionFlush = 10 * time.Second
	}
	return &Writer{
		ch:           make(chan Event, size),
		slots:        make(chan struct{}, size),
		sess:         sess,
		flushEvery:   flushEvery,
		sessionFlush: sessionFlush,
		closed:       closed,
		pool:         pool,
		accepting:    true,
	}
}

// Enqueue transfers ownership of e to the writer. It never blocks and returns
// false when all capacity is occupied by queued or not-yet-committed events.
func (w *Writer) Enqueue(e Event) bool {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	if !w.accepting {
		Dropped.Add(1)
		return false
	}
	select {
	case w.slots <- struct{}{}:
	default:
		Dropped.Add(1)
		return false
	}

	if e.Props != nil {
		props := make(map[string]string, len(e.Props))
		for key, value := range e.Props {
			props[key] = value
		}
		e.Props = props
	}
	select {
	case w.ch <- e:
		return true
	default:
		<-w.slots
		Dropped.Add(1)
		return false
	}
}

// SetPool replaces the destination pool. It exists to exercise outage
// recovery without rebuilding the writer; production wiring sets the pool in
// New and leaves it unchanged.
func (w *Writer) SetPool(pool *pgxpool.Pool) {
	w.poolMu.Lock()
	w.pool = pool
	w.poolMu.Unlock()
}

// Run writes until ctx is cancelled, then rejects new events, drains the
// queue, and gives all remaining event and session writes a fresh deadline.
func (w *Writer) Run(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return errors.New("writer: Run called more than once")
	}

	sessionDone := make(chan error, 1)
	go func() { sessionDone <- w.runSessions(ctx) }()
	eventErr := w.runEvents(ctx)
	sessionErr := <-sessionDone
	return errors.Join(eventErr, sessionErr)
}

func (w *Writer) runEvents(ctx context.Context) error {
	timer := time.NewTimer(w.flushEvery)
	defer timer.Stop()
	batch := make([]Event, 0, batchSize)

	for {
		select {
		case event := <-w.ch:
			batch = append(batch, event)
			if len(batch) == batchSize {
				if err := w.retryEvents(ctx, batch); err != nil {
					return w.shutdownEvents(batch)
				}
				w.release(batch)
				batch = batch[:0]
				timer.Reset(w.flushEvery)
			}
		case <-timer.C:
			if len(batch) != 0 {
				if err := w.retryEvents(ctx, batch); err != nil {
					return w.shutdownEvents(batch)
				}
				w.release(batch)
				batch = batch[:0]
			}
			timer.Reset(w.flushEvery)
		case <-ctx.Done():
			return w.shutdownEvents(batch)
		}
	}
}

func (w *Writer) shutdownEvents(batch []Event) error {
	w.stopAccepting()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	for {
		if len(batch) != 0 {
			if err := w.retryEvents(shutdownCtx, batch); err != nil {
				return fmt.Errorf("flush events at shutdown: %w", err)
			}
			w.release(batch)
			batch = batch[:0]
		}

		for len(batch) < batchSize {
			select {
			case event := <-w.ch:
				batch = append(batch, event)
			default:
				if len(batch) == 0 {
					return nil
				}
				break
			}
			if len(w.ch) == 0 {
				break
			}
		}
	}
}

func (w *Writer) release(batch []Event) {
	Written.Add(int64(len(batch)))
	for range batch {
		<-w.slots
	}
	clear(batch)
}

func (w *Writer) stopAccepting() {
	w.stateMu.Lock()
	w.accepting = false
	w.stateMu.Unlock()
}

func (w *Writer) retryEvents(ctx context.Context, batch []Event) error {
	backoff := initialBackoff
	for {
		if err := w.copyEvents(ctx, batch); err == nil {
			return nil
		}
		if err := waitBackoff(ctx, backoff); err != nil {
			return err
		}
		backoff = min(backoff*2, maximumBackoff)
	}
}

func (w *Writer) runSessions(ctx context.Context) error {
	if w.sess == nil {
		<-ctx.Done()
		return nil
	}
	ticker := time.NewTicker(w.sessionFlush)
	defer ticker.Stop()
	var pending []session.Snapshot

	for {
		select {
		case <-ticker.C:
			var err error
			pending, err = w.flushSessions(ctx, pending)
			if err != nil {
				return w.shutdownSessions(pending)
			}
		case <-ctx.Done():
			return w.shutdownSessions(pending)
		}
	}
}

func (w *Writer) shutdownSessions(pending []session.Snapshot) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if _, err := w.flushSessions(shutdownCtx, pending); err != nil {
		return fmt.Errorf("flush sessions at shutdown: %w", err)
	}
	return nil
}

func (w *Writer) flushSessions(ctx context.Context, pending []session.Snapshot) ([]session.Snapshot, error) {
	for {
		if len(pending) == 0 {
			pending = w.sess.Dirty(sessionBatchSize)
			if len(pending) == 0 {
				return nil, nil
			}
		}
		if err := w.retrySessions(ctx, pending); err != nil {
			return pending, err
		}
		clear(pending)
		pending = nil
	}
}

func (w *Writer) retrySessions(ctx context.Context, snapshots []session.Snapshot) error {
	backoff := initialBackoff
	for {
		if err := w.upsertSessions(ctx, snapshots); err == nil {
			return nil
		}
		if err := waitBackoff(ctx, backoff); err != nil {
			return err
		}
		backoff = min(backoff*2, maximumBackoff)
	}
}

func waitBackoff(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) currentPool() *pgxpool.Pool {
	w.poolMu.RLock()
	defer w.poolMu.RUnlock()
	return w.pool
}

var eventColumns = []string{
	"site_id", "retention_class", "local_day", "ts", "visitor_id", "session_id", "kind",
	"name_id", "path_id", "referrer_id", "source_id", "medium_id", "campaign_id",
	"utm_content_id", "utm_term_id", "country", "browser_id", "os_id", "device", "screen",
	"engagement_ms", "scroll_pct", "props",
}

func (w *Writer) copyEvents(ctx context.Context, events []Event) error {
	pool := w.currentPool()
	if pool == nil {
		return errors.New("writer: database pool is nil")
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"events"}, eventColumns, &eventSource{events: events}); err != nil {
			return err
		}
		if w.closed == nil {
			return nil
		}
		dirty := make(map[siteDay]struct{})
		for _, event := range events {
			if !event.LocalDay.IsZero() && w.closed.IsClosed(event.SiteID, event.LocalDay) {
				dirty[siteDay{site: event.SiteID, day: event.LocalDay}] = struct{}{}
			}
		}
		for key := range dirty {
			if _, err := tx.Exec(ctx, `
				insert into dirty_day(site_id, day, reason)
				values ($1, $2, 'late_event')
				on conflict (site_id, day) do nothing`, key.site, key.day); err != nil {
				return err
			}
		}
		return nil
	})
}

type siteDay struct {
	site int32
	day  time.Time
}

type eventSource struct {
	events []Event
	index  int
	row    [23]any
}

func (s *eventSource) Next() bool {
	s.index++
	return s.index <= len(s.events)
}

func (s *eventSource) Err() error { return nil }

func (s *eventSource) Values() ([]any, error) {
	event := &s.events[s.index-1]
	var props any
	if event.Props != nil {
		props = event.Props
	}
	s.row = [23]any{
		event.SiteID, event.Retention, event.LocalDay, event.TS, event.VisitorID, event.SessionID, event.Kind,
		event.NameID, event.PathID, event.ReferrerID, event.Source, event.Medium, event.Campaign,
		int32(0), int32(0), event.Country, event.Browser, event.OS, event.Device, event.Screen,
		event.EngagementMS, event.ScrollPct, props,
	}
	return s.row[:], nil
}

var sessionColumns = []string{
	"site_id", "retention_class", "session_id", "visitor_id", "start", "last", "local_day",
	"entry_path", "exit_path", "source_id", "medium_id", "campaign_id", "country", "browser_id",
	"os_id", "device", "pageviews", "events", "engagement_ms", "bounce",
}

func (w *Writer) upsertSessions(ctx context.Context, snapshots []session.Snapshot) error {
	pool := w.currentPool()
	if pool == nil {
		return errors.New("writer: database pool is nil")
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `create temporary table sessions_stage (like sessions) on commit drop`); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"sessions_stage"}, sessionColumns, &sessionSource{snapshots: snapshots}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			insert into sessions (`+joinColumns(sessionColumns)+`)
			select `+joinColumns(sessionColumns)+` from sessions_stage
			on conflict (site_id, start, session_id, retention_class) do update set
				last = excluded.last,
				exit_path = excluded.exit_path,
				pageviews = excluded.pageviews,
				events = excluded.events,
				engagement_ms = excluded.engagement_ms,
				bounce = excluded.bounce
			where excluded.last >= sessions.last`)
		return err
	})
}

func joinColumns(columns []string) string {
	result := ""
	for i, column := range columns {
		if i != 0 {
			result += ", "
		}
		result += column
	}
	return result
}

type sessionSource struct {
	snapshots []session.Snapshot
	index     int
	row       [20]any
}

func (s *sessionSource) Next() bool {
	s.index++
	return s.index <= len(s.snapshots)
}

func (s *sessionSource) Err() error { return nil }

func (s *sessionSource) Values() ([]any, error) {
	snapshot := &s.snapshots[s.index-1]
	s.row = [20]any{
		snapshot.SiteID, snapshot.Retention, snapshot.SessionID, snapshot.VisitorID,
		snapshot.Start, snapshot.Last, snapshot.LocalDay, snapshot.Entry, snapshot.Exit,
		snapshot.Source, snapshot.Medium, snapshot.Campaign, snapshot.Country,
		snapshot.Browser, snapshot.OS, snapshot.Device, snapshot.Pageviews,
		snapshot.Events, snapshot.EngagementMS, snapshot.Bounce,
	}
	return s.row[:], nil
}
