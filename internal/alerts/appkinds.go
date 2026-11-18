package alerts

import "supermetry/internal/sites"

// App alert kinds apply to app projects only; the traffic and goal kinds apply
// to web and pixel sites only.
const (
	AlertAppNewIssue       AlertKind = "app_new_issue"
	AlertAppIssueRegressed AlertKind = "app_issue_regressed"
	AlertAppCrashFreeBelow AlertKind = "app_crash_free_below"
	AlertAppIssueSpike     AlertKind = "app_issue_spike"
)

// Crash-free alert limits and defaults.
const (
	DefaultCrashFreeThreshold = 99.0
	MinCrashFreeThreshold     = 50.0
	MaxCrashFreeThreshold     = 100.0
	MetricUsers               = "users"
	MetricSessions            = "sessions"
)

// AlertConfig holds the per-kind settings that do not fit the threshold:
// Kinds filters app_new_issue by issue kind (empty means every kind); Metric
// picks the crash-free formula of app_crash_free_below.
type AlertConfig struct {
	Kinds  []string `json:"kinds,omitempty"`
	Metric string   `json:"metric,omitempty"`
}

var appIssueKinds = map[string]bool{
	"crash": true, "error": true, "hang": true, "install_failed": true,
	"install_stalled": true, "incompatibility": true, "operation_failed": true,
}

// IsApp reports whether the kind is one of the four app kinds.
func (k AlertKind) IsApp() bool {
	switch k {
	case AlertAppNewIssue, AlertAppIssueRegressed, AlertAppCrashFreeBelow, AlertAppIssueSpike:
		return true
	}
	return false
}

// AllowedOnSiteKind reports whether an alert kind may be created on a site of
// the given kind (an empty or unknown site kind counts as a web site).
func (k AlertKind) AllowedOnSiteKind(siteKind string) bool {
	if siteKind == sites.KindApp {
		return k.IsApp() || k == AlertRule
	}
	switch k {
	case AlertTrafficSpike, AlertTrafficDrop, AlertGoalReached:
		return true
	}
	return false
}

// NormalizeAppInput validates the app-specific shape of an input and fills the
// defaults: crash-free threshold 99 and metric users; kinds that have no
// threshold store 1 (the column needs a positive value). It returns false for
// an invalid combination.
func NormalizeAppInput(input AlertInput) (AlertInput, bool) {
	if !input.Kind.IsApp() || input.GoalID != nil {
		return input, false
	}
	cfg := AlertConfig{}
	if input.Config != nil {
		cfg = *input.Config
	}
	switch input.Kind {
	case AlertAppNewIssue:
		if cfg.Metric != "" {
			return input, false
		}
		seen := map[string]bool{}
		for _, k := range cfg.Kinds {
			if !appIssueKinds[k] || seen[k] {
				return input, false
			}
			seen[k] = true
		}
		input.Threshold = 1
	case AlertAppCrashFreeBelow:
		if len(cfg.Kinds) > 0 {
			return input, false
		}
		if cfg.Metric == "" {
			cfg.Metric = MetricUsers
		}
		if cfg.Metric != MetricUsers && cfg.Metric != MetricSessions {
			return input, false
		}
		if input.Threshold == 0 {
			input.Threshold = DefaultCrashFreeThreshold
		}
		if input.Threshold < MinCrashFreeThreshold || input.Threshold > MaxCrashFreeThreshold {
			return input, false
		}
	default:
		if len(cfg.Kinds) > 0 || cfg.Metric != "" {
			return input, false
		}
		input.Threshold = 1
	}
	input.Config = &cfg
	return input, true
}
