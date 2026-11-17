package otlpingest

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Parameterisation here is written from the behaviour the plan describes
// (literals become "?", variable path segments become "*"); it is not derived
// from any other project's normaliser.

var sqlListRun = regexp.MustCompile(`\?(?:\s*,\s*\?)+`)

// parameteriseSQL replaces string and numeric literals with "?", drops
// comments and collapses literal lists, so a stored statement never holds a
// value from the application's data.
func parameteriseSQL(q string) string {
	var b strings.Builder
	b.Grow(len(q))
	n := len(q)
	for i := 0; i < n; {
		c := q[i]
		switch {
		case c == '\'':
			i++
			for i < n {
				if q[i] == '\'' {
					if i+1 < n && q[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			b.WriteByte('?')
		case c == '-' && i+1 < n && q[i+1] == '-':
			for i < n && q[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && q[i+1] == '*':
			end := strings.Index(q[i+2:], "*/")
			if end < 0 {
				i = n
			} else {
				i += end + 4
			}
		case c == '$' && i+1 < n && q[i+1] == '$':
			end := strings.Index(q[i+2:], "$$")
			if end < 0 {
				i = n
			} else {
				i += end + 4
			}
			b.WriteByte('?')
		case c >= '0' && c <= '9' && !prevIdent(q, i):
			for i < n && (isDigit(q[i]) || q[i] == '.' || q[i] == 'e' || q[i] == 'E' || q[i] == 'x' || (q[i] >= 'a' && q[i] <= 'f') || (q[i] >= 'A' && q[i] <= 'F')) {
				i++
			}
			b.WriteByte('?')
		case c == '"':
			// quoted identifier: keep as is
			j := i + 1
			for j < n && q[j] != '"' {
				j++
			}
			if j < n {
				j++
			}
			b.WriteString(q[i:j])
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	out := sqlListRun.ReplaceAllString(strings.Join(strings.Fields(b.String()), " "), "?")
	return clip(out, 1000)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func prevIdent(s string, i int) bool {
	if i == 0 {
		return false
	}
	p := s[i-1]
	return p == '_' || p == '$' || (p >= 'a' && p <= 'z') || (p >= 'A' && p <= 'Z') || p >= 0x80
}

// paramSegment reports whether a path segment looks like a value (an id, a
// uuid, a token, an address) rather than a route word.
func paramSegment(s string) bool {
	if s == "" {
		return false
	}
	allDigits, allHex, hasDigit := true, true, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		d := isDigit(c)
		hasDigit = hasDigit || d
		if !d {
			allDigits = false
		}
		if !d && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') && c != '-' {
			allHex = false
		}
		if c == '@' || c == '%' || c == ':' {
			return true
		}
	}
	switch {
	case allDigits:
		return true
	case allHex && len(s) >= 8:
		return true // hex ids and uuids
	case len(s) >= 20 && hasDigit:
		return true // long mixed tokens
	}
	return false
}

// parameterisePath replaces variable segments of a URL path with "*" and
// drops any query or fragment.
func parameterisePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	parts := strings.Split(p, "/")
	for i, s := range parts {
		if paramSegment(s) {
			parts[i] = "*"
		}
	}
	return strings.Join(parts, "/")
}

// parameteriseURL reduces a URL to scheme://host/path with variable segments
// replaced; user info, query and fragment are dropped. ok is false when raw is
// not an absolute URL.
func parameteriseURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host + parameterisePath(u.EscapedPath()), true
}

// parameteriseName parameterises a span name that may embed a path ("GET
// /users/42?x=1"): words stay, path tokens lose variable segments.
func parameteriseName(name string) string {
	fields := strings.Fields(name)
	for i, f := range fields {
		if strings.HasPrefix(f, "/") {
			fields[i] = parameterisePath(f)
		} else if strings.Contains(f, "://") {
			if p, ok := parameteriseURL(f); ok {
				fields[i] = p
			}
		}
	}
	return clip(strings.Join(fields, " "), 200)
}

// clip cuts s to at most max bytes on a rune boundary.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
