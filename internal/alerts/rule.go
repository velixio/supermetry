package alerts

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AlertRule is the kind of a rule-based app alert: its trigger, filters and
// actions live in the alert's rule document.
const AlertRule AlertKind = "app_rule"

// Rule limits.
const (
	MaxRulesPerSite       = 100
	MaxRuleActions        = 10
	MaxRuleTagFilters     = 20
	MaxRuleChannels       = 20
	MinRuleWindowMinutes  = 5
	MaxRuleWindowMinutes  = 1440
	maxRuleFilterValues   = 50
	maxRuleFilterLength   = 200
	maxRuleFrequency      = 10080
	defaultEventFrequency = 60
	defaultMinSample      = 100
)

// Trigger type names.
const (
	TriggerIssue   = "issue"
	TriggerEvent   = "event"
	TriggerMetric  = "metric"
	TriggerMonitor = "monitor"
)

// Rule is the stored document of an app_rule alert. Frequency is per group
// (an issue for event triggers); NotifyOnResolve defaults to true for metric
// and monitor triggers.
type Rule struct {
	Trigger          Trigger  `json:"trigger"`
	Filters          Filters  `json:"filters"`
	Actions          []Action `json:"actions"`
	FrequencyMinutes int      `json:"frequency_minutes,omitempty"`
	NotifyOnResolve  *bool    `json:"notify_on_resolve,omitempty"`
}

type Trigger struct {
	Type    string          `json:"type"`
	Issue   *IssueTrigger   `json:"issue,omitempty"`
	Event   *EventTrigger   `json:"event,omitempty"`
	Metric  *MetricTrigger  `json:"metric,omitempty"`
	Monitor *MonitorTrigger `json:"monitor,omitempty"`
}

type IssueTrigger struct {
	On []string `json:"on"`
}

// EventTrigger fires on the number of events (or distinct users) of one issue
// in a window: either above a count or as a spike against the hourly baseline.
type EventTrigger struct {
	Measure       string `json:"measure,omitempty"`
	WindowMinutes int    `json:"window_minutes,omitempty"`
	Above         int64  `json:"above,omitempty"`
	Spike         *Spike `json:"spike,omitempty"`
}

type Spike struct {
	Factor       float64 `json:"factor"`
	MinEvents    int64   `json:"min_events"`
	BaselineDays int     `json:"baseline_days"`
}

type MetricTrigger struct {
	Name            string   `json:"name"`
	Transaction     string   `json:"transaction,omitempty"`
	Op              string   `json:"op"`
	Critical        float64  `json:"critical"`
	Warning         *float64 `json:"warning,omitempty"`
	Resolve         *float64 `json:"resolve,omitempty"`
	Comparison      string   `json:"comparison,omitempty"`
	ChangeWindow    string   `json:"change_window,omitempty"`
	WindowMinutes   int      `json:"window_minutes"`
	MinSample       int64    `json:"min_sample,omitempty"`
	Release         string   `json:"release,omitempty"`
	RenotifyMinutes int      `json:"renotify_minutes,omitempty"`
}

type MonitorTrigger struct {
	Slugs []string `json:"slugs,omitempty"`
	On    []string `json:"on,omitempty"`
}

type Filters struct {
	Environments []string     `json:"environments,omitempty"`
	Releases     []string     `json:"releases,omitempty"`
	Platforms    []string     `json:"platforms,omitempty"`
	Levels       []string     `json:"levels,omitempty"`
	Kinds        []string     `json:"kinds,omitempty"`
	Tags         []TagFilter  `json:"tags,omitempty"`
	Owner        *OwnerFilter `json:"owner,omitempty"`
}

type TagFilter struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Negate bool   `json:"negate,omitempty"`
}

// OwnerFilter matches issues by assignee; UserID 0 means unassigned.
type OwnerFilter struct {
	UserID int64 `json:"user_id"`
}

