package config

import (
	"fmt"

	"panel/internal/privacy"
)

// TrackingModeEnv selects the instance identity collection and read policy.
const TrackingModeEnv = "PANEL_TRACKING_MODE"

// TrackingMode retains presence so rollout code can distinguish an unset value
// from an explicit anonymous choice. The zero value is unset and anonymous.
type TrackingMode struct {
	Mode     privacy.Mode
	Explicit bool
}

// ParseTrackingMode accepts the pair returned by os.LookupEnv, without reading
// the process environment. An explicitly empty, misspelled or noncanonical value
// is invalid. An absent value yields anonymous without implying a rollout
// decision for existing installations. LoadLookup preserves the presence bit;
// startup must call ValidateUpgrade before accepting requests.
func ParseTrackingMode(raw string, present bool) (TrackingMode, error) {
	if !present {
		return TrackingMode{}, nil
	}
	switch raw {
	case "anonymous":
		return TrackingMode{Mode: privacy.Anonymous, Explicit: true}, nil
	case "identified":
		return TrackingMode{Mode: privacy.Identified, Explicit: true}, nil
	default:
		return TrackingMode{}, fmt.Errorf("%s must be anonymous or identified", TrackingModeEnv)
	}
}

// ValidateUpgrade requires an explicit choice when existing sites collect
// profiles. Explicit anonymous is an intentional downgrade, not an erasure.
func (m TrackingMode) ValidateUpgrade(hasProfileSites bool) error {
	if m.Mode != privacy.Anonymous && m.Mode != privacy.Identified {
		return fmt.Errorf("%s must be anonymous or identified", TrackingModeEnv)
	}
	if !m.Explicit && (hasProfileSites || m.Mode == privacy.Identified) {
		return fmt.Errorf("existing profile-enabled sites require explicit %s=identified to continue identity collection, or %s=anonymous to disable new identity collection and ordinary historical identity reads without deleting data", TrackingModeEnv, TrackingModeEnv)
	}
	return nil
}
