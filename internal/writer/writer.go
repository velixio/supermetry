// Package writer persists accepted analytics events and changed sessions in
// bounded batches. Its queue accounts for all uncommitted events, including a
// batch currently being retried, so database outages apply prompt backpressure.
package writer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
)

const (
	defaultQueueSize = 1 << 16
	batchSize        = 5_000
	sessionBatchSize = 20_000
	shutdownTimeout  = 10 * time.Second
	initialBackoff   = 100 * time.Millisecond
	maximumBackoff   = 5 * time.Second
	dayCloseLockID   = 7469002
)

// Dropped counts events rejected because the writer has no remaining
// capacity. Written counts events committed to PostgreSQL.
var Dropped, Written atomic.Int64

// Clock supplies the writer's view of current time.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// Event is the durable representation of an accepted analytics hit. It must
// never contain an IP address or a full user-agent string.
type Event struct {
	session.Hit
	SessionID            int64
	PathSanitizerVersion int16
	PathPolicyID         string
	ProfileExternalID    string
	ProfileTraits        map[string]string
	ProfileID            int64
	Props                map[string]string
	Screen               int16
	ReferrerID           int32
	UTMContent           int32
	UTMTerm              int32
	RevenueMicros        int64
	RevenueCurrency      string
}

// EventReservation owns one bounded writer slot. Call Commit or Cancel exactly
// once. The writer tracks unresolved reservations separately from its state
// lock so enrichment and database calls do not block unrelated state changes.
type EventReservation struct {
	w    *Writer
	once sync.Once
}

// Writer owns the bounded event queue and the database batch loops.
type Writer struct {
	ch           chan Event
	slots        chan struct{}
	heatmapCh    chan HeatmapEvent
	heatmapSlots chan struct{}
	sess         session.Store

	flushEvery        time.Duration
	sessionFlush      time.Duration
	profileTombstones ProfileTombstones

	trackingMu     sync.RWMutex
	trackingMode   privacy.Mode
	trackingSites  sites.Registry
	pathPolicy     privacy.PathPolicy
	pathDictionary *dict.Dict

	poolMu sync.RWMutex
	pool   *pgxpool.Pool

	clockMu sync.RWMutex
	clock   Clock

	stateMu   sync.RWMutex
	accepting bool
	inflight  sync.WaitGroup
	started   atomic.Bool
}

// New constructs a writer. Run must be called exactly once to consume queued
// work. A negative queue size uses the production default; zero deliberately
// creates an always-full writer, which is useful for disabling intake.
func New(pool *pgxpool.Pool, sess session.Store, size int, flushEvery, sessionFlush time.Duration) *Writer {
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
		pool:         pool,
		clock:        wallClock{},
		accepting:    true,
	}
}

// RejectEventIfUnavailable rejects known-full or stopped intake without reserving
// capacity. Slots include queued, in-flight and retrying events. A false result
// is only a hint: callers must still use Enqueue as the final capacity check.
// A true result accounts for exactly one dropped event; do not enqueue it again.
func (w *Writer) RejectEventIfUnavailable() bool {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	if !w.accepting || len(w.slots) == cap(w.slots) {
		Dropped.Add(1)
		return true
	}
	return false
}

// ReserveEvent claims capacity before callers mutate state derived from an
// event. A successful reservation guarantees that Commit can enqueue without
// a competing producer taking its slot. Callers must defer Cancel immediately;
// Commit consumes the reservation and makes the deferred Cancel a no-op.
func (w *Writer) ReserveEvent() (*EventReservation, bool) {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	if !w.accepting {
		Dropped.Add(1)
		return nil, false
	}
	select {
	case w.slots <- struct{}{}:
		w.inflight.Add(1)
		return &EventReservation{w: w}, true
	default:
		Dropped.Add(1)
		return nil, false
	}
}

// Enqueue transfers ownership of e to the writer. It never blocks and returns
// false when all capacity is occupied by queued or not-yet-committed events.
func (w *Writer) Enqueue(e Event) bool {
	reservation, ok := w.ReserveEvent()
	if !ok {
		return false
	}
	return reservation.Commit(e)
}