type Action struct {
	Type               string  `json:"type"`
	ChannelIDs         []int64 `json:"channel_ids,omitempty"`
	UserID             int64   `json:"user_id,omitempty"`
	FallbackChannelIDs []int64 `json:"fallback_channel_ids,omitempty"`
}

// Action types.
const (
	ActionChannel     = "channel"
	ActionWebhook     = "webhook"
	ActionAssign      = "assign"
	ActionNotifyOwner = "notify_owner"
)

// Metric names.
const (
	MetricErrorCount          = "error_count"
	MetricCrashFreeSessions   = "crash_free_sessions"
	MetricCrashFreeUsers      = "crash_free_users"
	MetricTransactionCount    = "transaction_count"
	MetricTransactionFailRate = "transaction_failure_rate"
)

var (
	metricNames = map[string]bool{
		MetricErrorCount: true, MetricCrashFreeSessions: true, MetricCrashFreeUsers: true,
		MetricTransactionCount: true, MetricTransactionFailRate: true,
		"transaction_p50": true, "transaction_p75": true, "transaction_p95": true, "transaction_p99": true,
	}
	monitorSlugPattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
)

func isTransactionMetric(name string) bool { return strings.HasPrefix(name, "transaction_") }
func isCrashFreeMetric(name string) bool   { return strings.HasPrefix(name, "crash_free_") }

// usesClosedHours reports the metrics that read whole closed hours.
func usesClosedHours(name string) bool { return isTransactionMetric(name) || isCrashFreeMetric(name) }

// ChannelIDs returns the distinct channel ids the rule's actions deliver to,
// in first-seen order. It is what the alert row stores in channel_ids.
func (r Rule) ChannelIDs() []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	add := func(ids []int64) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	for _, a := range r.Actions {
		if a.Type == ActionChannel || a.Type == ActionWebhook {
			add(a.ChannelIDs)
		}
	}
	return out
}

// WebhookChannelIDs returns the channel ids used by webhook actions.
func (r Rule) WebhookChannelIDs() []int64 {
	var out []int64
	for _, a := range r.Actions {
		if a.Type == ActionWebhook {
			out = append(out, a.ChannelIDs...)
		}
	}
	return out
}

func invalidField(path, format string, args ...any) error {
	return fmt.Errorf("%w: %s %s", ErrInvalid, path, fmt.Sprintf(format, args...))
}

// ValidateRule checks a rule and returns it with the defaults filled. Errors
// wrap ErrInvalid and name the offending field path.
func ValidateRule(r Rule) (Rule, error) {
	t := r.Trigger
	subs := 0
	for _, present := range []bool{t.Issue != nil, t.Event != nil, t.Metric != nil, t.Monitor != nil} {
		if present {
			subs++
		}
	}
	if subs != 1 {
		return r, invalidField("trigger", "must carry exactly one of issue, event, metric or monitor")
	}
	switch t.Type {
	case TriggerIssue:
		if t.Issue == nil {
			return r, invalidField("trigger.issue", "is required for type issue")
		}
		if err := validateIssueTrigger(*t.Issue); err != nil {
			return r, err
		}
	case TriggerEvent:
		if t.Event == nil {
			return r, invalidField("trigger.event", "is required for type event")
		}
		event, err := validateEventTrigger(*t.Event)
		if err != nil {
			return r, err
		}
		r.Trigger.Event = &event
	case TriggerMetric:
		if t.Metric == nil {
			return r, invalidField("trigger.metric", "is required for type metric")
		}
		metric, err := validateMetricTrigger(*t.Metric)
		if err != nil {
			return r, err
		}
		r.Trigger.Metric = &metric
	case TriggerMonitor:
		if t.Monitor == nil {
			return r, invalidField("trigger.monitor", "is required for type monitor")
		}
		monitor, err := validateMonitorTrigger(*t.Monitor)
		if err != nil {
			return r, err
		}
		r.Trigger.Monitor = &monitor
	default:
		return r, invalidField("trigger.type", "must be issue, event, metric or monitor")
	}
	incidental := t.Type == TriggerMetric || t.Type == TriggerMonitor
	if err := validateFilters(r.Filters, incidental); err != nil {
		return r, err
	}
	if err := validateActions(r.Actions, incidental); err != nil {
		return r, err
	}
	switch {
	case r.FrequencyMinutes < 0 || r.FrequencyMinutes > maxRuleFrequency:
		return r, invalidField("frequency_minutes", "must be between 0 and %d", maxRuleFrequency)
	case t.Type == TriggerEvent && r.FrequencyMinutes == 0:
		r.FrequencyMinutes = defaultEventFrequency
	case t.Type != TriggerEvent && r.FrequencyMinutes != 0:
		return r, invalidField("frequency_minutes", "applies to event triggers only")
	}
	if incidental && r.NotifyOnResolve == nil {
		yes := true
		r.NotifyOnResolve = &yes
	}
	return r, nil
}

