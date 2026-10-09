package privacy

import (
	"golang.org/x/text/language"
	"strings"
	"time"
)

// AnonymousDimensionAllowed identifies finite-domain labels and time buckets.
// Free-form paths, properties, source/UTM labels and versions are excluded.
func AnonymousDimensionAllowed(dimension string) bool {
	switch dimension {
	case "time", "hour", "country", "device", "browser", "os", "channel":
		return true
	}
	return false
}

// CanonicalDimensionValue returns an allowed value, or the empty unknown bucket.
// Callers must check valid before accepting filter input; output may coalesce
// invalid stored/imported labels to unknown without exposing their contents.
func CanonicalDimensionValue(dimension, value string) (canonical string, valid bool) {
	if !AnonymousDimensionAllowed(dimension) {
		return "", false
	}
	if value == "" {
		return "", true
	}
	switch dimension {
	case "country":
		if len(value) != 2 || value != strings.ToUpper(value) {
			return "", false
		}
		region, err := language.ParseRegion(value)
		if err == nil && region.IsCountry() && region.String() == value {
			return value, true
		}
	case "device":
		return allowedLabel(value, "desktop", "mobile", "tablet")
	case "browser":
		return allowedLabel(value, "Chrome", "Chromium", "Firefox", "Safari", "Edge", "Opera", "Opera Mini", "Opera Touch", "NetFront", "Mozilla", "Internet Explorer", "Samsung Internet", "Samsung Browser", "Android Browser", "UC Browser", "Yandex Browser", "Vivaldi", "Brave", "Headless Chrome", "Facebook", "Instagram", "Facebook App", "Instagram App", "TikTok App")
	case "os":
		return allowedLabel(value, "Windows", "Windows Phone", "macOS", "Mac OS X", "iOS", "Android", "Linux", "ChromeOS", "Chrome OS", "Ubuntu", "FreeBSD", "OpenBSD", "BlackBerry", "Harmony", "Other")
	case "channel":
		return allowedLabel(value, "Direct", "Organic Search", "Paid Search", "Organic Social", "Paid Social", "Email", "Referral", "AI Assistants", "Other")
	case "time":
		if parsed, err := time.Parse(time.DateOnly, value); err == nil && parsed.Format(time.DateOnly) == value {
			return value, true
		}
	case "hour":
		for _, layout := range []string{"2006-01-02T15:00:00", time.RFC3339, "2006-01-02 15:00:00"} {
			if parsed, err := time.Parse(layout, value); err == nil && parsed.Format(layout) == value {
				return value, true
			}
		}
	}
	return "", false
}
func allowedLabel(value string, allowed ...string) (string, bool) {
	for _, label := range allowed {
		if value == label {
			return value, true
		}
	}
	return "", false
}
