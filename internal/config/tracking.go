package config

import (
	"fmt"

	"supermetry/internal/privacy"
)

// TrackingModeEnv selects the instance identity collection and read policy.
const TrackingModeEnv = "SUPERMETRY_TRACKING_MODE"

// TrackingMode retains presence so the server can tell an explicit choice from the default.
// ParseTrackingMode returns identified, not explicit, when the variable is unset. The zero
// value (anonymous, not explicit) is not produced by parsing; it only appears in struct literals.
type TrackingMode struct {
	Mode     privacy.Mode
	Explicit bool
}

// ParseTrackingMode accepts the pair returned by os.LookupEnv, without reading
// the process environment. An explicitly empty, misspelled or noncanonical value
// is invalid. An absent value yields identified: Supermetry collects page paths,
// referrers and campaigns by default, and an operator who wants the strictest mode
// sets SUPERMETRY_TRACKING_MODE=anonymous. Explicit records whether the choice was made.
func ParseTrackingMode(raw string, present bool) (TrackingMode, error) {
	if !present {
		return TrackingMode{Mode: privacy.Identified}, nil
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

// Validate rejects a mode that is neither anonymous nor identified.
func (m TrackingMode) Validate() error {
	if m.Mode != privacy.Anonymous && m.Mode != privacy.Identified {
		return fmt.Errorf("%s must be anonymous or identified", TrackingModeEnv)
	}
	return nil
}
