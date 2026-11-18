package sentryingest

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

const (
	// MaxSpansPerItem caps the child spans read from one transaction or span
	// container; the rest are counted as truncated.
	MaxSpansPerItem = 1000
	// maxLateDays is the hard ceiling on accepting late spans.
	maxLateDays = 13
	// maxSpanDuration clamps absurd durations (a day).
	maxSpanDuration = 24 * time.Hour
	maxAppStartMS   = 10 * 60 * 1000
	otherTxName     = "<unknown>"
)

var minSpanTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// SpanStats counts what the span parsers repaired or dropped. The handler
// turns it into counters.
type SpanStats struct {
	Clamped   int // negative, missing or absurd durations repaired
	Truncated int // spans past MaxSpansPerItem
	Invalid   int // child spans dropped for a bad id or time
	TooOld    int // spans older than the late-data window (span containers)
}

type txEvent struct {
	Transaction string `json:"transaction"`
	Info        struct {
		Source string `json:"source"`
	} `json:"transaction_info"`
	Start        any                        `json:"start_timestamp"`
	End          any                        `json:"timestamp"`
	Release      any                        `json:"release"`
	Environment  any                        `json:"environment"`
	Platform     any                        `json:"platform"`
	Tags         map[string]any             `json:"tags"`
	Measurements map[string]json.RawMessage `json:"measurements"`
	Contexts     struct {
		Trace *txSpanJSON `json:"trace"`
	} `json:"contexts"`
	Spans []json.RawMessage `json:"spans"`
}

type txSpanJSON struct {
	TraceID string         `json:"trace_id"`
	SpanID  string         `json:"span_id"`
	Parent  string         `json:"parent_span_id"`
	Op      any            `json:"op"`
	Desc    any            `json:"description"`
	Start   any            `json:"start_timestamp"`
	End     any            `json:"timestamp"`
	Status  any            `json:"status"`
	Data    map[string]any `json:"data"`
}

// ParseTransaction turns a Sentry transaction event into a segment span plus
// one span per child. It returns ErrInvalid for a payload without a usable
// trace context and ErrTooOld for one older than the site's late-data window.
func ParseTransaction(raw []byte, ctx EventContext, site sites.Site, now time.Time) ([]spans.Span, error) {
	return parseTransaction(raw, ctx, site, now, &SpanStats{})
}

func decodeUseNumber(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}

func parseTransaction(raw []byte, ctx EventContext, site sites.Site, now time.Time, st *SpanStats) ([]spans.Span, error) {
	if len(raw) > MaxEventBytes {
		return nil, ErrInvalid
	}
	var p txEvent
	if err := decodeUseNumber(raw, &p); err != nil || p.Contexts.Trace == nil {
		return nil, ErrInvalid
	}
	tr := p.Contexts.Trace
	var seg spans.Span
	if !hexBytes(tr.TraceID, seg.TraceID[:]) || !hexBytes(tr.SpanID, seg.SpanID[:]) || allZero(seg.TraceID[:]) || allZero(seg.SpanID[:]) {
		return nil, ErrInvalid
	}
	start, ok := parseTime(p.Start)
	if !ok {
		return nil, ErrInvalid
	}
	shift := clockShift(ctx.SentAt, now)
	start = start.Add(shift)
	if start.After(now.Add(futureSkew)) {
		d := now.Sub(start)
		shift += d
		start = now
	}
	if start.Before(now.Add(-lateWindow(site))) {
		return nil, ErrTooOld
	}
	var pid [8]byte
	if tr.Parent != "" && hexBytes(tr.Parent, pid[:]) && !allZero(pid[:]) {
		seg.ParentID = &pid
	}
	seg.SiteID = site.ID
	seg.Start = start
	seg.DurationUS = duration(start, p.End, shift, st)
	seg.SegmentID = seg.SpanID
	seg.IsSegment = true
	seg.Name = spans.TransactionName(strings.TrimSpace(p.Transaction), p.Info.Source)
	if seg.Name == "" {
		seg.Name = otherTxName
	}
	seg.Op = clip(str(tr.Op), 64)
	seg.Status = clip(str(tr.Status), 32)
	seg.Release = clip(str(p.Release), 64)
	seg.Environment = clip(str(p.Environment), 32)
	seg.Platform = clip(str(p.Platform), 32)
	seg.Attrs = spans.CleanAttrs(tr.Data)
	seg.HTTPStatus = httpStatus(tr.Data, p.Tags)
	seg.AppStartType, seg.AppStartMS, seg.AppStartBucket = appStart(p.Measurements)

	out := make([]spans.Span, 1, 1+min(len(p.Spans), MaxSpansPerItem))
	out[0] = seg
	for i, rawChild := range p.Spans {
		if i >= MaxSpansPerItem {
			st.Truncated += len(p.Spans) - i
			break
		}
		var c txSpanJSON
		if err := decodeUseNumber(rawChild, &c); err != nil {
			st.Invalid++
			continue
		}
		child, ok := childSpan(c, seg, shift, now, st)
		if !ok {
			st.Invalid++
			continue
		}
		out = append(out, child)
	}
	return out, nil
}