func validateIssueTrigger(t IssueTrigger) error {
	if len(t.On) == 0 {
		return invalidField("trigger.issue.on", "needs at least one of new, regressed, escalated")
	}
	seen := map[string]bool{}
	for _, on := range t.On {
		if (on != "new" && on != "regressed" && on != "escalated") || seen[on] {
			return invalidField("trigger.issue.on", "has an unknown or repeated value %q", on)
		}
		seen[on] = true
	}
	return nil
}

func validateEventTrigger(t EventTrigger) (EventTrigger, error) {
	if t.Measure == "" {
		t.Measure = "events"
	}
	if t.Measure != "events" && t.Measure != "users" {
		return t, invalidField("trigger.event.measure", "must be events or users")
	}
	if t.Spike != nil {
		if t.Measure == "users" {
			return t, invalidField("trigger.event.measure", "spike compares events, not users")
		}
		if t.Above != 0 {
			return t, invalidField("trigger.event.above", "cannot be combined with spike")
		}
		if t.WindowMinutes != 0 {
			return t, invalidField("trigger.event.window_minutes", "does not apply to spike (it compares closed hours)")
		}
		s := *t.Spike
		if s.Factor == 0 {
			s.Factor = spikeFactor
		}
		if s.MinEvents == 0 {
			s.MinEvents = spikeMinEvents
		}
		if s.BaselineDays == 0 {
			s.BaselineDays = spikeBaselineDays
		}
		switch {
		case math.IsNaN(s.Factor) || s.Factor < 1 || s.Factor > 1000:
			return t, invalidField("trigger.event.spike.factor", "must be between 1 and 1000")
		case s.MinEvents < 1:
			return t, invalidField("trigger.event.spike.min_events", "must be at least 1")
		case s.BaselineDays < 1 || s.BaselineDays > 30:
			return t, invalidField("trigger.event.spike.baseline_days", "must be between 1 and 30")
		}
		t.Spike = &s
		return t, nil
	}
	if t.WindowMinutes < MinRuleWindowMinutes || t.WindowMinutes > MaxRuleWindowMinutes {
		return t, invalidField("trigger.event.window_minutes", "must be between %d and %d", MinRuleWindowMinutes, MaxRuleWindowMinutes)
	}
	if t.Above <= 0 {
		return t, invalidField("trigger.event.above", "must be positive (or set spike)")
	}
	return t, nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func validateMetricTrigger(m MetricTrigger) (MetricTrigger, error) {
	const p = "trigger.metric."
	if !metricNames[m.Name] {
		return m, invalidField(p+"name", "is unknown")
	}
	if m.Transaction != "" && !isTransactionMetric(m.Name) {
		return m, invalidField(p+"transaction", "applies to transaction metrics only")
	}
	if len(m.Transaction) > maxRuleFilterLength {
		return m, invalidField(p+"transaction", "is too long")
	}
	if m.Op != "above" && m.Op != "below" {
		return m, invalidField(p+"op", "must be above or below")
	}
	if !finite(m.Critical) {
		return m, invalidField(p+"critical", "must be a number")
	}
	if m.Comparison == "" {
		m.Comparison = "static"
	}
	switch m.Comparison {
	case "static":
		if m.ChangeWindow != "" {
			return m, invalidField(p+"change_window", "applies to comparison change only")
		}
	case "change":
		if m.ChangeWindow == "" {
			m.ChangeWindow = "1d"
		}
		if m.ChangeWindow != "1d" && m.ChangeWindow != "1w" {
			return m, invalidField(p+"change_window", "must be 1d or 1w")
		}
	default:
		return m, invalidField(p+"comparison", "must be static or change")
	}
	if m.Warning != nil {
		if !finite(*m.Warning) || (m.Op == "above" && *m.Warning >= m.Critical) || (m.Op == "below" && *m.Warning <= m.Critical) {
			return m, invalidField(p+"warning", "must be a less severe level than critical")
		}
	}
	if m.Resolve != nil && !finite(*m.Resolve) {
		return m, invalidField(p+"resolve", "must be a number")
	}
	if usesClosedHours(m.Name) {
		if m.WindowMinutes < 60 || m.WindowMinutes > MaxRuleWindowMinutes || m.WindowMinutes%60 != 0 {
			return m, invalidField(p+"window_minutes", "must be whole hours between 60 and %d", MaxRuleWindowMinutes)
		}
	} else if m.WindowMinutes < MinRuleWindowMinutes || m.WindowMinutes > MaxRuleWindowMinutes {
		return m, invalidField(p+"window_minutes", "must be between %d and %d", MinRuleWindowMinutes, MaxRuleWindowMinutes)
	}
	if m.MinSample < 0 {
		return m, invalidField(p+"min_sample", "cannot be negative")
	}
	if m.MinSample == 0 && usesClosedHours(m.Name) {
		m.MinSample = defaultMinSample
	}
	if len(m.Release) > maxRuleFilterLength {
		return m, invalidField(p+"release", "is too long")
	}
	if m.RenotifyMinutes != 0 && (m.RenotifyMinutes < MinRuleWindowMinutes || m.RenotifyMinutes > maxRuleFrequency) {
		return m, invalidField(p+"renotify_minutes", "must be 0 or between %d and %d", MinRuleWindowMinutes, maxRuleFrequency)
	}
	return m, nil
}

func validateMonitorTrigger(m MonitorTrigger) (MonitorTrigger, error) {
	if len(m.Slugs) > 50 {
		return m, invalidField("trigger.monitor.slugs", "has more than 50 entries")
	}
	for _, slug := range m.Slugs {
		if !monitorSlugPattern.MatchString(slug) {
			return m, invalidField("trigger.monitor.slugs", "has an invalid slug %q", slug)
		}
	}
	if len(m.On) == 0 {
		m.On = []string{"missed", "failed", "timeout"}
	}
	seen := map[string]bool{}
	for _, on := range m.On {
		if (on != "missed" && on != "failed" && on != "timeout") || seen[on] {
			return m, invalidField("trigger.monitor.on", "has an unknown or repeated value %q", on)
		}
		seen[on] = true
	}
	return m, nil
}

func validateStringList(path string, values []string, allowed map[string]bool) error {
	if len(values) > maxRuleFilterValues {
		return invalidField(path, "has more than %d entries", maxRuleFilterValues)
	}
	for _, v := range values {
		if v == "" || len(v) > maxRuleFilterLength || (allowed != nil && !allowed[v]) {
			return invalidField(path, "has an invalid value %q", v)
		}
	}
	return nil
}

func validateFilters(f Filters, incidental bool) error {
	lists := []struct {
		path    string
		values  []string
		allowed map[string]bool
	}{
		{"filters.environments", f.Environments, nil}, {"filters.releases", f.Releases, nil},
		{"filters.platforms", f.Platforms, nil}, {"filters.levels", f.Levels, nil}, {"filters.kinds", f.Kinds, appIssueKinds},
	}
	for _, l := range lists {
		if err := validateStringList(l.path, l.values, l.allowed); err != nil {
			return err
		}
	}
	if len(f.Tags) > MaxRuleTagFilters {
		return invalidField("filters.tags", "has more than %d entries", MaxRuleTagFilters)
	}
	for i, tag := range f.Tags {
		if tag.Key == "" || len(tag.Key) > maxRuleFilterLength || len(tag.Value) > maxRuleFilterLength {
			return invalidField("filters.tags["+strconv.Itoa(i)+"]", "needs a key and values of at most %d characters", maxRuleFilterLength)
		}
	}
	if incidental && len(f.Tags) > 0 {
		return invalidField("filters.tags", "do not apply to metric and monitor triggers")
	}
	if incidental && f.Owner != nil {
		return invalidField("filters.owner", "does not apply to metric and monitor triggers")
	}
	if f.Owner != nil && f.Owner.UserID < 0 {
		return invalidField("filters.owner.user_id", "cannot be negative")
	}
	return nil
}

func validateChannelIDs(path string, ids []int64, required bool) error {
	if required && len(ids) == 0 {
		return invalidField(path, "needs at least one channel")
	}
	if len(ids) > MaxRuleChannels {
		return invalidField(path, "has more than %d channels", MaxRuleChannels)
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return invalidField(path, "has an invalid or repeated channel id")
		}
		seen[id] = true
	}
	return nil
}

