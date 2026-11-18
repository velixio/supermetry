package alerts

import (
	"context"
	"math"
	"strconv"
	"time"

	"supermetry/internal/sites"
)

const (
	severityWarning  = "warning"
	severityCritical = "critical"

	defaultMetricMinSample = 100
	latestRelease          = "latest"
)

// RuleMetricSource reads the values the metric triggers compare. It is not
// MetricSource, the web-metrics interface of the legacy evaluator.
type RuleMetricSource interface {
	// ErrorCount counts the error, crash and hang events in [from, to).
	ErrorCount(ctx context.Context, siteID int32, from, to time.Time, f Filters) (float64, error)
	// CrashFree returns the crash-free ratio (0..1) of the sessions (or
	// installs, when users is set) that started in [from, to), and the
	// number of sessions (installs) it rests on.
	CrashFree(ctx context.Context, siteID int32, from, to time.Time, f Filters, users bool) (ratio float64, sample int64, err error)
	// Perf returns a transaction metric over the closed hours in [from, to):
	// transaction_count, transaction_failure_rate (percent) or
	// transaction_p50..p99 (milliseconds), with the transaction count.
	Perf(ctx context.Context, siteID int32, from, to time.Time, transaction string, f Filters, name string) (value float64, sample int64, err error)
}

// SetMetricSource enables the metric triggers.
func (e *RuleEvaluator) SetMetricSource(src RuleMetricSource) *RuleEvaluator {
	e.metrics = src
	return e
}

// evaluateThreshold reports the severity a value reaches. A value equal to a
// threshold does not cross it.
func evaluateThreshold(m MetricTrigger, value float64) (string, bool) {
	crosses := func(threshold float64) bool {
		if m.Op == "below" {
			return value < threshold
		}
		return value > threshold
	}
	if crosses(m.Critical) {
		return severityCritical, true
	}
	if m.Warning != nil && crosses(*m.Warning) {
		return severityWarning, true
	}
	return "", false
}

// lowestThreshold is the least strict firing threshold: the default resolve level.
func lowestThreshold(m MetricTrigger) float64 {
	t := m.Critical
	if m.Warning != nil {
		if m.Op == "below" {
			t = math.Max(t, *m.Warning)
		} else {
			t = math.Min(t, *m.Warning)
		}
	}
	return t
}

// shouldResolve reports whether an open incident recovers at value. Strict, so
// a value equal to the resolve level neither opens nor resolves.
func shouldResolve(m MetricTrigger, value float64) bool {
	resolve := lowestThreshold(m)
	if m.Resolve != nil {
		resolve = *m.Resolve
	}
	if m.Op == "below" {
		return value > resolve
	}
	return value < resolve
}

func thresholdFor(m MetricTrigger, severity string) float64 {
	if severity == severityWarning && m.Warning != nil {
		return *m.Warning
	}
	return m.Critical
}

func minSample(m MetricTrigger) int64 {
	if !usesClosedHours(m.Name) {
		return 0
	}
	if m.MinSample > 0 {
		return m.MinSample
	}
	return defaultMetricMinSample
}

// metricWindow returns [from, to). Crash-free and transaction metrics read
// whole closed hours of the site time zone; error_count ends now.
func metricWindow(m MetricTrigger, now time.Time, tz *time.Location) (time.Time, time.Time) {
	to := now
	if usesClosedHours(m.Name) {
		l := now.In(tz)
		to = time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), 0, 0, 0, tz)
	}
	return to.Add(-time.Duration(m.WindowMinutes) * time.Minute), to
}

// metricValue evaluates the metric over [from, to); ok is false when the
// sample is too small (or the baseline missing), which leaves state unchanged.
func (e *RuleEvaluator) metricValue(ctx context.Context, siteID int32, m MetricTrigger, f Filters, from, to time.Time) (float64, bool, error) {
	value, sample, err := e.rawMetric(ctx, siteID, m, f, from, to)
	if err != nil {
		return 0, false, err
	}
	if usesClosedHours(m.Name) && sample < max(minSample(m), 1) {
		return 0, false, nil
	}
	if m.Comparison != "change" {
		return value, true, nil
	}
	shift := 24 * time.Hour
	if m.ChangeWindow == "1w" {
		shift = 7 * 24 * time.Hour
	}
	base, baseSample, err := e.rawMetric(ctx, siteID, m, f, from.Add(-shift), to.Add(-shift))
	if err != nil {
		return 0, false, err
	}
	if base == 0 || math.IsNaN(base) || (usesClosedHours(m.Name) && baseSample < 1) {
		return 0, false, nil
	}
	return (value - base) / base * 100, true, nil
}

