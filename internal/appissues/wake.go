package appissues

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/scheduler"
)

// Escalation rule (default): an archived issue escalates when its events in
// the last hour exceed escalateMinEvents and exceed escalateFactor times its
// hourly average over the seven days before that hour.
const (
	escalateMinEvents = 10
	escalateFactor    = 5
	baselineHours     = 7 * 24
)

// Escalates reports whether last events in the past hour, against prev events
// in the previous seven days, meet the escalation rule.
func Escalates(last, prev int64) bool {
	return last > escalateMinEvents && last*baselineHours > escalateFactor*prev
}

// Waker ends snoozes and escalates archived issues. All changes go through
// Service.Update as the system actor.
type Waker struct {
	pool *pgxpool.Pool
	svc  *Service
	now  func() time.Time
}

// NewWaker returns the wake job body. now is the clock.
func NewWaker(pool *pgxpool.Pool, now func() time.Time) *Waker {
	return &Waker{pool: pool, svc: New(pool, now), now: now}
}

// Job returns the every-minute scheduler entry.
func (w *Waker) Job() scheduler.Job {
	return scheduler.Job{Name: "app-issue-wake", Every: time.Minute, Run: func(ctx context.Context) error {
		_, err := w.Run(ctx)
		return err
	}}
}

type wakeCandidate struct {
	id      int64
	siteID  int32
	status  string
	reason  string
	reasonD map[string]any
}

// Run wakes every due snoozed issue and escalates every archived issue that
// meets the rule, returning how many issues changed.
func (w *Waker) Run(ctx context.Context) (int, error) {
	now := w.now()
	cands, err := w.snoozed(ctx, now)
	if err != nil {
		return 0, err
	}
	esc, err := w.escalating(ctx, now)
	if err != nil {
		return 0, err
	}
	cands = append(cands, esc...)
	woken := 0
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return woken, err
		}
		ok, err := w.apply(ctx, c)
		if err != nil {
			slog.Error("app issue wake failed", "issue", c.id, "err", err)
			continue
		}
		if ok {
			woken++
		}
	}
	return woken, nil
}

// snoozed finds ignored issues whose snooze has ended. Snoozed users are
// counted as the issue's distinct non-null user_hash values now minus the
// count recorded when the snooze began, so events without a user never add one.
func (w *Waker) snoozed(ctx context.Context, now time.Time) ([]wakeCandidate, error) {
	rows, err := w.pool.Query(ctx, `
select i.id, i.site_id,
  case when i.snooze_until is not null and i.snooze_until <= $1 then 'until'
       when i.snooze_events is not null and i.event_count - coalesce(i.snooze_base_events, 0) >= i.snooze_events then 'events'
       else 'users' end
from app_issues i
where i.status = 'ignored'
  and (i.snooze_until is not null or i.snooze_events is not null or i.snooze_users is not null)
  and (
    (i.snooze_until is not null and i.snooze_until <= $1)
    or (i.snooze_events is not null and i.event_count - coalesce(i.snooze_base_events, 0) >= i.snooze_events)
    or (i.snooze_users is not null and (select count(distinct e.user_hash) from app_events e
          where e.site_id = i.site_id and e.issue_id = i.id and e.user_hash is not null) - coalesce(i.snooze_base_users, 0) >= i.snooze_users)
  )
order by i.id`, now)
	if err != nil {
		return nil, fmt.Errorf("find due snoozes: %w", err)
	}
	defer rows.Close()
	var out []wakeCandidate
	for rows.Next() {
		c := wakeCandidate{status: "ignored"}
		if err := rows.Scan(&c.id, &c.siteID, &c.reason); err != nil {
			return nil, err
		}
		c.reasonD = map[string]any{"woke": c.reason}
		out = append(out, c)
	}
	return out, rows.Err()
}

// escalating finds archived issues that meet the escalation rule. The event
// windows are rolling (the hour before now, the seven days before that), read
// from the events themselves rather than the UTC-hour rollups.
func (w *Waker) escalating(ctx context.Context, now time.Time) ([]wakeCandidate, error) {
	hourAgo := now.Add(-time.Hour)
	rows, err := w.pool.Query(ctx, `
select i.id, i.site_id, c.last, c.prev
from app_issues i
cross join lateral (
  select coalesce(sum(e.occurrences) filter (where e.ts >= $1 and e.ts <= $2), 0)::bigint as last,
         coalesce(sum(e.occurrences) filter (where e.ts < $1), 0)::bigint as prev
  from app_events e
  where e.site_id = i.site_id and e.issue_id = i.id and e.ts >= $3 and e.ts <= $2
) c
where i.status = 'archived' and i.archived_until_escalating
order by i.id`, hourAgo, now, hourAgo.Add(-baselineHours*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("find escalating issues: %w", err)
	}
	defer rows.Close()
	var out []wakeCandidate
	for rows.Next() {
		var c wakeCandidate
		var last, prev int64
		if err := rows.Scan(&c.id, &c.siteID, &last, &prev); err != nil {
			return nil, err
		}
		if !Escalates(last, prev) {
			continue
		}
		c.status = "archived"
		c.reasonD = map[string]any{"events_last_hour": last, "events_previous_7d": prev}
		out = append(out, c)
	}
	return out, rows.Err()
}

// apply changes one issue in its own transaction, re-checking under the row
// lock that it is still in the state the candidate was found in, so a change a
// person made in between is never overwritten.
func (w *Waker) apply(ctx context.Context, c wakeCandidate) (bool, error) {
	changed := false
	err := pgx.BeginFunc(ctx, w.pool, func(tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(ctx, `select status from app_issues where id = $1 and site_id = $2 for update`, c.id, c.siteID).Scan(&status)
		if err == pgx.ErrNoRows || (err == nil && status != c.status) {
			return nil
		}
		if err != nil {
			return err
		}
		ch := Change{Reason: c.reasonD}
		if c.status == "archived" {
			f := false
			ch.Archive = &f
		} else {
			open := "open"
			ch.Status = &open
		}
		res, err := w.svc.UpdateTx(ctx, tx, c.siteID, []int64{c.id}, Actor{System: true}, ch)
		changed = err == nil && len(res.Updated) == 1 && len(res.Fields) > 0
		return err
	})
	return changed, err
}
