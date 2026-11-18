package otlpingest

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"supermetry/internal/appingest"
	"supermetry/internal/obslogs"
	"supermetry/internal/obsquota"
	"supermetry/internal/sentryingest"
	"supermetry/internal/sites"
)

const pathLogs42 = "/api/42/otlp/v1/logs"

var logSecret = []byte("0123456789abcdef0123456789abcdef")

func nanos(t time.Time) uint64 { return uint64(t.UnixNano()) }

func strVal(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}

func logRec(sev logspb.SeverityNumber, body string, kvs ...*commonpb.KeyValue) *logspb.LogRecord {
	return &logspb.LogRecord{TimeUnixNano: nanos(testNow.Add(-time.Minute)), SeverityNumber: sev, Body: strVal(body), Attributes: kvs}
}

func logsReq(res []*commonpb.KeyValue, recs ...*logspb.LogRecord) *LogsRequest {
	return &LogsRequest{ResourceLogs: []*logspb.ResourceLogs{{
		Resource:  &resourcepb.Resource{Attributes: res},
		ScopeLogs: []*logspb.ScopeLogs{{Scope: &commonpb.InstrumentationScope{Name: "s", Attributes: []*commonpb.KeyValue{kv("scope.attr", "dropped")}}, LogRecords: recs}},
	}}}
}

func marshalLogs(t testing.TB, req *LogsRequest) []byte {
	t.Helper()
	var out []byte
	for _, rl := range req.ResourceLogs {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(rl)
		if err != nil {
			t.Fatal(err)
		}
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendBytes(out, b)
	}
	return out
}

func javaAgentLogs() *LogsRequest {
	res := []*commonpb.KeyValue{kv("service.name", "orders"), kv("service.version", "3.1.0"), kv("deployment.environment", "production"), kv("telemetry.sdk.language", "java"), kv("process.runtime.name", "OpenJDK")}
	r1 := logRec(logspb.SeverityNumber_SEVERITY_NUMBER_INFO, "order 1001 placed", kv("thread.name", "http-nio-8080-exec-1"), kv("code.namespace", "com.example.Orders"))
	r1.TraceId = []byte{0x5b, 0x8e, 0xff, 0xf7, 0x98, 0x03, 0x81, 0x03, 0xd2, 0x69, 0xb6, 0x33, 0x81, 0x3f, 0xc6, 0x0c}
	r1.SpanId = []byte{0xee, 0xe1, 0x9b, 0x7e, 0xc3, 0xc1, 0xb1, 0x74}
	r2 := logRec(logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, "payment declined", kv("exception.type", "java.io.IOException"))
	return logsReq(res, r1, r2)
}

// TestJavaAgentFixtureUpToDate keeps testdata/logs/java_agent.pb equal to the
// builder above; UPDATE_FIXTURES=1 rewrites it.
func TestJavaAgentFixtureUpToDate(t *testing.T) {
	want := marshalLogs(t, javaAgentLogs())
	const path = "testdata/logs/java_agent.pb"
	if os.Getenv("UPDATE_FIXTURES") != "" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s is stale or missing (run with UPDATE_FIXTURES=1): %v", path, err)
	}
}

type countStats struct {
	mu sync.Mutex
	m  map[string]int
}

func (s *countStats) Add(name string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]int{}
	}
	s.m[name] += n
}

func (s *countStats) get(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[name]
}

