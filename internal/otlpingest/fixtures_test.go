package otlpingest

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"supermetry/internal/sites"
)

const testKey = "0123456789abcdef0123456789abcdef"

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func kvStr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func kvInt(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

func id16(b byte) []byte { return bytes.Repeat([]byte{b}, 16) }
func id8(b byte) []byte  { return bytes.Repeat([]byte{b}, 8) }

// mkSpan builds a span that starts startAgo before testNow and lasts dur.
func mkSpan(trace, id, parent byte, name string, kind tracepb.Span_SpanKind, startAgo, dur time.Duration, attrs ...*commonpb.KeyValue) *tracepb.Span {
	start := testNow.Add(-startAgo)
	s := &tracepb.Span{
		TraceId: id16(trace), SpanId: id8(id), Name: name, Kind: kind,
		StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(dur).UnixNano()),
		Attributes: attrs, Status: &tracepb.Status{},
	}
	if parent != 0 {
		s.ParentSpanId = id8(parent)
	}
	return s
}

func request(res []*commonpb.KeyValue, spans ...*tracepb.Span) *Request {
	return &Request{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   &resourcepb.Resource{Attributes: res},
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
	}}}
}

// marshalRequest encodes a Request the way an exporter does.
func marshalRequest(r *Request) []byte {
	var out []byte
	for _, rs := range r.ResourceSpans {
		b, err := proto.Marshal(rs)
		if err != nil {
			panic(err)
		}
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendBytes(out, b)
	}
	return out
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func testSite() sites.Site {
	return sites.Site{ID: 42, OrgID: 1, Kind: sites.KindApp, TZ: time.UTC, App: &sites.AppConfig{
		DSNKeys: []sites.DSNKey{{PublicKey: testKey}}, MaxReportAgeDays: 7,
		HashSecret: []byte("0123456789abcdef0123456789abcdef"),
	}}
}

// goHTTP is the shape the Go otelhttp instrumentation exports: a server span
// with a route, a database child and an outgoing HTTP call.
func goHTTP() *Request {
	res := []*commonpb.KeyValue{kvStr("service.name", "api"), kvStr("service.version", "1.4.0"), kvStr("deployment.environment", "production"), kvStr("telemetry.sdk.language", "go")}
	root := mkSpan(1, 1, 0, "GET /users/{id}", tracepb.Span_SPAN_KIND_SERVER, 10*time.Second, 120*time.Millisecond,
		kvStr("http.request.method", "GET"), kvStr("http.route", "/users/{id}"), kvInt("http.response.status_code", 200), kvStr("url.path", "/users/42"))
	dbq := mkSpan(1, 2, 1, "SELECT users", tracepb.Span_SPAN_KIND_CLIENT, 10*time.Second-5*time.Millisecond, 20*time.Millisecond,
		kvStr("db.system", "postgresql"), kvStr("db.operation", "SELECT"),
		kvStr("db.statement", "select * from users where email = 'a@b.c' and id = 42"))
	call := mkSpan(1, 3, 1, "GET", tracepb.Span_SPAN_KIND_CLIENT, 10*time.Second-30*time.Millisecond, 60*time.Millisecond,
		kvStr("http.request.method", "GET"), kvStr("url.full", "https://user:pw@api.example.com/v1/items/12345?token=SECRET#frag"), kvInt("http.response.status_code", 502))
	call.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR}
	return request(res, root, dbq, call)
}

// nodeExpress is a server span with an exception event and a stack trace.
func nodeExpress() *Request {
	res := []*commonpb.KeyValue{kvStr("service.name", "web"), kvStr("service.version", "2.0.1"), kvStr("deployment.environment.name", "staging"), kvStr("telemetry.sdk.language", "nodejs")}
	root := mkSpan(9, 1, 0, "GET", tracepb.Span_SPAN_KIND_SERVER, 5*time.Second, 80*time.Millisecond,
		kvStr("http.method", "GET"), kvStr("http.target", "/orders/9f8b7c6d5e4f3a2b/items"), kvInt("http.status_code", 500))
	root.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR}
	root.Events = []*tracepb.Span_Event{{
		Name: "exception", TimeUnixNano: root.StartTimeUnixNano + 1_000_000,
		Attributes: []*commonpb.KeyValue{
			kvStr("exception.type", "TypeError"),
			kvStr("exception.message", "Cannot read properties of undefined (reading 'id') for order 1234567"),
			kvStr("exception.stacktrace", "TypeError: boom\n    at handler (/home/alice/app/src/orders.js:10:5)\n    at Layer.handle (/home/alice/app/node_modules/express/lib/router/layer.js:95:5)"),
		},
	}, {Name: "log", TimeUnixNano: root.StartTimeUnixNano}}
	child := mkSpan(9, 2, 1, "middleware - query", tracepb.Span_SPAN_KIND_INTERNAL, 5*time.Second-time.Millisecond, 10*time.Millisecond)
	return request(res, root, child)
}

// TestGenerateFixtures rewrites testdata/*.pb. Run with GENERATE_FIXTURES=1.
func TestGenerateFixtures(t *testing.T) {
	if os.Getenv("GENERATE_FIXTURES") == "" {
		t.Skip("set GENERATE_FIXTURES=1 to rewrite testdata")
	}
	for name, r := range map[string]*Request{"go-http.pb": goHTTP(), "node-express.pb": nodeExpress()} {
		if err := os.WriteFile(filepath.Join("testdata", name), marshalRequest(r), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
