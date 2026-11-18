// Package otlpingest is the OTLP/HTTP traces front door: it authenticates a
// project's DSN public key, decodes protobuf or JSON export requests under
// hard size and count bounds, and hands normalised spans to the spans writer.
package otlpingest

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Size caps from the phase 4 plan: 4 MiB on the wire, 20 MiB decompressed.
const (
	MaxCompressedBytes   = 4 << 20
	MaxDecompressedBytes = 20 << 20

	// maxAttrs bounds the attributes kept from one span, event or resource on
	// the JSON path; anything past it is ignored.
	maxAttrs         = 128
	maxEventsPerSpan = 16
)

// Decoding errors.
var (
	ErrTooLarge  = errors.New("request too large")
	ErrMalformed = errors.New("malformed request")
	ErrEncoding  = errors.New("unsupported content encoding")
)

// readBody reads the request body, undoing gzip, with both sizes capped. A
// gzip bomb stops at maxDecompressed, so it costs about that much memory.
func readBody(r io.Reader, encoding string, maxCompressed, maxDecompressed int64) ([]byte, error) {
	raw, err := readLimited(r, maxCompressed)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		if int64(len(raw)) > maxDecompressed {
			return nil, ErrTooLarge
		}
		return raw, nil
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: gzip", ErrMalformed)
		}
		defer zr.Close()
		out, err := readLimited(zr, maxDecompressed)
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: gzip", ErrMalformed)
		}
		return out, nil
	default:
		return nil, ErrEncoding
	}
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	var buf bytes.Buffer
	n, err := buf.ReadFrom(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if n > max {
		return nil, ErrTooLarge
	}
	return buf.Bytes(), nil
}

// Request is an OTLP ExportTraceServiceRequest. It is declared here, not
// imported from the collector package, because that package links gRPC and
// grpc-gateway into the binary for a message that has a single field.
type Request struct {
	ResourceSpans []*tracepb.ResourceSpans
}

// DecodeProto decodes an ExportTraceServiceRequest (field 1: repeated
// ResourceSpans) from protobuf. Unknown fields are skipped.
func DecodeProto(body []byte) (*Request, error) {
	req := &Request{}
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
			rs := &tracepb.ResourceSpans{}
			if err := proto.Unmarshal(v, rs); err != nil {
				return nil, fmt.Errorf("%w: protobuf", ErrMalformed)
			}
			req.ResourceSpans = append(req.ResourceSpans, rs)
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

// encodeResponse builds an ExportTraceServiceResponse: empty, or with
// partial_success{rejected_spans, error_message}.
func encodeResponse(asProto bool, rejected int, msg string) []byte {
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
	return []byte(`{"partialSuccess":{"rejectedSpans":"` + strconv.Itoa(rejected) + `","errorMessage":` + string(m) + `}}`)
}

// flexUint reads a 64-bit unsigned integer sent as a JSON number or string.
type flexUint uint64

func (f *flexUint) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		return nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return err
	}
	*f = flexUint(v)
	return nil
}

// flexInt64 reads a signed 64-bit integer sent as a JSON number or string.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*f = flexInt64(v)
	return nil
}

// flexEnum reads an enum sent as a number or as its name.
type flexEnum int32

func (f *flexEnum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		return nil
	}
	if v, err := strconv.ParseInt(s, 10, 32); err == nil {
		*f = flexEnum(v)
		return nil
	}
	if v, ok := tracepb.Span_SpanKind_value[s]; ok {
		*f = flexEnum(v)
		return nil
	}
	if v, ok := tracepb.Status_StatusCode_value[s]; ok {
		*f = flexEnum(v)
		return nil
	}
	return fmt.Errorf("bad enum %q", s)
}

type jsonAny struct {
	StringValue *string         `json:"stringValue"`
	IntValue    *flexInt64      `json:"intValue"`
	BoolValue   *bool           `json:"boolValue"`
	DoubleValue *float64        `json:"doubleValue"`
	ArrayValue  json.RawMessage `json:"arrayValue"`
	KvlistValue json.RawMessage `json:"kvlistValue"`
}

