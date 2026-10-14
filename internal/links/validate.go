// Package links implements managed short links and tracking pixels: input
// validation, storage, a batched aggregate writer and the public redirect and
// pixel handlers.
package links

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits shared by validation, the schema and the API.
const (
	MaxDestinationLength = 2048
	MaxSlugLength        = 64
	MinSlugLength        = 2
	MaxNameLength        = 120
	MaxUTMLength         = 120
	MaxCampaignLength    = 64
	MaxLinksPerSite      = 500
)

// ErrInvalid wraps every user-correctable validation failure.
var ErrInvalid = errors.New("invalid link")

var (
	slugPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)
	campaignPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
)

// reserved slugs cannot be claimed so short links never shadow the product's
// own well-known paths or look like administrative pages.
var reserved = map[string]struct{}{
	"api": {}, "app": {}, "admin": {}, "login": {}, "logout": {}, "signup": {}, "setup": {},
	"static": {}, "assets": {}, "health": {}, "healthz": {}, "readyz": {}, "metrics": {},
	"robots": {}, "robots-txt": {}, "favicon": {}, "sitemap": {}, "www": {}, "px": {},
	"null": {}, "undefined": {}, "new": {}, "edit": {}, "delete": {}, "support": {}, "security": {},
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// NormalizeSlug lowercases and validates a slug.
func NormalizeSlug(raw string) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(raw))
	if !slugPattern.MatchString(slug) {
		return "", invalid("slug must be 2 to 64 characters: lowercase letters, digits, hyphen or underscore, starting with a letter or digit")
	}
	if _, blocked := reserved[slug]; blocked {
		return "", invalid("slug %q is reserved", slug)
	}
	return slug, nil
}

// RandomSlug returns a random 7-character slug from an unambiguous alphabet.
func RandomSlug() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	var raw [7]byte
	_, _ = rand.Read(raw[:])
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}

// RandomCampaignTag returns an eight-character lowercase alphanumeric tag.
func RandomCampaignTag() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}

// NormalizeName trims and bounds the display name.
func NormalizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength || hasControl(name) {
		return "", invalid("name must be 1 to %d characters without control characters", MaxNameLength)
	}
	return name, nil
}

// NormalizeDestination accepts only absolute http and https URLs. It rejects
// credentials in the authority, control characters and whitespace, oversized
// values and any other scheme (javascript:, data:, file:, ftp: and so on) so a
// stored destination can never become a script or credential-phishing target.
// selfHost, when set, is the instance's own host: destinations pointing back at
// the short-link path would create redirect loops and are refused.
func NormalizeDestination(raw, selfHost string) (string, error) {
	dest := strings.TrimSpace(raw)
	if dest == "" {
		return "", invalid("destination is required")
	}
	if len(dest) > MaxDestinationLength {
		return "", invalid("destination must be at most %d characters", MaxDestinationLength)
	}
	for _, r := range dest {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", invalid("destination must not contain spaces or control characters")
		}
	}
	parsed, err := url.Parse(dest)
	if err != nil {
		return "", invalid("destination is not a valid URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", invalid("destination must start with http:// or https://")
	}
	if parsed.Opaque != "" || parsed.Hostname() == "" {
		return "", invalid("destination must be an absolute URL with a host")
	}
	if parsed.User != nil {
		return "", invalid("destination must not contain credentials")
	}
	if strings.ContainsAny(parsed.Hostname(), `\`) || strings.HasPrefix(parsed.Hostname(), ".") || strings.HasSuffix(strings.TrimSuffix(parsed.Hostname(), "."), ".") {
		return "", invalid("destination host is invalid")
	}
	if selfHost != "" && strings.EqualFold(parsed.Host, selfHost) &&
		(strings.HasPrefix(parsed.Path, "/l/") || strings.HasPrefix(parsed.Path, "/px/")) {
		return "", invalid("destination must not point back at a short link")
	}
	parsed.Scheme = scheme
	return parsed.String(), nil
}

// NormalizeUTM trims one default UTM value; empty means unset.
func NormalizeUTM(name, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if utf8.RuneCountInString(value) > MaxUTMLength || hasControl(value) {
		return "", invalid("%s must be at most %d characters without control characters", name, MaxUTMLength)
	}
	return value, nil
}

// NormalizeCampaign validates a pixel campaign tag. An empty result means the
// tag is absent or unusable; pixel requests must never fail because of it.
func NormalizeCampaign(raw string) string {
	value := strings.TrimSpace(raw)
	if !campaignPattern.MatchString(value) {
		return ""
	}
	return value
}

// UTM holds the default campaign parameters appended to a destination.
type UTM struct {
	Source, Medium, Campaign, Term, Content string
}

// Apply appends each non-empty default parameter that the destination does not
// already set, leaving the destination's own query string untouched.
func (u UTM) Apply(destination string) string {
	parsed, err := url.Parse(destination)
	if err != nil {
		return destination
	}
	existing := parsed.Query()
	var extra []string
	for _, pair := range [...][2]string{
		{"utm_source", u.Source}, {"utm_medium", u.Medium}, {"utm_campaign", u.Campaign},
		{"utm_term", u.Term}, {"utm_content", u.Content},
	} {
		if pair[1] == "" || existing.Has(pair[0]) {
			continue
		}
		extra = append(extra, url.QueryEscape(pair[0])+"="+url.QueryEscape(pair[1]))
	}
	if len(extra) == 0 {
		return destination
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery += "&"
	}
	parsed.RawQuery += strings.Join(extra, "&")
	return parsed.String()
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
