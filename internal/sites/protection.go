package sites

import (
	"fmt"
	"regexp"
	"strings"
)

const MaxBlockedReferrers = 200

var hostnameLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeBlockedReferrers validates and de-duplicates domain suffixes.
func NormalizeBlockedReferrers(values []string, limit int) ([]string, error) {
	if limit > 0 && len(values) > limit {
		return nil, fmt.Errorf("too many blocked referrers (maximum %d)", limit)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		host := strings.ToLower(strings.TrimSpace(raw))
		if !validProtectionHostname(host) {
			return nil, fmt.Errorf("invalid blocked referrer %q", raw)
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		result = append(result, host)
	}
	return result, nil
}

func validProtectionHostname(host string) bool {
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.ContainsAny(host, ":/?#@*") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !hostnameLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
