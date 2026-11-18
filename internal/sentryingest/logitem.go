package sentryingest

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"supermetry/internal/obslogs"
	"supermetry/internal/obsquota"
	"supermetry/internal/otlpingest/attrs"
	"supermetry/internal/sites"
)

// LogAttrConfig is the attribute rule set for log records: it is shared with
// the OTLP front door, with this package's scrubber and sensitive-key list.
func LogAttrConfig() attrs.Config { return attrs.Logs(ScrubText, IsSensitiveKey) }

var logAttrs = LogAttrConfig()

// ErrBadLogItem is returned for a log item that is not a valid container.
var ErrBadLogItem = errors.New("sentryingest: malformed log item")

// LogStats counts what ParseLogItem dropped.
type LogStats struct {
	Rejected     int // records with a bad or out-of-window timestamp or over the per-item cap
	AttrsDropped int // attributes dropped by the count and size caps
}

type logContainer struct {
	Items []logEntry `json:"items"`
}

type logEntry struct {
	Timestamp      json.Number               `json:"timestamp"`
	Level          string                    `json:"level"`
	Body           json.RawMessage           `json:"body"`
	TraceID        string                    `json:"trace_id"`
	SpanID         string                    `json:"span_id"`
	SeverityNumber json.Number               `json:"severity_number"`
	Attributes     map[string]logEntryAttrib `json:"attributes"`
}

type logEntryAttrib struct {
	Value json.RawMessage `json:"value"`
	Type  string          `json:"type"`
}

// ParseLogItem turns one Sentry log item (a {"items": [...]} container) into
// records for the site. Records it cannot keep are counted in stats, never an
// error: only an unreadable container is.
func ParseLogItem(payload []byte, site sites.Site, now time.Time, stats *LogStats) ([]obslogs.Record, error) {
	var c logContainer
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&c); err != nil {
		return nil, ErrBadLogItem
	}
	var secret []byte
	if site.App != nil {
		secret = site.App.HashSecret
	}
	if stats == nil {
		stats = &LogStats{}
	}
	if len(c.Items) > obslogs.MaxSentryItemLogs {
		stats.Rejected += len(c.Items) - obslogs.MaxSentryItemLogs
		c.Items = c.Items[:obslogs.MaxSentryItemLogs]
	}
	out := make([]obslogs.Record, 0, len(c.Items))
	for i := range c.Items {
		e := &c.Items[i]
		ts, ok := logTime(e.Timestamp, now)
		if !ok || !obslogs.InWindow(ts, now) {
			stats.Rejected++
			continue
		}
		r := obslogs.Record{SiteID: site.ID, TS: ts, Observed: now, Source: obslogs.SourceSentry}
		r.Severity = logSeverity(e)
		var bodyText string
		if err := json.Unmarshal(e.Body, &bodyText); err != nil {
			bodyText = strings.TrimSpace(string(e.Body))
			if bodyText == "null" {
				bodyText = ""
			}
		}
		r.Body, r.Truncated = logAttrs.Text(bodyText, attrs.LogMaxBody)
		if b, err := hex.DecodeString(e.TraceID); err == nil && len(b) == 16 && !allZero(b) {
			r.TraceID = b
		}
		if b, err := hex.DecodeString(e.SpanID); err == nil && len(b) == 8 && !allZero(b) {
			r.SpanID = b
		}
		set := logAttrs.NewSet()
		for k, a := range e.Attributes {
			var v any
			vd := json.NewDecoder(bytes.NewReader(a.Value))
			vd.UseNumber()
			if vd.Decode(&v) != nil {
				continue
			}
			v = plainValue(v)
			switch {
			case k == "sentry.environment":
				r.Environment = attrString(v, 64)
			case k == "sentry.release":
				r.Release = attrString(v, 128)
			case strings.HasPrefix(k, "sentry.sdk."):
			default:
				set.Add(k, v)
			}
		}
		var dropped int
		r.Attributes, dropped = set.Finish()
		stats.AttrsDropped += dropped
		r.UserHash = HashUser(secret, set.UserID())
		out = append(out, r)
	}
	return out, nil
}

// attrString reads an identifier such as the environment or release: cleaned
// and cut, not scrubbed (a release string is not an email address).
func attrString(v any, limit int) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return attrs.Cut(attrs.CleanText(s), limit)
}

// plainValue turns json.Number into int64 or float64 and keeps the rest.
func plainValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return nil
	case []any:
		for i := range x {
			x[i] = plainValue(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = plainValue(x[k])
		}
	}
	return v
}

func logTime(n json.Number, now time.Time) (time.Time, bool) {
	if n == "" {
		return now, true
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1e11 {
		return time.Time{}, false
	}
	sec := math.Floor(f)
	return time.Unix(int64(sec), int64((f-sec)*1e9)).UTC(), true
}

func logSeverity(e *logEntry) int16 {
	if n, err := e.SeverityNumber.Int64(); err == nil && n >= 1 && n <= 24 {
		return int16(n)
	}
	return obslogs.SeverityFromText(e.Level)
}

// ingestLogs parses one log item and hands its records to the log sink under
// the site's daily quota. Logs are not events: they never touch the spike
// guard or the per-key event budget.
func (h *Handler) ingestLogs(out *outcome, site sites.Site, it Item, now time.Time) {
	if h.logs == nil {
		h.stats.Add("item:log", 1)
		return
	}
	if len(it.Payload) > MaxLogItemBytes {
		h.stats.Add("log_too_large", 1)
		return
	}
	var st LogStats
	recs, err := ParseLogItem(it.Payload, site, now, &st)
	h.stats.Add("log_rejected", uint64(st.Rejected))
	h.stats.Add("log_attrs_dropped", uint64(st.AttrsDropped))
	if err != nil {
		h.stats.Add("log_item_invalid", 1)
		return
	}
	q := h.logQuota
	if q != nil {
		q.AddBytes(site.ID, obsquota.ClassLogs, len(it.Payload))
		q.Reject(site.ID, obsquota.ClassLogs, st.Rejected)
	}
	if len(recs) == 0 {
		return
	}
	if q != nil {
		var limit int64
		if site.App != nil {
			limit = site.App.LogDailyQuota
		}
		taken := q.Take(site.ID, obsquota.ClassLogs, len(recs), limit)
		if taken < len(recs) {
			h.stats.Add("log_quota_rejected", uint64(len(recs)-taken))
			out.logLimited++
			recs = recs[:taken]
		}
	}
	if len(recs) == 0 {
		return
	}
	accepted := h.logs.Enqueue(recs)
	if accepted < len(recs) {
		if q != nil {
			q.Release(site.ID, obsquota.ClassLogs, len(recs)-accepted)
		}
		if accepted == 0 {
			out.queueFull++
			return
		}
	}
	out.accepted++
}
