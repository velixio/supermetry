package sentryingest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"supermetry/internal/appingest"
)

const (
	maxScrubInput        = 64 << 10
	maxBreadcrumbs       = 100
	maxBreadcrumbBytes   = 16 << 10
	maxBreadcrumbMessage = 512
	maxBreadcrumbValue   = 256
	maxBreadcrumbData    = 10
	maxContextKeys       = 24
	maxContextValue      = 200
)

var (
	reJWT       = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)
	reBearer    = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{6,}`)
	reSecretKV  = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|authorization|auth)\b\s*[:=]\s*(?:(?:bearer|basic)\s+)?[^\s,;&"']+`)
	rePrefixKey = regexp.MustCompile(`\b(?:(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{8,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,})`)
	reEmail     = regexp.MustCompile(`[A-Za-z0-9._%+'-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
	reUUID      = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reIPv4      = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`)
	reIPv6      = regexp.MustCompile(`(?i)(?:\b|:)(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}\b`)
	reCard      = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	reHomeUnix  = regexp.MustCompile(`(?:/home|/Users)/[^/\s:'"()\[\]]+`)
	reHomeWin   = regexp.MustCompile(`(?i)[A-Za-z]:\\users\\[^\\\s:'"()\[\]]+`)
	reLongToken = regexp.MustCompile(`\b[A-Za-z0-9_-]{32,}\b`)
	reLongDigit = regexp.MustCompile(`\d{6,}`)
	reMethod    = regexp.MustCompile(`^[A-Z]{1,16}$`)
)

// ScrubText removes personal data and secrets from free text: JWTs, bearer
// tokens, "password=..." pairs, well-known API key shapes, emails, UUIDs, IPs,
// card numbers (Luhn-valid), home-directory names, long token-like strings and
// long digit runs. Input beyond 64 KiB is cut first.
func ScrubText(s string) string { return scrubText(s, true) }

// scrubText is ScrubText; homes false leaves "/home/x" and "/Users/x" alone,
// for URL paths where those are ordinary routes.
func scrubText(s string, homes bool) string {
	if len(s) > maxScrubInput {
		s = cutUTF8(s, maxScrubInput)
	}
	s = reJWT.ReplaceAllString(s, "[token]")
	s = reBearer.ReplaceAllString(s, "[token]")
	s = reSecretKV.ReplaceAllString(s, "$1=[token]")
	s = rePrefixKey.ReplaceAllString(s, "[token]")
	s = reEmail.ReplaceAllString(s, "[email]")
	s = reUUID.ReplaceAllString(s, "[uuid]")
	if homes {
		s = reHomeUnix.ReplaceAllString(s, "~")
		s = reHomeWin.ReplaceAllString(s, "~")
	}
	s = reIPv4.ReplaceAllString(s, "[ip]")
	s = reIPv6.ReplaceAllStringFunc(s, func(m string) string {
		trimmed := strings.TrimLeft(m, ":")
		if m != trimmed && !strings.HasPrefix(m, "::") {
			return m
		}
		if strings.Count(m, ":") < 4 && !strings.Contains(m, "::") {
			return m
		}
		if _, err := netip.ParseAddr(m); err != nil {
			return m
		}
		return "[ip]"
	})
	s = reCard.ReplaceAllStringFunc(s, func(m string) string {
		if luhn(m) {
			return "[card]"
		}
		return m
	})
	s = reLongToken.ReplaceAllStringFunc(s, func(m string) string {
		if strings.ContainsAny(m, "0123456789") && strings.ContainsAny(m, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return "[token]"
		}
		return m
	})
	return reLongDigit.ReplaceAllString(s, "[num]")
}

func luhn(s string) bool {
	sum, double, n := 0, false, 0
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}
		d := int(c - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
		n++
	}
	return n >= 13 && sum%10 == 0
}

// HashUser is the HMAC-SHA256 of a user id under the app's secret, the same
// MAC the writer applies to install ids. It returns nil for an empty id or
// secret.
func HashUser(secret []byte, id string) []byte {
	if id == "" || len(secret) == 0 {
		return nil
	}
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(id))
	return h.Sum(nil)
}

func cutUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// clip cuts s to limit bytes on a rune boundary and drops NULs.
func clip(s string, limit int) string {
	s = strings.ReplaceAll(s, "\x00", "")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return cutUTF8(s, limit)
}

// scrubClip scrubs and clips free text.
func scrubClip(s string, limit int) string { return clip(ScrubText(clip(s, limit*4)), limit) }

// urlPath reduces a URL to its scrubbed path: no scheme, host, port, query or
// fragment. A value without "://" is treated as a path or module name.
func urlPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		p := u.Path
		if p == "" {
			p = "/"
		}
		return clip(scrubText(p, false), 512)
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return clip(scrubText(raw, false), 512)
}

var requestHeaderAllow = map[string]struct{}{
	"content-type": {}, "content-length": {}, "accept": {}, "accept-language": {},
}

