package alerts

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"supermetry/internal/sites"
)

// App alert tuning. Candidate look-back is bounded by the alert's creation, so
// a new alert does not announce old issues; dedup keys make each one fire once.
const (
	appLookback          = 24 * time.Hour
	appMaxPerRun         = 20
	appCandidateLimit    = 200
	crashFreeMinInstalls = 100
	crashFreeCooldown    = 24 * time.Hour
	spikeMinEvents       = 10
	spikeFactor          = 3.0
	spikeBaselineDays    = 7
)

// AppSite names a project for messages and builds the issue URL.
type AppSite struct {
	Name     string
	OrgSlug  string
	SiteSlug string
}

// AppIssue is a candidate row from the app issue tables. Events is the issue's
// event count (for new issues, the total so far).
type AppIssue struct {
	ID          int64
	Title       string
	Kind        string
	Release     string
	Events      int64
	FirstSeen   time.Time
	RegressedAt time.Time
}

// AppRelease is the newest release with enough installs, with crash-free
// ratios in [0,1]; nil means the formula has no data (no session pings).
type AppRelease struct {
	Release           string
	Installs          int64
	CrashFreeUsers    *float64
	CrashFreeSessions *float64
}

// IssueHour is an issue's events in one UTC hour with the sum of its events in
// the same hour of the previous seven days (missing hours count as 0).
type IssueHour struct {
	AppIssue
	Events      int64
	BaselineSum int64
}

// AppNotification carries app-alert details for message templates. It never
// holds stack frames or tags.
type AppNotification struct {
	AppName    string  `json:"app_name"`
	IssueID    int64   `json:"issue_id,omitempty"`
	IssueTitle string  `json:"issue_title,omitempty"`
	IssueKind  string  `json:"issue_kind,omitempty"`
	IssueURL   string  `json:"issue_url,omitempty"`
	Release    string  `json:"release,omitempty"`
	Metric     string  `json:"metric,omitempty"`
	Events     int64   `json:"events,omitempty"`
	Installs   int64   `json:"installs,omitempty"`
	Baseline   float64 `json:"baseline,omitempty"`

	// Cron monitor alerts: the monitor's slug and environment, and the check-in
	// status that opened the failure (error, missed or timeout).
	MonitorSlug        string `json:"monitor_slug,omitempty"`
	MonitorEnvironment string `json:"monitor_environment,omitempty"`
	MonitorStatus      string `json:"monitor_status,omitempty"`
}

// AppSource reads the candidates the app alerts evaluate.
type AppSource interface {
	Site(ctx context.Context, siteID int32) (AppSite, error)
	// NewIssues lists issues first seen at or after since; kinds filters when non-empty.
	NewIssues(ctx context.Context, siteID int32, since time.Time, kinds []string, limit int) ([]AppIssue, error)
	// RegressedIssues lists issues regressed at or after since.
	RegressedIssues(ctx context.Context, siteID int32, since time.Time, limit int) ([]AppIssue, error)
	// LatestRelease returns the newest release with at least minInstalls installs
	// since the given time, or nil.
	LatestRelease(ctx context.Context, siteID int32, since time.Time, minInstalls int64) (*AppRelease, error)
	// IssueHours lists issues with at least minEvents events in the UTC hour.
	IssueHours(ctx context.Context, siteID int32, hour time.Time, minEvents int64) ([]IssueHour, error)
}

// AppDedup remembers which app notifications an alert already sent.
type AppDedup interface {
	AppFired(ctx context.Context, alertID int64, keys []string) (map[string]time.Time, error)
	RecordAppFired(ctx context.Context, alertID int64, key string, at time.Time) error
	ForgetAppFired(ctx context.Context, alertID int64, key string) error
}

// WithApp enables the app alert kinds.
func (e *Evaluator) WithApp(source AppSource, dedup AppDedup) *Evaluator {
	e.app, e.appDedup = source, dedup
	return e
}

type appCandidate struct {
	key          string
	notification Notification
}

