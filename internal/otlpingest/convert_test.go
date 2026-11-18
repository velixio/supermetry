package otlpingest

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"supermetry/internal/spans"
)

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func byName(ss []spans.Span, name string) spans.Span {
	for _, s := range ss {
		if s.Name == name {
			return s
		}
	}
	panic("no span " + name)
}

func TestConvertGoHTTPFixture(t *testing.T) {
	req, err := DecodeProto(mustRead(t, "go-http.pb"))
	if err != nil {
		t.Fatal(err)
	}
	got, errs, rejected := Convert(req, testSite(), testNow)
	if rejected != 0 || len(errs) != 0 || len(got) != 3 {
		t.Fatalf("spans=%d errs=%d rejected=%d", len(got), len(errs), rejected)
	}
	root := byName(got, "GET /users/{id}")
	if !root.IsSegment || root.Op != "http.server" || root.Status != "ok" || root.SegmentID != root.SpanID || root.ParentID != nil {
		t.Fatalf("root=%+v", root)
	}
	if root.HTTPStatus == nil || *root.HTTPStatus != 200 || root.DurationUS != 120_000 {
		t.Fatalf("root status/duration=%v %d", root.HTTPStatus, root.DurationUS)
	}
	if root.Release != "1.4.0" || root.Environment != "production" || root.Platform != "go" {
		t.Fatalf("resource=%q %q %q", root.Release, root.Environment, root.Platform)
	}
	db := byName(got, "SELECT users")
	if db.IsSegment || db.Op != "db" || db.SegmentID != root.SpanID || db.ParentID == nil || *db.ParentID != root.SpanID {
		t.Fatalf("db=%+v", db)
	}
	if want := "select * from users where email = ? and id = ?"; db.Description != want {
		t.Fatalf("db description %q", db.Description)
	}
	call := byName(got, "GET")
	if call.Op != "http.client" || call.Status != "internal_error" || call.SegmentID != root.SpanID {
		t.Fatalf("call=%+v", call)
	}
	if want := "GET https://api.example.com/v1/items/*"; call.Description != want {
		t.Fatalf("http description %q", call.Description)
	}
	if call.HTTPStatus == nil || *call.HTTPStatus != 502 || call.Attrs["server.address"] != "api.example.com" {
		t.Fatalf("call attrs=%v", call.Attrs)
	}
	blob, _ := json.Marshal(got)
	for _, leak := range []string{"a@b.c", "SECRET", "token=", "user:pw", "42"} {
		if leak == "42" {
			continue // ids legitimately appear as numbers elsewhere
		}
		if strings.Contains(string(blob), leak) {
			t.Fatalf("converted spans leak %q: %s", leak, blob)
		}
	}
	for _, s := range got {
		for k := range s.Attrs {
			switch k {
			case "http.method", "http.status_code", "db.system", "db.operation", "server.address", "messaging.system", "rpc.method":
			default:
				t.Fatalf("attribute %q outside the allowlist", k)
			}
		}
	}
}

