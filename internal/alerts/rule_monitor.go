package alerts

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"supermetry/internal/appmonitors"
	"supermetry/internal/sites"
)

// MonitorSource lists the threshold crossings of cron monitors. The
// appmonitors Store implements it.
type MonitorSource interface {
	AlertEvents(ctx context.Context, siteID int32, since time.Time, limit int) ([]appmonitors.AlertEvent, error)
}

// WithMonitors sets the source monitor rules read; without one a monitor rule
// produces no candidates.
func (e *RuleEvaluator) WithMonitors(src MonitorSource) *RuleEvaluator {
	e.monitors = src
	return e
}

// monitorTriggerName maps a check-in status to the rule's "on" vocabulary.
func monitorTriggerName(status string) string {
	if status == appmonitors.StatusError {
		return "failed"
	}
	return status
}

// monitorCandidates turns the monitor crossings of the last day (or since the
// rule was created) into candidates. A crossing is recorded once by the
// monitor state machine, which applies the failure and recovery thresholds, so
// this only filters and keys: monitor:<id>:<expected unix>:<status> for a
// failure, monitor:<id>:<recovered unix>:resolved for a recovery.
func (e *RuleEvaluator) monitorCandidates(ctx context.Context, alert Alert, site sites.Site, now time.Time) ([]Candidate, error) {
	if e.monitors == nil {
		return nil, nil
	}
	rule := alert.Rule
	trig := rule.Trigger.Monitor
	since := now.Add(-appLookback)
	if alert.CreatedAt.After(since) {
		since = alert.CreatedAt
	}
	events, err := e.monitors.AlertEvents(ctx, site.ID, since, appCandidateLimit)
	if err != nil {
		return nil, err
	}
	info, err := e.app.Site(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	notifyResolve := rule.NotifyOnResolve == nil || *rule.NotifyOnResolve
	var out []Candidate
	for _, ev := range events {
		if len(trig.Slugs) > 0 && !slices.Contains(trig.Slugs, ev.Slug) {
			continue
		}
		if len(rule.Filters.Environments) > 0 && !slices.Contains(rule.Filters.Environments, ev.Environment) {
			continue
		}
		id := strconv.FormatInt(ev.MonitorID, 10)
		var key, status, severity string
		var when time.Time
		switch {
		case ev.Recovery:
			if !notifyResolve {
				continue
			}
			key, status, when = "monitor:"+id+":"+strconv.FormatInt(ev.At.Unix(), 10)+":resolved", ruleStatusResolve, ev.At
		default:
			if !slices.Contains(trig.On, monitorTriggerName(ev.Status)) {
				continue
			}
			when = ev.At
			if ev.ExpectedAt != nil {
				when = *ev.ExpectedAt
			}
			key, status, severity = "monitor:"+id+":"+strconv.FormatInt(when.Unix(), 10)+":"+ev.Status, ruleStatusFiring, "critical"
		}
		out = append(out, Candidate{
			Key: key, GroupKey: "monitor:" + id,
			Notification: Notification{
				Value: 1, Threshold: 1, From: when, To: now,
				App: &AppNotification{
					AppName: info.Name, Release: ev.Release, Metric: "cron_monitor",
					MonitorSlug: ev.Slug, MonitorEnvironment: ev.Environment, MonitorStatus: ev.Status,
					IssueURL: fmt.Sprintf("/app/%s/%s/settings/monitors", info.OrgSlug, info.SiteSlug),
				},
				Rule: &RuleNotification{ID: alert.ID, Name: alert.Name, TriggerType: TriggerMonitor, Severity: severity, Status: status},
			},
		})
	}
	return out, nil
}
