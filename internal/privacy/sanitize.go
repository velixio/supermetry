package privacy

import "net/url"

// AnonymousURL removes URL credentials, query parameters and fragments. Invalid
// or opaque URLs are discarded. Paths and hosts are retained for analytics and
// can still contain personal data; this is not a universal de-identification
// algorithm. Anonymous collection separately drops custom properties/traits.
func AnonymousURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}