func TestNormaliseLogsMapping(t *testing.T) {
	res := []*commonpb.KeyValue{kv("service.name", "api"), kv("deployment.environment.name", "prod"), kv("service.version", "1.4.2"), kv("host.name", "web-1"),
		{Key: "process.pid", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 77}}}}
	a := logRec(logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, "payment failed for jane@example.com",
		kv("user.id", "42"), kv("client.address", "203.0.113.9"), kv("http.route", "/pay"))
	b := &logspb.LogRecord{TimeUnixNano: nanos(testNow), SeverityText: "WARN", Body: strVal("careful")}
	recs, rejected, warn := NormaliseLogs(logsReq(res, a, b), SiteContext{ID: 42, HashSecret: logSecret}, testNow)
	if len(recs) != 2 || rejected != 0 || warn != "" {
		t.Fatalf("records %d rejected %d warn %q", len(recs), rejected, warn)
	}
	ra, rb := recs[0], recs[1]
	if ra.Severity != 17 || ra.Body != "payment failed for [email]" || ra.Service != "api" || ra.Environment != "prod" || ra.Release != "1.4.2" || ra.Source != obslogs.SourceOTLP {
		t.Errorf("a = %+v", ra)
	}
	if want := sentryingest.HashUser(logSecret, "42"); !bytes.Equal(ra.UserHash, want) || want == nil {
		t.Errorf("user hash = %x", ra.UserHash)
	}
	if len(ra.Attributes) != 2 || ra.Attributes["host.name"] != "web-1" || ra.Attributes["http.route"] != "/pay" {
		t.Errorf("a attributes = %v, want exactly host.name and http.route", ra.Attributes)
	}
	if rb.Severity != 13 {
		t.Errorf("b severity = %d, want 13 from WARN", rb.Severity)
	}
	if !ra.TS.Equal(testNow.Add(-time.Minute)) || !ra.Observed.Equal(testNow) {
		t.Errorf("a times: ts %v observed %v", ra.TS, ra.Observed)
	}
}

func TestLogCaps(t *testing.T) {
	var kvs []*commonpb.KeyValue
	for i := 0; i < 40; i++ {
		kvs = append(kvs, kv(fmt.Sprintf("k%02d", i), "v"))
	}
	kvs = append(kvs, kv("wide", strings.Repeat("w", 2000)))
	rec := logRec(logspb.SeverityNumber_SEVERITY_NUMBER_INFO, strings.Repeat("a", 9000), kvs...)
	st := &countStats{}
	recs, _, _ := NormaliseLogs(logsReq(nil, rec), SiteContext{ID: 42, Stats: st}, testNow)
	r := recs[0]
	if len(r.Body) != 8192 || !r.Truncated {
		t.Errorf("body %d bytes truncated=%v, want 8192 and true", len(r.Body), r.Truncated)
	}
	if len(r.Attributes) != 32 {
		t.Errorf("attributes kept = %d, want 32", len(r.Attributes))
	}
	if got := st.get("log_attrs_dropped"); got != 9 {
		// 41 attributes (40 plus "wide") sorted: k00..k39 then wide; 32 kept.
		t.Errorf("log_attrs_dropped = %d, want 9", got)
	}
	// The 2,000-byte value is cut to 1,024 when it survives the count cap.
	recs, _, _ = NormaliseLogs(logsReq(nil, logRec(logspb.SeverityNumber_SEVERITY_NUMBER_INFO, "x", kv("wide", strings.Repeat("w", 2000)))), SiteContext{ID: 42}, testNow)
	if v := recs[0].Attributes["wide"].(string); len(v) != 1024 {
		t.Errorf("wide value = %d bytes, want 1024", len(v))
	}
}

