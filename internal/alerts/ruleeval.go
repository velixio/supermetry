package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"supermetry/internal/scheduler"
	"supermetry/internal/sites"
)

const (
	ruleJobInterval   = time.Minute
	ruleEvalTimeout   = 10 * time.Second
	ruleJobName       = "alert-rules"
	ruleStatusFiring  = "firing"
	ruleStatusResolve = "resolved"
)

// Candidate is one notification a trigger evaluator wants to send. Key is the
// dedup key committed atomically with the delivery state; GroupKey names the
// firing group (an issue, a release) and IssueID the issue, when there is one.
type Candidate struct {
	Key      string
	GroupKey string
	IssueID  int64
	// AssigneeUserID is the issue's owner at evaluation time, 0 when unassigned.
	AssigneeUserID int64
	Notification   Notification
	// Done runs once the key is committed (sent now, or found already sent by
	// an earlier run). Incident transitions hang off it, so a crash between
	// delivery and the state update repeats the update, never the delivery.
	Done func(ctx context.Context) error
}

// RuleEvaluator runs the app_rule alerts. The old Evaluator keeps the web
// kinds; the two never see each other's alerts (EnabledAlerts and EnabledRules
// partition the table by kind).
type RuleEvaluator struct {
	base  *Evaluator
	rules RuleRepository
	app   AppSource
	dedup AppDedup
	sites sites.Registry
	now   func() time.Time

	assigner IssueAssigner
	mailer   OwnerMailer
	// metrics serves the metric triggers; nil leaves them inert.
	metrics RuleMetricSource

	monitors MonitorSource // cron monitor crossings (WithMonitors)
	// silences mutes candidates at the send point; nil mutes nothing.
	silences SilenceRepository
	// runSilences caches the silences of the sites seen in the current Run.
	runSilences map[int32][]Silence
}

// SetSilences enables silences and maintenance windows.
func (e *RuleEvaluator) SetSilences(repo SilenceRepository) *RuleEvaluator {
	e.silences = repo
	return e
}

// silencesFor loads the silences of a site once per run (fireKeyed called
// outside a run loads them per call).
func (e *RuleEvaluator) silencesFor(ctx context.Context, siteID int32, now time.Time) ([]Silence, error) {
	if e.silences == nil {
		return nil, nil
	}
	if list, ok := e.runSilences[siteID]; ok {
		return list, nil
	}
	list, err := e.silences.ActiveSilences(ctx, siteID, now)
	if err != nil {
		return nil, fmt.Errorf("load silences: %w", err)
	}
	if e.runSilences != nil {
		e.runSilences[siteID] = list
	}
	return list, nil
}

// NewRuleEvaluator builds the evaluator of the alert-rules job. It reuses the
// Evaluator's delivery state machine (fire and deliver) so rules and legacy
// alerts share one history log and one retry path.
func NewRuleEvaluator(repo Repository, rules RuleRepository, app AppSource, dedup AppDedup, deliverer Deliverer, registry sites.Registry, now func() time.Time, failures ...DeliveryFailureFunc) *RuleEvaluator {
	if now == nil {
		now = time.Now
	}
	base := NewEvaluator(repo, nil, registry, nil, deliverer, now, failures...)
	base.app, base.appDedup, base.rules = app, dedup, rules
	return &RuleEvaluator{base: base, rules: rules, app: app, dedup: dedup, sites: registry, now: now}
}

// Job is the scheduler job (advisory-locked like every job).
func (e *RuleEvaluator) Job() scheduler.Job {
	return scheduler.Job{Name: ruleJobName, Every: ruleJobInterval, Run: e.Run}
}

// Run evaluates every enabled rule. A rule that errors is reported and retried
// on the next run; it never blocks the others.
func (e *RuleEvaluator) Run(ctx context.Context) error {
	alerts, err := e.rules.EnabledRules(ctx)
	if err != nil {
		return fmt.Errorf("load enabled rules: %w", err)
	}
	now := e.now()
	e.runSilences = map[int32][]Silence{}
	defer func() { e.runSilences = nil }()
	var runErr error
	for _, alert := range alerts {
		if ctx.Err() != nil {
			return errors.Join(runErr, ctx.Err())
		}
		ruleCtx, cancel := context.WithTimeout(ctx, ruleEvalTimeout)
		err := e.evaluate(ruleCtx, alert, now)
		cancel()
		if err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("evaluate rule %d: %w", alert.ID, err))
		}
	}
	return runErr
}

