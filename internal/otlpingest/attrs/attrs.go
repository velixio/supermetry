// Package attrs is the shared attribute pipeline of the OTLP and Sentry log
// and metric front doors: it flattens attribute values, applies the privacy
// rules to keys and values, and enforces the size caps.
//
// It lives in its own package (under otlpingest) so that obsmetrics and
// sentryingest can use it without importing the OTLP handler, which imports
// them. Text scrubbing and the sensitive-key list are injected, because the
// scrubber lives in sentryingest.
package attrs

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// maxDepth bounds how deep an array or map value is walked.
const maxDepth = 4

// Config is one front door's attribute rules.
type Config struct {
	MaxAttrs    int // attributes kept (sorted key order); 0 = no cap
	MaxKeyBytes int // longer keys are dropped
	MaxValBytes int // string values are cut to this many bytes
	MaxEncoded  int // encoded JSON size of the whole map; 0 = no cap

	// Typed keeps ints, bools and doubles as they are and JSON-encodes arrays
	// and maps into a string. When false every scalar becomes a string and
	// arrays and maps are skipped (metric series attributes).
	Typed bool

	// Scrub is applied to string values (nil = none). Sensitive reports keys
	// whose values must never be stored (nil = none).
	Scrub     func(string) string
	Sensitive func(string) bool
}

// Log limits from the phase 6 plan.
const (
	LogMaxAttrs    = 32
	LogMaxKeyBytes = 128
	LogMaxValBytes = 1024
	LogMaxEncoded  = 4096
	LogMaxBody     = 8192

	MetricMaxAttrs    = 16
	MetricMaxKeyBytes = 128
	MetricMaxValBytes = 256
)

// Logs builds the log attribute rules.
func Logs(scrub func(string) string, sensitive func(string) bool) Config {
	return Config{MaxAttrs: LogMaxAttrs, MaxKeyBytes: LogMaxKeyBytes, MaxValBytes: LogMaxValBytes,
		MaxEncoded: LogMaxEncoded, Typed: true, Scrub: scrub, Sensitive: sensitive}
}

// Metrics builds the metric series attribute rules: values are not free
// text, so they are only length-capped.
func Metrics(sensitive func(string) bool) Config {
	return Config{MaxAttrs: MetricMaxAttrs, MaxKeyBytes: MetricMaxKeyBytes, MaxValBytes: MetricMaxValBytes, Sensitive: sensitive}
}

// ipKeys carry a client address and are never stored.
var ipKeys = map[string]struct{}{
	"client.address": {}, "net.peer.ip": {}, "net.sock.peer.addr": {}, "http.client_ip": {}, "source.address": {},
}

// personalKeys identify a person and are never stored.
var personalKeys = map[string]struct{}{
	"user.email": {}, "user.name": {}, "user.full_name": {},
}

// IsUserIDKey reports the attribute keys that carry a user id; the front
// door hashes the value into user_hash and removes the attribute.
func IsUserIDKey(k string) bool {
	k = strings.ToLower(k)
	return k == "user.id" || k == "enduser.id"
}

// KeyDropped applies the privacy key rules: IP attributes, personal user
// attributes, every enduser.* attribute (the id included, it is hashed
// elsewhere), user.id, and the sensitive-key list.
func (c Config) KeyDropped(k string) bool {
	lk := strings.ToLower(k)
	if _, ok := ipKeys[lk]; ok {
		return true
	}
	if _, ok := personalKeys[lk]; ok {
		return true
	}
	if lk == "user.id" || strings.HasPrefix(lk, "enduser.") {
		return true
	}
	return c.Sensitive != nil && c.Sensitive(lk)
}

// CleanText makes s storable in a Postgres text or jsonb value: NUL
// characters are removed (jsonb rejects \u0000) and invalid UTF-8 is
// replaced.
func CleanText(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return s
}

// Cut returns s cut to at most n bytes on a UTF-8 boundary.
func Cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Text cleans, scrubs and cuts free text such as a log body. It reports
// whether the text was cut.
func (c Config) Text(s string, limit int) (string, bool) {
	s = CleanText(s)
	if c.Scrub != nil {
		s = c.Scrub(s)
	}
	if len(s) > limit {
		return Cut(s, limit), true
	}
	return s, false
}

// Set collects the attributes of one record.
type Set struct {
	cfg     Config
	m       map[string]any
	userID  string
	dropped int
}

// NewSet starts an attribute set.
func (c Config) NewSet() *Set { return &Set{cfg: c, m: map[string]any{}} }

// UserID is the raw user id seen under user.id or enduser.id ("" if none).
// Callers hash it and never store it.
func (s *Set) UserID() string { return s.userID }