func TestLogNULStripped(t *testing.T) {
	rec := logRec(logspb.SeverityNumber_SEVERITY_NUMBER_INFO, "a\x00b", kv("k", "v\x00w"))
	recs, _, _ := NormaliseLogs(logsReq(nil, rec), SiteContext{ID: 42}, testNow)
	if recs[0].Body != "ab" || recs[0].Attributes["k"] != "vw" {
		t.Errorf("record = %+v", recs[0])
	}
	// The JSON form carries the NUL as an escape.
	req, err := DecodeLogs([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"x\u0000y"},"attributes":[{"key":"k","value":{"stringValue":"p\u0000q"}}]}]}]}]}`), ctJSON)
	if err != nil {
		t.Fatal(err)
	}
	recs, _, _ = NormaliseLogs(req, SiteContext{ID: 42}, testNow)
	if recs[0].Body != "xy" || recs[0].Attributes["k"] != "pq" {
		t.Errorf("json record = %+v", recs[0])
	}
}

func TestTraceIDForms(t *testing.T) {
	want := []byte{0x5b, 0x8e, 0xff, 0xf7, 0x98, 0x03, 0x81, 0x03, 0xd2, 0x69, 0xb6, 0x33, 0x81, 0x3f, 0xc6, 0x0c}
	mk := func(tid, sid []byte) obslogs.Record {
		rec := logRec(logspb.SeverityNumber_SEVERITY_NUMBER_INFO, "x")
		rec.TraceId, rec.SpanId = tid, sid
		recs, _, _ := NormaliseLogs(logsReq(nil, rec), SiteContext{ID: 42}, testNow)
		return recs[0]
	}
	if r := mk(want, []byte{1, 2, 3, 4, 5, 6, 7, 8}); !bytes.Equal(r.TraceID, want) || len(r.SpanID) != 8 {
		t.Errorf("protobuf ids: trace %x span %x", r.TraceID, r.SpanID)
	}
	if r := mk(want[:15], []byte{1, 2, 3}); r.TraceID != nil || r.SpanID != nil {
		t.Errorf("wrong lengths must be null: %x %x", r.TraceID, r.SpanID)
	}
	if r := mk(make([]byte, 16), make([]byte, 8)); r.TraceID != nil || r.SpanID != nil {
		t.Errorf("all-zero ids must be null: %x %x", r.TraceID, r.SpanID)
	}
	// OTLP/JSON: hex strings.
	req, err := DecodeLogs([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[
		{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174"},
		{"traceId":"5b8efff798038103d269b633813fc6","spanId":"zz"},
		{"traceId":"00000000000000000000000000000000"},
		{"traceId":"not hex at all not hex at all 123"}]}]}]}`), ctJSON)
	if err != nil {
		t.Fatal(err)
	}
	recs, _, _ := NormaliseLogs(req, SiteContext{ID: 42}, testNow)
	if len(recs) != 4 || !bytes.Equal(recs[0].TraceID, want) || len(recs[0].SpanID) != 8 {
		t.Fatalf("json hex ids: %+v", recs[0])
	}
	for i := 1; i < 4; i++ {
		if recs[i].TraceID != nil || recs[i].SpanID != nil {
			t.Errorf("record %d: bad json ids must be null, got %x %x", i, recs[i].TraceID, recs[i].SpanID)
		}
	}
}

func TestLogTimestamps(t *testing.T) {
	old := &logspb.LogRecord{TimeUnixNano: nanos(testNow.Add(-8 * 24 * time.Hour)), Body: strVal("old")}
	future := &logspb.LogRecord{TimeUnixNano: nanos(testNow.Add(11 * time.Minute)), Body: strVal("future")}
	observed := &logspb.LogRecord{ObservedTimeUnixNano: nanos(testNow.Add(-time.Hour)), Body: strVal("observed")}
	none := &logspb.LogRecord{Body: strVal("none")}
	edge := &logspb.LogRecord{TimeUnixNano: nanos(testNow.Add(-6 * 24 * time.Hour)), Body: strVal("edge")}
	recs, rejected, warn := NormaliseLogs(logsReq(nil, old, future, observed, none, edge), SiteContext{ID: 42}, testNow)
	if rejected != 2 || warn == "" || len(recs) != 3 {
		t.Fatalf("rejected %d warn %q kept %d, want 2, non-empty, 3", rejected, warn, len(recs))
	}
	if !recs[0].TS.Equal(testNow.Add(-time.Hour)) || !recs[0].Observed.Equal(testNow.Add(-time.Hour)) {
		t.Errorf("observed-only: ts %v observed %v", recs[0].TS, recs[0].Observed)
	}
	if !recs[1].TS.Equal(testNow) {
		t.Errorf("no timestamp: ts %v, want receive time", recs[1].TS)
	}
}

type logSink struct {
	mu   sync.Mutex
	got  []obslogs.Record
	full bool
	cap  int // accept at most this many per call when > 0
}

func (s *logSink) Enqueue(rs []obslogs.Record) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return 0
	}
	if s.cap > 0 && len(rs) > s.cap {
		rs = rs[:s.cap]
	}
	s.got = append(s.got, rs...)
	return len(rs)
}