func childSpan(c txSpanJSON, seg spans.Span, shift time.Duration, now time.Time, st *SpanStats) (spans.Span, bool) {
	s := spans.Span{SiteID: seg.SiteID, TraceID: seg.TraceID, SegmentID: seg.SpanID,
		Release: seg.Release, Environment: seg.Environment, Platform: seg.Platform}
	if !hexBytes(c.SpanID, s.SpanID[:]) || allZero(s.SpanID[:]) {
		return s, false
	}
	var pid [8]byte
	if c.Parent != "" && hexBytes(c.Parent, pid[:]) && !allZero(pid[:]) {
		s.ParentID = &pid
	} else {
		root := seg.SpanID
		s.ParentID = &root
	}
	start, ok := parseTime(c.Start)
	if !ok {
		return s, false
	}
	start = start.Add(shift)
	if start.Before(minSpanTime) || start.After(now.Add(48*time.Hour)) {
		return s, false
	}
	s.Start = start
	s.DurationUS = duration(start, c.End, shift, st)
	s.Op = clip(str(c.Op), 64)
	s.Status = clip(str(c.Status), 32)
	desc := describe(s.Op, str(c.Desc))
	s.Description = desc
	s.Name = clip(desc, spans.MaxNameBytes)
	if s.Name == "" {
		s.Name = s.Op
	}
	s.Attrs = spans.CleanAttrs(c.Data)
	s.HTTPStatus = httpStatus(c.Data, nil)
	return s, true
}

// describe parameterises a span description by its operation: SQL for db
// spans, URLs for http spans, the usual scrub for everything else.
func describe(op, desc string) string {
	switch {
	case strings.HasPrefix(op, "db"):
		return spans.ParameteriseSQL(desc)
	case strings.HasPrefix(op, "http"):
		return spans.ParameteriseURL("", desc)
	default:
		return scrubClip(desc, spans.MaxDescription)
	}
}

// duration is end - start in microseconds, clamped to [0, a day]; a missing or
// negative end counts as clamped.
func duration(start time.Time, endV any, shift time.Duration, st *SpanStats) int64 {
	end, ok := parseTime(endV)
	if !ok {
		st.Clamped++
		return 0
	}
	end = end.Add(shift)
	if end.Before(minSpanTime) || end.After(start.Add(maxSpanDuration)) {
		if end.Before(start) {
			st.Clamped++
			return 0
		}
		st.Clamped++
		return maxSpanDuration.Microseconds()
	}
	d := end.Sub(start).Round(time.Microsecond).Microseconds()
	if d < 0 {
		st.Clamped++
		return 0
	}
	return d
}

// clockShift corrects a device clock that is far from the envelope's sent_at.
func clockShift(sentAt, now time.Time) time.Duration {
	if sentAt.IsZero() {
		return 0
	}
	skew := now.Sub(sentAt)
	if skew > skewCorrectAfter || skew < -skewCorrectAfter {
		return skew
	}
	return 0
}

// lateWindow is how old a span may be: the site's report age, at most 13 days.
func lateWindow(site sites.Site) time.Duration {
	days := defaultMaxAgeDays
	if site.App != nil && site.App.MaxReportAgeDays > 0 {
		days = site.App.MaxReportAgeDays
	}
	return time.Duration(min(days, maxLateDays)) * 24 * time.Hour
}

func hexBytes(s string, dst []byte) bool {
	if len(s) != 2*len(dst) {
		return false
	}
	_, err := hex.Decode(dst, []byte(s))
	return err == nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func httpStatus(data map[string]any, tags map[string]any) *int16 {
	for _, src := range []struct {
		m map[string]any
		k string
	}{{data, "http.response.status_code"}, {data, "http.status_code"}, {tags, "http.status_code"}} {
		if src.m == nil {
			continue
		}
		var f float64
		switch v := src.m[src.k].(type) {
		case json.Number:
			f, _ = v.Float64()
		case string:
			f, _ = strconv.ParseFloat(v, 64)
		default:
			continue
		}
		if f >= 100 && f <= 599 {
			c := int16(f)
			return &c
		}
	}
	return nil
}

type measurement struct {
	Value json.Number `json:"value"`
	Unit  string      `json:"unit"`
}

// appStart reads measurements.app_start_cold / app_start_warm (milliseconds
// unless the unit says otherwise). Values that are not positive, finite and
// under ten minutes are ignored.
func appStart(m map[string]json.RawMessage) (string, *int32, *int16) {
	for _, typ := range []string{"cold", "warm"} {
		raw, ok := m["app_start_"+typ]
		if !ok {
			continue
		}
		var v measurement
		if json.Unmarshal(raw, &v) != nil {
			continue
		}
		f, err := v.Value.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		switch v.Unit {
		case "second":
			f *= 1000
		case "microsecond":
			f /= 1000
		case "nanosecond":
			f /= 1e6
		}
		if f < 1 || f > maxAppStartMS {
			continue
		}
		ms := int32(f)
		b := spans.Bucket(int64(ms) * 1000)
		return typ, &ms, &b
	}
	return "", nil, nil
}
