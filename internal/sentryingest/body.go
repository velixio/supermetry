package sentryingest

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"time"
)

// decodeObject decodes one JSON object, keeping numbers exact.
func decodeObject(raw []byte) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil || m == nil {
		return nil, ErrInvalid
	}
	return m, nil
}

// parseTime reads a Sentry timestamp: unix seconds (float, int or numeric
// string; values past 1e11 are milliseconds) or an RFC 3339 string with or
// without a zone.
func parseTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return time.Time{}, false
		}
		return unixFloat(f)
	case float64:
		return unixFloat(t)
	case string:
		t = strings.TrimSpace(t)
		if t == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999"} {
			if ts, err := time.Parse(layout, t); err == nil {
				return ts.UTC(), true
			}
		}
		if n := json.Number(t); n != "" {
			if f, err := n.Float64(); err == nil {
				return unixFloat(f)
			}
		}
	}
	return time.Time{}, false
}

func unixFloat(f float64) (time.Time, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1e14 {
		return time.Time{}, false
	}
	if f > 1e11 {
		f /= 1000
	}
	sec := math.Floor(f)
	return time.Unix(int64(sec), int64((f-sec)*1e9)).UTC(), true
}

// asList reads Sentry's two shapes for a list: an array, or {"values": [...]}.
func asList(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case map[string]any:
		if l, ok := t["values"].([]any); ok {
			return l
		}
	}
	return nil
}

// tagPairs reads tags sent as an object or as a list of [key, value] pairs.
func tagPairs(v any) map[string]string {
	out := map[string]string{}
	put := func(k string, val any) {
		var s string
		switch x := val.(type) {
		case string:
			s = x
		case json.Number:
			s = x.String()
		case bool:
			if x {
				s = "true"
			} else {
				s = "false"
			}
		default:
			return
		}
		if k != "" {
			out[k] = s
		}
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			put(k, val)
		}
	case []any:
		for _, p := range t {
			if pair, ok := p.([]any); ok && len(pair) == 2 {
				k, _ := pair[0].(string)
				put(k, pair[1])
			}
		}
	}
	return out
}

func asInt(v any) int {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil && i > math.MinInt32 && i < math.MaxInt32 {
			return int(i)
		}
		if f, err := t.Float64(); err == nil && f > math.MinInt32 && f < math.MaxInt32 {
			return int(f)
		}
	case float64:
		if t > math.MinInt32 && t < math.MaxInt32 {
			return int(t)
		}
	}
	return 0
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