func (s *logSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

type logRig struct {
	h     *Handler
	sink  *logSink
	quota *obsquota.Tracker
	stats *countStats
	now   *time.Time
	site  *sites.AppConfig
}

func newLogRig(t *testing.T, limit int64) *logRig {
	t.Helper()
	site := testSite()
	site.App.LogDailyQuota = limit
	now := testNow
	r := &logRig{sink: &logSink{}, stats: &countStats{}, now: &now, site: site.App}
	r.quota = obsquota.New(nil, func() time.Time { return now })
	r.h = NewHandler(registry{[]sites.Site{site}}, appingest.NewIntake(appingest.Config{}), &sink{}, nil, &stats{}, func() time.Time { return now }).
		WithLogs(r.sink, r.quota, r.stats)
	return r
}

func (r *logRig) post(body []byte, ct string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, pathLogs42, bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+testKey)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w
}

func nRecords(n int, tag string) *LogsRequest {
	recs := make([]*logspb.LogRecord, n)
	for i := range recs {
		recs[i] = logRec(logspb.SeverityNumber_SEVERITY_NUMBER_INFO, fmt.Sprintf("%s %d", tag, i))
	}
	return logsReq([]*commonpb.KeyValue{kv("service.name", "svc")}, recs...)
}

func TestLogsRouteOnlyWhenEnabled(t *testing.T) {
	r := newRig(t, 0)
	if r.h.Matches(httptestReq(pathLogs42)) {
		t.Fatal("logs route must be off until WithLogs")
	}
	if w := r.post(pathLogs42, marshalLogs(t, nRecords(1, "x")), nil); w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
	if !newLogRig(t, 0).h.Matches(httptestReq(pathLogs42)) {
		t.Fatal("route must match once enabled")
	}
}

