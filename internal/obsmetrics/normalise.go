package obsmetrics

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"supermetry/internal/sentryingest"
)

// Caps from the phase 6 plan.
const (
	MaxNameBytes     = 255
	MaxAttrs         = 16
	MaxAttrKeyBytes  = 128
	MaxAttrValBytes  = 256
	MaxBuckets       = 64
	MaxRequestPoints = 10_000

	maxAge    = 13 * 24 * time.Hour // late points are accepted this long
	maxFuture = time.Hour
)

var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.\-/]*$`)

// ipKeys and personal keys are dropped from metric attributes, as for logs.
var droppedKeys = map[string]struct{}{
	"client.address": {}, "net.peer.ip": {}, "net.sock.peer.addr": {}, "http.client_ip": {}, "source.address": {},
	"user.email": {}, "user.name": {}, "user.full_name": {}, "user.id": {},
}

// resourceKeys are the only resource attributes copied onto a series.
var resourceKeys = map[string]struct{}{
	"service.name": {}, "service.namespace": {}, "deployment.environment": {},
	"deployment.environment.name": {}, "service.version": {},
}

func metricPoints(m *metricspb.Metric) int {
	switch d := m.GetData().(type) {
	case *metricspb.Metric_Gauge:
		return len(d.Gauge.GetDataPoints())
	case *metricspb.Metric_Sum:
		return len(d.Sum.GetDataPoints())
	case *metricspb.Metric_Histogram:
		return len(d.Histogram.GetDataPoints())
	case *metricspb.Metric_ExponentialHistogram:
		return len(d.ExponentialHistogram.GetDataPoints())
	case *metricspb.Metric_Summary:
		return len(d.Summary.GetDataPoints())
	}
	return 0
}

// CountPoints returns the number of data points in a request, so the handler
// can refuse an oversize one before normalising.
func CountPoints(rms []*metricspb.ResourceMetrics) int {
	n := 0
	for _, rm := range rms {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				n += metricPoints(m)
			}
		}
	}
	return n
}

// Normalise turns an export request into points for one site. Points it cannot
// keep are counted in stats: metric_unsupported (summaries, exponential
// histograms) and metric_invalid (bad name, value, bounds or timestamp). It
// returns the points and the number of data points dropped.
func Normalise(rms []*metricspb.ResourceMetrics, siteID int32, now time.Time, stats Stats) ([]Point, int) {
	var out []Point
	dropped := 0
	count := func(name string, n int) {
		dropped += n
		if stats != nil && n > 0 {
			stats.Add(name, n)
		}
	}
	for _, rm := range rms {
		res := resourceAttrs(rm.GetResource().GetAttributes())
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				name := m.GetName()
				if len(name) > MaxNameBytes || !namePattern.MatchString(name) {
					count("metric_invalid", metricPoints(m))
					continue
				}
				unit := clip(m.GetUnit(), MaxAttrValBytes)
				mk := func(kind string, bounds []float64, attrs []*commonpb.KeyValue) SeriesKey {
					return SeriesKey{Name: name, Kind: kind, Unit: unit, Bounds: bounds, Attrs: mergeAttrs(res, attrs)}
				}
				switch d := m.GetData().(type) {
				case *metricspb.Metric_Gauge:
					for _, dp := range d.Gauge.GetDataPoints() {
						if p, ok := numberPoint(siteID, mk("gauge", nil, dp.Attributes), dp, false, now); ok {
							out = append(out, p)
						} else {
							count("metric_invalid", skipIfNoValue(dp))
						}
					}
				case *metricspb.Metric_Sum:
					cum := d.Sum.GetAggregationTemporality() == metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
					known := cum || d.Sum.GetAggregationTemporality() == metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
					kind := "counter"
					if !d.Sum.GetIsMonotonic() {
						kind = "updown"
					}
					for _, dp := range d.Sum.GetDataPoints() {
						if !known {
							count("metric_invalid", 1)
							continue
						}
						// Only monotonic cumulative sums are differenced; an
						// up-down counter's reading is its level.
						if p, ok := numberPoint(siteID, mk(kind, nil, dp.Attributes), dp, cum && kind == "counter", now); ok {
							out = append(out, p)
						} else {
							count("metric_invalid", skipIfNoValue(dp))
						}
					}
				case *metricspb.Metric_Histogram:
					cum := d.Histogram.GetAggregationTemporality() == metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
					known := cum || d.Histogram.GetAggregationTemporality() == metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
					for _, dp := range d.Histogram.GetDataPoints() {
						if !known {
							count("metric_invalid", 1)
							continue
						}
						if p, ok := histogramPoint(siteID, mk, dp, cum, now); ok {
							out = append(out, p)
						} else {
							count("metric_invalid", 1)
						}
					}
				case *metricspb.Metric_ExponentialHistogram:
					count("metric_unsupported", len(d.ExponentialHistogram.GetDataPoints()))
				case *metricspb.Metric_Summary:
					count("metric_unsupported", len(d.Summary.GetDataPoints()))
				}
			}
		}
	}
	for i := range out {
		out[i].Hash = out[i].SeriesKey.Hash()
	}
	return out, dropped
}

// skipIfNoValue counts a rejected number point unless it was a deliberate
// "no recorded value" marker, which is not an error.
func skipIfNoValue(dp *metricspb.NumberDataPoint) int {
	if dp.GetFlags()&uint32(metricspb.DataPointFlags_DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK) != 0 {
		return 0
	}
	return 1
}

func tsOf(unixNano uint64, now time.Time) (time.Time, bool) {
	if unixNano == 0 {
		return now, true
	}
	if unixNano > math.MaxInt64 {
		return time.Time{}, false
	}
	t := time.Unix(0, int64(unixNano)).UTC()
	if t.After(now.Add(maxFuture)) || t.Before(now.Add(-maxAge)) {
		return time.Time{}, false
	}
	return t, true
}

func numberPoint(siteID int32, key SeriesKey, dp *metricspb.NumberDataPoint, cumulative bool, now time.Time) (Point, bool) {
	if dp.GetFlags()&uint32(metricspb.DataPointFlags_DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK) != 0 {
		return Point{}, false
	}
	var v float64
	switch x := dp.GetValue().(type) {
	case *metricspb.NumberDataPoint_AsDouble:
		v = x.AsDouble
	case *metricspb.NumberDataPoint_AsInt:
		v = float64(x.AsInt)
	default:
		return Point{}, false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Point{}, false
	}
	ts, ok := tsOf(dp.GetTimeUnixNano(), now)
	if !ok {
		return Point{}, false
	}
	p := Point{SiteID: siteID, SeriesKey: key, TS: ts, Cumulative: cumulative, Value: &v}
	if cumulative && dp.GetStartTimeUnixNano() != 0 && dp.GetStartTimeUnixNano() <= math.MaxInt64 {
		p.Start = time.Unix(0, int64(dp.GetStartTimeUnixNano())).UTC()
	}
	return p, true
}

func histogramPoint(siteID int32, mk func(string, []float64, []*commonpb.KeyValue) SeriesKey, dp *metricspb.HistogramDataPoint, cumulative bool, now time.Time) (Point, bool) {
	bounds := dp.GetExplicitBounds()
	counts := dp.GetBucketCounts()
	if len(bounds)+1 > MaxBuckets || len(counts) != len(bounds)+1 {
		return Point{}, false
	}
	for i, b := range bounds {
		if math.IsNaN(b) || math.IsInf(b, 0) || (i > 0 && b <= bounds[i-1]) {
			return Point{}, false
		}
	}
	if dp.GetFlags()&uint32(metricspb.DataPointFlags_DATA_POINT_FLAGS_NO_RECORDED_VALUE_MASK) != 0 {
		return Point{}, false
	}
	ts, ok := tsOf(dp.GetTimeUnixNano(), now)
	if !ok || dp.GetCount() > math.MaxInt64 {
		return Point{}, false
	}
	buckets := make([]int64, len(counts))
	for i, c := range counts {
		if c > math.MaxInt64 {
			return Point{}, false
		}
		buckets[i] = int64(c)
	}
	n := int64(dp.GetCount())
	p := Point{
		SiteID: siteID, SeriesKey: mk("histogram", append([]float64(nil), bounds...), dp.Attributes),
		TS: ts, Cumulative: cumulative, Count: &n, Buckets: buckets,
	}
	finite := func(f *float64) *float64 {
		if f == nil || math.IsNaN(*f) || math.IsInf(*f, 0) {
			return nil
		}
		v := *f
		return &v
	}
	if dp.Sum != nil {
		p.Sum = finite(dp.Sum)
	}
	if dp.Min != nil {
		p.Min = finite(dp.Min)
	}
	if dp.Max != nil {
		p.Max = finite(dp.Max)
	}
	if cumulative && dp.GetStartTimeUnixNano() != 0 && dp.GetStartTimeUnixNano() <= math.MaxInt64 {
		p.Start = time.Unix(0, int64(dp.GetStartTimeUnixNano())).UTC()
	}
	return p, true
}

func resourceAttrs(kvs []*commonpb.KeyValue) map[string]string {
	var out map[string]string
	for _, kv := range kvs {
		if _, ok := resourceKeys[kv.GetKey()]; !ok {
			continue
		}
		if v, ok := attrString(kv.GetValue()); ok {
			if out == nil {
				out = map[string]string{}
			}
			out[kv.GetKey()] = clip(v, MaxAttrValBytes)
		}
	}
	return out
}

// mergeAttrs applies the key rules to point attributes, adds the kept resource
// attributes (point attributes win) and caps the result at MaxAttrs keys,
// keeping the first ones in sorted order. Values are not free text, so they
// are only length-capped.
func mergeAttrs(res map[string]string, kvs []*commonpb.KeyValue) map[string]string {
	all := make(map[string]string, len(res)+len(kvs))
	for k, v := range res {
		all[k] = v
	}
	for _, kv := range kvs {
		k := kv.GetKey()
		if k == "" || len(k) > MaxAttrKeyBytes || keyDropped(k) {
			continue
		}
		if v, ok := attrString(kv.GetValue()); ok {
			all[k] = clip(v, MaxAttrValBytes)
		}
	}
	if len(all) <= MaxAttrs {
		return all
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kept := make(map[string]string, MaxAttrs)
	for _, k := range keys[:MaxAttrs] {
		kept[k] = all[k]
	}
	return kept
}

func keyDropped(k string) bool {
	lk := strings.ToLower(k)
	if _, ok := droppedKeys[lk]; ok {
		return true
	}
	return strings.HasPrefix(lk, "enduser.") || sentryingest.IsSensitiveKey(lk)
}

// attrString renders a scalar attribute value; arrays and maps are skipped.
func attrString(v *commonpb.AnyValue) (string, bool) {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue, true
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10), true
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue), true
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64), true
	}
	return "", false
}

// clip cuts s to at most n bytes on a UTF-8 boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