// Commit transfers ownership of e to the writer and consumes the reservation.
// It returns false if this reservation was already committed or cancelled.
func (r *EventReservation) Commit(e Event) bool {
	if r == nil || r.w == nil {
		return false
	}
	committed := false
	r.once.Do(func() {
		w := r.w
		defer w.inflight.Done()
		copyEventMaps(&e)
		// ReserveEvent guarantees queue capacity. A blocking send avoids a
		// lossy fallback if that invariant is accidentally weakened later.
		w.ch <- e
		committed = true
	})
	return committed
}

// Cancel releases an unused reservation. It is safe to defer and becomes a
// no-op after Commit or an earlier Cancel.
func (r *EventReservation) Cancel() {
	if r == nil || r.w == nil {
		return
	}
	r.once.Do(func() {
		<-r.w.slots
		r.w.inflight.Done()
	})
}

func copyEventMaps(e *Event) {
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
}

// SetPool replaces the destination pool. It exists to exercise outage
// recovery without rebuilding the writer; production wiring sets the pool in
// New and leaves it unchanged.
func (w *Writer) SetPool(pool *pgxpool.Pool) {
	w.poolMu.Lock()
	w.pool = pool
	w.poolMu.Unlock()
}

// SetClock replaces the writer clock. Configure it before Run.
func (w *Writer) SetClock(clock Clock) {
	if clock == nil {
		clock = wallClock{}
	}
	w.clockMu.Lock()
	w.clock = clock
	w.clockMu.Unlock()
}

func (w *Writer) now() time.Time {
	w.clockMu.RLock()
	clock := w.clock
	w.clockMu.RUnlock()
	if clock == nil {
		return time.Now()
	}
	return clock.Now()
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
	w.inflight.Wait()
}

