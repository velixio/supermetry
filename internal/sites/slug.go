package sites

import "regexp"

var (
	slugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	publicIDPattern = regexp.MustCompile(`^[a-z0-9]{12}$`)
	reservedSlugs   = map[string]bool{"sites": true, "settings": true, "health": true, "new": true}
)

// ValidSlug reports whether s may be a site slug. It mirrors the sites_slug_format
// table constraint: reserved words name organization pages that share the URL
// segment, and twelve lowercase letters or digits could be mistaken for a public_id.
func ValidSlug(s string) bool {
	return len(s) >= 1 && len(s) <= 63 && slugPattern.MatchString(s) && !reservedSlugs[s] && !publicIDPattern.MatchString(s)
}
