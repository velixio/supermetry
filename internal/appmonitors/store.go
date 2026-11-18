package appmonitors

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
)

// Errors reported by Record.
var (
	// ErrUnknownMonitor: the slug is unknown and the check-in carried no config.
	ErrUnknownMonitor = errors.New("unknown monitor")
	// ErrInvalidConfig: the monitor_config cannot be used.
	ErrInvalidConfig = errors.New("invalid monitor config")
	// ErrMonitorLimit: the site already has MaxMonitorsPerSite monitors.
	ErrMonitorLimit = errors.New("monitor limit reached")
	// ErrNotFound: no such monitor on the site.
	ErrNotFound = errors.New("monitor not found")
)

// Monitor is one cron monitor row.
type Monitor struct {
	ID                int64      `json:"id"`
	SiteID            int32      `json:"-"`
	Slug              string     `json:"slug"`
	Environment       string     `json:"environment"`
	ScheduleType      string     `json:"schedule_type"`
	Schedule          string     `json:"schedule"`
	TZ                string     `json:"timezone"`
	MarginMinutes     int        `json:"checkin_margin_minutes"`
	MaxRuntimeMinutes int        `json:"max_runtime_minutes"`
	FailureThreshold  int        `json:"failure_threshold"`
	RecoveryThreshold int        `json:"recovery_threshold"`
	Status            string     `json:"status"`
	Failures          int        `json:"-"`
	OKs               int        `json:"-"`
	NextExpectedAt    *time.Time `json:"next_expected_at"`
	LastCheckinAt     *time.Time `json:"last_checkin_at"`
}

const monitorColumns = `id, site_id, slug, environment, schedule_type, schedule, tz, checkin_margin_minutes, max_runtime_minutes,
  failure_threshold, recovery_threshold, status, consecutive_failures, consecutive_ok, next_expected_at, last_checkin_at`

func scanMonitor(row pgx.Row) (Monitor, error) {
	var m Monitor
	err := row.Scan(&m.ID, &m.SiteID, &m.Slug, &m.Environment, &m.ScheduleType, &m.Schedule, &m.TZ, &m.MarginMinutes, &m.MaxRuntimeMinutes,
		&m.FailureThreshold, &m.RecoveryThreshold, &m.Status, &m.Failures, &m.OKs, &m.NextExpectedAt, &m.LastCheckinAt)
	return m, err
}

func (m Monitor) schedule() (Schedule, error) { return ParseSchedule(m.ScheduleType, m.Schedule, m.TZ) }
func (m Monitor) state() State                { return State{Status: m.Status, Failures: m.Failures, OKs: m.OKs} }
func (m Monitor) limits() Limits {
	return Limits{FailureThreshold: m.FailureThreshold, RecoveryThreshold: m.RecoveryThreshold}
}
func (m Monitor) margin() time.Duration { return time.Duration(m.MarginMinutes) * time.Minute }

// Store reads and writes monitors and check-ins.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time

	mu         sync.Mutex
	partitions map[time.Time]struct{}
}

// NewStore builds a store; now defaults to time.Now.
func NewStore(pool *pgxpool.Pool, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{pool: pool, now: now, partitions: map[time.Time]struct{}{}}
}

func (s *Store) ensurePartition(ctx context.Context, at time.Time) error {
	t := at.UTC()
	month := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	s.mu.Lock()
	_, done := s.partitions[month]
	s.mu.Unlock()
	if done {
		return nil
	}
	if err := db.EnsureAppEventPartitions(ctx, s.pool, month, month.AddDate(0, 1, 0)); err != nil {
		return err
	}
	s.mu.Lock()
	s.partitions[month] = struct{}{}
	s.mu.Unlock()
	return nil
}

func (s *Store) forgetPartitions() {
	s.mu.Lock()
	clear(s.partitions) // retention may have dropped one
	s.mu.Unlock()
}

func clampMinutes(v, def int) int {
	if v <= 0 {
		return def
	}
	return min(v, 1440)
}

