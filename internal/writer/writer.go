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
	SessionID         int64
	ProfileExternalID string
	ProfileTraits     map[string]string
	ProfileID         int64
	Props             map[string]string
	Screen            int16
	ReferrerID        int32
	RevenueMicros     int64
	RevenueCurrency   string
}

// ClosedDays reports whether a site-local day has already been compacted.
type ClosedDays interface {
	IsClosed(site int32, day time.Time) bool
}

// Writer owns the bounded event queue and the database batch loops.
type Writer struct {
	ch           chan Event
	slots        chan struct{}
	heatmapCh    chan HeatmapEvent
	heatmapSlots chan struct{}
	sess         session.Store

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
		heatmapCh:    make(chan HeatmapEvent, min(size, 1024)),
		heatmapSlots: make(chan struct{}, min(size, 1024)),
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
	if e.ProfileTraits != nil {
		traits := make(map[string]string, len(e.ProfileTraits))
		for key, value := range e.ProfileTraits {
			traits[key] = value
		}
		e.ProfileTraits = traits
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
	heatmapDone := make(chan error, 1)
	go func() { sessionDone <- w.runSessions(ctx) }()
	go func() { heatmapDone <- w.runHeatmaps(ctx, w.upsertHeatmaps) }()
	eventErr := w.runEvents(ctx, w.copyEvents)
	sessionErr := <-sessionDone
	heatmapErr := <-heatmapDone
	return errors.Join(eventErr, sessionErr, heatmapErr)
}

func (w *Writer) runEvents(ctx context.Context, write func(context.Context, []Event) error) error {
	timer := time.NewTimer(w.flushEvery)
	defer timer.Stop()
	batch := make([]Event, 0, batchSize)

	for {
		select {
		case event := <-w.ch:
			batch = append(batch, event)
			if len(batch) == batchSize {
				if err := w.retryEvents(ctx, context.WithoutCancel(ctx), batch, write); err != nil {
					return w.shutdownEvents(batch, write)
				}
				w.release(batch)
				batch = batch[:0]
				timer.Reset(w.flushEvery)
			}
		case <-timer.C:
			if len(batch) != 0 {
				if err := w.retryEvents(ctx, context.WithoutCancel(ctx), batch, write); err != nil {
					return w.shutdownEvents(batch, write)
				}
				w.release(batch)
				batch = batch[:0]
			}
			timer.Reset(w.flushEvery)
		case <-ctx.Done():
			return w.shutdownEvents(batch, write)
		}
	}
}

func (w *Writer) shutdownEvents(batch []Event, write func(context.Context, []Event) error) error {
	w.stopAccepting()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	for {
		if len(batch) != 0 {
			if err := w.retryEvents(shutdownCtx, shutdownCtx, batch, write); err != nil {
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

func (w *Writer) retryEvents(waitCtx, attemptCtx context.Context, batch []Event, write func(context.Context, []Event) error) error {
	return retryOperation(waitCtx, attemptCtx, func(ctx context.Context) error {
		return write(ctx, batch)
	})
}

func retryOperation(waitCtx, attemptCtx context.Context, operation func(context.Context) error) error {
	backoff := initialBackoff
	for {
		if err := operation(attemptCtx); err == nil {
			return nil
		}
		if err := waitBackoff(waitCtx, backoff); err != nil {
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
	"utm_content_id", "utm_term_id", "country", "region_id", "city_id", "browser_id", "os_id", "device", "screen",
	"engagement_ms", "scroll_pct", "props",
	"revenue_micros", "revenue_currency",
	"profile_id",
}

func (w *Writer) copyEvents(ctx context.Context, events []Event) error {
	pool := w.currentPool()
	if pool == nil {
		return errors.New("writer: database pool is nil")
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := upsertProfiles(ctx, tx, events); err != nil {
			return err
		}
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

type profileKey struct {
	siteID     int32
	externalID string
}

type profileRow struct {
	profileKey
	traits              map[string]string
	firstSeen, lastSeen time.Time
}

func upsertProfiles(ctx context.Context, tx pgx.Tx, events []Event) error {
	profiles := make(map[profileKey]*profileRow)
	for i := range events {
		event := &events[i]
		if event.ProfileExternalID == "" {
			continue
		}
		key := profileKey{siteID: event.SiteID, externalID: event.ProfileExternalID}
		row := profiles[key]
		if row == nil {
			row = &profileRow{
				profileKey: key,
				traits:     make(map[string]string, len(event.ProfileTraits)),
				firstSeen:  event.TS,
				lastSeen:   event.TS,
			}
			profiles[key] = row
		}
		if event.TS.Before(row.firstSeen) {
			row.firstSeen = event.TS
		}
		if event.TS.After(row.lastSeen) {
			row.lastSeen = event.TS
		}
		mergeTraits(row, event.ProfileTraits)
	}
	if len(profiles) == 0 {
		return nil
	}

	rowsToWrite := make([]profileRow, 0, len(profiles))
	for _, row := range profiles {
		rowsToWrite = append(rowsToWrite, *row)
	}
	if _, err := tx.Exec(ctx, `
		create temporary table profiles_stage (
			site_id integer not null,
			external_id text not null,
			traits jsonb not null,
			first_seen_at timestamptz not null,
			last_seen_at timestamptz not null
		) on commit drop`); err != nil {
		return err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"profiles_stage"}, profileColumns, &profileSource{profiles: rowsToWrite}); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		insert into profiles(site_id, external_id, traits, first_seen_at, last_seen_at)
		select site_id, external_id, traits, first_seen_at, last_seen_at
		from profiles_stage
		where true
		on conflict (site_id, external_id) do update set
			traits = case
				when (select count(*) from jsonb_object_keys(profiles.traits || excluded.traits)) <= 20
				then profiles.traits || excluded.traits
				else excluded.traits
			end,
			first_seen_at = least(profiles.first_seen_at, excluded.first_seen_at),
			last_seen_at = greatest(profiles.last_seen_at, excluded.last_seen_at)
		returning id, site_id, external_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := make(map[profileKey]int64, len(profiles))
	for rows.Next() {
		var key profileKey
		var id int64
		if err := rows.Scan(&id, &key.siteID, &key.externalID); err != nil {
			return err
		}
		ids[key] = id
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range events {
		if events[i].ProfileExternalID != "" {
			key := profileKey{siteID: events[i].SiteID, externalID: events[i].ProfileExternalID}
			id, ok := ids[key]
			if !ok {
				return fmt.Errorf("writer: profile upsert returned no identifier")
			}
			events[i].ProfileID = id
		}
	}
	return nil
}

// MaxProfileTraits caps the stored traits object. Ingest caps one identify
// call at the same size; merges that would exceed it keep the latest call's
// traits so a client cannot grow a profile without bound by rotating keys.
// The upsert SQL repeats the value.
const MaxProfileTraits = 20

func mergeTraits(row *profileRow, traits map[string]string) {
	added := 0
	for key := range traits {
		if _, ok := row.traits[key]; !ok {
			added++
		}
	}
	if len(row.traits)+added > MaxProfileTraits {
		clear(row.traits)
	}
	for key, value := range traits {
		row.traits[key] = value
	}
}

var profileColumns = []string{"site_id", "external_id", "traits", "first_seen_at", "last_seen_at"}

type profileSource struct {
	profiles []profileRow
	index    int
	row      [5]any
}

func (s *profileSource) Next() bool {
	s.index++
	return s.index <= len(s.profiles)
}

func (s *profileSource) Err() error { return nil }

func (s *profileSource) Values() ([]any, error) {
	profile := &s.profiles[s.index-1]
	s.row = [5]any{profile.siteID, profile.externalID, profile.traits, profile.firstSeen, profile.lastSeen}
	return s.row[:], nil
}

type siteDay struct {
	site int32
	day  time.Time
}

type eventSource struct {
	events []Event
	index  int
	row    [28]any
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
	s.row = [28]any{
		event.SiteID, event.Retention, event.LocalDay, event.TS, event.VisitorID, event.SessionID, event.Kind,
		event.NameID, event.PathID, event.ReferrerID, event.Source, event.Medium, event.Campaign,
		int32(0), int32(0), event.Country, event.Region, event.City, event.Browser, event.OS, event.Device, event.Screen,
		event.EngagementMS, event.ScrollPct, props,
		event.RevenueMicros, nullableString(event.RevenueCurrency),
		nullableInt64(event.ProfileID),
	}
	return s.row[:], nil
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var sessionColumns = []string{
	"site_id", "retention_class", "session_id", "visitor_id", "start", "last", "local_day",
	"entry_path", "exit_path", "source_id", "medium_id", "campaign_id", "country", "region_id", "city_id", "browser_id",
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
	row       [22]any
}

func (s *sessionSource) Next() bool {
	s.index++
	return s.index <= len(s.snapshots)
}

func (s *sessionSource) Err() error { return nil }

func (s *sessionSource) Values() ([]any, error) {
	snapshot := &s.snapshots[s.index-1]
	s.row = [22]any{
		snapshot.SiteID, snapshot.Retention, snapshot.SessionID, snapshot.VisitorID,
		snapshot.Start, snapshot.Last, snapshot.LocalDay, snapshot.Entry, snapshot.Exit,
		snapshot.Source, snapshot.Medium, snapshot.Campaign, snapshot.Country, snapshot.Region, snapshot.City,
		snapshot.Browser, snapshot.OS, snapshot.Device, snapshot.Pageviews,
		snapshot.Events, snapshot.EngagementMS, snapshot.Bounce,
	}
	return s.row[:], nil
}
