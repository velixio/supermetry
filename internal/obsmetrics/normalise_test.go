package obsmetrics

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type countStats struct{ m map[string]int }

func (c *countStats) Add(name string, n int) {
	if c.m == nil {
		c.m = map[string]int{}
	}
	c.m[name] += n
}

func str(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func numDP(ts time.Time, start time.Time, v float64, attrs ...*commonpb.KeyValue) *metricspb.NumberDataPoint {
	dp := &metricspb.NumberDataPoint{
		TimeUnixNano: uint64(ts.UnixNano()), Attributes: attrs,
		Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: v},
	}
	if !start.IsZero() {
		dp.StartTimeUnixNano = uint64(start.UnixNano())
	}
	return dp
}

func gauge(name string, dps ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: dps}}}
}

func sum(name string, mono bool, temp metricspb.AggregationTemporality, dps ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Unit: "1", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{DataPoints: dps, IsMonotonic: mono, AggregationTemporality: temp}}}
}

func hist(name string, temp metricspb.AggregationTemporality, bounds []float64, dps ...*metricspb.HistogramDataPoint) *metricspb.Metric {
	for _, dp := range dps {
		dp.ExplicitBounds = bounds
	}
	return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{DataPoints: dps, AggregationTemporality: temp}}}
}

func histDP(ts time.Time, start time.Time, count uint64, sumV float64, buckets ...uint64) *metricspb.HistogramDataPoint {
	dp := &metricspb.HistogramDataPoint{TimeUnixNano: uint64(ts.UnixNano()), Count: count, Sum: &sumV, BucketCounts: buckets}
	if !start.IsZero() {
		dp.StartTimeUnixNano = uint64(start.UnixNano())
	}
	return dp
}

func request(metrics ...*metricspb.Metric) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			str("service.name", "checkout"), str("deployment.environment", "prod"),
			str("host.name", "node-7"), str("process.command_line", "secret"),
		}},
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
	}}
}

const (
	cumulative = metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
	delta      = metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
)

func TestNormaliseMetrics(t *testing.T) {
	ts := testNow.Add(-time.Minute)
	st := testNow.Add(-time.Hour)
	rm := request(
		gauge("jvm.memory.used", numDP(ts, time.Time{}, 1024, str("pool", "heap")), numDP(ts, time.Time{}, 2048, str("pool", "nonheap"))),
		sum("http.server.requests", true, cumulative, numDP(ts, st, 100, str("http.route", "/a"), str("http.client_ip", "1.2.3.4"), str("auth.token", "x"), str("enduser.id", "u1"))),
		sum("jvm.threads", false, cumulative, numDP(ts, st, 17)),
		sum("queue.depth.delta", true, delta, numDP(ts, st, 5)),
		hist("http.server.duration", cumulative, []float64{10, 50, 100}, histDP(ts, st, 7, 90, 2, 4, 1, 0)),
		&metricspb.Metric{Name: "summary.m", Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: []*metricspb.SummaryDataPoint{{}, {}}}}},
		&metricspb.Metric{Name: "exp.m", Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{DataPoints: []*metricspb.ExponentialHistogramDataPoint{{}}}}},
		gauge("9bad name", numDP(ts, time.Time{}, 1)),
	)
	stats := &countStats{}
	pts, dropped := Normalise(rm, 7, testNow, stats)
	if CountPoints(rm) != 10 {
		t.Fatalf("CountPoints = %d, want 10", CountPoints(rm))
	}
	// 2 gauge + 1 counter + 1 updown + 1 delta counter + 1 histogram.
	if len(pts) != 6 {
		t.Fatalf("points = %d, want 6", len(pts))
	}
	if stats.m["metric_unsupported"] != 3 || stats.m["metric_invalid"] != 1 || dropped != 4 {
		t.Fatalf("stats %v dropped %d, want unsupported 3, invalid 1, dropped 4", stats.m, dropped)
	}
	byName := map[string][]Point{}
	for _, p := range pts {
		byName[p.SeriesKey.Name] = append(byName[p.SeriesKey.Name], p)
	}
	if g := byName["jvm.memory.used"]; len(g) != 2 || g[0].SeriesKey.Kind != "gauge" || g[0].Cumulative || string(g[0].Hash) == string(g[1].Hash) {
		t.Fatalf("gauges: %+v", g)
	}
	c := byName["http.server.requests"][0]
	if c.SeriesKey.Kind != "counter" || !c.Cumulative || !c.Start.Equal(st) || *c.Value != 100 {
		t.Fatalf("counter: %+v", c)
	}
	want := map[string]string{"http.route": "/a", "service.name": "checkout", "deployment.environment": "prod"}
	if len(c.SeriesKey.Attrs) != len(want) {
		t.Fatalf("attrs = %v, want %v (ip, token, enduser and unlisted resource keys dropped)", c.SeriesKey.Attrs, want)
	}
	for k, v := range want {
		if c.SeriesKey.Attrs[k] != v {
			t.Fatalf("attrs = %v, want %v", c.SeriesKey.Attrs, want)
		}
	}
	if u := byName["jvm.threads"][0]; u.SeriesKey.Kind != "updown" || u.Cumulative {
		t.Fatalf("updown: %+v", u)
	}
	if d := byName["queue.depth.delta"][0]; d.SeriesKey.Kind != "counter" || d.Cumulative {
		t.Fatalf("delta counter: %+v", d)
	}
	h := byName["http.server.duration"][0]
	if h.SeriesKey.Kind != "histogram" || !h.Cumulative || *h.Count != 7 || len(h.Buckets) != 4 || len(h.SeriesKey.Bounds) != 3 {
		t.Fatalf("histogram: %+v", h)
	}
}