func TestLogsProtobufGzipJSONAndAuth(t *testing.T) {
	r := newLogRig(t, 0)
	w := r.post(marshalLogs(t, nRecords(3, "proto")), ctProto, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != ctProto || w.Body.Len() != 0 || r.sink.count() != 3 {
		t.Fatalf("proto: %d ct=%q body=%q records=%d", w.Code, w.Header().Get("Content-Type"), w.Body.String(), r.sink.count())
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(marshalLogs(t, nRecords(2, "gzip")))
	_ = zw.Close()
	if w := r.post(gz.Bytes(), ctProto, map[string]string{"Content-Encoding": "gzip"}); w.Code != 200 || r.sink.count() != 5 {
		t.Fatalf("gzip: %d records=%d", w.Code, r.sink.count())
	}
	js, _ := os.ReadFile("testdata/logs/python_sdk.json")
	w = r.post(js, ctJSON, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != ctJSON || w.Body.String() != "{}" || r.sink.count() != 7 {
		t.Fatalf("json: %d ct=%q body=%q records=%d", w.Code, w.Header().Get("Content-Type"), w.Body.String(), r.sink.count())
	}
	// The same body again is a retry: stored once.
	if w := r.post(js, ctJSON, nil); w.Code != 200 || r.sink.count() != 7 {
		t.Fatalf("duplicate: %d records=%d", w.Code, r.sink.count())
	}
	if w := r.post(js, ctJSON, map[string]string{"Authorization": "Bearer " + strings.Repeat("a", 32)}); w.Code != 401 {
		t.Fatalf("bad key: %d", w.Code)
	}
	if w := r.post([]byte("not a protobuf \xff\xff"), ctProto, nil); w.Code != 400 {
		t.Fatalf("malformed: %d", w.Code)
	}
	if w := r.post([]byte(`{`), ctJSON, nil); w.Code != 400 {
		t.Fatalf("malformed json: %d", w.Code)
	}
	if r.sink.count() != 7 {
		t.Fatal("a rejected body stored records")
	}
}

func TestLogFixtures(t *testing.T) {
	java, err := os.ReadFile("testdata/logs/java_agent.pb")
	if err != nil {
		t.Fatal(err)
	}
	req, err := DecodeLogs(java, ctProto)
	if err != nil {
		t.Fatal(err)
	}
	recs, rejected, _ := NormaliseLogs(req, SiteContext{ID: 1}, testNow)
	if len(recs) != 2 || rejected != 0 || recs[0].Service != "orders" || recs[0].Release != "3.1.0" || recs[0].Environment != "production" ||
		recs[0].Severity != 9 || recs[1].Severity != 17 || len(recs[0].TraceID) != 16 || len(recs[0].SpanID) != 8 {
		t.Fatalf("java agent: %+v rejected %d", recs, rejected)
	}
	if got := recs[0].Attributes; len(got) != 3 || got["telemetry.sdk.language"] != "java" || got["thread.name"] == nil {
		t.Errorf("java attributes = %v", got)
	}

	py, _ := os.ReadFile("testdata/logs/python_sdk.json")
	req, err = DecodeLogs(py, ctJSON)
	if err != nil {
		t.Fatal(err)
	}
	recs, rejected, _ = NormaliseLogs(req, SiteContext{ID: 1, HashSecret: logSecret}, testNow)
	if len(recs) != 2 || rejected != 0 {
		t.Fatalf("python: %d records, %d rejected", len(recs), rejected)
	}
	p := recs[0]
	if p.Service != "billing" || p.Environment != "staging" || p.Release != "2.0.1" || p.Body != "charge failed for [email]" || p.Severity != 17 ||
		!bytes.Equal(p.UserHash, sentryingest.HashUser(logSecret, "cust-77")) || len(p.TraceID) != 16 ||
		len(p.Attributes) != 2 || p.Attributes["code.function"] != "charge" || p.Attributes["telemetry.sdk.language"] != "python" {
		t.Errorf("python record 0 = %+v", p)
	}
	if !recs[1].TS.Equal(time.Unix(1795003141, 0).UTC()) || recs[1].Attributes["attempt"] != int64(2) {
		t.Errorf("python record 1 (observed-only) = %+v", recs[1])
	}

	node, _ := os.ReadFile("testdata/logs/node_sdk.json")
	req, err = DecodeLogs(node, ctJSON)
	if err != nil {
		t.Fatal(err)
	}
	recs, rejected, _ = NormaliseLogs(req, SiteContext{ID: 1}, testNow)
	if len(recs) != 2 || rejected != 0 {
		t.Fatalf("node: %d records, %d rejected", len(recs), rejected)
	}
	n := recs[0]
	if n.Severity != 13 || n.Environment != "production" || n.TraceID != nil || n.SpanID != nil ||
		n.Body != `{"event":"slow","ms":1500.5,"tags":["a",2]}` || n.Attributes["labels"] != `["x","y"]` || n.Attributes["ok"] != true {
		t.Errorf("node record 0 = %+v", n)
	}
	if recs[1].Severity != 21 || len(recs[1].TraceID) != 16 {
		t.Errorf("node record 1 = %+v", recs[1])
	}
}

func TestQuota(t *testing.T) {
	r := newLogRig(t, 1000)
	w := r.post(marshalLogs(t, nRecords(600, "first")), ctProto, nil)
	if w.Code != 200 || r.sink.count() != 600 || w.Body.Len() != 0 {
		t.Fatalf("first: %d records=%d body=%q", w.Code, r.sink.count(), w.Body.String())
	}
	w = r.post(marshalLogs(t, nRecords(600, "second")), ctProto, nil)
	if w.Code != 200 || r.sink.count() != 1000 {
		t.Fatalf("second: %d records=%d, want 200 and 1000", w.Code, r.sink.count())
	}
	// partial_success{rejected_log_records=200, error_message="daily quota reached"}
	body := w.Body.Bytes()
	_, _, n := protowire.ConsumeTag(body)
	ps, _ := protowire.ConsumeBytes(body[n:])
	_, _, n = protowire.ConsumeTag(ps)
	rej, n2 := protowire.ConsumeVarint(ps[n:])
	_, _, n3 := protowire.ConsumeTag(ps[n+n2:])
	msg, _ := protowire.ConsumeBytes(ps[n+n2+n3:])
	if rej != 200 || string(msg) != "daily quota reached" {
		t.Fatalf("partial success: rejected %d message %q", rej, msg)
	}
	// JSON form of the same answer.
	w = r.post([]byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"1795003140000000000","body":{"stringValue":"more"}}]}]}]}`), ctJSON, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rejectedLogRecords":"1"`) || !strings.Contains(w.Body.String(), "daily quota reached") {
		t.Fatalf("json over quota: %d %s", w.Code, w.Body.String())
	}
	if r.sink.count() != 1000 {
		t.Fatalf("records = %d", r.sink.count())
	}
	// The next UTC day grants again.
	*r.now = testNow.Add(13 * time.Hour)
	w = r.post(marshalLogs(t, nRecords(10, "next day")), ctProto, nil)
	if w.Code != 200 || w.Body.Len() != 0 || r.sink.count() != 1010 {
		t.Fatalf("next day: %d records=%d", w.Code, r.sink.count())
	}
	if got := r.quota.Used(42, obsquota.ClassLogs); got != 10 {
		t.Fatalf("used today = %d, want 10", got)
	}
}

