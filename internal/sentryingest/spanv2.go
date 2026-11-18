package sentryingest

import (
	"encoding/json"
	"strings"
	"time"

	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

// SpanV2ContentType is the content type of a Sentry span v2 container item.
const SpanV2ContentType = "application/vnd.sentry.items.span.v2+json"

// The container shape (version, items[], per-item trace_id, span_id,
// parent_span_id, name, status, is_segment, start_timestamp, end_timestamp and
// typed attributes) is inferred from the public protocol notes and the SDK
// behaviour recorded in the research notes, not from a recorded SDK capture.
type spanV2Container struct {
	Items []json.RawMessage `json:"items"`
}

type spanV2Item struct {
	TraceID    string                `json:"trace_id"`
	SpanID     string                `json:"span_id"`
	Parent     string                `json:"parent_span_id"`
	Name       any                   `json:"name"`
	Status     any                   `json:"status"`
	IsSegment  bool                  `json:"is_segment"`
	Start      any                   `json:"start_timestamp"`
	End        any                   `json:"end_timestamp"`
	Attributes map[string]spanV2Attr `json:"attributes"`
}

type spanV2Attr struct {
	Value any `json:"value"`
}

// ParseSpanV2 turns a span v2 container into spans. Spans that cannot be used
// (bad ids or times) are skipped and counted; it returns ErrInvalid for a
// payload without items and ErrTooOld when every span is past the late-data
// window.
func ParseSpanV2(raw []byte, ctx EventContext, site sites.Site, now time.Time) ([]spans.Span, error) {
	return parseSpanV2(raw, ctx, site, now, &SpanStats{})
}

type v2Parsed struct {
	span   spans.Span
	attrs  map[string]any
	isSeg  bool
	source string
}

func parseSpanV2(raw []byte, ctx EventContext, site sites.Site, now time.Time, st *SpanStats) ([]spans.Span, error) {
	if len(raw) > MaxEventBytes {
		return nil, ErrInvalid
	}
	var c spanV2Container
	if err := decodeUseNumber(raw, &c); err != nil || len(c.Items) == 0 {
		return nil, ErrInvalid
	}
	if len(c.Items) > MaxSpansPerItem {
		st.Truncated += len(c.Items) - MaxSpansPerItem
		c.Items = c.Items[:MaxSpansPerItem]
	}
	shift := clockShift(ctx.SentAt, now)
	cutoff := now.Add(-lateWindow(site))
	parsed := make([]v2Parsed, 0, len(c.Items))
	for _, rawItem := range c.Items {
		var it spanV2Item
		if err := decodeUseNumber(rawItem, &it); err != nil {
			st.Invalid++
			continue
		}
		p, ok := v2Span(it, site, shift, now, st)
		if !ok {
			st.Invalid++
			continue
		}
		if p.span.Start.Before(cutoff) {
			st.TooOld++
			continue
		}
		parsed = append(parsed, p)
	}
	if len(parsed) == 0 {
		if st.TooOld > 0 {
			return nil, ErrTooOld
		}
		return nil, ErrInvalid
	}

	// Segment per trace: the flagged span, else the first span without a parent.
	type segInfo struct {
		id                     [8]byte
		release, env, platform string
	}
	segs := map[[16]byte]segInfo{}
	for _, flagged := range []bool{true, false} {
		for i := range parsed {
			p := &parsed[i]
			if _, done := segs[p.span.TraceID]; done || (flagged && !p.isSeg) || (!flagged && p.span.ParentID != nil) {
				continue
			}
			p.isSeg = true
			segs[p.span.TraceID] = segInfo{p.span.SpanID, p.span.Release, p.span.Environment, p.span.Platform}
		}
	}
	out := make([]spans.Span, 0, len(parsed))
	for i := range parsed {
		p := &parsed[i]
		s := p.span
		seg, hasSeg := segs[s.TraceID]
		switch {
		case p.isSeg:
			s.IsSegment = true
			s.SegmentID = s.SpanID
			name := strings.TrimSpace(firstNonEmpty(str(p.attrs["sentry.segment.name"]), s.Name))
			// A browser pageload without a router is called "Pageload" and carries
			// the page in url.path; one transaction per page beats one for all.
			if path := str(p.attrs["url.path"]); p.source == "url" && path != "" && !strings.Contains(name, "/") {
				name = path
			}
			s.Name = spans.TransactionName(name, p.source)
			if s.Name == "" {
				s.Name = otherTxName
			}
		default:
			s.SegmentID = s.SpanID
			if hasSeg {
				s.SegmentID = seg.id
			}
			if id, ok := hexAttr(p.attrs["sentry.segment.id"]); ok {
				s.SegmentID = id
			}
			if s.Release == "" {
				s.Release = seg.release
			}
			if s.Environment == "" {
				s.Environment = seg.env
			}
			if s.Platform == "" {
				s.Platform = seg.platform
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func v2Span(it spanV2Item, site sites.Site, shift time.Duration, now time.Time, st *SpanStats) (v2Parsed, bool) {
	var p v2Parsed
	s := &p.span
	if !hexBytes(it.TraceID, s.TraceID[:]) || !hexBytes(it.SpanID, s.SpanID[:]) || allZero(s.TraceID[:]) || allZero(s.SpanID[:]) {
		return p, false
	}
	var pid [8]byte
	if it.Parent != "" && hexBytes(it.Parent, pid[:]) && !allZero(pid[:]) {
		s.ParentID = &pid
	}
	start, ok := parseTime(it.Start)
	if !ok {
		return p, false
	}
	start = start.Add(shift)
	if start.Before(minSpanTime) || start.After(now.Add(48*time.Hour)) {
		return p, false
	}
	p.attrs = make(map[string]any, len(it.Attributes))
	for k, a := range it.Attributes {
		p.attrs[k] = a.Value
	}
	s.SiteID = site.ID
	s.Start = start
	s.DurationUS = duration(start, it.End, shift, st)
	s.Op = clip(str(p.attrs["sentry.op"]), 64)
	s.Status = clip(str(it.Status), 32)
	s.Release = clip(str(p.attrs["sentry.release"]), 64)
	s.Environment = clip(str(p.attrs["sentry.environment"]), 32)
	s.Platform = clip(str(p.attrs["sentry.platform"]), 32)
	s.Attrs = spans.CleanAttrs(p.attrs)
	s.HTTPStatus = httpStatus(p.attrs, nil)
	p.isSeg = it.IsSegment
	// The SDKs name the source of a segment name in sentry.segment.name.source
	// (and sentry.source); sentry.span.source is kept for older shapes.
	p.source = firstNonEmpty(str(p.attrs["sentry.segment.name.source"]), str(p.attrs["sentry.source"]), str(p.attrs["sentry.span.source"]))
	desc := describe(s.Op, str(it.Name))
	s.Description = desc
	s.Name = clip(desc, spans.MaxNameBytes)
	if s.Name == "" {
		s.Name = s.Op
	}
	return p, true
}

func hexAttr(v any) ([8]byte, bool) {
	var id [8]byte
	s, _ := v.(string)
	if !hexBytes(s, id[:]) || allZero(id[:]) {
		return id, false
	}
	return id, true
}
