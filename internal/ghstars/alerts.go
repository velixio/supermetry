package ghstars

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/notifications"
	"supermetry/internal/scheduler"
)

// Milestones are the verified star counts that raise a star_milestone alert.
var Milestones = []int{100, 250, 500, 1000, 2500, 5000, 10000, 25000, 50000, 100000}

const (
	alertWindow = 6 * time.Hour
	// alertBaselineDays is how far back the usual rate is measured.
	alertBaselineDays = 14
	// alertBaselineSlots is the number of 6 hour slots in the baseline.
	alertBaselineSlots = alertBaselineDays * 24 / 6

	spikeFloor = 10
	surgeFloor = 5
	// alertFactor is how many times the usual rate counts as unusual.
	alertFactor = 3.0
)

// AlertsJob returns the scheduler job that raises star alerts every ten
// minutes. It does nothing when Notifier is nil.
func (s *Service) AlertsJob() scheduler.Job {
	return scheduler.Job{Name: "github-stars-alerts", Every: 10 * time.Minute, Run: s.EvaluateAlerts}
}

type alertRepo struct {
	id, orgID        int64
	repo             string
	verified         *int
	backfillComplete bool
	baseline         *int
}

// EvaluateAlerts checks every enabled repository of every live organization
// and sends milestone, spike and unstar-surge alerts through Notifier. Events
// are scoped by repository row, so organizations never see each other's data.
func (s *Service) EvaluateAlerts(ctx context.Context) error {
	if s.Notifier == nil {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		select r.id, r.org_id, r.repo, r.verified_stars, r.backfill_complete, a.stars
		from github_star_repos r
		join orgs o on o.id = r.org_id and o.deleted_at is null
		left join github_star_alert_state a on a.repo_id = r.id
		where r.enabled order by r.id`)
	if err != nil {
		return fmt.Errorf("list repos for star alerts: %w", err)
	}
	var repos []alertRepo
	for rows.Next() {
		var r alertRepo
		if err := rows.Scan(&r.id, &r.orgID, &r.repo, &r.verified, &r.backfillComplete, &r.baseline); err != nil {
			rows.Close()
			return fmt.Errorf("scan repo for star alerts: %w", err)
		}
		repos = append(repos, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	now := s.Now()
	var errs []error
	for _, r := range repos {
		if err := s.evaluateRepo(ctx, r, now); err != nil {
			slog.Warn("github star alert evaluation failed", "repo_id", r.id, "error", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) evaluateRepo(ctx context.Context, r alertRepo, now time.Time) error {
	if err := s.evaluateMilestones(ctx, r, now); err != nil {
		return err
	}
	// Rates are meaningless while the history import is unfinished.
	if !r.backfillComplete {
		return nil
	}
	return s.evaluateRates(ctx, r, now)
}

// evaluateMilestones compares the verified count with the last one seen. The
// first evaluation of a repository only records a baseline.
func (s *Service) evaluateMilestones(ctx context.Context, r alertRepo, now time.Time) error {
	if r.verified == nil {
		return nil
	}
	current := *r.verified
	if r.baseline != nil {
		if crossed := highestCrossed(*r.baseline, current); crossed > 0 {
			s.Notifier.Notify(ctx, r.orgID, notifications.Event{
				Kind:  notifications.StarMilestone,
				Key:   fmt.Sprintf("%d:%d", r.id, crossed),
				Title: fmt.Sprintf("%s reached %s stars", r.repo, formatCount(crossed)),
				Body:  fmt.Sprintf("%s now has %s stars.\nhttps://github.com/%s", r.repo, formatCount(current), r.repo),
			})
		}
	}
	_, err := s.pool.Exec(ctx, `
		insert into github_star_alert_state(repo_id, stars, evaluated_at) values ($1,$2,$3)
		on conflict (repo_id) do update set stars=excluded.stars, evaluated_at=excluded.evaluated_at`, r.id, current, now)
	if err != nil {
		return fmt.Errorf("save star alert state: %w", err)
	}
	return nil
}

// highestCrossed returns the largest milestone m with previous < m <= current,
// or 0. Only the highest is reported so one jump sends one message.
func highestCrossed(previous, current int) int {
	crossed := 0
	for _, m := range Milestones {
		if previous < m && m <= current {
			crossed = m
		}
	}
	return crossed
}

type rateCounts struct{ gained, lost, baseGained, baseLost int }

// rates counts stars and unstars in the last six hours (leaving out imported
// history) and in the 14 days before that (everything).
func (s *Service) rates(ctx context.Context, repoID int64, now time.Time) (rateCounts, error) {
	windowStart := now.Add(-alertWindow)
	baseStart := windowStart.Add(-alertBaselineDays * 24 * time.Hour)
	var c rateCounts
	err := s.pool.QueryRow(ctx, `
		select
		  count(*) filter (where action='starred'   and occurred_at > $3 and source <> 'backfill'),
		  count(*) filter (where action='unstarred' and occurred_at > $3 and source <> 'backfill'),
		  count(*) filter (where action='starred'   and occurred_at <= $3),
		  count(*) filter (where action='unstarred' and occurred_at <= $3)
		from github_star_events
		where repo_id=$1 and occurred_at > $2 and occurred_at <= $4`, repoID, baseStart, windowStart, now).
		Scan(&c.gained, &c.lost, &c.baseGained, &c.baseLost)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return rateCounts{}, fmt.Errorf("count star rates: %w", err)
	}
	return c, nil
}

// threshold is max(floor, 3 x the usual count per six hours).
func threshold(floor, baseTotal int) float64 {
	t := alertFactor * float64(baseTotal) / alertBaselineSlots
	if t < float64(floor) {
		return float64(floor)
	}
	return t
}

func (s *Service) evaluateRates(ctx context.Context, r alertRepo, now time.Time) error {
	c, err := s.rates(ctx, r.id, now)
	if err != nil {
		return err
	}
	slot := fmt.Sprintf("%s:%d", now.UTC().Format("2006-01-02"), now.UTC().Hour()/6)
	if float64(c.gained) >= threshold(spikeFloor, c.baseGained) {
		s.Notifier.Notify(ctx, r.orgID, notifications.Event{
			Kind:  notifications.StarSpike,
			Key:   fmt.Sprintf("%d:spike:%s", r.id, slot),
			Title: fmt.Sprintf("%s is gaining stars fast", r.repo),
			Body: fmt.Sprintf("%s gained %d stars in the last 6 hours (usual: %.1f per 6 hours).\nhttps://github.com/%s",
				r.repo, c.gained, float64(c.baseGained)/alertBaselineSlots, r.repo),
		})
	}
	if float64(c.lost) >= threshold(surgeFloor, c.baseLost) {
		s.Notifier.Notify(ctx, r.orgID, notifications.Event{
			Kind:  notifications.UnstarSurge,
			Key:   fmt.Sprintf("%d:unstar:%s", r.id, slot),
			Title: fmt.Sprintf("%s is losing stars", r.repo),
			Body: fmt.Sprintf("%s lost %d stars in the last 6 hours (usual: %.1f per 6 hours).\nhttps://github.com/%s",
				r.repo, c.lost, float64(c.baseLost)/alertBaselineSlots, r.repo),
		})
	}
	return nil
}

// formatCount writes 1000 as 1,000.
func formatCount(n int) string {
	s := fmt.Sprint(n)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
