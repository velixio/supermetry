package config

import (
	"fmt"

	"panel/internal/privacy"
)

// TrackingModeEnv is the proposed environment key. Load does not read it yet.
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
// decision for existing installations. This parser is intentionally not wired
// into Load; its getenv callback cannot distinguish absent from empty.
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