type jsonKV struct {
	Key   string  `json:"key"`
	Value jsonAny `json:"value"`
}

type jsonEvent struct {
	TimeUnixNano flexUint `json:"timeUnixNano"`
	Name         string   `json:"name"`
	Attributes   []jsonKV `json:"attributes"`
}

type jsonSpan struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	ParentSpanID      string      `json:"parentSpanId"`
	Name              string      `json:"name"`
	Kind              flexEnum    `json:"kind"`
	StartTimeUnixNano flexUint    `json:"startTimeUnixNano"`
	EndTimeUnixNano   flexUint    `json:"endTimeUnixNano"`
	Attributes        []jsonKV    `json:"attributes"`
	Events            []jsonEvent `json:"events"`
	Status            struct {
		Code flexEnum `json:"code"`
	} `json:"status"`
}

type jsonRequest struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []jsonKV `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Spans []jsonSpan `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

// DecodeJSON decodes OTLP/JSON. Unlike protojson it reads trace and span ids
// as hex (the OTLP rule) and tolerates 64-bit numbers sent as strings. An id
// that is not valid hex of the right length becomes nil so Convert rejects
// that span alone; the rest of the request is kept.
func DecodeJSON(body []byte) (*Request, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	var in jsonRequest
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("%w: json", ErrMalformed)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: json", ErrMalformed)
	}
	out := &Request{}
	for _, rs := range in.ResourceSpans {
		res := &tracepb.ResourceSpans{Resource: &resourcepb.Resource{Attributes: convertKVs(rs.Resource.Attributes)}}
		for _, ss := range rs.ScopeSpans {
			scope := &tracepb.ScopeSpans{}
			for i := range ss.Spans {
				js := &ss.Spans[i]
				sp := &tracepb.Span{
					TraceId:           hexID(js.TraceID, 16),
					SpanId:            hexID(js.SpanID, 8),
					ParentSpanId:      hexID(js.ParentSpanID, 8),
					Name:              js.Name,
					Kind:              tracepb.Span_SpanKind(js.Kind),
					StartTimeUnixNano: uint64(js.StartTimeUnixNano),
					EndTimeUnixNano:   uint64(js.EndTimeUnixNano),
					Attributes:        convertKVs(js.Attributes),
					Status:            &tracepb.Status{Code: tracepb.Status_StatusCode(js.Status.Code)},
				}
				for j, ev := range js.Events {
					if j >= maxEventsPerSpan {
						break
					}
					sp.Events = append(sp.Events, &tracepb.Span_Event{Name: ev.Name, TimeUnixNano: uint64(ev.TimeUnixNano), Attributes: convertKVs(ev.Attributes)})
				}
				scope.Spans = append(scope.Spans, sp)
			}
			res.ScopeSpans = append(res.ScopeSpans, scope)
		}
		out.ResourceSpans = append(out.ResourceSpans, res)
	}
	return out, nil
}

// hexID decodes a hex id of exactly n bytes; anything else yields nil (an
// absent id, which Convert rejects for trace and span ids).
func hexID(s string, n int) []byte {
	if len(s) != n*2 {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

func convertKVs(in []jsonKV) []*commonpb.KeyValue {
	if len(in) > maxAttrs {
		in = in[:maxAttrs]
	}
	out := make([]*commonpb.KeyValue, 0, len(in))
	for _, kv := range in {
		var v *commonpb.AnyValue
		switch {
		case kv.Value.StringValue != nil:
			v = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: *kv.Value.StringValue}}
		case kv.Value.IntValue != nil:
			v = &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(*kv.Value.IntValue)}}
		case kv.Value.BoolValue != nil:
			v = &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: *kv.Value.BoolValue}}
		case kv.Value.DoubleValue != nil && !math.IsNaN(*kv.Value.DoubleValue):
			v = &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: *kv.Value.DoubleValue}}
		default:
			continue // arrays and maps carry nothing the converter reads
		}
		out = append(out, &commonpb.KeyValue{Key: kv.Key, Value: v})
	}
	return out
}
