package sites

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultMaxReportAgeDays = 7
	maxReportAgeDaysLimit   = 30
	maxTagKeys              = 50
	maxEnvironments         = 20
)

var (
	tagKeyPattern      = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,39}$`)
	environmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)
)

// AppSettings is the per-project configuration of an app project.
type AppSettings struct {
	TagAllowlist     []string `json:"tag_allowlist"`
	MaxReportAgeDays int      `json:"max_report_age_days"`
	Environments     []string `json:"environments"`
}

// ParseAppSettings decodes stored settings; empty or missing input gives the defaults.
func ParseAppSettings(raw []byte) (AppSettings, error) {
	var settings AppSettings
	if trimmed := strings.TrimSpace(string(raw)); trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return AppSettings{}, fmt.Errorf("parse app settings: %w", err)
		}
	}
	return settings.Normalize()
}

// Normalize lower-cases, trims, dedupes and sorts the lists, applies the
// default report age and validates every field.
func (a AppSettings) Normalize() (AppSettings, error) {
	var err error
	if a.TagAllowlist, err = normalizeList(a.TagAllowlist, "tag_allowlist", maxTagKeys, tagKeyPattern); err != nil {
		return AppSettings{}, err
	}
	if a.Environments, err = normalizeList(a.Environments, "environments", maxEnvironments, environmentPattern); err != nil {
		return AppSettings{}, err
	}
	if a.MaxReportAgeDays == 0 {
		a.MaxReportAgeDays = defaultMaxReportAgeDays
	}
	if a.MaxReportAgeDays < 1 || a.MaxReportAgeDays > maxReportAgeDaysLimit {
		return AppSettings{}, fmt.Errorf("max_report_age_days must be between 1 and %d", maxReportAgeDaysLimit)
	}
	return a, nil
}

func normalizeList(in []string, field string, limit int, pattern *regexp.Regexp) ([]string, error) {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.ToLower(strings.TrimSpace(item))
		if _, dup := seen[item]; dup {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	if len(out) > limit {
		return nil, fmt.Errorf("%s allows at most %d entries", field, limit)
	}
	for _, item := range out {
		if !pattern.MatchString(item) {
			return nil, fmt.Errorf("%s entry %q is not valid", field, item)
		}
	}
	return out, nil
}