// AddOTLP adds OTLP key-values.
func (s *Set) AddOTLP(kvs []*commonpb.KeyValue) {
	for _, kv := range kvs {
		s.Add(kv.GetKey(), AnyToGo(kv.GetValue(), 0))
	}
}

// Add adds one attribute; v is a string, bool, int, int64, float64, []any,
// map[string]any or nil (ignored). A later Add of the same key wins.
func (s *Set) Add(key string, v any) {
	key = CleanText(key)
	if key == "" || len(key) > s.cfg.MaxKeyBytes {
		return
	}
	if IsUserIDKey(key) {
		if s.userID == "" {
			s.userID = scalarString(v)
		}
		return
	}
	if s.cfg.KeyDropped(key) {
		return
	}
	val, ok := s.value(v)
	if !ok {
		return
	}
	s.m[key] = val
}

func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return CleanText(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

func (s *Set) str(v string) string {
	v = CleanText(v)
	if s.cfg.Scrub != nil {
		v = s.cfg.Scrub(v)
	}
	return Cut(v, s.cfg.MaxValBytes)
}

func (s *Set) value(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		return s.str(x), true
	case bool:
		if s.cfg.Typed {
			return x, true
		}
		return strconv.FormatBool(x), true
	case int:
		return s.number(int64(x))
	case int64:
		return s.number(x)
	case float64:
		if x != x || x > 1.7976931348623157e308 || x < -1.7976931348623157e308 {
			return nil, false
		}
		if s.cfg.Typed {
			return x, true
		}
		return strconv.FormatFloat(x, 'g', -1, 64), true
	case []any, map[string]any:
		if !s.cfg.Typed {
			return nil, false
		}
		raw, err := json.Marshal(sanitize(x, 0))
		if err != nil {
			return nil, false
		}
		return s.str(string(raw)), true
	}
	return nil, false
}

func (s *Set) number(n int64) (any, bool) {
	if s.cfg.Typed {
		return n, true
	}
	return strconv.FormatInt(n, 10), true
}

// EncodeValue renders a decoded value as text: a string as is, anything else
// as JSON. NULs and invalid UTF-8 inside it are cleaned first.
func EncodeValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return CleanText(x)
	}
	raw, err := json.Marshal(sanitize(v, 0))
	if err != nil {
		return ""
	}
	return string(raw)
}

// sanitize removes NULs and invalid UTF-8 from every string inside a decoded
// value before it is encoded.
func sanitize(v any, depth int) any {
	switch x := v.(type) {
	case string:
		return CleanText(x)
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, sanitize(e, depth+1))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[CleanText(k)] = sanitize(e, depth+1)
		}
		return out
	}
	return v
}

// Finish applies the count and encoded-size caps and returns the attributes
// with the number of attributes dropped by them. Keys beyond a cap go in
// sorted-key order, so the kept set is deterministic.
func (s *Set) Finish() (map[string]any, int) {
	m := s.m
	if len(m) == 0 {
		return map[string]any{}, 0
	}
	capCount := s.cfg.MaxAttrs > 0 && len(m) > s.cfg.MaxAttrs
	capBytes := s.cfg.MaxEncoded > 0
	if !capCount && !capBytes {
		return m, 0
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if capCount {
		keys = keys[:s.cfg.MaxAttrs]
	}
	kept := make(map[string]any, len(keys))
	size := 2 // braces
	for i, k := range keys {
		if capBytes {
			kb, _ := json.Marshal(k)
			vb, err := json.Marshal(m[k])
			if err != nil {
				continue
			}
			add := len(kb) + 1 + len(vb)
			if i > 0 {
				add++ // comma
			}
			if size+add > s.cfg.MaxEncoded {
				// Everything from here on is dropped, so the kept set is a
				// sorted-key prefix.
				break
			}
			size += add
		}
		kept[k] = m[k]
	}
	return kept, len(m) - len(kept)
}

// Strings converts a typed attribute map to strings (metric series).
func Strings(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// AnyToGo converts an OTLP AnyValue to a plain Go value: string, int64,
// bool, float64, []any or map[string]any (nil for an empty value).
func AnyToGo(v *commonpb.AnyValue, depth int) any {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue
	case *commonpb.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(x.BytesValue)
	case *commonpb.AnyValue_ArrayValue:
		if depth >= maxDepth {
			return nil
		}
		out := make([]any, 0, len(x.ArrayValue.GetValues()))
		for _, e := range x.ArrayValue.GetValues() {
			out = append(out, AnyToGo(e, depth+1))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		if depth >= maxDepth {
			return nil
		}
		out := make(map[string]any, len(x.KvlistValue.GetValues()))
		for _, kv := range x.KvlistValue.GetValues() {
			out[kv.GetKey()] = AnyToGo(kv.GetValue(), depth+1)
		}
		return out
	}
	return nil
}
