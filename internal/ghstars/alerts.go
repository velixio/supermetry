package ghstars

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/alerts"
	"supermetry/internal/scheduler"
)

// AlertKind names an event an owner can opt into for a repository.
type AlertKind string

const (
	AlertStarMilestone   AlertKind = "star_milestone"
	AlertStarSpike       AlertKind = "star_spike"
	AlertUnstarSurge     AlertKind = "unstar_surge"
	AlertTrendingEntered AlertKind = "trending_entered"
	AlertTrendingLeft    AlertKind = "trending_left"
)

// AlertKindInfo describes an alert kind for the settings UI.
type AlertKindInfo struct {
	Kind        AlertKind `json:"kind"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
}

var alertKinds = []AlertKindInfo{
	{AlertStarMilestone, "Star milestones", "The repository reaches a round star count such as 100, 500 or 1,000."},
	{AlertStarSpike, "Star spikes", "Stars arrive much faster than usual."},
	{AlertUnstarSurge, "Unstar surges", "An unusual number of stars is removed in a short time."},
	{AlertTrendingEntered, "Entered trending", "The repository appears on GitHub trending."},
	{AlertTrendingLeft, "Left trending", "The repository drops off GitHub trending."},
}

// AlertKinds returns the alert kinds in display order.
func AlertKinds() []AlertKindInfo { return append([]AlertKindInfo(nil), alertKinds...) }

func validAlertKind(k AlertKind) bool {
	for _, info := range alertKinds {
		if info.Kind == k {
			return true
		}
	}
	return false
}

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

	// alertMinInterval is the minimum gap between two alerts of one kind for
	// one repository.
	alertMinInterval  = 10 * time.Minute
	alertLogRetention = 30 * 24 * time.Hour
	alertSendTimeout  = 30 * time.Second
	alertTestGap      = 30 * time.Second
)

// AlertChannels loads delivery configuration for notification channels.
// *alerts.Service implements it.
type AlertChannels interface {
	DeliveryChannels(ctx context.Context, ids []int64) ([]alerts.DeliveryChannel, error)
}

// AlertSender sends one written message through a channel.
// *alerts.Dispatcher implements it.
type AlertSender interface {
	DeliverMessage(ctx context.Context, channel alerts.DeliveryChannel, kind, subject, text string) error
}

// StarAlerts is what other packages (the trending watcher) call to raise
// alerts for a tracked repository. Both methods are best effort and never
// fail the caller.
type StarAlerts interface {
	TrendingEntered(ctx context.Context, repoID int64, details TrendingDetails)
	TrendingLeft(ctx context.Context, repoID int64, details TrendingDetails)
}

var _ StarAlerts = (*Service)(nil)

// TrendingDetails describes where a repository trends. Empty fields are left
// out of the message; Period ("daily", "weekly") and Language also scope the
// dedup key, so each list raises its own alert once per day.
type TrendingDetails struct {
	Period   string
	Language string
	Rank     int
}

// AlertStats counts alert deliveries since the process started.
type AlertStats struct {
	Sent   int64 `json:"sent"`
	Failed int64 `json:"failed"`
}

// AlertSettings is a repository's alert configuration.
type AlertSettings struct {
	Events     map[AlertKind]bool `json:"events"`
	ChannelIDs []int64            `json:"channel_ids"`
}

// AlertDelivery is the result of a test send to one channel.
type AlertDelivery struct {
	ChannelID int64  `json:"channel_id"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// AlertStats returns the alert delivery counters.
func (s *Service) AlertStats() AlertStats {
	return AlertStats{Sent: s.alertsSent.Load(), Failed: s.alertsFailed.Load()}
}

func (s *Service) alertsEnabled() bool { return s.AlertChannels != nil && s.AlertSender != nil }

// AlertSettings returns the repository's alert settings.
func (s *Service) AlertSettings(ctx context.Context, orgID, repoID int64) (AlertSettings, error) {
	if _, err := s.loadIn(ctx, orgID, repoID); err != nil {
		return AlertSettings{}, err
	}
	return s.readAlertSettings(ctx, s.pool, repoID)
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Service) readAlertSettings(ctx context.Context, db queryer, repoID int64) (AlertSettings, error) {
	out := AlertSettings{Events: map[AlertKind]bool{}, ChannelIDs: []int64{}}
	for _, info := range alertKinds {
		out.Events[info.Kind] = false
	}
	var raw []byte
	err := db.QueryRow(ctx, `select events from github_star_alert_settings where repo_id=$1`, repoID).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AlertSettings{}, fmt.Errorf("read star alert settings: %w", err)
	}
	stored := map[string]bool{}
	_ = json.Unmarshal(raw, &stored)
	for _, info := range alertKinds {
		out.Events[info.Kind] = stored[string(info.Kind)]
	}
	rows, err := db.Query(ctx, `select channel_id from github_star_alert_channels where repo_id=$1 order by channel_id`, repoID)
	if err != nil {
		return AlertSettings{}, fmt.Errorf("read star alert channels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return AlertSettings{}, err
		}
		out.ChannelIDs = append(out.ChannelIDs, id)
	}
	return out, rows.Err()
}