func TestNormaliseRejectsBadPoints(t *testing.T) {
	ts := testNow.Add(-time.Minute)
	nan := numDP(ts, time.Time{}, math.NaN())
	old := numDP(testNow.Add(-30*24*time.Hour), time.Time{}, 1)
	future := numDP(testNow.Add(3*time.Hour), time.Time{}, 1)
	noValue := numDP(ts, time.Time{}, 1)
	noValue.Flags = 1 // no recorded value: skipped silently
	rm := request(
		gauge("g", nan, old, future, noValue, numDP(ts, time.Time{}, 3)),
		hist("h", cumulative, []float64{1, 2}, histDP(ts, time.Time{}, 1, 1, 1, 0)), // 2 counts for 3 buckets
		hist("h2", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_UNSPECIFIED, []float64{1}, histDP(ts, time.Time{}, 1, 1, 1, 0)),
	)
	stats := &countStats{}
	pts, _ := Normalise(rm, 1, testNow, stats)
	if len(pts) != 1 || *pts[0].Value != 3 {
		t.Fatalf("points = %+v", pts)
	}
	if stats.m["metric_invalid"] != 5 { // nan, old, future, bad bucket count, unspecified temporality
		t.Fatalf("metric_invalid = %d, want 5", stats.m["metric_invalid"])
	}
}

func TestAttributeCaps(t *testing.T) {
	var kvs []*commonpb.KeyValue
	for i := 0; i < 30; i++ {
		kvs = append(kvs, str("key"+strconv.Itoa(i), "v"))
	}
	pts, _ := Normalise(request(gauge("g", numDP(testNow, time.Time{}, 1, kvs...))), 1, testNow, nil)
	if n := len(pts[0].SeriesKey.Attrs); n != MaxAttrs {
		t.Fatalf("attrs = %d, want %d", n, MaxAttrs)
	}
	v := clip(strings.Repeat("é", 200), MaxAttrValBytes)
	if len(v) > MaxAttrValBytes || !utf8.ValidString(v) {
		t.Fatalf("clip: %d bytes, valid=%v", len(v), utf8.ValidString(v))
	}
}