func (w *Writer) retryEvents(waitCtx, attemptCtx context.Context, batch []Event, write func(context.Context, []Event) error) error {
	return retryOperation(waitCtx, attemptCtx, func(ctx context.Context) error {
		w.suppressRestrictedIdentity(batch)
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
	w.stopAccepting()
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
	"name_id", "path_id", "referrer_id", "source_id", "utm_source_id", "medium_id", "campaign_id",
	"utm_content_id", "utm_term_id", "country", "region_id", "city_id", "browser_id", "os_id",
	"browser_ver_id", "os_ver_id", "language_id", "hostname_id", "device", "screen",
	"engagement_ms", "scroll_pct", "props",
	"revenue_micros", "revenue_currency",
	"profile_id", "path_sanitizer_version", "path_policy_id",
}

func (w *Writer) copyEvents(ctx context.Context, events []Event) error {
	pool := w.currentPool()
	if pool == nil {
		return errors.New("writer: database pool is nil")
	}
	w.suppressRestrictedIdentity(events)
	w.suppressTombstonedProfiles(events)
	lateDays := w.olderLocalDays(events, w.now())
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if len(lateDays) != 0 {
			if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock_shared($1)`, dayCloseLockID); err != nil {
				return fmt.Errorf("lock day close for late events: %w", err)
			}
		}
		if err := upsertProfiles(ctx, tx, events); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"events"}, eventColumns, &eventSource{events: events}); err != nil {
			return err
		}
		if len(lateDays) != 0 {
			siteIDs := make([]int32, len(lateDays))
			days := make([]time.Time, len(lateDays))
			for i, key := range lateDays {
				siteIDs[i], days[i] = key.site, key.day
			}
			if _, err := tx.Exec(ctx, `
insert into dirty_day(site_id, day, reason)
select distinct late.site_id, late.day, 'late_event'
from unnest($1::integer[], $2::date[]) as late(site_id, day)
where exists (
  select 1 from day_close closed
  where closed.site_id=late.site_id and closed.day=late.day and closed.status='done'
)
on conflict (site_id, day) do nothing`, siteIDs, days); err != nil {
				return fmt.Errorf("mark closed days dirty: %w", err)
			}
		}
		return nil
	})
}

func (w *Writer) olderLocalDays(events []Event, now time.Time) []siteDay {
	w.trackingMu.RLock()
	registry := w.trackingSites
	w.trackingMu.RUnlock()
	unique := make(map[siteDay]struct{})
	for _, event := range events {
		if event.LocalDay.IsZero() {
			continue
		}
		if registry == nil {
			unique[siteDay{site: event.SiteID, day: event.LocalDay}] = struct{}{}
			continue
		}
		site, ok := registry.Site(event.SiteID)
		if !ok || site.TZ == nil {
			// The event's LocalDay was derived while the site was known. If the
			// registry entry is unavailable at flush time, conservatively check
			// that day under the close lock instead of assuming UTC.
			unique[siteDay{site: event.SiteID, day: event.LocalDay}] = struct{}{}
			continue
		}
		localNow := now.In(site.TZ)
		currentDay := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
		if event.LocalDay.Before(currentDay) {
			unique[siteDay{site: event.SiteID, day: event.LocalDay}] = struct{}{}
		}
	}
	result := make([]siteDay, 0, len(unique))
	for key := range unique {
		result = append(result, key)
	}
	return result
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
			external_hash bytea not null,
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
		select staged.site_id, staged.external_id, staged.traits, staged.first_seen_at, staged.last_seen_at
		from profiles_stage staged
		where not exists (
			select 1 from profile_tombstones tombstone
			where tombstone.site_id=staged.site_id
				and tombstone.external_hash=staged.external_hash
		)
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
				events[i].ProfileExternalID = ""
				events[i].ProfileTraits = nil
				events[i].ProfileID = 0
				continue
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

var profileColumns = []string{"site_id", "external_id", "external_hash", "traits", "first_seen_at", "last_seen_at"}

type profileSource struct {
	profiles []profileRow
	index    int
	row      [6]any
}

func (s *profileSource) Next() bool {
	s.index++
	return s.index <= len(s.profiles)
}

func (s *profileSource) Err() error { return nil }

func (s *profileSource) Values() ([]any, error) {
	profile := &s.profiles[s.index-1]
	externalHash := sha256.Sum256([]byte(profile.externalID))
	s.row = [6]any{profile.siteID, profile.externalID, externalHash[:], profile.traits, profile.firstSeen, profile.lastSeen}
	return s.row[:], nil
}

type siteDay struct {
	site int32
	day  time.Time
}

type eventSource struct {
	events []Event
	index  int
	row    [35]any
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
	s.row = [35]any{
		event.SiteID, event.Retention, event.LocalDay, event.TS, event.VisitorID, event.SessionID, event.Kind,
		event.NameID, event.PathID, event.ReferrerID, event.Source, event.UTMSource, event.Medium, event.Campaign,
		event.UTMContent, event.UTMTerm, event.Country, event.Region, event.City, event.Browser, event.OS,
		event.BrowserVer, event.OSVer, event.Language, event.Hostname, event.Device, event.Screen,
		event.EngagementMS, event.ScrollPct, props,
		event.RevenueMicros, nullableString(event.RevenueCurrency),
		nullableInt64(event.ProfileID), event.PathSanitizerVersion, event.PathPolicyID,
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
	"entry_path", "exit_path", "source_id", "utm_source_id", "medium_id", "campaign_id", "country", "region_id", "city_id", "browser_id",
	"os_id", "browser_ver_id", "os_ver_id", "language_id", "hostname_id", "screen_bucket",
	"device", "pageviews", "events", "engagement_ms", "bounce",
}

func (w *Writer) upsertSessions(ctx context.Context, snapshots []session.Snapshot) error {
	w.suppressRestrictedSessions(snapshots)
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
	row       [28]any
}

func (s *sessionSource) Next() bool {
	s.index++
	return s.index <= len(s.snapshots)
}

func (s *sessionSource) Err() error { return nil }

func (s *sessionSource) Values() ([]any, error) {
	snapshot := &s.snapshots[s.index-1]
	s.row = [28]any{
		snapshot.SiteID, snapshot.Retention, snapshot.SessionID, snapshot.VisitorID,
		snapshot.Start, snapshot.Last, snapshot.LocalDay, snapshot.Entry, snapshot.Exit,
		snapshot.Source, snapshot.UTMSource, snapshot.Medium, snapshot.Campaign, snapshot.Country, snapshot.Region, snapshot.City,
		snapshot.Browser, snapshot.OS, snapshot.BrowserVer, snapshot.OSVer, snapshot.Language, snapshot.Hostname,
		snapshot.ScreenBucket, snapshot.Device, snapshot.Pageviews,
		snapshot.Events, snapshot.EngagementMS, snapshot.Bounce,
	}
	return s.row[:], nil
}
