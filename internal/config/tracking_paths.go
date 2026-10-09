package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"supermetry/internal/privacy"
)

// TrackingPublicPathsEnv declares exact operator-reviewed public path literals.
const TrackingPublicPathsEnv = "SUPERMETRY_TRACKING_PUBLIC_PATHS"

// ParseTrackingPublicPaths reads an immutable startup policy. Absence and []
// both deny all paths; explicit empty, null, or malformed values are errors.
// Canonical path validation and collection bounds belong to privacy.NewPathPolicy.
func ParseTrackingPublicPaths(raw string, present bool) (privacy.PathPolicy, error) {
	if !present {
		return privacy.PathPolicy{}, nil
	}
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return privacy.PathPolicy{}, fmt.Errorf("%s must be a JSON array of exact canonical public path literals", TrackingPublicPathsEnv)
	}
	var paths []string
	if err := json.Unmarshal([]byte(trimmed), &paths); err != nil {
		return privacy.PathPolicy{}, fmt.Errorf("%s must be a JSON array of strings", TrackingPublicPathsEnv)
	}
	policy, err := privacy.NewPathPolicy(paths)
	if err != nil {
		return privacy.PathPolicy{}, fmt.Errorf("%s: %w", TrackingPublicPathsEnv, err)
	}
	return policy, nil
}
