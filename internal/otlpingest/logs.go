package otlpingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"supermetry/internal/obslogs"
	"supermetry/internal/obsquota"
	obsattrs "supermetry/internal/otlpingest/attrs"
	"supermetry/internal/sentryingest"
	"supermetry/internal/sites"
)

// LogSink takes normalised log records; obslogs.Writer implements it. Enqueue
// returns how many records it accepted (a prefix); the rest were refused
// because the queue is full.
type LogSink interface {
	Enqueue([]obslogs.Record) int
}

type logsFront struct {
	sink  LogSink
	quota *obsquota.Tracker
	stats obslogs.Stats
}

// WithLogs enables POST /api/{project_id}/otlp/v1/logs. quota and stats may
// be nil.
func (h *Handler) WithLogs(sink LogSink, quota *obsquota.Tracker, stats obslogs.Stats) *Handler {
	h.logs = &logsFront{sink: sink, quota: quota, stats: stats}
	return h
}

// logAttrCfg is the shared attribute rule set for log records.
var logAttrCfg = sentryingest.LogAttrConfig()

// Caps on the identifiers lifted from the resource.
const (
	maxServiceBytes     = 128
	maxEnvironmentBytes = 64
	maxReleaseBytes     = 128
)

// resourceLogAttrs are the resource attributes kept in a record's attributes;
// service.name, the environment and service.version become columns, and every
// other resource and scope attribute is dropped.
var resourceLogAttrs = map[string]struct{}{
	"service.namespace": {}, "host.name": {}, "telemetry.sdk.language": {},
}

// DecodeLogs decodes an ExportLogsServiceRequest from OTLP protobuf or
// OTLP/JSON (contentType application/json). JSON trace and span ids are hex,
// per the OTLP/JSON rule; 64-bit numbers may be strings.
func DecodeLogs(body []byte, contentType string) (*LogsRequest, error) {
	if contentType == ctJSON {
		return decodeLogsJSON(body)
	}
	return decodeLogsProto(body)
}

// LogsRequest is an OTLP ExportLogsServiceRequest. It is declared here, not
// taken from the collector package, which drags in the gRPC and gateway
// modules.
type LogsRequest struct {
	ResourceLogs []*logspb.ResourceLogs
}

// decodeLogsProto decodes field 1 (repeated ResourceLogs) and skips unknown
// fields.
func decodeLogsProto(body []byte) (*LogsRequest, error) {
	req := &LogsRequest{}
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
			rl := &logspb.ResourceLogs{}
			if err := proto.Unmarshal(v, rl); err != nil {
				return nil, fmt.Errorf("%w: protobuf", ErrMalformed)
			}
			req.ResourceLogs = append(req.ResourceLogs, rl)
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

// CountLogRecords returns the number of log records in a request, so the
// handler can refuse an oversize one before normalising.
func CountLogRecords(req *LogsRequest) int {
	n := 0
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.GetScopeLogs() {
			n += len(sl.GetLogRecords())
		}
	}
	return n
}

// SiteContext is what NormaliseLogs needs to know about the project.
type SiteContext struct {
	ID         int32
	HashSecret []byte
	Stats      obslogs.Stats // optional: log_attrs_dropped
}

