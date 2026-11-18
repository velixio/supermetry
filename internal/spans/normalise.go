package spans

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Size limits shared by the front doors.
const (
	MaxDescription = 1000
	MaxNameBytes   = 200
	maxAttrs       = 10
	maxAttrValue   = 200
	maxNormInput   = 8 << 10
	// OtherName replaces transaction names past the per-day cardinality cap.
	OtherName = "<other>"
)

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// ParameteriseSQL replaces string and numeric literals with "?", collapses
// "IN (?, ?, ...)" to "IN (?)", drops comments and collapses whitespace. The
// result is at most MaxDescription bytes. Identifiers, quoted identifiers and
// $n placeholders are kept.
func ParameteriseSQL(s string) string {
	s = clip(s, maxNormInput)
	out := make([]byte, 0, min(len(s), MaxDescription+8))
	space := false
	emit := func(b ...byte) {
		if space && len(out) > 0 {
			out = append(out, ' ')
		}
		space = false
		out = append(out, b...)
	}
	for i := 0; i < len(s) && len(out) <= MaxDescription; {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			space = true
			i++
		case c == '\'':
			i++
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			emit('?')
		case c == '$' && i+1 < len(s) && s[i+1] == '$':
			end := strings.Index(s[i+2:], "$$")
			if end < 0 {
				i = len(s)
			} else {
				i += end + 4
			}
			emit('?')
		case c == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				j++
			}
			if j < len(s) {
				j++
			}
			emit([]byte(s[i:j])...)
			i = j
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			space = true
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				i = len(s)
			} else {
				i += end + 4
			}
			space = true
		case c >= '0' && c <= '9':
			i = skipNumber(s, i)
			emit('?')
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80 || c == '$':
			j := i + 1
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			emit([]byte(s[i:j])...)
			i = j
		default:
			emit(c)
			i++
		}
	}
	res := string(out)
	if strings.Contains(res, "?, ?") {
		res = collapseIn(res)
	}
	return clip(res, MaxDescription)
}

// skipNumber returns the index after the numeric literal starting at i.
func skipNumber(s string, i int) int {
	if s[i] == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X') {
		i += 2
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f' || s[i] >= 'A' && s[i] <= 'F') {
			i++
		}
		return i
	}
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			i = j
		}
	}
	return i
}

// collapseIn rewrites "in (?, ?, ?)" to "in (?)".
func collapseIn(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for {
		p := strings.Index(s, "(?")
		if p < 0 {
			break
		}
		// The text before "(" must end with the word IN.
		head := strings.TrimRight(s[:p], " ")
		isIn := len(head) >= 2 && strings.EqualFold(head[len(head)-2:], "in") && (len(head) == 2 || !isIdentByte(head[len(head)-3]))
		j := p + 2
		for isIn && strings.HasPrefix(s[j:], ", ?") {
			j += 3
		}
		if isIn && j > p+2 && j < len(s) && s[j] == ')' {
			b.WriteString(s[:p])
			b.WriteString("(?)")
			s = s[j+1:]
			continue
		}
		b.WriteString(s[:p+2])
		s = s[p+2:]
	}
	b.WriteString(s)
	return b.String()
}

// ParameteriseURL removes credentials, query and fragment from a URL or path
// and replaces identifier-like path segments (digits, UUIDs, hex of 12 or more
// characters, anything holding an "@") with "*". raw may carry a leading
// method ("GET https://..."); an explicit method wins. A relative path stays
// relative.
func ParameteriseURL(method, raw string) string {
	raw = strings.TrimSpace(clip(raw, 4096))
	if sp := strings.IndexByte(raw, ' '); sp >= 2 && sp <= 10 && isUpper(raw[:sp]) {
		if method == "" {
			method = raw[:sp]
		}
		raw = strings.TrimSpace(raw[sp+1:])
	}
	if i := strings.IndexAny(raw, "#?"); i >= 0 {
		raw = raw[:i]
	}
	prefix := ""
	if i := strings.Index(raw, "://"); i >= 0 && i <= 10 {
		rest := raw[i+3:]
		authEnd := strings.IndexByte(rest, '/')
		if authEnd < 0 {
			authEnd = len(rest)
		}
		auth := rest[:authEnd]
		if at := strings.LastIndexByte(auth, '@'); at >= 0 {
			auth = auth[at+1:]
		}
		prefix = raw[:i+3] + auth
		raw = rest[authEnd:]
	}
	var b strings.Builder
	if method != "" {
		b.WriteString(clip(method, 10))
		b.WriteByte(' ')
	}
	b.WriteString(prefix)
	for i, seg := range strings.Split(raw, "/") {
		if i > 0 {
			b.WriteByte('/')
		}
		if isIDSegment(seg) {
			seg = "*"
		}
		b.WriteString(seg)
	}
	return clip(b.String(), MaxDescription)
}

func isUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return s != ""
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func isIDSegment(seg string) bool {
	if seg == "" {
		return false
	}
	if strings.IndexByte(seg, '@') >= 0 {
		return true
	}
	digits, hex := true, true
	for i := 0; i < len(seg); i++ {
		if seg[i] < '0' || seg[i] > '9' {
			digits = false
		}
		if !isHex(seg[i]) {
			hex = false
		}
	}
	if digits || hex && len(seg) >= 12 {
		return true
	}
	if len(seg) == 36 { // UUID
		for i := 0; i < 36; i++ {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				if seg[i] != '-' {
					return false
				}
			} else if !isHex(seg[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// TransactionName normalises a transaction name: names whose source is "url"
// are parameterised first; the result is at most MaxNameBytes.
func TransactionName(name, source string) string {
	name = strings.TrimSpace(name)
	if source == "url" {
		name = ParameteriseURL("", name)
	}
	return clip(name, MaxNameBytes)
}

// attrKeys maps accepted input keys to the stored allowlisted key.
var attrKeys = []struct {
	out string
	in  []string
}{
	{"http.method", []string{"http.method", "http.request.method"}},
	{"http.status_code", []string{"http.status_code", "http.response.status_code"}},
	{"db.system", []string{"db.system", "db.system.name"}},
	{"db.operation", []string{"db.operation", "db.operation.name"}},
	{"server.address", []string{"server.address"}},
	{"messaging.system", []string{"messaging.system"}},
	{"rpc.method", []string{"rpc.method"}},
}

// CleanAttrs keeps only the allowlisted span attributes (at most 10, values at
// most 200 bytes). server.address is reduced to a host: scheme, credentials,
// port and path never survive, and when absent it is taken from url.full,
// http.url or url. Values may be strings, numbers or booleans.
func CleanAttrs(in map[string]any) map[string]string {
	if len(in) == 0 {
		return nil
	}
	var out map[string]string
	put := func(k, v string) {
		if v == "" || len(out) >= maxAttrs {
			return
		}
		if out == nil {
			out = make(map[string]string, 4)
		}
		out[k] = clip(v, maxAttrValue)
	}
	for _, a := range attrKeys {
		for _, k := range a.in {
			v, ok := in[k]
			if !ok {
				continue
			}
			s := scalarString(v)
			if a.out == "server.address" {
				s = hostOnly(s)
			}
			if s != "" {
				put(a.out, s)
				break
			}
		}
	}
	if _, ok := out["server.address"]; !ok {
		for _, k := range []string{"url.full", "http.url", "url"} {
			if h := hostOnly(scalarString(in[k])); h != "" {
				put("server.address", h)
				break
			}
		}
	}
	return out
}

func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case interface{ String() string }: // json.Number
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

// hostOnly extracts the host from a URL or host[:port] string.
func hostOnly(s string) string {
	s = strings.TrimSpace(clip(s, 1024))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	if strings.HasPrefix(s, "[") { // IPv6 literal
		if i := strings.IndexByte(s, ']'); i >= 0 {
			return s[:i+1]
		}
		return ""
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	if bytes.ContainsAny([]byte(s), " \t\r\n") {
		return ""
	}
	return s
}

// NameCaps limits distinct transaction names per site per local day, so a name
// that embeds ids cannot mint unbounded groups. It is in memory.
type NameCaps struct {
	mu    sync.Mutex
	max   int
	sites map[int32]*nameDay
}

type nameDay struct {
	day   time.Time
	names map[string]struct{}
}

// NewNameCaps builds a cap of max distinct names per site per day.
func NewNameCaps(max int) *NameCaps { return &NameCaps{max: max, sites: map[int32]*nameDay{}} }

// Admit returns name when it is known or fits under the cap, else OtherName
// and true. day identifies the site-local day (any fixed instant of it).
func (c *NameCaps) Admit(siteID int32, day time.Time, name string) (string, bool) {
	if c == nil || name == OtherName {
		return name, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.sites[siteID]
	if d == nil || !d.day.Equal(day) {
		d = &nameDay{day: day, names: map[string]struct{}{}}
		c.sites[siteID] = d
	}
	if _, ok := d.names[name]; ok {
		return name, false
	}
	if len(d.names) >= c.max {
		return OtherName, true
	}
	d.names[name] = struct{}{}
	return name, false
}
