// Package orgbundle exports an organization's settings as one JSON document
// and recreates them in another organization. The bundle never contains
// secrets: no channel configuration, webhook targets, share-link tokens or
// password hashes, API keys, SMTP settings or user credentials.
package orgbundle

import (
	"encoding/json"
	"regexp"
	"strings"
)

// SchemaVersion of the bundle format. Imports refuse a newer version.
const SchemaVersion = 1

// Kind identifies the document type.
const Kind = "supermetry.org-settings"

// Bundle is the exported document.
type Bundle struct {
	SchemaVersion int            `json:"schema_version"`
	Kind          string         `json:"kind"`
	GeneratedAt   string         `json:"generated_at"`
	Org           BundleOrg      `json:"org"`
	Channels      []Channel      `json:"channels"`
	Sites         []Site         `json:"sites"`
	Members       []Member       `json:"members"`
	Skipped       []SkippedEntry `json:"skipped"`
}

type BundleOrg struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Channel is a notification channel without its configuration: alerts refer to
// it by kind and name, and an import matches channels with the same pair.
type Channel struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type Site struct {
	Name            string          `json:"name"`
	Slug            string          `json:"slug"`
	Kind            string          `json:"kind"`
	Timezone        string          `json:"timezone"`
	RetentionDays   int             `json:"retention_days"`
	AllowedHosts    []string        `json:"allowed_hosts"`
	RevenueCurrency string          `json:"revenue_currency,omitempty"`
	Settings        json.RawMessage `json:"settings"`
	Goals           []Goal          `json:"goals"`
	Funnels         []Funnel        `json:"funnels"`
	Segments        []Segment       `json:"segments"`
	Alerts          []Alert         `json:"alerts"`
	ShareLinks      []ShareLink     `json:"share_links"`
}

type Goal struct {
	Ref             string `json:"ref"`
	Kind            string `json:"kind"`
	Match           string `json:"match"`
	Value           string `json:"value"`
	PropKey         string `json:"prop_key,omitempty"`
	PropValue       string `json:"prop_value,omitempty"`
	Name            string `json:"name"`
	Currency        string `json:"currency,omitempty"`
	ScrollThreshold *int16 `json:"scroll_threshold,omitempty"`
}

type Funnel struct {
	Name  string          `json:"name"`
	Steps json.RawMessage `json:"steps"`
}

type Segment struct {
	Name    string          `json:"name"`
	Filters json.RawMessage `json:"filters"`
}

// Alert refers to its goal by ref and to channels by kind and name.
type Alert struct {
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Threshold float64         `json:"threshold"`
	GoalRef   string          `json:"goal_ref,omitempty"`
	Channels  []ChannelRef    `json:"channels"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config,omitempty"`
}

type ChannelRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// ShareLink is informational: tokens and password hashes are never exported,
// and imports never create public links.
type ShareLink struct {
	CreatedAt   string          `json:"created_at"`
	ExpiresAt   string          `json:"expires_at,omitempty"`
	Revoked     bool            `json:"revoked"`
	HasPassword bool            `json:"has_password"`
	Config      json.RawMessage `json:"config"`
}

type Member struct {
	Email        string   `json:"email"`
	Role         string   `json:"role"`
	SiteSlugs    []string `json:"site_slugs,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	BillingAdmin bool     `json:"billing_admin,omitempty"`
}

// SkippedEntry says what an export left out and why.
type SkippedEntry struct {
	What   string `json:"what"`
	Site   string `json:"site,omitempty"`
	Reason string `json:"reason"`
}

// exportedSettingKeys is the allow-list of site settings that travel in a
// bundle. Everything else (SMTP, link tokens, replay storage, erasure and demo
// state, icon cache) stays behind.
var exportedSettingKeys = map[string]bool{
	"profiles_enabled": true, "heatmaps_enabled": true, "tracker": true, "exclude_paths": true,
	"exclude_ips": true, "exclude_countries": true, "blocked_referrers": true, "strict_origin": true,
	"respect_privacy_signals": true, "app": true, "replay": true,
}

var secretKey = regexp.MustCompile(`(?i)secret|token|password|passwd|key|dsn|hook|smtp|credential|auth|url|endpoint`)

// FilterSettings keeps only allow-listed top-level keys and drops any nested
// key whose name looks secret-bearing. Imports run it again, so a hand-edited
// bundle cannot smuggle settings in.
func FilterSettings(raw json.RawMessage) json.RawMessage {
	var top map[string]any
	if json.Unmarshal(raw, &top) != nil {
		return json.RawMessage(`{}`)
	}
	out := map[string]any{}
	for k, v := range top {
		if exportedSettingKeys[k] {
			out[k] = scrub(v)
		}
	}
	b, _ := json.Marshal(out)
	return b
}

func scrub(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			if secretKey.MatchString(k) && !strings.HasSuffix(strings.ToLower(k), "_enabled") {
				continue
			}
			out[k] = scrub(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = scrub(val)
		}
		return out
	default:
		return v
	}
}