// NormaliseLogs turns an export request into records for one project. A
// record with no usable timestamp (older than 7 days or more than 10 minutes
// ahead) is rejected and counted. warn is non-empty when anything was
// rejected.
func NormaliseLogs(req *LogsRequest, site SiteContext, now time.Time) (records []obslogs.Record, rejected int, warn string) {
	attrsDropped := 0
	for _, rl := range req.ResourceLogs {
		var service, environment, release, envFallback string
		res := logAttrCfg.NewSet()
		for _, kv := range rl.GetResource().GetAttributes() {
			v, _ := obsattrs.AnyToGo(kv.GetValue(), 0).(string)
			switch k := kv.GetKey(); k {
			case "service.name":
				service = obsattrs.Cut(obsattrs.CleanText(v), maxServiceBytes)
			case "deployment.environment.name":
				environment = obsattrs.Cut(obsattrs.CleanText(v), maxEnvironmentBytes)
			case "deployment.environment":
				envFallback = obsattrs.Cut(obsattrs.CleanText(v), maxEnvironmentBytes)
			case "service.version":
				release = obsattrs.Cut(obsattrs.CleanText(v), maxReleaseBytes)
			default:
				if _, keep := resourceLogAttrs[k]; keep {
					res.Add(k, obsattrs.AnyToGo(kv.GetValue(), 0))
				}
			}
		}
		if environment == "" {
			environment = envFallback
		}
		resAttrs, _ := res.Finish()
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				ts := unixNano(lr.GetTimeUnixNano())
				observed := unixNano(lr.GetObservedTimeUnixNano())
				switch {
				case ts.IsZero() && !observed.IsZero():
					ts = observed
				case ts.IsZero():
					ts = now
				}
				if observed.IsZero() {
					observed = now
				}
				if !obslogs.InWindow(ts, now) {
					rejected++
					continue
				}
				rec := obslogs.Record{
					SiteID: site.ID, TS: ts, Observed: observed, Source: obslogs.SourceOTLP,
					Service: service, Environment: environment, Release: release,
					TraceID: idOrNil(lr.GetTraceId(), 16), SpanID: idOrNil(lr.GetSpanId(), 8),
				}
				rec.Severity = int16(lr.GetSeverityNumber())
				if rec.Severity < 0 || rec.Severity > 24 {
					rec.Severity = 0
				}
				if rec.Severity == 0 {
					rec.Severity = obslogs.SeverityFromText(lr.GetSeverityText())
				}
				rec.Body, rec.Truncated = logAttrCfg.Text(obsattrs.EncodeValue(obsattrs.AnyToGo(lr.GetBody(), 0)), obsattrs.LogMaxBody)

				set := logAttrCfg.NewSet()
				for k, v := range resAttrs {
					set.Add(k, v)
				}
				set.AddOTLP(lr.GetAttributes())
				var dropped int
				rec.Attributes, dropped = set.Finish()
				attrsDropped += dropped
				rec.UserHash = sentryingest.HashUser(site.HashSecret, set.UserID())
				records = append(records, rec)
			}
		}
	}
	if site.Stats != nil && attrsDropped > 0 {
		site.Stats.Add("log_attrs_dropped", attrsDropped)
	}
	if rejected > 0 {
		warn = "some log records were rejected (timestamp too old or too far ahead)"
	}
	return records, rejected, warn
}

func unixNano(n uint64) time.Time {
	if n == 0 || n > 1<<63-1 {
		return time.Time{}
	}
	return time.Unix(0, int64(n)).UTC()
}

// idOrNil keeps an id of exactly n bytes that is not all zero; anything else
// is stored as null.
func idOrNil(b []byte, n int) []byte {
	if len(b) != n {
		return nil
	}
	for _, c := range b {
		if c != 0 {
			return append([]byte(nil), b...)
		}
	}
	return nil
}

// ---- OTLP/JSON ----

// flexSeverity reads a severity number as a number or its enum name.
type flexSeverity int32

func (f *flexSeverity) UnmarshalJSON(b []byte) error {
	s := string(bytes.Trim(b, `"`))
	if s == "null" || s == "" {
		return nil
	}
	if v, err := strconv.ParseInt(s, 10, 32); err == nil {
		*f = flexSeverity(v)
		return nil
	}
	if v, ok := logspb.SeverityNumber_value[s]; ok {
		*f = flexSeverity(v)
		return nil
	}
	return fmt.Errorf("bad severity %q", s)
}

