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
func (e *RuleEvaluator) evalMetric(context.Context, Alert, sites.Site, time.Time) ([]Candidate, error) {
	return nil, nil
}

func (e *RuleEvaluator) evalMonitor(context.Context, Alert, sites.Site, time.Time) ([]Candidate, error) {
	return nil, nil
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
	sent := 0
	for _, c := range candidates {
		if sent >= appMaxPerRun {
			break
		}
		if _, done := seen[c.Key]; done {
			continue
		}
		seen[c.Key] = now
		e.runAssignActions(ctx, alert, &c)
		n := c.Notification
		n.AlertID, n.SiteID, n.Kind, n.FiredAt = alert.ID, alert.SiteID, AlertRule, now
		if n.Rule == nil {
			n.Rule = &RuleNotification{ID: alert.ID, Name: alert.Name, TriggerType: alert.Rule.Trigger.Type, Status: ruleStatusFiring}
		}
		routed := alert
		routed.ChannelIDs = ruleChannels(alert, c)
		if err := e.base.fireWithKey(ctx, routed, n, nil, c.Key); err != nil {
			return err
		}
		e.runOwnerActions(ctx, alert, c)
		sent++
	}
	return nil
}
