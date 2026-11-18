package exportschedule

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/platform/mail"
)

// retryAfterFailure is how long a failed schedule waits before trying again.
const retryAfterFailure = 6 * time.Hour

// runSchedule exports every site of a schedule and records the outcome.
func (s *Service) runSchedule(ctx context.Context, id int64) {
	dest, sc, err := s.destination(ctx, id)
	var errs []string
	skipped, done := 0, 0
	if err == nil {
		var client *S3
		client, err = s.newS3(dest)
		if err == nil {
			var siteIDs []int32
			siteIDs, err = s.sitesOf(ctx, sc)
			for _, siteID := range siteIDs {
				status, runErr := s.runSite(ctx, client, sc, siteID)
				switch {
				case runErr != nil:
					errs = append(errs, fmt.Sprintf("site %d: %v", siteID, runErr))
				case status == "skipped":
					skipped++
				default:
					done++
				}
			}
		}
	}
	if err != nil {
		errs = append(errs, err.Error())
	}
	status, message := "succeeded", ""
	switch {
	case len(errs) > 0:
		status, message = "failed", truncate(strings.Join(errs, "; "), 2000)
	case done == 0:
		status = "skipped"
	}
	now := s.now()
	next := NextRun(sc.Frequency, now)
	if status == "failed" {
		next = now.Add(retryAfterFailure)
	}
	if sc.Frequency == "" { // schedule vanished while loading
		return
	}
	if _, execErr := s.pool.Exec(context.WithoutCancel(ctx), `update export_schedules set last_run_at=$2,last_status=$3,last_error=$4,next_run_at=$5 where id=$1`,
		id, now, status, message, next); execErr != nil {
		s.log.Error("record export schedule result", "schedule_id", id, "error", execErr)
	}
	if status == "failed" {
		s.log.Warn("scheduled export failed", "schedule_id", id, "error", message)
		if sc.LastStatus == nil || *sc.LastStatus != "failed" {
			s.notifyFailure(ctx, sc, message)
		}
	}
}

func truncate(v string, n int) string {
	if len(v) <= n {
		return v
	}
	return v[:n]
}

func (s *Service) sitesOf(ctx context.Context, sc Schedule) ([]int32, error) {
	if sc.SiteID != nil {
		return []int32{*sc.SiteID}, nil
	}
	rows, err := s.pool.Query(ctx, `select id from sites where org_id=$1 and deleted_at is null and erased_at is null and not is_demo order by id`, sc.OrgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// runSite exports the days not yet exported (all days on the first run or for
// non-incremental schedules) up to the last closed day in the site's time zone.
func (s *Service) runSite(ctx context.Context, client *S3, sc Schedule, siteID int32) (string, error) {
	var lastClosed, earliest string
	err := s.pool.QueryRow(ctx, `
select (($2::timestamptz at time zone s.timezone)::date - 1)::text,
 least((s.created_at at time zone s.timezone)::date, coalesce((select min(day) from imported_day where site_id=s.id),'infinity'::date))::text
from sites s where s.id=$1`, siteID, s.now()).Scan(&lastClosed, &earliest)
	if err != nil {
		return "", fmt.Errorf("load site: %w", err)
	}
	to, err := time.Parse(time.DateOnly, lastClosed)
	if err != nil {
		return "", err
	}
	from, err := time.Parse(time.DateOnly, earliest)
	if err != nil {
		return "", err
	}
	if sc.Incremental {
		var through time.Time
		err := s.pool.QueryRow(ctx, `select exported_through from export_schedule_progress where schedule_id=$1 and site_id=$2`, sc.ID, siteID).Scan(&through)
		if err == nil {
			from = through.AddDate(0, 0, 1)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	var runID int64
	if from.After(to) {
		_, err := s.pool.Exec(ctx, `insert into export_runs(schedule_id,site_id,status,finished_at,from_day,to_day) values($1,$2,'skipped',now(),null,$3)`,
			sc.ID, siteID, to)
		return "skipped", err
	}
	key := client.Key(fmt.Sprintf("site-%d/supermetry-site-%d-%s-%s.zip", siteID, siteID, from.Format(time.DateOnly), to.Format(time.DateOnly)))
	if err := s.pool.QueryRow(ctx, `insert into export_runs(schedule_id,site_id,status,from_day,to_day,object_key) values($1,$2,'running',$3,$4,$5) returning id`,
		sc.ID, siteID, from, to, key).Scan(&runID); err != nil {
		return "", err
	}
	size, err := s.upload(ctx, client, key, siteID, from, to)
	bg := context.WithoutCancel(ctx)
	if err != nil {
		_, _ = s.pool.Exec(bg, `update export_runs set status='failed',finished_at=now(),error=$2 where id=$1`, runID, truncate(err.Error(), 1000))
		return "failed", err
	}
	tx, err := s.pool.Begin(bg)
	if err != nil {
		return "failed", err
	}
	defer tx.Rollback(bg)
	if _, err := tx.Exec(bg, `update export_runs set status='succeeded',finished_at=now(),bytes=$2 where id=$1`, runID, size); err != nil {
		return "failed", err
	}
	if _, err := tx.Exec(bg, `insert into export_schedule_progress(schedule_id,site_id,exported_through) values($1,$2,$3)
		on conflict (schedule_id,site_id) do update set exported_through=excluded.exported_through`, sc.ID, siteID, to); err != nil {
		return "failed", err
	}
	if err := tx.Commit(bg); err != nil {
		return "failed", err
	}
	return "succeeded", nil
}

// upload streams the site export straight into the object: the ZIP is never
// held in memory or on disk beyond one multipart part.
func (s *Service) upload(ctx context.Context, client *S3, key string, siteID int32, from, to time.Time) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(s.exporter.Write(ctx, pw, siteID, from, to)) }()
	size, err := client.Put(ctx, key, pr)
	pr.CloseWithError(err)
	return size, err
}

// notifyFailure emails the organization's owners and admins once per failing streak.
func (s *Service) notifyFailure(ctx context.Context, sc Schedule, message string) {
	if s.mailer == nil || !s.mailer.Enabled() {
		return
	}
	rows, err := s.pool.Query(ctx, `select u.email from memberships m join users u on u.id=m.user_id
		where m.org_id=$1 and m.role in ('owner','admin') and u.deleted_at is null and u.disabled_at is null and not u.is_system`, sc.OrgID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		if rows.Scan(&email) != nil {
			continue
		}
		_ = s.mailer.Send(ctx, mail.Message{To: email, Subject: "Scheduled export failed: " + sc.Name,
			Text: fmt.Sprintf("The scheduled export %q could not be written to its destination.\n\n%s\n\nIt will be retried automatically. Check the destination settings under Settings, Exports.\n", sc.Name, message)})
	}
}