type jsonLogAny struct {
	StringValue *string    `json:"stringValue"`
	IntValue    *flexInt64 `json:"intValue"`
	BoolValue   *bool      `json:"boolValue"`
	DoubleValue *flexFloat `json:"doubleValue"`
	BytesValue  *string    `json:"bytesValue"`
	ArrayValue  *struct {
		Values []jsonLogAny `json:"values"`
	} `json:"arrayValue"`
	KvlistValue *struct {
		Values []jsonLogKV `json:"values"`
	} `json:"kvlistValue"`
}

type jsonLogKV struct {
	Key   string     `json:"key"`
	Value jsonLogAny `json:"value"`
}

type jsonLogRecord struct {
	TimeUnixNano         flexUint     `json:"timeUnixNano"`
	ObservedTimeUnixNano flexUint     `json:"observedTimeUnixNano"`
	SeverityNumber       flexSeverity `json:"severityNumber"`
	SeverityText         string       `json:"severityText"`
	Body                 *jsonLogAny  `json:"body"`
	Attributes           []jsonLogKV  `json:"attributes"`
	TraceID              string       `json:"traceId"`
	SpanID               string       `json:"spanId"`
}

type jsonLogsRequest struct {
	ResourceLogs []struct {
		Resource struct {
			Attributes []jsonLogKV `json:"attributes"`
		} `json:"resource"`
		ScopeLogs []struct {
			LogRecords []jsonLogRecord `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

// jsonDepth bounds how deep a JSON array or map value is converted.
const jsonDepth = 6

func (j *jsonLogAny) convert(depth int) *commonpb.AnyValue {
	switch {
	case j == nil:
		return nil
	case j.StringValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: *j.StringValue}}
	case j.IntValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(*j.IntValue)}}
	case j.BoolValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: *j.BoolValue}}
	case j.DoubleValue != nil:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: float64(*j.DoubleValue)}}
	case j.BytesValue != nil:
		// Bytes are base64 in OTLP/JSON; keep them as that text.
		if _, err := base64.StdEncoding.DecodeString(*j.BytesValue); err != nil {
			return nil
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: *j.BytesValue}}
	case j.ArrayValue != nil && depth < jsonDepth:
		arr := &commonpb.ArrayValue{}
		for i := range j.ArrayValue.Values {
			if v := j.ArrayValue.Values[i].convert(depth + 1); v != nil {
				arr.Values = append(arr.Values, v)
			}
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: arr}}
	case j.KvlistValue != nil && depth < jsonDepth:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: convertLogKVs(j.KvlistValue.Values, depth+1)}}}
	}
	return nil
}

func convertLogKVs(in []jsonLogKV, depth int) []*commonpb.KeyValue {
	out := make([]*commonpb.KeyValue, 0, len(in))
	for i := range in {
		if v := in[i].Value.convert(depth); v != nil {
			out = append(out, &commonpb.KeyValue{Key: in[i].Key, Value: v})
		}
	}
	return out
}

func decodeLogsJSON(body []byte) (*LogsRequest, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	var in jsonLogsRequest
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("%w: json", ErrMalformed)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: json", ErrMalformed)
	}
	out := &LogsRequest{}
	for _, rl := range in.ResourceLogs {
		res := &logspb.ResourceLogs{Resource: &resourcepb.Resource{Attributes: convertLogKVs(rl.Resource.Attributes, 0)}}
		for _, sl := range rl.ScopeLogs {
			scope := &logspb.ScopeLogs{}
			for i := range sl.LogRecords {
				j := &sl.LogRecords[i]
				scope.LogRecords = append(scope.LogRecords, &logspb.LogRecord{
					TimeUnixNano:         uint64(j.TimeUnixNano),
					ObservedTimeUnixNano: uint64(j.ObservedTimeUnixNano),
					SeverityNumber:       logspb.SeverityNumber(j.SeverityNumber),
					SeverityText:         j.SeverityText,
					Body:                 j.Body.convert(0),
					Attributes:           convertLogKVs(j.Attributes, 0),
					TraceId:              hexID(j.TraceID, 16),
					SpanId:               hexID(j.SpanID, 8),
				})
			}
			res.ScopeLogs = append(res.ScopeLogs, scope)
		}
		out.ResourceLogs = append(out.ResourceLogs, res)
	}
	return out, nil
}

// encodeLogsResponse builds an ExportLogsServiceResponse: empty, or with
// partial_success{rejected_log_records, error_message}.
func encodeLogsResponse(asProto bool, rejected int, msg string) []byte {
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
	return []byte(`{"partialSuccess":{"rejectedLogRecords":"` + strconv.Itoa(rejected) + `","errorMessage":` + string(m) + `}}`)
}

func (h *Handler) respondLogs(w http.ResponseWriter, ct string, rejected int, msg string) {
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encodeLogsResponse(ct == ctProto, rejected, msg))
}

func (f *logsFront) count(name string, n int) {
	if f.stats != nil && n > 0 {
		f.stats.Add(name, n)
	}
}

// serveLogs handles an authenticated, content-type-checked logs export. It
// reads nothing from the database: the site comes from the in-memory registry
// and the daily quota from memory.
func (h *Handler) serveLogs(w http.ResponseWriter, r *http.Request, site sites.Site, ct string, now time.Time) {
	f := h.logs
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
	// A leading "logs" tag keeps the digest apart from a traces or metrics body.
	hs := sha256.New()
	var id [4]byte
	binary.BigEndian.PutUint32(id[:], uint32(site.ID))
	hs.Write([]byte("logs"))
	hs.Write(id[:])
	hs.Write(body)
	var digest [32]byte
	hs.Sum(digest[:0])
	if h.dedup.seen(digest, now) {
		f.count("log_duplicate_request", 1)
		h.respondLogs(w, ct, 0, "")
		return
	}
	req, err := DecodeLogs(body, ct)
	if f.quota != nil {
		f.quota.AddBytes(site.ID, obsquota.ClassLogs, len(body))
	}
	body = nil
	release()
	if err != nil {
		h.dedup.release(digest)
		fail(w, http.StatusBadRequest, "malformed request")
		return
	}
	if CountLogRecords(req) > obslogs.MaxRequestRecords {
		h.dedup.release(digest)
		fail(w, http.StatusRequestEntityTooLarge, "too many log records in one request")
		return
	}
	var secret []byte
	var limit int64
	if site.App != nil {
		secret, limit = site.App.HashSecret, site.App.LogDailyQuota
	}
	records, rejected, warn := NormaliseLogs(req, SiteContext{ID: site.ID, HashSecret: secret, Stats: f.stats}, now)
	req = nil
	f.count("log_invalid", rejected)
	invalid := rejected

	msg := warn
	taken := 0
	if n := len(records); n > 0 && f.quota != nil {
		fit := f.quota.Take(site.ID, obsquota.ClassLogs, n, limit)
		taken = fit
		if fit < n {
			f.count("log_quota_rejected", n-fit)
			rejected += n - fit
			records = records[:fit]
			msg = "daily quota reached"
		}
	}
	if len(records) > 0 {
		accepted := f.sink.Enqueue(records)
		if accepted == 0 {
			h.dedup.release(digest)
			if f.quota != nil {
				f.quota.Release(site.ID, obsquota.ClassLogs, taken)
			}
			w.Header().Set("Retry-After", "5")
			fail(w, http.StatusServiceUnavailable, "ingest queue full")
			return
		}
		if short := len(records) - accepted; short > 0 {
			// The writer counts log_queue_full itself.
			rejected += short
			msg = "ingest queue full, some log records were dropped"
			if f.quota != nil {
				f.quota.Release(site.ID, obsquota.ClassLogs, short)
				f.quota.Reject(site.ID, obsquota.ClassLogs, short)
			}
		}
	}
	if f.quota != nil {
		// Quota rejections were counted by Take, queue drops above.
		f.quota.Reject(site.ID, obsquota.ClassLogs, invalid)
	}
	h.respondLogs(w, ct, rejected, msg)
}