func validateActions(actions []Action, incidental bool) error {
	if len(actions) == 0 || len(actions) > MaxRuleActions {
		return invalidField("actions", "needs between 1 and %d actions", MaxRuleActions)
	}
	for i, a := range actions {
		path := "actions[" + strconv.Itoa(i) + "]"
		switch a.Type {
		case ActionChannel, ActionWebhook:
			if err := validateChannelIDs(path+".channel_ids", a.ChannelIDs, true); err != nil {
				return err
			}
			if a.UserID != 0 || len(a.FallbackChannelIDs) > 0 {
				return invalidField(path, "carries fields that do not belong to %s", a.Type)
			}
		case ActionAssign:
			if incidental {
				return invalidField(path+".type", "assign needs an issue or event trigger")
			}
			if a.UserID <= 0 || len(a.ChannelIDs) > 0 || len(a.FallbackChannelIDs) > 0 {
				return invalidField(path+".user_id", "is required for assign")
			}
		case ActionNotifyOwner:
			if incidental {
				return invalidField(path+".type", "notify_owner needs an issue or event trigger")
			}
			if a.UserID != 0 || len(a.ChannelIDs) > 0 {
				return invalidField(path, "carries fields that do not belong to notify_owner")
			}
			if err := validateChannelIDs(path+".fallback_channel_ids", a.FallbackChannelIDs, false); err != nil {
				return err
			}
		default:
			return invalidField(path+".type", "must be channel, webhook, assign or notify_owner")
		}
	}
	return nil
}

// RuleNotification is the rule context of a notification. Status is firing or
// resolved; Severity and IncidentID are set for metric and monitor triggers.
type RuleNotification struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	TriggerType string `json:"trigger_type"`
	Severity    string `json:"severity,omitempty"`
	Status      string `json:"status"`
	IncidentID  int64  `json:"incident_id,omitempty"`
	// Key is the dedup key the notification was fired under; PagerDuty events
	// of one keyed candidate share a dedup key derived from it.
	Key string `json:"key,omitempty"`
}

// Incident is one firing group of a metric or monitor rule.
type Incident struct {
	ID           int64      `json:"id"`
	AlertID      int64      `json:"alert_id"`
	SiteID       int32      `json:"site_id"`
	GroupKey     string     `json:"group_key"`
	Severity     string     `json:"severity"`
	OpenedAt     time.Time  `json:"opened_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	LastValue    float64    `json:"last_value"`
	PeakValue    float64    `json:"peak_value"`
	Threshold    float64    `json:"threshold"`
	NotifiedAt   *time.Time `json:"notified_at,omitempty"`
	RenotifiedAt *time.Time `json:"renotified_at,omitempty"`
}