func (e *RuleEvaluator) rawMetric(ctx context.Context, siteID int32, m MetricTrigger, f Filters, from, to time.Time) (float64, int64, error) {
	switch {
	case m.Name == MetricErrorCount:
		v, err := e.metrics.ErrorCount(ctx, siteID, from, to, f)
		return v, 0, err
	case isCrashFreeMetric(m.Name):
		ratio, sample, err := e.metrics.CrashFree(ctx, siteID, from, to, f, m.Name == MetricCrashFreeUsers)
		return ratio * 100, sample, err
	default:
		v, sample, err := e.metrics.Perf(ctx, siteID, from, to, m.Transaction, f, m.Name)
		if err == nil && math.IsNaN(v) {
			return 0, 0, nil
		}
		return v, sample, err
	}
}

// metricGroup resolves the firing group: a release (exact or the newest) or the site.
func (e *RuleEvaluator) metricGroup(ctx context.Context, alert Alert, m MetricTrigger, siteID int32, now time.Time) (group, release string, ok bool, err error) {
	release = m.Release
	if release == "" && len(alert.Rule.Filters.Releases) == 1 && alert.Rule.Filters.Releases[0] == latestRelease {
		release = latestRelease
	}
	if release == "" {
		return "site", "", true, nil
	}
	if release == latestRelease {
		newest, err := e.app.LatestRelease(ctx, siteID, now.Add(-24*time.Hour), max(minSample(m), 1))
		if err != nil || newest == nil {
			return "", "", false, err
		}
		release = newest.Release
	}
	return "release:" + release, release, true, nil
}

