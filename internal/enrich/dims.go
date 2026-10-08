package enrich

import (
	"net"
	"strconv"
	"strings"
)

var screenLimits = [...]int{768, 1024, 1440, 1920}

// ScreenBucket maps a viewport width to 0 unknown, 1 mobile, 2 tablet,
// 3 laptop, 4 desktop or 5 large.
func ScreenBucket(width int) int16 {
	if width <= 0 {
		return 0
	}
	for i, limit := range screenLimits {
		if width < limit {
			return int16(i + 1)
		}
	}
	return 5
}

// PrimaryLanguage returns the lowercase primary subtag (2 or 3 ASCII letters)
// of the most preferred Accept-Language entry, or "" when none is usable.
// Only the first 16 entries are read, which bounds the work.
func PrimaryLanguage(header string) string {
	best, bestQ := "", 0.0
	for i, entry := range strings.SplitN(header, ",", 17) {
		if i == 16 {
			break
		}
		tag, params, _ := strings.Cut(strings.TrimSpace(entry), ";")
		q := 1.0
		if param := strings.TrimSpace(params); param != "" {
			value, ok := strings.CutPrefix(param, "q=")
			parsed, err := strconv.ParseFloat(value, 64)
			if !ok || err != nil || parsed < 0 || parsed > 1 {
				continue
			}
			q = parsed
		}
		primary, _, _ := strings.Cut(strings.ReplaceAll(tag, "_", "-"), "-")
		primary = strings.ToLower(primary)
		if q > bestQ && isLanguageSubtag(primary) {
			best, bestQ = primary, q
		}
	}
	return best
}

func isLanguageSubtag(value string) bool {
	return len(value) >= 2 && len(value) <= 3 && strings.Trim(value, "abcdefghijklmnopqrstuvwxyz") == ""
}

// HostLabel lowercases a URL host and strips any port and trailing dot.
func HostLabel(host string) string {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