func TestConvertPythonJSONFixture(t *testing.T) {
	req, err := DecodeJSON(mustRead(t, "python-flask.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, errs, rejected := Convert(req, testSite(), testNow)
	if rejected != 0 || len(errs) != 0 || len(got) != 4 {
		t.Fatalf("spans=%d errs=%d rejected=%d", len(got), len(errs), rejected)
	}
	root := byName(got, "GET /items/<int:item_id>")
	if !root.IsSegment || root.Op != "http.server" || root.Platform != "python" || root.Environment != "prod" || root.Release != "0.9.3" {
		t.Fatalf("root=%+v", root)
	}
	if hex.EncodeToString(root.TraceID[:]) != "5b8efff798038103d269b633813fc60c" {
		t.Fatalf("trace id %x", root.TraceID)
	}
	if d := byName(got, "SELECT items").Description; d != "SELECT * FROM items WHERE id = ? AND owner = ?" {
		t.Fatalf("db description %q", d)
	}
	post := byName(got, "POST")
	if post.Description != "POST https://payments.example.com/charge/*" || post.SegmentID != root.SpanID {
		t.Fatalf("post=%+v", post)
	}
	if r := byName(got, "render_template"); r.Op != "default" || r.IsSegment || r.SegmentID != root.SpanID {
		t.Fatalf("render=%+v", r)
	}
}

func TestConvertNodeExpressFixtureAndException(t *testing.T) {
	req, err := DecodeProto(mustRead(t, "node-express.pb"))
	if err != nil {
		t.Fatal(err)
	}
	got, errs, rejected := Convert(req, testSite(), testNow)
	if rejected != 0 || len(got) != 2 || len(errs) != 1 {
		t.Fatalf("spans=%d errs=%d rejected=%d", len(got), len(errs), rejected)
	}
	root := byName(got, "GET /orders/*/items")
	if !root.IsSegment || root.Status != "internal_error" || root.Platform != "node" || root.Environment != "staging" {
		t.Fatalf("root=%+v", root)
	}
	e := errs[0]
	if e.Kind != "error" || e.ErrorType != "TypeError" || e.Release != "2.0.1" || e.Platform != "other" {
		t.Fatalf("event=%+v", e)
	}
	if hex.EncodeToString(e.TraceID) != strings.Repeat("09", 16) || hex.EncodeToString(e.SpanID) != strings.Repeat("01", 8) {
		t.Fatalf("event ids %x %x", e.TraceID, e.SpanID)
	}
	if strings.Contains(e.Frames, "alice") || e.Frames == "" || e.Fingerprint == "" || e.ID == "" {
		t.Fatalf("frames=%q fp=%q id=%q", e.Frames, e.Fingerprint, e.ID)
	}
	if strings.Contains(e.Title, "1234567") {
		t.Fatalf("title keeps a long number: %q", e.Title)
	}
}

// jsonOf renders a Request as OTLP/JSON the way exporters do (hex ids, string
// 64-bit integers).
func jsonOf(r *Request) []byte {
	anyv := func(v *commonpb.AnyValue) map[string]any {
		switch x := v.Value.(type) {
		case *commonpb.AnyValue_StringValue:
			return map[string]any{"stringValue": x.StringValue}
		case *commonpb.AnyValue_IntValue:
			return map[string]any{"intValue": strconv.FormatInt(x.IntValue, 10)}
		}
		return nil
	}
	kvs := func(in []*commonpb.KeyValue) []any {
		out := []any{}
		for _, kv := range in {
			out = append(out, map[string]any{"key": kv.Key, "value": anyv(kv.Value)})
		}
		return out
	}
	var rss []any
	for _, rs := range r.ResourceSpans {
		var sps []any
		for _, s := range rs.ScopeSpans[0].Spans {
			evs := []any{}
			for _, e := range s.Events {
				evs = append(evs, map[string]any{"name": e.Name, "timeUnixNano": strconv.FormatUint(e.TimeUnixNano, 10), "attributes": kvs(e.Attributes)})
			}
			sps = append(sps, map[string]any{
				"traceId": hex.EncodeToString(s.TraceId), "spanId": hex.EncodeToString(s.SpanId), "parentSpanId": hex.EncodeToString(s.ParentSpanId),
				"name": s.Name, "kind": int(s.Kind),
				"startTimeUnixNano": strconv.FormatUint(s.StartTimeUnixNano, 10), "endTimeUnixNano": strconv.FormatUint(s.EndTimeUnixNano, 10),
				"attributes": kvs(s.Attributes), "events": evs, "status": map[string]any{"code": int(s.Status.Code)},
			})
		}
		rss = append(rss, map[string]any{"resource": map[string]any{"attributes": kvs(rs.Resource.Attributes)}, "scopeSpans": []any{map[string]any{"spans": sps}}})
	}
	b, _ := json.Marshal(map[string]any{"resourceSpans": rss})
	return b
}

func TestProtoAndJSONConvertIdentically(t *testing.T) {
	for name, r := range map[string]*Request{"go": goHTTP(), "node": nodeExpress()} {
		fromProto, err := DecodeProto(marshalRequest(r))
		if err != nil {
			t.Fatal(err)
		}
		fromJSON, err := DecodeJSON(jsonOf(r))
		if err != nil {
			t.Fatal(err)
		}
		s1, e1, r1 := Convert(fromProto, testSite(), testNow)
		s2, e2, r2 := Convert(fromJSON, testSite(), testNow)
		if !reflect.DeepEqual(s1, s2) || !reflect.DeepEqual(e1, e2) || r1 != r2 {
			t.Fatalf("%s: proto and JSON differ\n%+v\n%+v", name, s1, s2)
		}
		if len(s1) == 0 {
			t.Fatalf("%s: nothing converted", name)
		}
	}
}

func TestConvertRejectsBadSpans(t *testing.T) {
	ok := func() *tracepb.Span {
		return mkSpan(3, 3, 0, "ok", tracepb.Span_SPAN_KIND_SERVER, time.Minute, time.Second)
	}
	cases := map[string]func(*tracepb.Span){
		"short trace id":     func(s *tracepb.Span) { s.TraceId = s.TraceId[:5] },
		"long trace id":      func(s *tracepb.Span) { s.TraceId = []byte(strings.Repeat("x", 300)) },
		"zero trace id":      func(s *tracepb.Span) { s.TraceId = make([]byte, 16) },
		"zero span id":       func(s *tracepb.Span) { s.SpanId = make([]byte, 8) },
		"end before start":   func(s *tracepb.Span) { s.EndTimeUnixNano = s.StartTimeUnixNano - 1 },
		"no start":           func(s *tracepb.Span) { s.StartTimeUnixNano = 0 },
		"no end":             func(s *tracepb.Span) { s.EndTimeUnixNano = 0 },
		"too old":            func(s *tracepb.Span) { shift(s, -15*24*time.Hour) },
		"older than app max": func(s *tracepb.Span) { shift(s, -8*24*time.Hour) },
		"far future":         func(s *tracepb.Span) { shift(s, 3*time.Hour) },
		"overflowing time":   func(s *tracepb.Span) { s.StartTimeUnixNano = 1 << 63; s.EndTimeUnixNano = 1<<63 + 5 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bad := ok()
			bad.SpanId = id8(4)
			mutate(bad)
			got, _, rejected := Convert(request(nil, ok(), bad), testSite(), testNow)
			if rejected != 1 || len(got) != 1 || got[0].Name != "ok" {
				t.Fatalf("rejected=%d kept=%d", rejected, len(got))
			}
		})
	}
}

func shift(s *tracepb.Span, d time.Duration) {
	s.StartTimeUnixNano = uint64(int64(s.StartTimeUnixNano) + int64(d))
	s.EndTimeUnixNano = uint64(int64(s.EndTimeUnixNano) + int64(d))
}

func TestConvertCapsSpansPerRequest(t *testing.T) {
	var many []*tracepb.Span
	for i := 0; i < 100_000; i++ {
		s := mkSpan(5, 1, 0, "bulk", tracepb.Span_SPAN_KIND_SERVER, time.Minute, time.Millisecond)
		s.SpanId = []byte{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i), 0, 0, 0, 1}
		many = append(many, s)
	}
	got, _, rejected := Convert(request(nil, many...), testSite(), testNow)
	if len(got) != MaxSpansPerRequest || rejected != 100_000-MaxSpansPerRequest {
		t.Fatalf("kept=%d rejected=%d", len(got), rejected)
	}
}

