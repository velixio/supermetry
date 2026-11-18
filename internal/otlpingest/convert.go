package otlpingest

import (
	"math"
	"strconv"
	"strings"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"supermetry/internal/appingest"
	"supermetry/internal/sentryingest"
	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

const (
	// MaxSpansPerRequest bounds the spans converted from one request; the
	// rest are counted as rejected.
	MaxSpansPerRequest = 10_000
	// maxExceptionsPerRequest bounds the error events one request can create.
	maxExceptionsPerRequest = 100

	maxAttrValue     = 200
	maxDescription   = 1000
	maxName          = 200
	maxSpanDuration  = 7 * 24 * time.Hour
	maxFutureSkew    = time.Hour
	maxSegmentWalk   = 64
	maxEnvReleaseLen = 64
)

type spanKey [24]byte

func keyOf(trace, span []byte) spanKey {
	var k spanKey
	copy(k[:16], trace)
	copy(k[16:], span)
	return k
}

// attrs is a small lookup over the first maxAttrs attributes of a span.
type attrs map[string]*commonpb.AnyValue

func indexAttrs(in []*commonpb.KeyValue) attrs {
	if len(in) > maxAttrs {
		in = in[:maxAttrs]
	}
	m := make(attrs, len(in))
	for _, kv := range in {
		if kv != nil && kv.Value != nil {
			m[kv.Key] = kv.Value
		}
	}
	return m
}

// str returns the first present key as a string; numbers are formatted.
func (a attrs) str(keys ...string) string {
	for _, k := range keys {
		v, ok := a[k]
		if !ok {
			continue
		}
		switch x := v.Value.(type) {
		case *commonpb.AnyValue_StringValue:
			if x.StringValue != "" {
				return x.StringValue
			}
		case *commonpb.AnyValue_IntValue:
			return strconv.FormatInt(x.IntValue, 10)
		}
	}
	return ""
}

func (a attrs) integer(keys ...string) (int64, bool) {
	for _, k := range keys {
		switch x := a[k].GetValue().(type) {
		case *commonpb.AnyValue_IntValue:
			return x.IntValue, true
		case *commonpb.AnyValue_StringValue:
			if n, err := strconv.ParseInt(x.StringValue, 10, 64); err == nil {
				return n, true
			}
		case *commonpb.AnyValue_DoubleValue:
			if !math.IsNaN(x.DoubleValue) && math.Abs(x.DoubleValue) < 1e9 {
				return int64(x.DoubleValue), true
			}
		}
	}
	return 0, false
}

type resourceInfo struct {
	release, environment, platform, osPlatform string
	webjs                                      bool
}

func readResource(a attrs) resourceInfo {
	r := resourceInfo{
		release:     clip(a.str("service.version"), maxEnvReleaseLen),
		environment: clip(a.str("deployment.environment.name", "deployment.environment"), maxEnvReleaseLen),
	}
	lang := strings.ToLower(a.str("telemetry.sdk.language"))
	r.webjs = lang == "webjs"
	switch lang {
	case "nodejs":
		r.platform = "node"
	case "webjs":
		r.platform = "javascript"
	default:
		r.platform = clip(lang, 32)
	}
	switch strings.ToLower(a.str("os.type")) {
	case "windows":
		r.osPlatform = "windows"
	case "darwin":
		r.osPlatform = "macos"
	case "linux":
		r.osPlatform = "linux"
	}
	if r.platform == "" {
		r.platform = r.osPlatform
	}
	return r
}

// eventPlatform maps a resource to the platform vocabulary of app events.
func (r resourceInfo) eventPlatform() string {
	switch {
	case r.webjs:
		return "web"
	case r.osPlatform != "":
		return r.osPlatform
	}
	return "other"
}

type converted struct {
	span   spans.Span
	parent spanKey
	hasPar bool
	kind   tracepb.Span_SpanKind
}

// Convert normalises an export request into spans and error events. Spans
// that are unusable (bad ids, no or inverted timestamps, too old or too far in
// the future) are counted in rejected and skipped; the rest are kept. Nothing
// here reads the database.
func Convert(req *Request, site sites.Site, now time.Time) (out []spans.Span, errs []appingest.Event, rejected int) {
	maxAgeDays := 13
	if site.App != nil && site.App.MaxReportAgeDays > 0 && site.App.MaxReportAgeDays < maxAgeDays {
		maxAgeDays = site.App.MaxReportAgeDays
	}
	oldest := now.Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	newest := now.Add(maxFutureSkew)

	var all []converted
	index := map[spanKey]int{}
	for _, rs := range req.ResourceSpans {
		res := readResource(indexAttrs(rs.GetResource().GetAttributes()))
		for _, ss := range rs.GetScopeSpans() {
			for _, sp := range ss.GetSpans() {
				if len(all) >= MaxSpansPerRequest {
					rejected++
					continue
				}
				c, ok := convertSpan(sp, res, site, oldest, newest)
				if !ok {
					rejected++
					continue
				}
				if len(errs) < maxExceptionsPerRequest {
					errs = append(errs, exceptionEvents(sp, res, c.span, maxExceptionsPerRequest-len(errs))...)
				}
				index[keyOf(c.span.TraceID[:], c.span.SpanID[:])] = len(all)
				all = append(all, c)
			}
		}
	}

	// Segment roles: a root span, or a server/consumer span whose parent is
	// not in this request (a remote parent), starts a segment. Others point at
	// the nearest segment ancestor in the batch, else at themselves.
	for i := range all {
		c := &all[i]
		parentIn := false
		if c.hasPar {
			_, parentIn = index[c.parent]
		}
		c.span.IsSegment = !c.hasPar || (!parentIn && (c.kind == tracepb.Span_SPAN_KIND_SERVER || c.kind == tracepb.Span_SPAN_KIND_CONSUMER))
	}
	out = make([]spans.Span, len(all))
	for i := range all {
		c := &all[i]
		seg := c.span.SpanID
		if !c.span.IsSegment {
			cur := c
			for step := 0; step < maxSegmentWalk && cur.hasPar; step++ {
				pi, ok := index[cur.parent]
				if !ok {
					break
				}
				cur = &all[pi]
				if cur.span.IsSegment {
					seg = cur.span.SpanID
					break
				}
			}
		}
		c.span.SegmentID = seg
		out[i] = c.span
	}
	return out, errs, rejected
}

// convertSpan validates and maps one span. The returned attrs index is passed
// on for the exception events so the attributes are indexed once.
func convertSpan(sp *tracepb.Span, res resourceInfo, site sites.Site, oldest, newest time.Time) (converted, bool) {
	var c converted
	if sp == nil || len(sp.TraceId) != 16 || len(sp.SpanId) != 8 || allZero(sp.TraceId) || allZero(sp.SpanId) {
		return c, false
	}
	start, end := sp.StartTimeUnixNano, sp.EndTimeUnixNano
	if start == 0 || end == 0 || end < start || end > math.MaxInt64 || start > math.MaxInt64 {
		return c, false
	}
	startT := time.Unix(0, int64(start)).UTC()
	if startT.Before(oldest) || startT.After(newest) {
		return c, false
	}
	dur := time.Duration(end - start)
	if dur > maxSpanDuration || dur < 0 {
		dur = maxSpanDuration
	}

	a := indexAttrs(sp.Attributes)
	s := spans.Span{SiteID: site.ID, Start: startT, DurationUS: dur.Microseconds(), Release: res.release, Environment: res.environment, Platform: res.platform}
	copy(s.TraceID[:], sp.TraceId)
	copy(s.SpanID[:], sp.SpanId)
	if len(sp.ParentSpanId) == 8 && !allZero(sp.ParentSpanId) {
		var p [8]byte
		copy(p[:], sp.ParentSpanId)
		s.ParentID = &p
		c.parent = keyOf(sp.TraceId, sp.ParentSpanId)
		c.hasPar = true
	}
	c.kind = sp.Kind

	method := strings.ToUpper(a.str("http.request.method", "http.method"))
	route := a.str("http.route")
	dbSystem := a.str("db.system.name", "db.system")
	msgSystem := a.str("messaging.system")
	server := sp.Kind == tracepb.Span_SPAN_KIND_SERVER
	rawURL := a.str("url.full", "http.url")

	switch {
	case dbSystem != "":
		s.Op = "db"
	case method != "" && sp.Kind == tracepb.Span_SPAN_KIND_CLIENT:
		s.Op = "http.client"
	case method != "" && server:
		s.Op = "http.server"
	case msgSystem != "":
		s.Op = "queue"
	default:
		s.Op = "default"
	}

	name := parameteriseName(sp.Name)
	if route != "" {
		name = clip(strings.TrimSpace(method+" "+route), maxName)
	} else if server && method != "" && rawURL == "" {
		// A server span named only by its method and a raw path: normalise it.
		if p := a.str("url.path", "http.target"); p != "" {
			name = clip(method+" "+parameterisePath(p), maxName)
		}
	}
	if name == "" {
		name = "<unnamed>"
	}
	s.Name = clip(sentryingest.ScrubText(name), maxName)

	desc := name
	switch s.Op {
	case "db":
		if q := a.str("db.query.text", "db.statement"); q != "" {
			desc = parameteriseSQL(q)
		}
	case "http.client":
		if p, ok := parameteriseURL(rawURL); ok {
			desc = clip(method+" "+p, maxDescription)
		} else if host := a.str("server.address", "net.peer.name", "http.host"); host != "" {
			desc = clip(method+" "+host+parameterisePath(a.str("url.path", "http.target")), maxDescription)
		}
	}
	s.Description = clip(sentryingest.ScrubText(desc), maxDescription)

	switch sp.GetStatus().GetCode() {
	case tracepb.Status_STATUS_CODE_ERROR:
		s.Status = "internal_error"
	default:
		s.Status = "ok"
	}
	if code, ok := a.integer("http.response.status_code", "http.status_code"); ok && code >= 100 && code <= 599 {
		v := int16(code)
		s.HTTPStatus = &v
	}
	s.Attrs = allowlist(a, method, dbSystem, msgSystem, rawURL)
	c.span = s
	return c, true
}

// allowlist keeps only the attribute keys the plan permits; values are cut to
// 200 bytes and the server address is a bare host.
func allowlist(a attrs, method, dbSystem, msgSystem, rawURL string) map[string]string {
	out := map[string]string{}
	put := func(k, v string) {
		if v != "" {
			out[k] = clip(v, maxAttrValue)
		}
	}
	put("http.method", method)
	if code, ok := a.integer("http.response.status_code", "http.status_code"); ok {
		put("http.status_code", strconv.FormatInt(code, 10))
	}
	put("db.system", dbSystem)
	put("db.operation", a.str("db.operation.name", "db.operation"))
	host := a.str("server.address", "net.peer.name", "http.host")
	if host == "" && rawURL != "" {
		if p, ok := parameteriseURL(rawURL); ok {
			host = strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(p, "https://"), "http://"), "/", 2)[0]
		}
	}
	put("server.address", hostOnly(host))
	put("messaging.system", msgSystem)
	put("rpc.method", a.str("rpc.method"))
	if len(out) == 0 {
		return nil
	}
	return out
}

// hostOnly drops any userinfo, port-less path or query from a host value.
func hostOnly(h string) string {
	if i := strings.LastIndexByte(h, '@'); i >= 0 {
		h = h[i+1:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	return h
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