// ScrubRequest builds the stored request from a Sentry request interface. It
// keeps the method, the URL path and a few harmless headers. Cookies,
// credentials, query strings, bodies, env (REMOTE_ADDR) and the user agent
// are dropped. It returns nil when nothing is left.
func ScrubRequest(raw map[string]any) *appingest.Request {
	if raw == nil {
		return nil
	}
	r := &appingest.Request{}
	if u, ok := raw["url"].(string); ok {
		r.Path = urlPath(u)
	}
	if m, ok := raw["method"].(string); ok {
		m = strings.ToUpper(clip(m, 16))
		if reMethod.MatchString(m) {
			r.Method = m
		}
	}
	for k, v := range headerPairs(raw["headers"]) {
		k = strings.ToLower(k)
		if _, ok := requestHeaderAllow[k]; ok {
			if r.Headers == nil {
				r.Headers = map[string]string{}
			}
			r.Headers[k] = scrubClip(v, 120)
		}
	}
	if r.Path == "" && r.Method == "" && len(r.Headers) == 0 {
		return nil
	}
	return r
}

// headerPairs reads headers sent as an object or as a list of [name, value].
func headerPairs(v any) map[string]string {
	out := map[string]string{}
	switch h := v.(type) {
	case map[string]any:
		for k, val := range h {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
	case []any:
		for _, pair := range h {
			if p, ok := pair.([]any); ok && len(p) == 2 {
				k, _ := p[0].(string)
				val, _ := p[1].(string)
				if k != "" {
					out[k] = val
				}
			}
		}
	}
	return out
}

var sensitiveKey = []string{"auth", "cookie", "password", "passwd", "secret", "token", "email", "ip_address", "apikey", "api_key", "session", "credential", "username", "geo"}

func isSensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range sensitiveKey {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// scalar converts a decoded JSON scalar; ok is false for objects and arrays.
func scalar(v any, limit int, urlKey bool) (any, bool) {
	switch t := v.(type) {
	case string:
		if urlKey {
			return urlPath(t), true
		}
		return scrubClip(t, limit), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case bool:
		return t, true
	}
	return nil, false
}

func isURLKey(k string) bool {
	switch strings.ToLower(k) {
	case "url", "from", "to", "href", "uri", "referrer":
		return true
	}
	return false
}

// scrubBreadcrumbs keeps the newest 100 breadcrumbs within 16 KiB of JSON.
// Each keeps type, category, level, timestamp, a scrubbed message and up to
// ten scalar data values (URLs reduced to paths, sensitive keys dropped).
func scrubBreadcrumbs(in []any) []appingest.Breadcrumb {
	var rev []appingest.Breadcrumb
	size := 2
	for i := len(in) - 1; i >= 0 && len(rev) < maxBreadcrumbs; i-- {
		m, ok := in[i].(map[string]any)
		if !ok {
			continue
		}
		b := appingest.Breadcrumb{
			Type:     clip(str(m["type"]), 32),
			Category: clip(str(m["category"]), 64),
			Level:    clip(str(m["level"]), 16),
			Message:  scrubClip(str(m["message"]), maxBreadcrumbMessage),
		}
		if ts, ok := parseTime(m["timestamp"]); ok {
			b.Timestamp = float64(ts.UnixMilli()) / 1000
		}
		if d, ok := m["data"].(map[string]any); ok {
			keys := make([]string, 0, len(d))
			for k := range d {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if len(b.Data) >= maxBreadcrumbData || isSensitiveKey(k) {
					continue
				}
				if v, ok := scalar(d[k], maxBreadcrumbValue, isURLKey(k)); ok {
					if b.Data == nil {
						b.Data = map[string]any{}
					}
					b.Data[clip(k, 48)] = v
				}
			}
		}
		raw, err := json.Marshal(b)
		if err != nil || size+len(raw)+1 > maxBreadcrumbBytes {
			break
		}
		size += len(raw) + 1
		rev = append(rev, b)
	}
	out := make([]appingest.Breadcrumb, len(rev))
	for i, b := range rev {
		out[len(rev)-1-i] = b
	}
	return out
}

var contextAllow = map[string]struct{}{
	"os": {}, "device": {}, "runtime": {}, "app": {}, "browser": {}, "culture": {},
	"trace": {}, "gpu": {}, "chrome": {}, "node": {}, "response": {},
}

// scrubContexts keeps the known device, OS, runtime and app contexts with
// scalar values only. Free-form contexts, names (device.name), IPs and emails
// are dropped.
func scrubContexts(in map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for name, v := range in {
		if _, ok := contextAllow[name]; !ok {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ctx := map[string]any{}
		for _, k := range keys {
			if len(ctx) >= maxContextKeys || isSensitiveKey(k) || (k == "name" && name == "device") {
				continue
			}
			if val, ok := scalar(m[k], maxContextValue, false); ok {
				if s, isStr := val.(string); isStr {
					val = strings.TrimSpace(s)
				}
				ctx[clip(k, 48)] = val
			}
		}
		if len(ctx) > 0 {
			out[name] = ctx
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