// loadOrCreate locks the monitor of a check-in, creating it from the config
// when it does not exist, and applies a changed config to an existing one.
func (s *Store) loadOrCreate(ctx context.Context, tx pgx.Tx, ci CheckIn) (Monitor, error) {
	m, err := scanMonitor(tx.QueryRow(ctx, `select `+monitorColumns+` from app_monitors where site_id=$1 and slug=$2 and environment=$3 for update`,
		ci.SiteID, ci.Slug, ci.Environment))
	if errors.Is(err, pgx.ErrNoRows) {
		return s.create(ctx, tx, ci)
	}
	if err != nil {
		return Monitor{}, err
	}
	if ci.Config == nil {
		return m, nil
	}
	c := *ci.Config
	typ, value, tz := m.ScheduleType, m.Schedule, m.TZ
	if c.ScheduleType != "" && c.Schedule != "" {
		typ, value = c.ScheduleType, c.Schedule
	}
	if c.TZ != "" {
		tz = c.TZ
	}
	sched, err := ParseSchedule(typ, value, tz)
	if err != nil {
		return Monitor{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	changed := typ != m.ScheduleType || sched.Value != m.Schedule || tz != m.TZ
	m.ScheduleType, m.Schedule, m.TZ = typ, sched.Value, tz
	m.MarginMinutes = clampMinutes(c.MarginMinutes, m.MarginMinutes)
	m.MaxRuntimeMinutes = clampMinutes(c.MaxRuntimeMinutes, m.MaxRuntimeMinutes)
	m.FailureThreshold = min(pick(c.FailureThreshold, m.FailureThreshold), 100)
	m.RecoveryThreshold = min(pick(c.RecoveryThreshold, m.RecoveryThreshold), 100)
	if changed && m.NextExpectedAt != nil && m.Status != StatusDisabled {
		next := sched.After(ci.At)
		m.NextExpectedAt = &next
	}
	_, err = tx.Exec(ctx, `update app_monitors set schedule_type=$2, schedule=$3, tz=$4, checkin_margin_minutes=$5, max_runtime_minutes=$6,
  failure_threshold=$7, recovery_threshold=$8, next_expected_at=$9, updated_at=$10 where id=$1`,
		m.ID, m.ScheduleType, m.Schedule, m.TZ, m.MarginMinutes, m.MaxRuntimeMinutes, m.FailureThreshold, m.RecoveryThreshold, m.NextExpectedAt, ci.At)
	return m, err
}

// pick returns v when it is set (positive), else the current value.
func pick(v, current int) int {
	if v > 0 {
		return v
	}
	return current
}

func (s *Store) create(ctx context.Context, tx pgx.Tx, ci CheckIn) (Monitor, error) {
	if ci.Config == nil {
		return Monitor{}, ErrUnknownMonitor
	}
	c := *ci.Config
	sched, err := ParseSchedule(c.ScheduleType, c.Schedule, c.TZ)
	if err != nil {
		return Monitor{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	var count int
	if err := tx.QueryRow(ctx, `select count(*) from app_monitors where site_id=$1`, ci.SiteID).Scan(&count); err != nil {
		return Monitor{}, err
	}
	if count >= MaxMonitorsPerSite {
		return Monitor{}, ErrMonitorLimit
	}
	m, err := scanMonitor(tx.QueryRow(ctx, `insert into app_monitors (site_id, slug, environment, schedule_type, schedule, tz,
  checkin_margin_minutes, max_runtime_minutes, failure_threshold, recovery_threshold, created_at, updated_at)
  values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11) returning `+monitorColumns,
		ci.SiteID, ci.Slug, ci.Environment, sched.Type, sched.Value, sched.TZ,
		clampMinutes(c.MarginMinutes, 1), clampMinutes(c.MaxRuntimeMinutes, 30),
		min(max(c.FailureThreshold, 1), 100), min(max(c.RecoveryThreshold, 1), 100), ci.At))
	return m, err
}

// Record stores one check-in and advances its monitor, in one transaction. A
// check-in carrying the id of an in-progress, timed-out or missed row completes
// that row; a repeat of a final check-in is ignored.
func (s *Store) Record(ctx context.Context, ci CheckIn) error {
	if err := s.ensurePartition(ctx, ci.At); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	m, err := s.loadOrCreate(ctx, tx, ci)
	if err != nil {
		return err
	}
	if err := s.apply(ctx, tx, m, ci); err != nil {
		s.forgetPartitions()
		return err
	}
	return tx.Commit(ctx)
}

type checkinRow struct {
	ts      time.Time
	id      string
	status  string
	expects *time.Time
}

func (s *Store) apply(ctx context.Context, tx pgx.Tx, m Monitor, ci CheckIn) error {
	sched, err := m.schedule()
	if err != nil {
		return err
	}
	lookback := ci.At.Add(-48 * time.Hour)

	// The same check_in_id again: an in-progress, timed-out or missed row is completed.
	var row checkinRow
	err = tx.QueryRow(ctx, `select ts, checkin_id::text, status, expected_at from app_checkins
  where site_id=$1 and checkin_id=$2 and ts >= $3 order by ts desc limit 1`, ci.SiteID, ci.ID, lookback).
		Scan(&row.ts, &row.id, &row.status, &row.expects)
	switch {
	case err == nil:
		return s.complete(ctx, tx, m, ci, row)
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}

	// A fresh check-in that is far earlier than the next slot while the last
	// row is a missed one is that slot arriving late.
	var last checkinRow
	err = tx.QueryRow(ctx, `select ts, checkin_id::text, status, expected_at from app_checkins
  where monitor_id=$1 and ts >= $2 order by ts desc limit 1`, m.ID, lookback).Scan(&last.ts, &last.id, &last.status, &last.expects)
	switch {
	case err == nil:
		if last.status == StatusMissed && m.NextExpectedAt != nil && ci.At.Before(m.NextExpectedAt.Add(-m.margin())) {
			return s.completeLate(ctx, tx, m, sched, ci, last)
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}

	state, outcome := m.state(), OutcomeNone
	if ci.Status != StatusInProgress {
		state, outcome = Transition(state, m.limits(), ci.Status)
	}
	if _, err := tx.Exec(ctx, `insert into app_checkins (site_id, monitor_id, ts, checkin_id, status, expected_at, duration_ms, release, failure_status, recovery_at)
  values ($1,$2,$3,$4,$5,$6,$7,nullif($8,''),$9,$10) on conflict do nothing`,
		ci.SiteID, m.ID, ci.At, ci.ID, ci.Status, m.NextExpectedAt, ci.DurationMS, ci.Release,
		failureStatus(outcome, ci.Status), recoveryAt(outcome, ci.At)); err != nil {
		return err
	}
	base := ci.At
	if m.NextExpectedAt != nil && m.NextExpectedAt.After(base) {
		base = *m.NextExpectedAt
	}
	if sched.Type == ScheduleInterval {
		base = ci.At
	}
	next := sched.After(base)
	return s.saveMonitor(ctx, tx, m, state, &next, ci.At)
}

func (s *Store) saveMonitor(ctx context.Context, tx pgx.Tx, m Monitor, st State, next *time.Time, lastCheckin time.Time) error {
	_, err := tx.Exec(ctx, `update app_monitors set status=$2, consecutive_failures=$3, consecutive_ok=$4,
  next_expected_at=coalesce($5, next_expected_at), last_checkin_at=greatest(last_checkin_at, $6), updated_at=$6 where id=$1`,
		m.ID, st.Status, st.Failures, st.OKs, next, lastCheckin)
	return err
}

// complete finishes the row a repeated check_in_id points at.
func (s *Store) complete(ctx context.Context, tx pgx.Tx, m Monitor, ci CheckIn, row checkinRow) error {
	if ci.Status == StatusInProgress || (row.status != StatusInProgress && row.status != StatusTimeout) {
		return nil // a duplicate, or the row is already final
	}
	state, outcome := m.state(), OutcomeNone
	switch {
	case ci.Status == StatusOK:
		state, outcome = Transition(state, m.limits(), StatusOK)
	case row.status == StatusInProgress: // error after in_progress: the first failure of this run
		state, outcome = Transition(state, m.limits(), ci.Status)
	}
	if _, err := tx.Exec(ctx, `update app_checkins set status=$4, duration_ms=coalesce($5, duration_ms), release=coalesce(nullif($6,''), release),
  failure_status = coalesce(failure_status, $7), recovery_at = coalesce(recovery_at, $8)
  where site_id=$1 and checkin_id=$2 and ts=$3`,
		ci.SiteID, row.id, row.ts, ci.Status, ci.DurationMS, ci.Release, failureStatus(outcome, ci.Status), recoveryAt(outcome, ci.At)); err != nil {
		return err
	}
	return s.saveMonitor(ctx, tx, m, state, nil, ci.At)
}

// completeLate turns the latest missed row into the check-in that arrived late.
// It counts toward recovery but never raises a second alert for the slot.
func (s *Store) completeLate(ctx context.Context, tx pgx.Tx, m Monitor, sched Schedule, ci CheckIn, row checkinRow) error {
	state, outcome := m.state(), OutcomeNone
	if ci.Status == StatusOK {
		state, outcome = Transition(state, m.limits(), StatusOK)
	}
	if _, err := tx.Exec(ctx, `update app_checkins set status=$4, checkin_id=$5, duration_ms=$6, release=nullif($7,''),
  recovery_at = coalesce(recovery_at, $8)
  where site_id=$1 and checkin_id=$2 and ts=$3`,
		ci.SiteID, row.id, row.ts, ci.Status, ci.ID, ci.DurationMS, ci.Release, recoveryAt(outcome, ci.At)); err != nil {
		return err
	}
	var next *time.Time
	if sched.Type == ScheduleInterval {
		n := sched.After(ci.At)
		next = &n
	}
	return s.saveMonitor(ctx, tx, m, state, next, ci.At)
}

// Sweep writes the missed and timed-out check-ins due at now. It returns how
// many of each. A monitor that errors is skipped and reported after the rest.
func (s *Store) Sweep(ctx context.Context, now time.Time) (missed, timeouts int, err error) {
	if err := s.ensurePartition(ctx, now); err != nil {
		return 0, 0, err
	}
	var errs []error
	timeoutRows, err := s.pool.Query(ctx, `select c.site_id, c.checkin_id::text, c.ts, c.monitor_id from app_checkins c
  join app_monitors m on m.id = c.monitor_id
  where c.status='in_progress' and c.ts >= $1 and c.ts + m.max_runtime_minutes * interval '1 minute' < $2 and m.status <> 'disabled'
  order by c.ts limit 500`, now.Add(-49*time.Hour), now)
	if err != nil {
		return 0, 0, fmt.Errorf("list timeouts: %w", err)
	}
	type timeoutRef struct {
		site    int32
		id      string
		ts      time.Time
		monitor int64
	}
	var refs []timeoutRef
	for timeoutRows.Next() {
		var r timeoutRef
		if err := timeoutRows.Scan(&r.site, &r.id, &r.ts, &r.monitor); err != nil {
			timeoutRows.Close()
			return 0, 0, err
		}
		refs = append(refs, r)
	}
	timeoutRows.Close()
	if err := timeoutRows.Err(); err != nil {
		return 0, 0, err
	}
	for _, r := range refs {
		done, err := s.timeoutOne(ctx, r.site, r.id, r.ts, r.monitor, now)
		if err != nil {
			errs = append(errs, err)
		}
		if done {
			timeouts++
		}
	}

	dueRows, err := s.pool.Query(ctx, `select id from app_monitors
  where status <> 'disabled' and next_expected_at is not null and next_expected_at + checkin_margin_minutes * interval '1 minute' < $1
  order by next_expected_at limit 500`, now)
	if err != nil {
		return missed, timeouts, fmt.Errorf("list due monitors: %w", err)
	}
	var ids []int64
	for dueRows.Next() {
		var id int64
		if err := dueRows.Scan(&id); err != nil {
			dueRows.Close()
			return missed, timeouts, err
		}
		ids = append(ids, id)
	}
	dueRows.Close()
	if err := dueRows.Err(); err != nil {
		return missed, timeouts, err
	}
	for _, id := range ids {
		done, err := s.missOne(ctx, id, now)
		if err != nil {
			errs = append(errs, err)
		}
		if done {
			missed++
		}
	}
	return missed, timeouts, errors.Join(errs...)
}

func (s *Store) timeoutOne(ctx context.Context, siteID int32, id string, ts time.Time, monitorID int64, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	m, err := scanMonitor(tx.QueryRow(ctx, `select `+monitorColumns+` from app_monitors where id=$1 for update`, monitorID))
	if err != nil {
		return false, err
	}
	var status string
	if err := tx.QueryRow(ctx, `select status from app_checkins where site_id=$1 and checkin_id=$2 and ts=$3 for update`, siteID, id, ts).Scan(&status); err != nil {
		return false, err
	}
	if status != StatusInProgress { // completed since the list
		return false, nil
	}
	state, outcome := Transition(m.state(), m.limits(), StatusTimeout)
	if _, err := tx.Exec(ctx, `update app_checkins set status='timeout', failure_status=$4 where site_id=$1 and checkin_id=$2 and ts=$3`,
		siteID, id, ts, failureStatus(outcome, StatusTimeout)); err != nil {
		return false, err
	}
	if err := s.saveMonitor(ctx, tx, m, state, nil, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) missOne(ctx context.Context, id int64, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	m, err := scanMonitor(tx.QueryRow(ctx, `select `+monitorColumns+` from app_monitors where id=$1 for update skip locked`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // locked by a check-in, or gone
	}
	if err != nil {
		return false, err
	}
	if m.Status == StatusDisabled || m.NextExpectedAt == nil || !m.NextExpectedAt.Add(m.margin()).Before(now) {
		return false, nil // a check-in arrived since the list
	}
	sched, err := m.schedule()
	if err != nil {
		return false, err
	}
	slot := *m.NextExpectedAt
	next := sched.After(slot)
	if !next.After(now) {
		next = sched.After(now) // long outage: one miss, then back on schedule
	}
	state, outcome := Transition(m.state(), m.limits(), StatusMissed)
	if _, err := tx.Exec(ctx, `insert into app_checkins (site_id, monitor_id, ts, checkin_id, status, expected_at, failure_status)
  values ($1,$2,$3,$4,'missed',$5,$6)`, m.SiteID, m.ID, now, NewUUID(), slot, failureStatus(outcome, StatusMissed)); err != nil {
		return false, err
	}
	if err := s.saveMonitor(ctx, tx, m, state, &next, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// AlertEvent is a threshold crossing the alert-rules job turns into a
// notification: a monitor that started failing, or recovered.
type AlertEvent struct {
	MonitorID   int64
	Slug        string
	Environment string
	Status      string // the check-in status: error, missed or timeout for a failure
	Recovery    bool
	At          time.Time
	ExpectedAt  *time.Time
	Release     string
}

func failureStatus(o Outcome, status string) *string {
	if o == OutcomeFailure {
		return &status
	}
	return nil
}

func recoveryAt(o Outcome, at time.Time) *time.Time {
	if o == OutcomeRecovery {
		return &at
	}
	return nil
}

// AlertEvents lists the crossings of a site's monitors since a time, oldest
// first: one failure event per row that opened a failure and one recovery
// event per row that closed it (a late ok yields both from one row).
func (s *Store) AlertEvents(ctx context.Context, siteID int32, since time.Time, limit int) ([]AlertEvent, error) {
	rows, err := s.pool.Query(ctx, `
select monitor_id, slug, environment, status, recovery, at, expected_at, release from (
  select c.monitor_id, m.slug, m.environment, c.failure_status as status, false as recovery, c.ts as at, c.expected_at, coalesce(c.release,'') as release
  from app_checkins c join app_monitors m on m.id = c.monitor_id
  where c.site_id=$1 and c.ts >= $2 and c.failure_status is not null
  union all
  select c.monitor_id, m.slug, m.environment, c.status, true, c.recovery_at, c.expected_at, coalesce(c.release,'')
  from app_checkins c join app_monitors m on m.id = c.monitor_id
  where c.site_id=$1 and c.ts >= $2 and c.recovery_at is not null
) e order by at, recovery limit $3`, siteID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertEvent
	for rows.Next() {
		var e AlertEvent
		if err := rows.Scan(&e.MonitorID, &e.Slug, &e.Environment, &e.Status, &e.Recovery, &e.At, &e.ExpectedAt, &e.Release); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListMonitors returns the site's monitors by slug.
func (s *Store) ListMonitors(ctx context.Context, siteID int32) ([]Monitor, error) {
	rows, err := s.pool.Query(ctx, `select `+monitorColumns+` from app_monitors where site_id=$1 order by slug, environment`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Checkin is one check-in row for the API.
type Checkin struct {
	ID         string     `json:"checkin_id"`
	At         time.Time  `json:"timestamp"`
	Status     string     `json:"status"`
	ExpectedAt *time.Time `json:"expected_at"`
	DurationMS *int64     `json:"duration_ms"`
	Release    string     `json:"release"`
}

// ListCheckins returns a monitor's newest check-ins.
func (s *Store) ListCheckins(ctx context.Context, siteID int32, monitorID int64, limit int) ([]Checkin, error) {
	limit = min(max(limit, 1), 100)
	var exists bool
	if err := s.pool.QueryRow(ctx, `select exists(select 1 from app_monitors where id=$1 and site_id=$2)`, monitorID, siteID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `select checkin_id::text, ts, status, expected_at, duration_ms, coalesce(release,'')
  from app_checkins where site_id=$1 and monitor_id=$2 order by ts desc limit $3`, siteID, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Checkin{}
	for rows.Next() {
		var c Checkin
		if err := rows.Scan(&c.ID, &c.At, &c.Status, &c.ExpectedAt, &c.DurationMS, &c.Release); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Patch changes the operator-editable fields of a monitor; nil leaves a field.
type Patch struct {
	Disabled          *bool
	MarginMinutes     *int
	MaxRuntimeMinutes *int
}

// Update applies a patch. Re-enabling restarts the monitor from ok with the
// next slot computed from now.
func (s *Store) Update(ctx context.Context, siteID int32, id int64, p Patch) (Monitor, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Monitor{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	m, err := scanMonitor(tx.QueryRow(ctx, `select `+monitorColumns+` from app_monitors where id=$1 and site_id=$2 for update`, id, siteID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, err
	}
	now := s.now()
	if p.MarginMinutes != nil {
		if *p.MarginMinutes < 1 || *p.MarginMinutes > 1440 {
			return Monitor{}, fmt.Errorf("%w: checkin_margin_minutes must be 1-1440", ErrInvalidConfig)
		}
		m.MarginMinutes = *p.MarginMinutes
	}
	if p.MaxRuntimeMinutes != nil {
		if *p.MaxRuntimeMinutes < 1 || *p.MaxRuntimeMinutes > 1440 {
			return Monitor{}, fmt.Errorf("%w: max_runtime_minutes must be 1-1440", ErrInvalidConfig)
		}
		m.MaxRuntimeMinutes = *p.MaxRuntimeMinutes
	}
	if p.Disabled != nil {
		switch {
		case *p.Disabled:
			m.Status = StatusDisabled
		case m.Status == StatusDisabled:
			sched, err := m.schedule()
			if err != nil {
				return Monitor{}, err
			}
			next := sched.After(now)
			m.Status, m.Failures, m.OKs, m.NextExpectedAt = StatusOK, 0, 0, &next
		}
	}
	if _, err := tx.Exec(ctx, `update app_monitors set checkin_margin_minutes=$2, max_runtime_minutes=$3, status=$4, consecutive_failures=$5,
  consecutive_ok=$6, next_expected_at=$7, updated_at=$8 where id=$1`,
		m.ID, m.MarginMinutes, m.MaxRuntimeMinutes, m.Status, m.Failures, m.OKs, m.NextExpectedAt, now); err != nil {
		return Monitor{}, err
	}
	return m, tx.Commit(ctx)
}

// Delete removes a monitor and its check-ins.
func (s *Store) Delete(ctx context.Context, siteID int32, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	tag, err := tx.Exec(ctx, `delete from app_monitors where id=$1 and site_id=$2`, id, siteID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `delete from app_checkins where site_id=$1 and monitor_id=$2`, siteID, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