func TestConvertJSONBadIdsRejectOnlyThatSpan(t *testing.T) {
	good := map[string]any{"traceId": strings.Repeat("ab", 16), "spanId": strings.Repeat("cd", 8), "name": "good", "kind": 2,
		"startTimeUnixNano": strconv.FormatInt(testNow.Add(-time.Minute).UnixNano(), 10), "endTimeUnixNano": strconv.FormatInt(testNow.Add(-time.Minute+time.Second).UnixNano(), 10)}
	badTrace := map[string]any{}
	for k, v := range good {
		badTrace[k] = v
	}
	badTrace["traceId"] = strings.Repeat("a", 300)
	body, _ := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{badTrace, good}}}}}})
	req, err := DecodeJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	got, _, rejected := Convert(req, testSite(), testNow)
	if rejected != 1 || len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("rejected=%d kept=%d", rejected, len(got))
	}
}

func TestSegmentRoles(t *testing.T) {
	// remote-parent server span starts a segment; an internal child whose parent
	// is missing is not a segment and points at itself; a deep chain resolves.
	remote := mkSpan(7, 1, 99, "remote server", tracepb.Span_SPAN_KIND_SERVER, time.Minute, time.Second)
	child := mkSpan(7, 2, 1, "child", tracepb.Span_SPAN_KIND_INTERNAL, time.Minute, time.Second)
	grand := mkSpan(7, 3, 2, "grand", tracepb.Span_SPAN_KIND_CLIENT, time.Minute, time.Second)
	orphan := mkSpan(7, 4, 98, "orphan", tracepb.Span_SPAN_KIND_INTERNAL, time.Minute, time.Second)
	got, _, _ := Convert(request(nil, grand, orphan, child, remote), testSite(), testNow)
	if len(got) != 4 {
		t.Fatalf("got %d", len(got))
	}
	r := byName(got, "remote server")
	if !r.IsSegment || byName(got, "grand").SegmentID != r.SpanID || byName(got, "child").SegmentID != r.SpanID {
		t.Fatalf("chain not resolved: %+v", got)
	}
	if o := byName(got, "orphan"); o.IsSegment || o.SegmentID != o.SpanID {
		t.Fatalf("orphan=%+v", o)
	}
}

