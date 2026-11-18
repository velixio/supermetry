package otlpingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"supermetry/internal/obsmetrics"
	"supermetry/internal/obsquota"
	"supermetry/internal/sites"
)

// MetricSink takes normalised metric points; obsmetrics.Writer implements it.
// Enqueue reports how many points it dropped (series cap) and false when the
// queue is full, in which case nothing was queued.
type MetricSink interface {
	Enqueue(batch []obsmetrics.Point) (dropped int, ok bool)
}

type metricsFront struct {
	sink  MetricSink
	quota *obsquota.Tracker
	stats obsmetrics.Stats
}

// WithMetrics enables POST /api/{project_id}/otlp/v1/metrics. quota and stats
// may be nil.
func (h *Handler) WithMetrics(sink MetricSink, quota *obsquota.Tracker, stats obsmetrics.Stats) *Handler {
	h.metrics = &metricsFront{sink: sink, quota: quota, stats: stats}
	return h
}

// MetricsRequest is an OTLP ExportMetricsServiceRequest.
type MetricsRequest struct {
	ResourceMetrics []*metricspb.ResourceMetrics
}

// DecodeMetricsProto decodes an ExportMetricsServiceRequest (field 1: repeated
// ResourceMetrics). Unknown fields are skipped.
func DecodeMetricsProto(body []byte) (*MetricsRequest, error) {
	req := &MetricsRequest{}
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return nil, fmt.Errorf("%w: protobuf", ErrMalformed)
		}
		body = body[n:]
		if num == 1 && typ == protowire.BytesType {
			v, n := protowire.ConsumeBytes(body)
			if n < 0 {
				return nil, fmt.Errorf("%w: protobuf", ErrMalformed)
			}
			rm := &metricspb.ResourceMetrics{}
			if err := proto.Unmarshal(v, rm); err != nil {
				return nil, fmt.Errorf("%w: protobuf", ErrMalformed)
			}
			req.ResourceMetrics = append(req.ResourceMetrics, rm)
			body = body[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(num, typ, body)
		if n < 0 {
			return nil, fmt.Errorf("%w: protobuf", ErrMalformed)
		}
		body = body[n:]
	}
	return req, nil
}

// flexFloat reads a double sent as a JSON number or as a string (NaN, Infinity).
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*f = flexFloat(v)
	return nil
}

// flexTemporality reads an aggregation temporality as a number or its name.
type flexTemporality int32

func (f *flexTemporality) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		return nil
	}
	if v, err := strconv.ParseInt(s, 10, 32); err == nil {
		*f = flexTemporality(v)
		return nil
	}
	if v, ok := metricspb.AggregationTemporality_value[s]; ok {
		*f = flexTemporality(v)
		return nil
	}
	return fmt.Errorf("bad temporality %q", s)
}

type jsonNumberPoint struct {
	Attributes        []jsonKV   `json:"attributes"`
	StartTimeUnixNano flexUint   `json:"startTimeUnixNano"`
	TimeUnixNano      flexUint   `json:"timeUnixNano"`
	AsDouble          *flexFloat `json:"asDouble"`
	AsInt             *flexInt64 `json:"asInt"`
	Flags             flexUint   `json:"flags"`
}

type jsonHistogramPoint struct {
	Attributes        []jsonKV    `json:"attributes"`
	StartTimeUnixNano flexUint    `json:"startTimeUnixNano"`
	TimeUnixNano      flexUint    `json:"timeUnixNano"`
	Count             flexUint    `json:"count"`
	Sum               *flexFloat  `json:"sum"`
	BucketCounts      []flexUint  `json:"bucketCounts"`
	ExplicitBounds    []flexFloat `json:"explicitBounds"`
	Min               *flexFloat  `json:"min"`
	Max               *flexFloat  `json:"max"`
	Flags             flexUint    `json:"flags"`
}

type jsonMetric struct {
	Name  string `json:"name"`
	Unit  string `json:"unit"`
	Gauge *struct {
		DataPoints []jsonNumberPoint `json:"dataPoints"`
	} `json:"gauge"`
	Sum *struct {
		DataPoints             []jsonNumberPoint `json:"dataPoints"`
		AggregationTemporality flexTemporality   `json:"aggregationTemporality"`
		IsMonotonic            bool              `json:"isMonotonic"`
	} `json:"sum"`
	Histogram *struct {
		DataPoints             []jsonHistogramPoint `json:"dataPoints"`
		AggregationTemporality flexTemporality      `json:"aggregationTemporality"`
	} `json:"histogram"`
	Summary *struct {
		DataPoints []struct{} `json:"dataPoints"`
	} `json:"summary"`
	ExponentialHistogram *struct {
		DataPoints []struct{} `json:"dataPoints"`
	} `json:"exponentialHistogram"`
}