func (e *RuleEvaluator) evaluate(ctx context.Context, alert Alert, now time.Time) error {
	if alert.DeliveryState != nil {
		return e.base.deliver(ctx, alert.ID, *alert.DeliveryState)
	}
	if alert.Rule == nil {
		return errors.New("rule alert has no rule")
	}
	site, ok := e.sites.Site(alert.SiteID)
	if !ok || site.TZ == nil {
		return fmt.Errorf("site %d is unavailable", alert.SiteID)
	}
	if site.Kind != sites.KindApp {
		return nil
	}
	if e.app == nil || e.dedup == nil {
		return errors.New("rule source is unavailable")
	}
	var candidates []Candidate
	var err error
	switch alert.Rule.Trigger.Type {
	case TriggerIssue:
		candidates, err = e.evalIssue(ctx, alert, site, now)
	case TriggerEvent:
		candidates, err = e.evalEvent(ctx, alert, site, now)
	case TriggerMetric:
		candidates, err = e.evalMetric(ctx, alert, site, now)
	case TriggerMonitor:
		candidates, err = e.evalMonitor(ctx, alert, site, now)
	default:
		return fmt.Errorf("unknown trigger type %q", alert.Rule.Trigger.Type)
	}
	if err != nil {
		return err
	}
	return e.fireKeyed(ctx, alert, candidates, now)
}

// The remaining trigger evaluators land with their tasks; until then a rule of
// that type stores and validates but produces no candidates.
func (e *RuleEvaluator) evalMonitor(ctx context.Context, alert Alert, site sites.Site, now time.Time) ([]Candidate, error) {
	return e.monitorCandidates(ctx, alert, site, now)
}

// fireKeyed sends the candidates whose key has not fired yet, at most
// appMaxPerRun per run, stopping at the first delivery failure (the pending
// delivery is retried through the delivery state; the rest follow next run).
// The key is committed together with the delivery state, so each key is
// delivered exactly once across restarts.
func (e *RuleEvaluator) fireKeyed(ctx context.Context, alert Alert, candidates []Candidate, now time.Time) error {
	if len(candidates) == 0 {
		return nil
	}
	keys := make([]string, len(candidates))
	for i, c := range candidates {
		keys[i] = c.Key
	}
	seen, err := e.dedup.AppFired(ctx, alert.ID, keys)
	if err != nil {
		return err
	}
	silences, err := e.silencesFor(ctx, alert.SiteID, now)
	if err != nil {
		return err
	}
	sent, muted := 0, 0
	for _, c := range candidates {
		if sent >= appMaxPerRun {
			break
		}
		if _, done := seen[c.Key]; done {
			if c.Done != nil {
				if err := c.Done(ctx); err != nil {
					return err
				}
			}
			continue
		}
		seen[c.Key] = now
		e.runAssignActions(ctx, alert, &c)
		n := c.Notification
		n.AlertID, n.SiteID, n.Kind, n.FiredAt = alert.ID, alert.SiteID, AlertRule, now
		if n.Rule == nil {
			n.Rule = &RuleNotification{ID: alert.ID, Name: alert.Name, TriggerType: alert.Rule.Trigger.Type, Status: ruleStatusFiring}
		}
		if silence, ok := Silenced(silences, alert.ID, now); ok {
			if err := e.suppress(ctx, alert, c, n, silence, muted, now); err != nil {
				return err
			}
			muted++
			continue
		}
		routed := alert
		routed.ChannelIDs = ruleChannels(alert, c)
		if err := e.base.fireWithKey(ctx, routed, n, nil, c.Key); err != nil {
			return err
		}
		e.runOwnerActions(ctx, alert, c)
		if c.Done != nil {
			if err := c.Done(ctx); err != nil {
				return err
			}
		}
		sent++
	}
	return nil
}

// suppress handles a candidate under a silence. Metric incidents are only
// withheld: the incident keeps its state, the candidate is logged once, and the
// next run after the silence builds it again, so an incident still open then is
// sent once. Every other candidate is committed as fired with a suppressed
// history row and never sent later. index spaces the history timestamps of one
// run apart (the log is unique on alert, channel and time).
func (e *RuleEvaluator) suppress(ctx context.Context, alert Alert, c Candidate, n Notification, silence Silence, index int, now time.Time) error {
	commit := alert.Rule.Trigger.Type != TriggerMetric
	at := now.Add(time.Duration(index) * time.Microsecond)
	n.FiredAt = at
	if _, err := e.silences.RecordSuppressed(ctx, alert, c.Key, commit, n, silence.Reason, at); err != nil {
		return err
	}
	if commit && c.Done != nil {
		return c.Done(ctx)
	}
	return nil
}