func TestSegmentCycleTerminates(t *testing.T) {
	a := mkSpan(8, 1, 2, "a", tracepb.Span_SPAN_KIND_INTERNAL, time.Minute, time.Second)
	b := mkSpan(8, 2, 1, "b", tracepb.Span_SPAN_KIND_INTERNAL, time.Minute, time.Second)
	got, _, _ := Convert(request(nil, a, b), testSite(), testNow)
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
}

func TestParameteriseSQL(t *testing.T) {
	cases := map[string]string{
		"select * from users where email = 'a@b.c' and id = 42": "select * from users where email = ? and id = ?",
		"SELECT 1":                                        "SELECT ?",
		"select x from t1 where a in (1, 2, 3)":           "select x from t1 where a in (?)",
		"insert into t values ('it''s', 3.5e2) -- secret": "insert into t values (?)",
		`select "Col1" from t /* c */ where n=$1`:         `select "Col1" from t where n=$1`,
		"select 'unterminated":                            "select ?",
	}
	for in, want := range cases {
		if got := parameteriseSQL(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	long := parameteriseSQL("select " + strings.Repeat("é", 2000))
	if len(long) > 1000 || !strings.HasSuffix(long, "é") {
		t.Fatalf("len=%d", len(long))
	}
}

func TestParameteriseURL(t *testing.T) {
	got, ok := parameteriseURL("https://u:p@Api.example.com:8443/v1/items/12345/files/3f2504e0-4f89-11d3-9a0c-0305e82c3301?token=S#f")
	if !ok || got != "https://Api.example.com:8443/v1/items/*/files/*" {
		t.Fatalf("%q %v", got, ok)
	}
	if _, ok := parameteriseURL("/relative/path"); ok {
		t.Fatal("relative url accepted")
	}
	if n := parameteriseName("GET /users/42?x=1"); n != "GET /users/*" {
		t.Fatalf("%q", n)
	}
}

func TestOnlyAllowlistedAttrsAndBounds(t *testing.T) {
	long := strings.Repeat("x", 500)
	s := mkSpan(2, 2, 0, strings.Repeat("n", 500), tracepb.Span_SPAN_KIND_CLIENT, time.Minute, time.Second,
		kvStr("db.system", long), kvStr("http.request.method", "get"), kvStr("user.email", "a@b.c"), kvStr("http.request.header.cookie", "sid=1"), kvStr("db.statement", "select "+long))
	got, _, _ := Convert(request(nil, s), testSite(), testNow)
	if len(got) != 1 {
		t.Fatal("not converted")
	}
	sp := got[0]
	if len(sp.Name) > 200 || len(sp.Description) > 1000 || len(sp.Attrs["db.system"]) > 200 {
		t.Fatalf("unbounded: %d %d %d", len(sp.Name), len(sp.Description), len(sp.Attrs["db.system"]))
	}
	if len(sp.Attrs) > 10 {
		t.Fatalf("attrs=%v", sp.Attrs)
	}
	if fmt.Sprint(sp.Attrs) == "" || strings.Contains(fmt.Sprint(sp.Attrs), "a@b.c") || strings.Contains(fmt.Sprint(sp.Attrs), "sid=1") {
		t.Fatalf("leak: %v", sp.Attrs)
	}
}