func (e *Evaluator) evaluateApp(ctx context.Context, alert Alert, site sites.Site, now time.Time) error {
	if site.Kind != sites.KindApp {
		return nil
	}
	if e.app == nil || e.appDedup == nil {
		return errors.New("app alert source is unavailable")
	}
	info, err := e.app.Site(ctx, site.ID)
	if err != nil {
		return err
	}
	cfg := AlertConfig{}
	if alert.Config != nil {
		cfg = *alert.Config
	}
	since := now.Add(-appLookback)
	if alert.CreatedAt.After(since) {
		since = alert.CreatedAt
	}
	base := func(issue AppIssue) *AppNotification {
		return &AppNotification{
			AppName: info.Name, IssueID: issue.ID, IssueTitle: issue.Title, IssueKind: issue.Kind, Release: issue.Release, Events: issue.Events,
			IssueURL: fmt.Sprintf("/app/%s/%s/issues/%d", info.OrgSlug, info.SiteSlug, issue.ID),
		}
	}
	var candidates []appCandidate
	switch alert.Kind {
	case AlertAppNewIssue:
		issues, err := e.app.NewIssues(ctx, site.ID, since, cfg.Kinds, appCandidateLimit)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			candidates = append(candidates, appCandidate{
				key:          "issue:" + strconv.FormatInt(issue.ID, 10),
				notification: Notification{Value: float64(issue.Events), Threshold: alert.Threshold, From: issue.FirstSeen, To: now, App: base(issue)},
			})
		}
	case AlertAppIssueRegressed:
		issues, err := e.app.RegressedIssues(ctx, site.ID, since, appCandidateLimit)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			candidates = append(candidates, appCandidate{
				key:          "regress:" + strconv.FormatInt(issue.ID, 10) + ":" + strconv.FormatInt(issue.RegressedAt.Unix(), 10),
				notification: Notification{Value: float64(issue.Events), Threshold: alert.Threshold, From: issue.RegressedAt, To: now, App: base(issue)},
			})
		}
	case AlertAppIssueSpike:
		hour := now.UTC().Truncate(time.Hour).Add(-time.Hour)
		rows, err := e.app.IssueHours(ctx, site.ID, hour, spikeMinEvents)
		if err != nil {
			return err
		}
		for _, row := range rows {
			average := float64(row.BaselineSum) / spikeBaselineDays
			if row.Events < spikeMinEvents || float64(row.Events) < spikeFactor*average {
				continue
			}
			issue := row.AppIssue
			issue.Events = row.Events
			details := base(issue)
			details.Baseline = average
			candidates = append(candidates, appCandidate{
				key:          "spike:" + strconv.FormatInt(issue.ID, 10) + ":" + strconv.FormatInt(hour.Unix(), 10),
				notification: Notification{Value: float64(row.Events), Threshold: average * spikeFactor, From: hour, To: hour.Add(time.Hour), App: details},
			})
		}
	case AlertAppCrashFreeBelow:
		candidate, err := e.crashFreeCandidate(ctx, alert, cfg, site.ID, info, now)
		if err != nil || candidate == nil {
			return err
		}
		candidates = append(candidates, *candidate)
	default:
		return fmt.Errorf("unknown kind %q", alert.Kind)
	}
	return e.fireApp(ctx, alert, candidates, now)
}

// crashFreeCandidate applies the threshold to the newest release with enough
// installs. A key fired within the cooldown suppresses the repeat; a recovered
// release forgets its key so a later drop fires at once.
func (e *Evaluator) crashFreeCandidate(ctx context.Context, alert Alert, cfg AlertConfig, siteID int32, info AppSite, now time.Time) (*appCandidate, error) {
	release, err := e.app.LatestRelease(ctx, siteID, now.Add(-24*time.Hour), crashFreeMinInstalls)
	if err != nil || release == nil {
		return nil, err
	}
	metric := cfg.Metric
	ratio := release.CrashFreeUsers
	if metric == MetricSessions {
		ratio = release.CrashFreeSessions
	} else {
		metric = MetricUsers
	}
	if ratio == nil {
		return nil, nil
	}
	key := "crashfree:" + release.Release
	percent := *ratio * 100
	if percent >= alert.Threshold {
		return nil, e.appDedup.ForgetAppFired(ctx, alert.ID, key)
	}
	fired, err := e.appDedup.AppFired(ctx, alert.ID, []string{key})
	if err != nil {
		return nil, err
	}
	if at, ok := fired[key]; ok && now.Sub(at) < crashFreeCooldown {
		return nil, nil
	}
	return &appCandidate{key: key, notification: Notification{
		Value: percent, Threshold: alert.Threshold, From: now.Add(-24 * time.Hour), To: now,
		App: &AppNotification{AppName: info.Name, Release: release.Release, Metric: metric, Installs: release.Installs},
	}}, nil
}

// fireApp sends the candidates whose key has not fired yet, at most appMaxPerRun
// per evaluation, stopping at the first delivery failure (the pending delivery
// is retried through the delivery state and the rest follow on the next run).
func (e *Evaluator) fireApp(ctx context.Context, alert Alert, candidates []appCandidate, now time.Time) error {
	if len(candidates) == 0 {
		return nil
	}
	keys := make([]string, len(candidates))
	for i, c := range candidates {
		keys[i] = c.key
	}
	seen, err := e.appDedup.AppFired(ctx, alert.ID, keys)
	if err != nil {
		return err
	}
	sent := 0
	for _, c := range candidates {
		if sent >= appMaxPerRun {
			break
		}
		if _, done := seen[c.key]; done && alert.Kind != AlertAppCrashFreeBelow {
			continue
		}
		key := c.key
		n := c.notification
		n.AlertID, n.SiteID, n.Kind, n.FiredAt = alert.ID, alert.SiteID, alert.Kind, now
		if err := e.fire(ctx, alert, n, func() error { return e.appDedup.RecordAppFired(ctx, alert.ID, key, now) }); err != nil {
			return err
		}
		sent++
	}
	return nil
}