// SaveAlertSettings replaces the repository's alert settings. Every channel
// must belong to the repository's own organization.
func (s *Service) SaveAlertSettings(ctx context.Context, orgID, repoID int64, in AlertSettings, actor int64) (AlertSettings, error) {
	row, err := s.loadIn(ctx, orgID, repoID)
	if err != nil {
		return AlertSettings{}, err
	}
	events := map[string]bool{}
	for kind, on := range in.Events {
		if !validAlertKind(kind) {
			return AlertSettings{}, fmt.Errorf("%w: unknown alert kind %q", ErrInvalid, truncate(string(kind), 40))
		}
		events[string(kind)] = on
	}
	ids := dedupeIDs(in.ChannelIDs)
	if len(ids) > 20 {
		return AlertSettings{}, fmt.Errorf("%w: at most 20 channels", ErrInvalid)
	}
	if len(ids) > 0 {
		if s.AlertChannels == nil {
			return AlertSettings{}, fmt.Errorf("%w: notification channels are unavailable", ErrInvalid)
		}
		found, err := s.AlertChannels.DeliveryChannels(ctx, ids)
		if err != nil {
			return AlertSettings{}, fmt.Errorf("load channels: %w", err)
		}
		ok := map[int64]bool{}
		for _, c := range found {
			if c.OrgID == orgID {
				ok[c.ID] = true
			}
		}
		for _, id := range ids {
			if !ok[id] {
				return AlertSettings{}, fmt.Errorf("%w: channel %d does not exist in this organization", ErrInvalid, id)
			}
		}
	}
	eventsJSON, _ := json.Marshal(events)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			insert into github_star_alert_settings(repo_id, events, updated_at) values ($1,$2,now())
			on conflict (repo_id) do update set events=excluded.events, updated_at=now()`, repoID, eventsJSON); err != nil {
			return fmt.Errorf("save star alert settings: %w", err)
		}
		if _, err := tx.Exec(ctx, `delete from github_star_alert_channels where repo_id=$1`, repoID); err != nil {
			return fmt.Errorf("clear star alert channels: %w", err)
		}
		if len(ids) > 0 {
			if _, err := tx.Exec(ctx, `insert into github_star_alert_channels(repo_id, channel_id) select $1, unnest($2::bigint[])`, repoID, ids); err != nil {
				return fmt.Errorf("save star alert channels: %w", err)
			}
		}
		return s.audit(ctx, tx, orgID, actor, "github_stars.alerts_updated", repoID, row.Repo.Repo)
	})
	if err != nil {
		return AlertSettings{}, err
	}
	return s.readAlertSettings(ctx, s.pool, repoID)
}

func dedupeIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// SendTestAlert sends one message to the repository's selected channels,
// whatever events are on. At most one every 30 seconds per repository.
func (s *Service) SendTestAlert(ctx context.Context, orgID, repoID int64) ([]AlertDelivery, error) {
	row, err := s.loadIn(ctx, orgID, repoID)
	if err != nil {
		return nil, err
	}
	if !s.alertsEnabled() {
		return nil, fmt.Errorf("%w: alert delivery is unavailable", ErrInvalid)
	}
	settings, err := s.readAlertSettings(ctx, s.pool, repoID)
	if err != nil {
		return nil, err
	}
	if len(settings.ChannelIDs) == 0 {
		return nil, fmt.Errorf("%w: select at least one channel first", ErrInvalid)
	}
	now := s.Now()
	s.mu.Lock()
	if last, ok := s.lastTestAlert[repoID]; ok && now.Sub(last) < alertTestGap {
		s.mu.Unlock()
		return nil, ErrTooSoon
	}
	s.lastTestAlert[repoID] = now
	s.mu.Unlock()
	title := fmt.Sprintf("Test alert for %s", row.Repo.Repo)
	body := fmt.Sprintf("This is a test alert. Star alerts for %s will arrive here.\nhttps://github.com/%s", row.Repo.Repo, row.Repo.Repo)
	results, _ := s.deliverAll(ctx, row.OrgID, settings.ChannelIDs, "github_star_test", title, body)
	return results, nil
}

// deliverAll sends to every channel; one failing channel never stops the
// others. It reports how many succeeded.
func (s *Service) deliverAll(ctx context.Context, orgID int64, ids []int64, kind, title, body string) ([]AlertDelivery, int) {
	channels, err := s.AlertChannels.DeliveryChannels(ctx, ids)
	if err != nil {
		slog.Warn("star alert channels could not be loaded", "error", scrubAlertError(err))
		return nil, 0
	}
	var results []AlertDelivery
	ok := 0
	for _, channel := range channels {
		if channel.OrgID != orgID || !channel.Enabled {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, alertSendTimeout)
		err := s.AlertSender.DeliverMessage(cctx, channel, kind, title, title+"\n\n"+body)
		cancel()
		if err != nil {
			s.alertsFailed.Add(1)
			msg := scrubAlertError(err)
			slog.Warn("star alert delivery failed", "channel_id", channel.ID, "error", msg)
			results = append(results, AlertDelivery{ChannelID: channel.ID, Error: msg})
			continue
		}
		s.alertsSent.Add(1)
		ok++
		results = append(results, AlertDelivery{ChannelID: channel.ID, OK: true})
	}
	return results, ok
}

var urlPattern = regexp.MustCompile(`https?://\S+`)
var tokenPattern = regexp.MustCompile(`[0-9]{5,20}:[A-Za-z0-9_-]{20,200}`)

// scrubAlertError removes URLs (webhook endpoints hold secrets) and anything
// token shaped, and caps the length.
func scrubAlertError(err error) string {
	msg := urlPattern.ReplaceAllString(err.Error(), "[url]")
	msg = tokenPattern.ReplaceAllString(msg, "[token]")
	return truncate(strings.TrimSpace(msg), 200)
}

// raise sends one alert if the event kind is on for the repository, it is not
// a duplicate and the per-kind interval has passed. Nothing is recorded when
// no channel would receive it, so enabling an event later is never blocked by
// used-up keys.
func (s *Service) raise(ctx context.Context, repoID, orgID int64, kind AlertKind, key, title, body string) {
	if !s.alertsEnabled() {
		return
	}
	settings, err := s.readAlertSettings(ctx, s.pool, repoID)
	if err != nil {
		slog.Warn("star alert settings unreadable", "repo_id", repoID, "error", scrubAlertError(err))
		return
	}
	if !settings.Events[kind] || len(settings.ChannelIDs) == 0 {
		return
	}
	claimed, err := s.claimAlert(ctx, repoID, kind, key)
	if err != nil {
		slog.Warn("star alert claim failed", "repo_id", repoID, "error", scrubAlertError(err))
		return
	}
	if !claimed {
		return
	}
	_, ok := s.deliverAll(ctx, orgID, settings.ChannelIDs, "github_star_"+string(kind), title, body)
	if ok == 0 {
		// Nobody received it: do not let it count as sent.
		if _, err := s.pool.Exec(ctx, `delete from github_star_alert_log where repo_id=$1 and kind=$2 and key=$3`, repoID, string(kind), key); err != nil {
			slog.Warn("star alert release failed", "repo_id", repoID, "error", scrubAlertError(err))
		}
	}
}

func (s *Service) claimAlert(ctx context.Context, repoID int64, kind AlertKind, key string) (bool, error) {
	claimed := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("star-alert:%d:%s", repoID, kind)); err != nil {
			return err
		}
		now := s.Now()
		var recent bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from github_star_alert_log where repo_id=$1 and kind=$2 and sent_at > $3)`,
			repoID, string(kind), now.Add(-alertMinInterval)).Scan(&recent); err != nil {
			return err
		}
		if recent {
			return nil
		}
		tag, err := tx.Exec(ctx, `insert into github_star_alert_log(repo_id,kind,key,sent_at) values ($1,$2,$3,$4) on conflict do nothing`,
			repoID, string(kind), truncate(key, 200), now)
		if err != nil {
			return err
		}
		claimed = tag.RowsAffected() == 1
		if claimed {
			_, err = tx.Exec(ctx, `delete from github_star_alert_log where repo_id=$1 and kind=$2 and sent_at < $3`, repoID, string(kind), now.Add(-alertLogRetention))
		}
		return err
	})
	return claimed, err
}

// TrendingEntered raises a trending_entered alert for a tracked repository.
func (s *Service) TrendingEntered(ctx context.Context, repoID int64, d TrendingDetails) {
	s.raiseTrending(ctx, repoID, AlertTrendingEntered, "is now trending on GitHub", d)
}

// TrendingLeft raises a trending_left alert for a tracked repository.
func (s *Service) TrendingLeft(ctx context.Context, repoID int64, d TrendingDetails) {
	s.raiseTrending(ctx, repoID, AlertTrendingLeft, "dropped off GitHub trending", d)
}

func (s *Service) raiseTrending(ctx context.Context, repoID int64, kind AlertKind, phrase string, d TrendingDetails) {
	if !s.alertsEnabled() {
		return
	}
	r, err := s.load(ctx, repoID)
	if err != nil || !r.Enabled {
		return
	}
	where := strings.TrimSpace(d.Language + " " + d.Period)
	if where != "" {
		where = " (" + where + ")"
	}
	body := fmt.Sprintf("%s %s%s.", r.Repo.Repo, phrase, where)
	if d.Rank > 0 {
		body += fmt.Sprintf(" Rank %d.", d.Rank)
	}
	body += "\nhttps://github.com/" + r.Repo.Repo
	key := fmt.Sprintf("%d:%s:%s:%s:%s", r.ID, kind, s.Now().UTC().Format("2006-01-02"), d.Period, d.Language)
	s.raise(ctx, r.ID, r.OrgID, kind, key, fmt.Sprintf("%s %s", r.Repo.Repo, phrase), body)
}

// AlertsJob returns the scheduler job that evaluates star alerts every ten
// minutes. It does nothing when alert delivery is not configured.
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
// and raises milestone, spike and unstar-surge alerts. Events are scoped by
// repository row, so organizations never see each other's data.
func (s *Service) EvaluateAlerts(ctx context.Context) error {
	if !s.alertsEnabled() {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		select r.id, r.org_id, r.repo, r.verified_stars, r.backfill_complete, a.stars
		from github_star_repos r
		join orgs o on o.id = r.org_id and o.deleted_at is null
		join github_star_alert_settings st on st.repo_id = r.id
		left join github_star_alert_state a on a.repo_id = r.id
		where r.enabled and st.events <> '{}'::jsonb order by r.id`)
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
			slog.Warn("github star alert evaluation failed", "repo_id", r.id, "error", scrubAlertError(err))
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
// first evaluation of a repository only records a baseline, so enabling alerts
// on a repository that already has thousands of stars sends nothing.
func (s *Service) evaluateMilestones(ctx context.Context, r alertRepo, now time.Time) error {
	if r.verified == nil {
		return nil
	}
	current := *r.verified
	if r.baseline != nil {
		if crossed := highestCrossed(*r.baseline, current); crossed > 0 {
			s.raise(ctx, r.id, r.orgID, AlertStarMilestone, fmt.Sprintf("%d:%d", r.id, crossed),
				fmt.Sprintf("%s reached %s stars", r.repo, formatCount(crossed)),
				fmt.Sprintf("%s now has %s stars.\nhttps://github.com/%s", r.repo, formatCount(current), r.repo))
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
		s.raise(ctx, r.id, r.orgID, AlertStarSpike, fmt.Sprintf("%d:spike:%s", r.id, slot),
			fmt.Sprintf("%s is gaining stars fast", r.repo),
			fmt.Sprintf("%s gained %d stars in the last 6 hours (usual: %.1f per 6 hours).\nhttps://github.com/%s",
				r.repo, c.gained, float64(c.baseGained)/alertBaselineSlots, r.repo))
	}
	if float64(c.lost) >= threshold(surgeFloor, c.baseLost) {
		s.raise(ctx, r.id, r.orgID, AlertUnstarSurge, fmt.Sprintf("%d:unstar:%s", r.id, slot),
			fmt.Sprintf("%s is losing stars", r.repo),
			fmt.Sprintf("%s lost %d stars in the last 6 hours (usual: %.1f per 6 hours).\nhttps://github.com/%s",
				r.repo, c.lost, float64(c.baseLost)/alertBaselineSlots, r.repo))
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