type jsonMetricsRequest struct {
	ResourceMetrics []struct {
		Resource struct {
			Attributes []jsonKV `json:"attributes"`
		} `json:"resource"`
		ScopeMetrics []struct {
			Metrics []jsonMetric `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

func numberPoints(in []jsonNumberPoint) []*metricspb.NumberDataPoint {
	out := make([]*metricspb.NumberDataPoint, 0, len(in))
	for i := range in {
		j := &in[i]
		dp := &metricspb.NumberDataPoint{
			Attributes:        convertKVs(j.Attributes),
			StartTimeUnixNano: uint64(j.StartTimeUnixNano),
			TimeUnixNano:      uint64(j.TimeUnixNano),
			Flags:             uint32(j.Flags),
		}
		switch {
		case j.AsInt != nil:
			dp.Value = &metricspb.NumberDataPoint_AsInt{AsInt: int64(*j.AsInt)}
		case j.AsDouble != nil:
			dp.Value = &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(*j.AsDouble)}
		}
		out = append(out, dp)
	}
	return out
}

func optFloat(f *flexFloat) *float64 {
	if f == nil {
		return nil
	}
	v := float64(*f)
	return &v
}

// DecodeMetricsJSON decodes OTLP/JSON metrics, tolerating 64-bit numbers sent
// as strings and enums sent as names or numbers. Attributes whose values are
// arrays or maps are ignored.
func DecodeMetricsJSON(body []byte) (*MetricsRequest, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	var in jsonMetricsRequest
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("%w: json", ErrMalformed)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: json", ErrMalformed)
	}
	out := &MetricsRequest{}
	for _, rm := range in.ResourceMetrics {
		res := &metricspb.ResourceMetrics{Resource: &resourcepb.Resource{Attributes: convertKVs(rm.Resource.Attributes)}}
		for _, sm := range rm.ScopeMetrics {
			scope := &metricspb.ScopeMetrics{}
			for i := range sm.Metrics {
				jm := &sm.Metrics[i]
				m := &metricspb.Metric{Name: jm.Name, Unit: jm.Unit}
				switch {
				case jm.Gauge != nil:
					m.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: numberPoints(jm.Gauge.DataPoints)}}
				case jm.Sum != nil:
					m.Data = &metricspb.Metric_Sum{Sum: &metricspb.Sum{
						DataPoints:             numberPoints(jm.Sum.DataPoints),
						AggregationTemporality: metricspb.AggregationTemporality(jm.Sum.AggregationTemporality),
						IsMonotonic:            jm.Sum.IsMonotonic,
					}}
				case jm.Histogram != nil:
					h := &metricspb.Histogram{AggregationTemporality: metricspb.AggregationTemporality(jm.Histogram.AggregationTemporality)}
					for k := range jm.Histogram.DataPoints {
						j := &jm.Histogram.DataPoints[k]
						dp := &metricspb.HistogramDataPoint{
							Attributes:        convertKVs(j.Attributes),
							StartTimeUnixNano: uint64(j.StartTimeUnixNano),
							TimeUnixNano:      uint64(j.TimeUnixNano),
							Count:             uint64(j.Count),
							Sum:               optFloat(j.Sum),
							Min:               optFloat(j.Min),
							Max:               optFloat(j.Max),
							Flags:             uint32(j.Flags),
						}
						for _, c := range j.BucketCounts {
							dp.BucketCounts = append(dp.BucketCounts, uint64(c))
						}
						for _, b := range j.ExplicitBounds {
							dp.ExplicitBounds = append(dp.ExplicitBounds, float64(b))
						}
						h.DataPoints = append(h.DataPoints, dp)
					}
					m.Data = &metricspb.Metric_Histogram{Histogram: h}
				case jm.Summary != nil:
					m.Data = &metricspb.Metric_Summary{Summary: &metricspb.Summary{DataPoints: make([]*metricspb.SummaryDataPoint, len(jm.Summary.DataPoints))}}
				case jm.ExponentialHistogram != nil:
					m.Data = &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
						DataPoints: make([]*metricspb.ExponentialHistogramDataPoint, len(jm.ExponentialHistogram.DataPoints)),
					}}
				}
				scope.Metrics = append(scope.Metrics, m)
			}
			res.ScopeMetrics = append(res.ScopeMetrics, scope)
		}
		out.ResourceMetrics = append(out.ResourceMetrics, res)
	}
	return out, nil
}

// encodeMetricsResponse builds an ExportMetricsServiceResponse: empty, or with
// partial_success{rejected_data_points, error_message}.
func encodeMetricsResponse(asProto bool, rejected int, msg string) []byte {
	if rejected <= 0 {
		if asProto {
			return nil
		}
		return []byte("{}")
	}
	if asProto {
		var ps []byte
		ps = protowire.AppendTag(ps, 1, protowire.VarintType)
		ps = protowire.AppendVarint(ps, uint64(rejected))
		ps = protowire.AppendTag(ps, 2, protowire.BytesType)
		ps = protowire.AppendString(ps, msg)
		out := protowire.AppendTag(nil, 1, protowire.BytesType)
		return protowire.AppendBytes(out, ps)
	}
	m, _ := json.Marshal(msg)
	return []byte(`{"partialSuccess":{"rejectedDataPoints":"` + strconv.Itoa(rejected) + `","errorMessage":` + string(m) + `}}`)
}

func (h *Handler) respondMetrics(w http.ResponseWriter, ct string, rejected int, msg string) {
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encodeMetricsResponse(ct == ctProto, rejected, msg))
}

// serveMetrics handles an authenticated, content-type-checked metrics export.
func (h *Handler) serveMetrics(w http.ResponseWriter, r *http.Request, site sites.Site, ct string, now time.Time) {
	m := h.metrics
	if !h.acquire(r) {
		w.Header().Set("Retry-After", "5")
		fail(w, http.StatusServiceUnavailable, "too many requests in flight")
		return
	}
	released := false
	release := func() {
		if !released {
			released = true
			<-h.inflight
		}
	}
	defer release()

	body, err := readBody(r.Body, r.Header.Get("Content-Encoding"), MaxCompressedBytes, MaxDecompressedBytes)
	if err != nil {
		switch {
		case errors.Is(err, ErrTooLarge):
			fail(w, http.StatusRequestEntityTooLarge, "request too large")
		case errors.Is(err, ErrEncoding):
			fail(w, http.StatusUnsupportedMediaType, "unsupported content encoding")
		default:
			fail(w, http.StatusBadRequest, "malformed request body")
		}
		return
	}
	// A leading "metrics" tag keeps the digest apart from a traces body.
	hs := sha256.New()
	var id [4]byte
	binary.BigEndian.PutUint32(id[:], uint32(site.ID))
	hs.Write([]byte("metrics"))
	hs.Write(id[:])
	hs.Write(body)
	var digest [32]byte
	hs.Sum(digest[:0])
	if h.dedup.seen(digest, now) {
		h.count("metric_duplicate_request", 1)
		h.respondMetrics(w, ct, 0, "")
		return
	}
	var req *MetricsRequest
	if ct == ctProto {
		req, err = DecodeMetricsProto(body)
	} else {
		req, err = DecodeMetricsJSON(body)
	}
	if m.quota != nil {
		m.quota.AddBytes(site.ID, obsquota.ClassMetrics, len(body))
	}
	body = nil
	release()
	if err != nil {
		h.dedup.release(digest)
		fail(w, http.StatusBadRequest, "malformed request")
		return
	}
	if obsmetrics.CountPoints(req.ResourceMetrics) > obsmetrics.MaxRequestPoints {
		h.dedup.release(digest)
		fail(w, http.StatusRequestEntityTooLarge, "too many data points in one request")
		return
	}
	points, rejected := obsmetrics.Normalise(req.ResourceMetrics, site.ID, now, m.stats)
	req = nil

	msg := ""
	taken, quotaRejected := 0, 0
	if n := len(points); n > 0 && m.quota != nil {
		var limit int64
		if site.App != nil {
			limit = site.App.MetricDailyQuota
		}
		fit := m.quota.Take(site.ID, obsquota.ClassMetrics, n, limit)
		taken, quotaRejected = fit, n-fit
		if fit < n {
			if m.stats != nil {
				m.stats.Add("metric_quota_rejected", n-fit)
			}
			rejected += n - fit
			points = points[:fit]
			msg = "daily quota reached"
		}
	}
	if len(points) > 0 {
		dropped, ok := m.sink.Enqueue(points)
		if !ok {
			h.dedup.release(digest)
			if m.quota != nil {
				m.quota.Release(site.ID, obsquota.ClassMetrics, taken)
			}
			w.Header().Set("Retry-After", "5")
			fail(w, http.StatusServiceUnavailable, "ingest queue full")
			return
		}
		rejected += dropped
		if dropped > 0 && msg == "" {
			msg = "some data points were rejected (series limit)"
		}
	}
	if rejected > 0 && msg == "" {
		msg = "some data points were rejected (invalid, unsupported or too old)"
	}
	if m.quota != nil && rejected > 0 {
		// Quota rejections were counted by Take; count the others here.
		m.quota.Reject(site.ID, obsquota.ClassMetrics, rejected-quotaRejected)
	}
	h.respondMetrics(w, ct, rejected, msg)
}
