package platformhttp

import (
	"fmt"
	"net/http"

	"panel/internal/config"
	"panel/internal/privacy"
	coresites "panel/internal/sites"
)

type trackingPolicyResponse struct {
	InstanceMode              string   `json:"instance_mode"`
	ConfiguredProfilesEnabled bool     `json:"configured_profiles_enabled"`
	EffectiveMode             string   `json:"effective_mode"`
	Restrictions              []string `json:"restrictions"`
}

func (h *handler) trackingPolicy(profiles bool) trackingPolicyResponse {
	result := trackingPolicyResponse{InstanceMode: h.deps.TrackingMode.String(), ConfiguredProfilesEnabled: profiles, EffectiveMode: privacy.Anonymous.String(), Restrictions: []string{}}
	if h.deps.TrackingMode != privacy.Identified {
		result.Restrictions = append(result.Restrictions, string(privacy.InstanceAnonymous))
	}
	if !profiles {
		result.Restrictions = append(result.Restrictions, string(privacy.ProjectProfilesDisabled))
	}
	if h.deps.TrackingMode == privacy.Identified && profiles {
		result.EffectiveMode = privacy.Identified.String()
	}
	return result
}

// Historical records are retained for separately authorized erasure. The
// ordinary read surface fails closed because old payloads cannot be proven free
// of identity simply by hiding the profile foreign key.
func (h *handler) allowHistoricalIdentity(w http.ResponseWriter) bool {
	if h.deps.TrackingMode == privacy.Identified {
		return true
	}
	writeProblem(w, http.StatusForbidden, "Forbidden", "historical identity reads are unavailable in anonymous tracking mode; authorized erasure remains available")
	return false
}

func validateReplayPolicy(env config.ReplayEnv, in coresites.ReplaySiteInput) error {
	switch {
	case in.Enabled && !env.Enabled:
		return fmt.Errorf("replay requires instance permission through PANEL_REPLAY_ENABLED")
	case in.Mask != nil && env.Mask != nil && *env.Mask && !*in.Mask:
		return fmt.Errorf("replay masking is required by the instance")
	case in.SampleRate != nil && env.SampleRate != nil && *in.SampleRate > *env.SampleRate:
		return fmt.Errorf("replay sample rate exceeds the instance cap")
	case in.RetentionDays != nil && env.Retention != nil && *in.RetentionDays > *env.Retention:
		return fmt.Errorf("replay retention exceeds the instance cap")
	case in.QuotaBytes != nil && env.QuotaBytes != nil && *in.QuotaBytes > *env.QuotaBytes:
		return fmt.Errorf("replay quota exceeds the instance cap")
	}
	return nil
}
