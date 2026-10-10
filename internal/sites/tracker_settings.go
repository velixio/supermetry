package sites

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// TrackerSettingsInput is the stored form of a site's tracker switches. A nil
// field means the owner never set it, so the default (or, for Clicks, the
// site's heatmaps setting) applies.
type TrackerSettingsInput struct {
	Outbound  *bool `json:"outbound,omitempty"`
	Downloads *bool `json:"downloads,omitempty"`
	Forms     *bool `json:"forms,omitempty"`
	Vitals    *bool `json:"vitals,omitempty"`
	NotFound  *bool `json:"not_found,omitempty"`
	Hash      *bool `json:"hash,omitempty"`
	Clicks    *bool `json:"clicks,omitempty"`
}

// TrackerSettings is the resolved form used to build a site's tracker script.
type TrackerSettings struct {
	Outbound  bool `json:"outbound"`
	Downloads bool `json:"downloads"`
	Forms     bool `json:"forms"`
	Vitals    bool `json:"vitals"`
	NotFound  bool `json:"not_found"`
	Hash      bool `json:"hash"`
	Clicks    bool `json:"clicks"`
}

// ParseTrackerSettings strictly decodes stored or submitted settings: unknown
// fields, non-boolean values and trailing data are errors. Empty input gives
// the zero input, which resolves to the defaults.
func ParseTrackerSettings(raw []byte) (TrackerSettingsInput, error) {
	var in TrackerSettingsInput
	if len(bytes.TrimSpace(raw)) == 0 {
		return in, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return TrackerSettingsInput{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return TrackerSettingsInput{}, errors.New("unexpected data after tracker settings")
	}
	return in, nil
}

// Resolve applies defaults: everything on except hash routing, with heatmap
// clicks following the site's heatmaps setting unless explicitly set.
func (in TrackerSettingsInput) Resolve(heatmapsEnabled bool) TrackerSettings {
	pick := func(value *bool, fallback bool) bool {
		if value != nil {
			return *value
		}
		return fallback
	}
	return TrackerSettings{
		Outbound:  pick(in.Outbound, true),
		Downloads: pick(in.Downloads, true),
		Forms:     pick(in.Forms, true),
		Vitals:    pick(in.Vitals, true),
		NotFound:  pick(in.NotFound, true),
		Hash:      pick(in.Hash, false),
		Clicks:    pick(in.Clicks, heatmapsEnabled),
	}
}

// AnyExtension reports whether the full tracker (with its extensions) is needed.
func (s TrackerSettings) AnyExtension() bool {
	return s.Outbound || s.Downloads || s.Forms || s.Vitals || s.NotFound || s.Hash || s.Clicks
}
