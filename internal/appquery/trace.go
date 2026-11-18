package appquery

import (
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

// MaxTraceSpans caps the spans a trace read returns.
const MaxTraceSpans = 10000

// maxTraceErrors caps the errors linked to a trace.
const maxTraceErrors = 50

// TraceSpan is one span of a waterfall, in display (depth-first) order.
type TraceSpan struct {
	SpanID        string    `json:"span_id"`
	ParentID      string    `json:"parent_id,omitempty"`
	Depth         int       `json:"depth"`
	IsSegment     bool      `json:"is_segment"`
	Name          string    `json:"name"`
	Op            string    `json:"op"`
	Description   string    `json:"description"`
	Status        string    `json:"status"`
	HTTPStatus    *int16    `json:"http_status,omitempty"`
	Start         time.Time `json:"start"`
	StartOffsetMS float64   `json:"start_offset_ms"`
	DurationMS    float64   `json:"duration_ms"`
	Release       string    `json:"release,omitempty"`
}

// TraceError is an error event linked to the trace.
type TraceError struct {
	EventID string    `json:"event_id"`
	IssueID *int64    `json:"issue_id"`
	Title   string    `json:"title"`
	TS      time.Time `json:"ts"`
	SpanID  string    `json:"span_id,omitempty"`
}

// Trace is a trace as a waterfall tree plus the errors captured inside it.
type Trace struct {
	TraceID    string       `json:"trace_id"`
	DurationMS float64      `json:"duration_ms"`
	Spans      []TraceSpan  `json:"spans"`
	Truncated  bool         `json:"truncated"`
	Errors     []TraceError `json:"errors"`
}

// Trace returns the spans of one trace of the site (through the trace index),
// ordered depth first with start offsets for a waterfall, and the error
// events recorded inside it. It returns ErrNotFound when the site has no span
// of the trace.
func (q *Querier) Trace(ctx context.Context, scope Scope, traceID [16]byte) (Trace, error) {
	t := Trace{TraceID: hex.EncodeToString(traceID[:]), Spans: []TraceSpan{}, Errors: []TraceError{}}
	rows, err := q.pool.Query(ctx, `
select span_id, parent_span_id, is_segment, name, op, description, status, http_status, start_ts, duration_us, release
from spans where site_id = $1 and trace_id = $2
order by start_ts, duration_us desc limit $3`, scope.SiteID, traceID[:], MaxTraceSpans+1)
	if err != nil {
		return t, fmt.Errorf("read trace: %w", err)
	}
	defer rows.Close()
	type node struct {
		TraceSpan
		parent   string
		children []int
	}
	var nodes []node
	for rows.Next() {
		var n node
		var sid, pid []byte
		var dur int64
		if err := rows.Scan(&sid, &pid, &n.IsSegment, &n.Name, &n.Op, &n.Description, &n.Status, &n.HTTPStatus, &n.Start, &dur, &n.Release); err != nil {
			return t, err
		}
		n.SpanID = hex.EncodeToString(sid)
		if pid != nil {
			n.parent = hex.EncodeToString(pid)
			n.ParentID = n.parent
		}
		n.DurationMS = ms(float64(dur))
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return t, err
	}
	if len(nodes) == 0 {
		return t, ErrNotFound
	}
	if len(nodes) > MaxTraceSpans {
		nodes, t.Truncated = nodes[:MaxTraceSpans], true
	}
	byID := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if _, dup := byID[n.SpanID]; !dup {
			byID[n.SpanID] = i
		}
	}
	var roots []int
	for i := range nodes {
		if p, ok := byID[nodes[i].parent]; ok && nodes[i].parent != "" && p != i {
			nodes[p].children = append(nodes[p].children, i)
		} else {
			roots = append(roots, i)
		}
	}
	origin := nodes[0].Start // rows are ordered by start
	var end time.Time
	for _, n := range nodes {
		if e := n.Start.Add(time.Duration(n.DurationMS * float64(time.Millisecond))); e.After(end) {
			end = e
		}
	}
	t.DurationMS = float64(end.Sub(origin)) / float64(time.Millisecond)
	// Depth-first walk; children are already in start order. Spans in a parent
	// cycle are unreachable from a root and appended last at depth 0.
	seen := make([]bool, len(nodes))
	var walk func(i, depth int)
	walk = func(i, depth int) {
		if seen[i] {
			return
		}
		seen[i] = true
		s := nodes[i].TraceSpan
		s.Depth = depth
		s.StartOffsetMS = float64(s.Start.Sub(origin)) / float64(time.Millisecond)
		t.Spans = append(t.Spans, s)
		for _, c := range nodes[i].children {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	for i := range nodes {
		walk(i, 0)
	}

	erows, err := q.pool.Query(ctx, `
select e.event_id, e.issue_id, coalesce(i.title, ''), e.ts, e.span_id
from app_events e left join app_issues i on i.id = e.issue_id and i.site_id = e.site_id
where e.site_id = $1 and e.trace_id = $2
order by e.ts limit $3`, scope.SiteID, traceID[:], maxTraceErrors)
	if err != nil {
		return t, fmt.Errorf("read trace errors: %w", err)
	}
	defer erows.Close()
	for erows.Next() {
		var e TraceError
		var sid []byte
		if err := erows.Scan(&e.EventID, &e.IssueID, &e.Title, &e.TS, &sid); err != nil {
			return t, err
		}
		if sid != nil {
			e.SpanID = hex.EncodeToString(sid)
		}
		t.Errors = append(t.Errors, e)
	}
	sort.SliceStable(t.Errors, func(i, j int) bool { return t.Errors[i].TS.Before(t.Errors[j].TS) })
	return t, erows.Err()
}

// ParseTraceID parses a 32-character hex trace id.
func ParseTraceID(s string) ([16]byte, error) {
	var id [16]byte
	if len(s) != 32 {
		return id, invalid("trace_id must be 32 hex characters")
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return id, invalid("trace_id must be 32 hex characters")
	}
	return id, nil
}
