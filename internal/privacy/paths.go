package privacy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PathSanitizerVersion describes only the exact public-path substitution below.
// It makes no assertion about other event fields or operator-declared literals.
const PathSanitizerVersion int16 = 1

// RedactedPath is the fixed label used for every path not declared public.
const RedactedPath = "/[redacted]"

const (
	maxPublicPaths = 128
	maxPathBytes   = 512
)

// PathValue is server-established evidence for one sanitized path, not a
// client-supplied assertion that an entire record is anonymous.
type PathValue struct {
	Path    string
	Version int16
	Policy  string
}

// PathPolicy is immutable after construction and safe for concurrent reads.
// Its zero value declares no paths public and has a valid policy fingerprint.
type PathPolicy struct {
	public      map[string]struct{}
	fingerprint string
}

// NewPathPolicy compiles exact operator-declared public literals. No wildcard,
// pattern matching, or inference about identifiers is performed. The caller is
// responsible for ensuring that each declared literal is suitable for exposure.
// Limits are 128 entries and 512 UTF-8 bytes per path. Trailing slashes
// are allowed and significant; dot segments and repeated slashes are rejected.
func NewPathPolicy(paths []string) (PathPolicy, error) {
	if len(paths) > maxPublicPaths {
		return PathPolicy{}, fmt.Errorf("public path policy permits at most %d entries", maxPublicPaths)
	}
	public := make(map[string]struct{}, len(paths))
	for i, literal := range paths {
		if !canonicalPublicPath(literal) {
			return PathPolicy{}, fmt.Errorf("public path entry %d must be a canonical absolute literal path of at most %d bytes without encoding, query, fragment or wildcard", i+1, maxPathBytes)
		}
		public[literal] = struct{}{}
	}
	ordered := make([]string, 0, len(public))
	for literal := range public {
		ordered = append(ordered, literal)
	}
	sort.Strings(ordered)
	return PathPolicy{public: public, fingerprint: pathFingerprint(ordered)}, nil
}

// Fingerprint identifies the sanitizer version and exact public literal set.
// It is deterministic across order and duplicate entries; it is not a secret.
func (p PathPolicy) Fingerprint() string {
	if p.fingerprint != "" {
		return p.fingerprint
	}
	return emptyPathFingerprint
}

var emptyPathFingerprint = pathFingerprint(nil)

func pathFingerprint(paths []string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("panel/path-sanitizer/v%d\n", PathSanitizerVersion) + strings.Join(paths, "\n")))
	return hex.EncodeToString(digest[:])
}

// SanitizePath emits only an exact public literal or the fixed fallback. URL
// credentials, host, query and fragment are never included. Percent-encoded
// paths are rejected rather than normalized into an allowed literal. Callers
// must evaluate exclusions on the original URL before replacing its path.
func (p PathPolicy) SanitizePath(rawURL string) PathValue {
	value := PathValue{Path: RedactedPath, Version: PathSanitizerVersion, Policy: p.Fingerprint()}
	u, err := url.Parse(rawURL)
	if err != nil || rawURL == "" || u.Opaque != "" || strings.Contains(rawPath(rawURL, u.Scheme), "%") {
		return value
	}
	if u.Scheme != "" && ((!strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https")) || u.Hostname() == "") {
		return value
	}
	literal := u.Path
	if literal == "" && u.Hostname() != "" {
		literal = "/"
	}
	if canonicalPublicPath(literal) {
		value.Path = p.PublicPath(literal)
	}
	return value
}

// url.URL.RawPath is optional: canonical escapes can be decoded with RawPath
// empty. Inspect the original path while excluding credentials/query/fragment.
func rawPath(rawURL, scheme string) string {
	value, _, _ := strings.Cut(rawURL, "#")
	value, _, _ = strings.Cut(value, "?")
	if scheme != "" {
		value = value[len(scheme)+1:]
	}
	if strings.HasPrefix(value, "//") {
		value = value[2:]
		if slash := strings.IndexByte(value, '/'); slash >= 0 {
			return value[slash:]
		}
		return ""
	}
	return value
}

// PublicPath validates an output label against the current policy. Removed or
// unapproved stored labels map to the fixed fallback, without trusting historic
// policy evidence. It does not itself establish collection provenance.
func (p PathPolicy) PublicPath(literal string) string {
	if _, ok := p.public[literal]; ok {
		return literal
	}
	return RedactedPath
}

func canonicalPublicPath(literal string) bool {
	if !utf8.ValidString(literal) || literal == "" || !strings.HasPrefix(literal, "/") || len(literal) > maxPathBytes || strings.ContainsAny(literal, "?#%\\*") || strings.Contains(literal, "//") {
		return false
	}
	for _, r := range literal {
		if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	canonical := literal
	if literal != "/" {
		canonical = strings.TrimSuffix(literal, "/")
	}
	return path.Clean(literal) == canonical
}
