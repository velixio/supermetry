package sites

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ReplaySiteInput is the stored form of a site's replay settings. Pointer
// fields distinguish an explicit UI value from an unset value.
type ReplaySiteInput struct {
	Enabled       bool                `json:"enabled"`
	Mask          *bool               `json:"mask,omitempty"`
	SampleRate    *int                `json:"sample_rate,omitempty"`
	RetentionDays *int                `json:"retention_days,omitempty"`
	QuotaBytes    *int64              `json:"quota_bytes,omitempty"`
	Triggers      ReplayTriggersInput `json:"triggers,omitempty"`
	Consent       *string             `json:"consent,omitempty"`

	// OrgRetentionCapDays is the owning organization's replay retention cap.
	// It is not part of the stored per-site document: loaders copy it from
	// orgs.replay_retention_cap_days so every resolver sees the same ceiling.
	OrgRetentionCapDays *int `json:"-"`
}

type ReplayTriggersInput struct {
	Error       *bool    `json:"error,omitempty"`
	RageClick   *bool    `json:"rage_click,omitempty"`
	URL         *bool    `json:"url,omitempty"`
	Manual      *bool    `json:"manual,omitempty"`
	URLPatterns []string `json:"url_patterns,omitempty"`
}

// ParseReplaySiteInput strictly decodes stored or submitted replay settings.
// Empty input gives the zero input, which resolves to defaults.
func ParseReplaySiteInput(raw []byte) (ReplaySiteInput, error) {
	var in ReplaySiteInput
	if len(bytes.TrimSpace(raw)) == 0 {
		return in, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return ReplaySiteInput{}, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ReplaySiteInput{}, errors.New("unexpected data after replay settings")
	}
	if in.SampleRate != nil && (*in.SampleRate < 0 || *in.SampleRate > 100) {
		return ReplaySiteInput{}, fmt.Errorf("sample_rate must be from 0 to 100")
	}
	if in.RetentionDays != nil && (*in.RetentionDays < 1 || *in.RetentionDays > 365) {
		return ReplaySiteInput{}, fmt.Errorf("retention_days must be from 1 to 365")
	}
	if in.QuotaBytes != nil && *in.QuotaBytes <= 0 {
		return ReplaySiteInput{}, errors.New("quota_bytes must be positive")
	}
	if in.Consent != nil && *in.Consent != "auto" && *in.Consent != "always" && *in.Consent != "never" {
		return ReplaySiteInput{}, errors.New("consent must be auto, always or never")
	}
	return in, nil
}