func TestQuotaZeroIsUnlimited(t *testing.T) {
	r := newLogRig(t, 0)
	if w := r.post(marshalLogs(t, nRecords(500, "u")), ctProto, nil); w.Code != 200 || r.sink.count() != 500 {
		t.Fatalf("%d records=%d", w.Code, r.sink.count())
	}
}

func TestQueueFull503(t *testing.T) {
	r := newLogRig(t, 1000)
	r.sink.full = true
	body := marshalLogs(t, nRecords(5, "q"))
	w := r.post(body, ctProto, nil)
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("%d retry-after=%q", w.Code, w.Header().Get("Retry-After"))
	}
	if got := r.quota.Used(42, obsquota.ClassLogs); got != 0 {
		t.Fatalf("a refused request must not use quota: %d", got)
	}
	r.sink.full = false
	// The refused body is accepted when the sender retries.
	if w := r.post(body, ctProto, nil); w.Code != 200 || r.sink.count() != 5 {
		t.Fatalf("retry: %d records=%d", w.Code, r.sink.count())
	}
}

func TestQueuePrefixIsPartialSuccess(t *testing.T) {
	r := newLogRig(t, 1000)
	r.sink.cap = 3
	w := r.post(marshalLogs(t, nRecords(5, "p")), ctProto, nil)
	if w.Code != 200 || r.sink.count() != 3 || w.Body.Len() == 0 {
		t.Fatalf("%d records=%d body=%d bytes", w.Code, r.sink.count(), w.Body.Len())
	}
	if got := r.quota.Used(42, obsquota.ClassLogs); got != 3 {
		t.Fatalf("used = %d, want 3", got)
	}
}

func TestTooManyRecordsIs413(t *testing.T) {
	r := newLogRig(t, 0)
	if w := r.post(marshalLogs(t, nRecords(10_001, "big")), ctProto, nil); w.Code != http.StatusRequestEntityTooLarge || r.sink.count() != 0 {
		t.Fatalf("%d records=%d", w.Code, r.sink.count())
	}
	if w := r.post(marshalLogs(t, nRecords(10_000, "ok")), ctProto, nil); w.Code != 200 || r.sink.count() != 10_000 {
		t.Fatalf("exactly 10,000: %d records=%d", w.Code, r.sink.count())
	}
}

func TestOldRecordsArePartialSuccess(t *testing.T) {
	r := newLogRig(t, 0)
	old := &logspb.LogRecord{TimeUnixNano: nanos(testNow.Add(-9 * 24 * time.Hour)), Body: strVal("old")}
	w := r.post(marshalLogs(t, logsReq(nil, logRec(logspb.SeverityNumber_SEVERITY_NUMBER_INFO, "ok"), old)), ctProto, nil)
	if w.Code != 200 || r.sink.count() != 1 || w.Body.Len() == 0 {
		t.Fatalf("%d records=%d body=%d", w.Code, r.sink.count(), w.Body.Len())
	}
}