func (e *RuleEvaluator) evalMetric(ctx context.Context, alert Alert, site sites.Site, now time.Time) ([]Candidate, error) {
	if e.metrics == nil {
		return nil, nil
	}
	m := *alert.Rule.Trigger.Metric
	group, release, ok, err := e.metricGroup(ctx, alert, m, site.ID, now)
	if err != nil || !ok {
		return nil, err
	}
	open, err := e.rules.OpenIncidents(ctx, alert.ID)
	if err != nil {
		return nil, err
	}
	var cur *Incident
	for i := range open {
		if open[i].GroupKey == group {
			cur = &open[i]
			continue
		}
		// The group moved on (a newer release took over): close it quietly.
		stale := open[i]
		stale.ResolvedAt = &now
		if err := e.rules.UpdateIncident(ctx, stale); err != nil {
			return nil, err
		}
	}
	filters := alert.Rule.Filters
	if release != "" {
		filters.Releases = []string{release}
	}
	from, to := metricWindow(m, now, site.TZ)
	value, ok, err := e.metricValue(ctx, site.ID, m, filters, from, to)
	if err != nil || !ok {
		return nil, err
	}
	info, err := e.app.Site(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	b := metricNotes{alert: alert, info: info, m: m, release: release, value: value, from: from, to: to, now: now}
	severity, firing := evaluateThreshold(m, value)
	if cur == nil {
		if !firing {
			return nil, nil
		}
		created, err := e.rules.OpenIncident(ctx, Incident{AlertID: alert.ID, SiteID: site.ID, GroupKey: group, Severity: severity,
			OpenedAt: now, LastValue: value, PeakValue: value, Threshold: thresholdFor(m, severity)})
		if err != nil {
			return nil, err
		}
		return []Candidate{e.openCandidate(b, &created)}, nil
	}
	if cur.LastValue != value || worse(m, value, cur.PeakValue) {
		cur.LastValue = value
		if worse(m, value, cur.PeakValue) {
			cur.PeakValue = value
		}
		if err := e.rules.UpdateIncident(ctx, *cur); err != nil {
			return nil, err
		}
	}
	if shouldResolve(m, value) {
		return e.resolveCandidates(ctx, b, cur)
	}
	if !firing {
		return nil, nil
	}
	switch {
	case cur.NotifiedAt == nil:
		return []Candidate{e.openCandidate(b, cur)}, nil
	case severity == severityCritical && cur.Severity == severityWarning:
		return []Candidate{e.escalateCandidate(b, cur)}, nil
	case m.RenotifyMinutes > 0:
		last := *cur.NotifiedAt
		if cur.RenotifiedAt != nil && cur.RenotifiedAt.After(last) {
			last = *cur.RenotifiedAt
		}
		due := last.Add(time.Duration(m.RenotifyMinutes) * time.Minute)
		if !now.Before(due) {
			return []Candidate{e.renotifyCandidate(b, cur, due)}, nil
		}
	}
	return nil, nil
}

// worse reports whether value is further past the threshold than peak.
func worse(m MetricTrigger, value, peak float64) bool {
	if m.Op == "below" {
		return value < peak
	}
	return value > peak
}

type metricNotes struct {
	alert    Alert
	info     AppSite
	m        MetricTrigger
	release  string
	value    float64
	from, to time.Time
	now      time.Time
}

func (b metricNotes) note(inc *Incident, severity, status string, threshold float64) Notification {
	return Notification{
		Value: b.value, Threshold: threshold, From: b.from, To: b.to,
		App:  &AppNotification{AppName: b.info.Name, Release: b.release, Metric: b.m.Name},
		Rule: &RuleNotification{ID: b.alert.ID, Name: b.alert.Name, TriggerType: TriggerMetric, Severity: severity, Status: status, IncidentID: inc.ID},
	}
}

func incidentKey(inc *Incident, suffix string) string {
	return "incident:" + strconv.FormatInt(inc.ID, 10) + ":" + suffix
}

func (e *RuleEvaluator) saveIncident(inc *Incident, change func(*Incident)) func(context.Context) error {
	return func(ctx context.Context) error {
		change(inc)
		return e.rules.UpdateIncident(ctx, *inc)
	}
}

func (e *RuleEvaluator) openCandidate(b metricNotes, inc *Incident) Candidate {
	return Candidate{Key: incidentKey(inc, "open"), GroupKey: inc.GroupKey,
		Notification: b.note(inc, inc.Severity, ruleStatusFiring, inc.Threshold),
		Done:         e.saveIncident(inc, func(i *Incident) { i.NotifiedAt = &b.now })}
}

func (e *RuleEvaluator) escalateCandidate(b metricNotes, inc *Incident) Candidate {
	threshold := b.m.Critical
	return Candidate{Key: incidentKey(inc, "escalate"), GroupKey: inc.GroupKey,
		Notification: b.note(inc, severityCritical, ruleStatusFiring, threshold),
		Done: e.saveIncident(inc, func(i *Incident) {
			i.Severity, i.Threshold = severityCritical, threshold
		})}
}

func (e *RuleEvaluator) renotifyCandidate(b metricNotes, inc *Incident, due time.Time) Candidate {
	return Candidate{Key: incidentKey(inc, "renotify:"+strconv.FormatInt(due.Unix(), 10)), GroupKey: inc.GroupKey,
		Notification: b.note(inc, inc.Severity, ruleStatusFiring, inc.Threshold),
		Done:         e.saveIncident(inc, func(i *Incident) { i.RenotifiedAt = &b.now })}
}

// resolveCandidates closes a recovered incident. The resolve notice goes out
// only when the open notice did (a never-sent incident closes quietly) and the
// rule wants it; the incident closes after the notice is committed.
func (e *RuleEvaluator) resolveCandidates(ctx context.Context, b metricNotes, inc *Incident) ([]Candidate, error) {
	closeIt := e.saveIncident(inc, func(i *Incident) { i.ResolvedAt = &b.now })
	notify := b.alert.Rule.NotifyOnResolve == nil || *b.alert.Rule.NotifyOnResolve
	sent := inc.NotifiedAt != nil
	if notify && !sent {
		// The open notice may be committed while its bookkeeping is not.
		seen, err := e.dedup.AppFired(ctx, b.alert.ID, []string{incidentKey(inc, "open")})
		if err != nil {
			return nil, err
		}
		_, sent = seen[incidentKey(inc, "open")]
	}
	if !notify || !sent {
		return nil, closeIt(ctx)
	}
	return []Candidate{{Key: incidentKey(inc, "resolved"), GroupKey: inc.GroupKey,
		Notification: b.note(inc, inc.Severity, ruleStatusResolve, inc.Threshold), Done: closeIt}}, nil
}
