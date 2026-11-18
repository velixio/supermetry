package otlpingest

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"supermetry/internal/obsmetrics"
	"supermetry/internal/obsquota"
	"supermetry/internal/sites"
)

const pathMetrics42 = "/api/42/otlp/v1/metrics"

type metricSink struct {
	mu   sync.Mutex
	got  []obsmetrics.Point
	full bool
	drop int
}

func (s *metricSink) Enqueue(b []obsmetrics.Point) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return 0, false
	}
	s.got = append(s.got, b...)
	return s.drop, true
}

func (s *metricSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func metricsRig(t *testing.T, quota int64) (*rig, *metricSink) {
	t.Helper()
	r := newRig(t, 0)
	ms := &metricSink{}
	site, _, _ := r.h.sites.ByDSNKey(testKey)
	site.App.MetricDailyQuota = quota
	r.h.WithMetrics(ms, obsquota.New(nil, func() time.Time { return testNow }), r.stats)
	return r, ms
}

func kv(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func javaAgent(points int) []byte {
	ts := uint64(testNow.Add(-time.Minute).UnixNano())
	var gauges []*metricspb.NumberDataPoint
	for i := 0; i < points; i++ {
		gauges = append(gauges, &metricspb.NumberDataPoint{TimeUnixNano: ts, Attributes: []*commonpb.KeyValue{kv("id", strings.Repeat("x", 1+i%5)+string(rune('a'+i%26)))},
			Value: &metricspb.NumberDataPoint_AsInt{AsInt: int64(i)}})
	}
	req := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{kv("service.name", "orders")}},
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{
			{Name: "jvm.memory.used", Unit: "By", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: gauges}}},
			{Name: "legacy.summary", Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: []*metricspb.SummaryDataPoint{{}}}}},
		}}},
	}
	b, _ := proto.Marshal(req)
	out := protowire.AppendTag(nil, 1, protowire.BytesType)
	return protowire.AppendBytes(out, b)
}

func TestMetricsRouteOnlyWhenEnabled(t *testing.T) {
	r := newRig(t, 0)
	if r.h.Matches(httptestReq(pathMetrics42)) {
		t.Fatal("metrics route must be off until WithMetrics")
	}
	if w := r.post(pathMetrics42, javaAgent(1), nil); w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
	r2, _ := metricsRig(t, 0)
	if !r2.h.Matches(httptestReq(pathMetrics42)) || !r2.h.Matches(httptestReq(path42)) {
		t.Fatal("both routes must match")
	}
}

func TestMetricsProtobufGzipAndAuth(t *testing.T) {
	r, ms := metricsRig(t, 0)
	body := javaAgent(5)
	w := r.post(pathMetrics42, body, nil)
	// 5 gauge points kept, 1 summary point dropped: partial success.
	if w.Code != 200 || w.Header().Get("Content-Type") != ctProto || w.Body.Len() == 0 {
		t.Fatalf("proto: %d ct=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if ms.count() != 5 || r.stats.m["metric_unsupported"] != 1 {
		t.Fatalf("points %d stats %v", ms.count(), r.stats.m)
	}
	// The same body again is a retry: stored once.
	if w := r.post(pathMetrics42, body, nil); w.Code != 200 || ms.count() != 5 {
		t.Fatalf("duplicate: %d points %d", w.Code, ms.count())
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(javaAgent(2))
	_ = zw.Close()
	if w := r.post(pathMetrics42, gz.Bytes(), map[string]string{"Content-Encoding": "gzip"}); w.Code != 200 || ms.count() != 7 {
		t.Fatalf("gzip: %d points %d", w.Code, ms.count())
	}
	if w := r.post(pathMetrics42, body, map[string]string{"Authorization": "Bearer " + otherKey}); w.Code != http.StatusUnauthorized {
		t.Fatalf("other project's key: %d", w.Code)
	}
	if w := r.post(pathMetrics42, body, map[string]string{"Authorization": ""}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", w.Code)
	}
}

func TestMetricsJSONFixture(t *testing.T) {
	r, ms := metricsRig(t, 0)
	raw, err := os.ReadFile("testdata/metrics/python_sdk.json")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's timestamps are relative to a fixed day; shift them into range.
	body := strings.ReplaceAll(string(raw), "@NOW@", `"`+itoa(testNow.Add(-time.Minute).UnixNano())+`"`)
	w := r.post(pathMetrics42, []byte(body), map[string]string{"Content-Type": "application/json"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rejectedDataPoints":"1"`) {
		t.Fatalf("json: %d %s", w.Code, w.Body.String())
	}
	if ms.count() != 4 {
		t.Fatalf("points = %d, want 4 (2 gauge incl. NaN-free, 1 counter, 1 histogram; 1 exponential dropped)", ms.count())
	}
	kinds := map[string]int{}
	for _, p := range ms.got {
		kinds[p.SeriesKey.Kind]++
		if p.SeriesKey.Kind == "histogram" && (!p.Cumulative || len(p.Buckets) != 3 || *p.Count != 6) {
			t.Fatalf("histogram %+v", p)
		}
	}
	if kinds["gauge"] != 2 || kinds["counter"] != 1 || kinds["histogram"] != 1 {
		t.Fatalf("kinds %v", kinds)
	}
}

func TestMetricsLimitsAndBackpressure(t *testing.T) {
	r, ms := metricsRig(t, 0)
	if w := r.post(pathMetrics42, javaAgent(obsmetrics.MaxRequestPoints+1), nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over 10000 points: %d", w.Code)
	}
	if w := r.post(pathMetrics42, []byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x0f}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("truncated: %d", w.Code)
	}
	ms.full = true
	w := r.post(pathMetrics42, javaAgent(3), nil)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("queue full: %d retry-after %q", w.Code, w.Header().Get("Retry-After"))
	}
	ms.full = false
	// The refused body must be accepted when the sender retries.
	if w := r.post(pathMetrics42, javaAgent(3), nil); w.Code != 200 || ms.count() != 3 {
		t.Fatalf("retry after 503: %d points %d", w.Code, ms.count())
	}
}

func TestMetricsDailyQuotaIsPartialSuccess(t *testing.T) {
	r, ms := metricsRig(t, 4)
	w := r.post(pathMetrics42, javaAgent(6), nil)
	if w.Code != 200 || ms.count() != 4 || !strings.Contains(w.Body.String(), "daily quota reached") {
		t.Fatalf("over quota must be 200 with partial success: %d points %d body %q", w.Code, ms.count(), w.Body.String())
	}
	// Different body, same day: everything is now rejected, still 200.
	w = r.post(pathMetrics42, javaAgent(7), nil)
	if w.Code != 200 || ms.count() != 4 {
		t.Fatalf("quota exhausted: %d points %d", w.Code, ms.count())
	}
}

func TestMetricsSeriesLimitDropsAreRejected(t *testing.T) {
	r, ms := metricsRig(t, 0)
	ms.drop = 2
	w := r.post(pathMetrics42, javaAgent(3), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "series limit") {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
}

func TestMetricsOriginRules(t *testing.T) {
	site := testSite()
	site.App.DSNKeys = []sites.DSNKey{{PublicKey: testKey, AllowedOrigins: []string{"https://ok.example"}}}
	r, _ := metricsRig(t, 0)
	r.h.sites = registry{[]sites.Site{site}}
	if w := r.post(pathMetrics42, javaAgent(1), map[string]string{"Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatalf("origin: %d", w.Code)
	}
}

func httptestReq(path string) *http.Request {
	return httptest.NewRequest(http.MethodPost, path, nil)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
