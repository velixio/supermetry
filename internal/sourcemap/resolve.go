package sourcemap

import (
	"bytes"
	"strings"
)

var debugIDMarker = []byte("//# debugId=")

// NormalizeDebugID returns the lowercase dashed UUID form of s, accepting
// the dashless form, or "" when s is not a UUID.
func NormalizeDebugID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 32 {
		s = s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
	}
	if len(s) != 36 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if s[i] != '-' {
				return ""
			}
		case (s[i] >= '0' && s[i] <= '9') || (s[i] >= 'a' && s[i] <= 'f'):
		default:
			return ""
		}
	}
	return s
}

// DebugIDFromSource returns the debug id of a generated JavaScript file from
// its last "//# debugId=<uuid>" comment, or "".
func DebugIDFromSource(b []byte) string {
	i := bytes.LastIndex(b, debugIDMarker)
	if i < 0 {
		return ""
	}
	v := b[i+len(debugIDMarker):]
	end := len(v)
	for j, c := range v {
		if c == '\n' || c == '\r' || c == ' ' || c == '\t' {
			end = j
			break
		}
	}
	return NormalizeDebugID(string(v[:end]))
}
