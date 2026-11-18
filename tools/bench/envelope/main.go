// Command envelope replays Sentry SDK fixture envelopes against
// POST /api/{site}/envelope/ for a fixed time with many forwarded client
// addresses, and reports accepted events per second and latency percentiles.
//
// Each request is a fixture body with the DSN and every event_id rewritten, so
// nothing is deduplicated. Sites and keys come from a file of "<site id> <key>"
// lines: the per-key budget is 600 events a minute, so a load above 10 events/s
// needs many keys. Throwaway benchmark code, like the rest of tools/bench.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type fixture struct {
	name   string
	header []byte // first envelope line, with the DSN as a placeholder
	rest   []byte // everything after the first newline
	events int    // items of type "event", "transaction" or "span" (all count as accepted events)
	items  int
	tx     bool // from the transactions directory: timestamps are refreshed per request
}

var eventID = regexp.MustCompile(`"event_id":"[0-9a-f]{32}"`)
var tsNum = regexp.MustCompile(`"(start_timestamp|timestamp|end_timestamp)":[0-9.]+`)
var tsStr = regexp.MustCompile(`"(start_timestamp|timestamp|sent_at)":"[^"]*"`)
var lengthRe = regexp.MustCompile(`,"length":[0-9]+`)
var dsnRe = regexp.MustCompile(`"dsn":"[^"]*"`)

func loadFixtures(dir string) ([]fixture, error) {
	var out []fixture
	for _, sdk := range []string{"node", "browser", "python", "go", "transactions"} {
		files, _ := filepath.Glob(filepath.Join(dir, sdk, "*.envelope"))
		for _, f := range files {
			if strings.HasSuffix(f, "-br.envelope") {
				continue // brotli is not in the standard library
			}
			raw, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
				zr, err := gzip.NewReader(bytes.NewReader(raw))
				if err != nil {
					return nil, err
				}
				if raw, err = io.ReadAll(zr); err != nil {
					return nil, err
				}
			}
			head, rest, ok := bytes.Cut(raw, []byte("\n"))
			if !ok {
				continue
			}
			fx := fixture{name: sdk + "/" + strings.TrimSuffix(filepath.Base(f), ".envelope"), header: head, rest: rest}
			fx.events, fx.items = countItems(rest)
			fx.tx = sdk == "transactions"
			out = append(out, fx)
		}
	}
	return out, nil
}

// countItems walks "<item header>\n<payload>\n" pairs, using the item length.
func countItems(rest []byte) (events, items int) {
	for len(rest) > 0 {
		line, tail, ok := bytes.Cut(rest, []byte("\n"))
		if !ok {
			return
		}
		var h struct {
			Type   string `json:"type"`
			Length *int   `json:"length"`
		}
		if json.Unmarshal(line, &h) != nil {
			return
		}
		items++
		if h.Type == "event" || h.Type == "transaction" || h.Type == "span" {
			events++
		}
		if h.Length != nil {
			if *h.Length+1 > len(tail) {
				return
			}
			rest = tail[*h.Length+1:]
			continue
		}
		_, rest, ok = bytes.Cut(tail, []byte("\n"))
		if !ok {
			return
		}
	}
	return
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type key struct{ site, key string }

func readKeys(path string) ([]key, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ks []key
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if p := strings.Fields(sc.Text()); len(p) == 2 {
			ks = append(ks, key{p[0], p[1]})
		}
	}
	return ks, sc.Err()
}

func main() {
	base := flag.String("base", "http://127.0.0.1:8787", "server base URL")
	keysFile := flag.String("keys", "", "file of '<site id> <public key>' lines (required)")
	dir := flag.String("fixtures", "internal/sentryingest/testdata", "fixture directory")
	minutes := flag.Float64("minutes", 5, "duration")
	workers := flag.Int("workers", 24, "concurrent clients (the server caps concurrent body reads at 32 by default)")
	gz := flag.Float64("gzip", 0.5, "share of requests sent gzip-compressed")
	otlp := flag.Bool("otlp", false, "send OTLP/JSON trace requests to /api/{site}/otlp/v1/traces instead of envelopes and report accepted spans/s")
	otlpMetrics := flag.Bool("otlp-metrics", false, "send OTLP/JSON metric requests to /api/{site}/otlp/v1/metrics and report accepted points/s")
	otlpLogs := flag.Bool("otlp-logs", false, "send gzip OTLP protobuf log requests to /api/{site}/otlp/v1/logs and report accepted records/s")
	logsPerReq := flag.Int("logs-per-request", 100, "log records in each OTLP logs request (-otlp-logs only)")
	pointsPerReq := flag.Int("points-per-request", 100, "data points in each OTLP metrics request (-otlp-metrics only)")
	perReq := flag.Int("spans-per-request", 50, "spans in each OTLP request (-otlp only)")
	txShare := flag.Float64("tx", 0.25, "share of requests that carry a transaction or span fixture")
	flag.Parse()
	if *keysFile == "" {
		fmt.Fprintln(os.Stderr, "-keys is required")
		os.Exit(2)
	}
	keys, err := readKeys(*keysFile)
	if err != nil || len(keys) == 0 {
		fmt.Fprintln(os.Stderr, "no keys:", err)
		os.Exit(2)
	}
	fixtures, err := loadFixtures(*dir)
	if err != nil || len(fixtures) == 0 {
		fmt.Fprintln(os.Stderr, "no fixtures:", err)
		os.Exit(2)
	}

	var plain, txs []fixture
	for _, f := range fixtures {
		if f.tx {
			txs = append(txs, f)
		} else {
			plain = append(plain, f)
		}
	}
	if len(plain) == 0 {
		plain, txs = txs, nil
	}
	var (
		accEvents, accItems, reqs, ok200, r429, r503, other, netErr atomic.Int64
		mu                                                          sync.Mutex
		lat                                                         []time.Duration
		statuses                                                    sync.Map
	)
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *workers * 2, MaxConnsPerHost: *workers * 2}}
	deadline := time.Now().Add(time.Duration(*minutes * float64(time.Minute)))
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := mrand.New(mrand.NewPCG(uint64(w), 7))
			var local []time.Duration
			for time.Now().Before(deadline) {
				fx := plain[r.IntN(len(plain))]
				if len(txs) > 0 && r.Float64() < *txShare {
					fx = txs[r.IntN(len(txs))]
				}
				k := keys[r.IntN(len(keys))]
				if *otlp {
					fx = fixture{events: *perReq, items: *perReq}
				}
				if *otlpMetrics {
					fx = fixture{events: *pointsPerReq, items: *pointsPerReq}
				}
				if *otlpLogs {
					fx = fixture{events: *logsPerReq, items: *logsPerReq}
				}
				head := dsnRe.ReplaceAll(fx.header, []byte(fmt.Sprintf(`"dsn":"http://%s@%s/%s"`, k.key, strings.TrimPrefix(*base, "http://"), k.site)))
				body := append(append(append([]byte{}, head...), '\n'), fx.rest...)
				if fx.tx {
					now := time.Now().UTC()
					// Rewriting timestamps changes payload sizes; payloads are one line, so drop the lengths.
					body = lengthRe.ReplaceAll(body, nil)
					body = tsNum.ReplaceAllFunc(body, func(m []byte) []byte {
						off := 500 * time.Millisecond
						if bytes.HasPrefix(m, []byte(`"start_timestamp"`)) {
							off = time.Second
						}
						return []byte(fmt.Sprintf(`"%s":%.3f`, m[1:bytes.IndexByte(m[1:], '"')+1], float64(now.Add(-off).UnixMilli())/1000))
					})
					body = tsStr.ReplaceAllFunc(body, func(m []byte) []byte {
						off := 500 * time.Millisecond
						if bytes.HasPrefix(m, []byte(`"start_timestamp"`)) {
							off = time.Second
						}
						if bytes.HasPrefix(m, []byte(`"sent_at"`)) {
							off = 0
						}
						return []byte(fmt.Sprintf(`"%s":"%s"`, m[1:bytes.IndexByte(m[1:], '"')+1], now.Add(-off).Format(time.RFC3339Nano)))
					})
				}
				id := newID()
				body = eventID.ReplaceAll(body, []byte(`"event_id":"`+id+`"`))
				target := fmt.Sprintf("%s/api/%s/envelope/", *base, k.site)
				if *otlp {
					body = otlpBody(r, *perReq)
					target = fmt.Sprintf("%s/api/%s/otlp/v1/traces", *base, k.site)
				}
				if *otlpMetrics {
					body = otlpMetricsBody(r, *pointsPerReq)
					target = fmt.Sprintf("%s/api/%s/otlp/v1/metrics", *base, k.site)
				}
				if *otlpLogs {
					body = otlpLogsBody(r, *logsPerReq)
					target = fmt.Sprintf("%s/api/%s/otlp/v1/logs", *base, k.site)
				}
				req, _ := http.NewRequest(http.MethodPost, target, nil)
				if *otlpLogs || r.Float64() < *gz {
					var b bytes.Buffer
					zw := gzip.NewWriter(&b)
					_, _ = zw.Write(body)
					_ = zw.Close()
					body = b.Bytes()
					req.Header.Set("Content-Encoding", "gzip")
				}
				req.Body = io.NopCloser(bytes.NewReader(body))
				req.ContentLength = int64(len(body))
				if *otlpLogs {
					req.Header.Set("Content-Type", "application/x-protobuf")
					req.Header.Set("Authorization", "Bearer "+k.key)
				} else if *otlp || *otlpMetrics {
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer "+k.key)
				} else {
					req.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7, sentry_client=bench/1, sentry_key="+k.key)
				}
				req.Header.Set("X-Forwarded-For", fmt.Sprintf("%d.%d.%d.%d", 20+r.IntN(180), r.IntN(256), r.IntN(256), 1+r.IntN(254)))
				t0 := time.Now()
				resp, err := client.Do(req)
				if err != nil {
					netErr.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				local = append(local, time.Since(t0))
				reqs.Add(1)
				switch resp.StatusCode {
				case 200:
					ok200.Add(1)
					accEvents.Add(int64(fx.events))
					accItems.Add(int64(fx.items))
				case 429:
					r429.Add(1)
				case 503:
					r503.Add(1)
				default:
					other.Add(1)
					c, _ := statuses.LoadOrStore(resp.StatusCode, new(atomic.Int64))
					c.(*atomic.Int64).Add(1)
				}
			}
			mu.Lock()
			lat = append(lat, local...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	secs := time.Since(start).Seconds()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(float64(len(lat)-1)*p)]
	}
	fmt.Printf("duration=%.1fs workers=%d sites=%d fixtures=%d\n", secs, *workers, len(keys), len(fixtures))
	fmt.Printf("requests=%d (%.0f/s) ok=%d 429=%d 503=%d other=%d net_errors=%d\n", reqs.Load(), float64(reqs.Load())/secs, ok200.Load(), r429.Load(), r503.Load(), other.Load(), netErr.Load())
	statuses.Range(func(k, v any) bool { fmt.Printf("  status %v: %d\n", k, v.(*atomic.Int64).Load()); return true })
	if *otlp {
		fmt.Printf("accepted spans/s=%.0f\n", float64(accEvents.Load())/secs)
	}
	if *otlpMetrics {
		fmt.Printf("accepted points/s=%.0f\n", float64(accEvents.Load())/secs)
	}
	if *otlpLogs {
		fmt.Printf("accepted log records/s=%.0f\n", float64(accEvents.Load())/secs)
	}
	fmt.Printf("accepted events/s=%.0f  accepted items/s=%.0f\n", float64(accEvents.Load())/secs, float64(accItems.Load())/secs)
	fmt.Printf("latency p50=%s p99=%s max=%s\n", pct(0.5), pct(0.99), pct(1))
}

// otlpBody builds an OTLP/JSON request of n spans: one server segment with
// n-1 children (a database call and an HTTP call alternating), fresh ids.
func otlpBody(r *mrand.Rand, n int) []byte {
	trace := make([]byte, 16)
	for i := range trace {
		trace[i] = byte(r.IntN(255) + 1)
	}
	now := time.Now().Add(-2 * time.Second)
	nano := func(d time.Duration) string { return fmt.Sprint(now.Add(d).UnixNano()) }
	sid := func(i int) string { return fmt.Sprintf("%016x", uint64(r.Uint32())<<32|uint64(i)+1) }
	root := sid(0)
	var sb strings.Builder
	sb.WriteString(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.version","value":{"stringValue":"1.0.0"}},{"key":"telemetry.sdk.language","value":{"stringValue":"go"}}]},"scopeSpans":[{"spans":[`)
	fmt.Fprintf(&sb, `{"traceId":"%x","spanId":"%s","name":"GET /bench/%d","kind":2,"startTimeUnixNano":"%s","endTimeUnixNano":"%s","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}},{"key":"http.route","value":{"stringValue":"/bench/{id}"}},{"key":"http.response.status_code","value":{"intValue":"200"}}]}`,
		trace, root, r.IntN(100000), nano(0), nano(200*time.Millisecond))
	for i := 1; i < n; i++ {
		if i%2 == 1 {
			fmt.Fprintf(&sb, `,{"traceId":"%x","spanId":"%s","parentSpanId":"%s","name":"query","kind":3,"startTimeUnixNano":"%s","endTimeUnixNano":"%s","attributes":[{"key":"db.system","value":{"stringValue":"postgresql"}},{"key":"db.statement","value":{"stringValue":"select * from t where id = %d"}}]}`,
				trace, sid(i), root, nano(time.Duration(i)*time.Millisecond), nano(time.Duration(i+5)*time.Millisecond), r.IntN(1000000))
		} else {
			fmt.Fprintf(&sb, `,{"traceId":"%x","spanId":"%s","parentSpanId":"%s","name":"GET","kind":3,"startTimeUnixNano":"%s","endTimeUnixNano":"%s","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}},{"key":"url.full","value":{"stringValue":"https://api.example.com/v1/items/%d"}}]}`,
				trace, sid(i), root, nano(time.Duration(i)*time.Millisecond), nano(time.Duration(i+9)*time.Millisecond), r.IntN(1000000))
		}
	}
	sb.WriteString(`]}]}]}`)
	return []byte(sb.String())
}

// otlpMetricsBody builds an OTLP/JSON metrics request of n data points: 70%
// gauges, 20% cumulative counter readings and 10% cumulative histograms over a
// bounded set of series (200 per kind), stamped now.
func otlpMetricsBody(r *mrand.Rand, n int) []byte {
	now := time.Now().Add(-time.Second).UnixNano()
	start := time.Now().Add(-time.Hour).UnixNano()
	var gauges, sums, hists []string
	for i := 0; i < n; i++ {
		attr := fmt.Sprintf(`[{"key":"series","value":{"stringValue":"s%d"}}]`, r.IntN(200))
		switch v := r.IntN(10); {
		case v < 7:
			gauges = append(gauges, fmt.Sprintf(`{"attributes":%s,"timeUnixNano":"%d","asDouble":%d.5}`, attr, now, r.IntN(1000)))
		case v < 9:
			sums = append(sums, fmt.Sprintf(`{"attributes":%s,"startTimeUnixNano":"%d","timeUnixNano":"%d","asInt":"%d"}`, attr, start, now, time.Now().Unix()%100000+int64(r.IntN(50))))
		default:
			c := r.IntN(100) + 10
			hists = append(hists, fmt.Sprintf(`{"attributes":%s,"startTimeUnixNano":"%d","timeUnixNano":"%d","count":"%d","sum":%d,"bucketCounts":["%d","%d","%d"],"explicitBounds":[10,100]}`,
				attr, start, now, c, c*20, c/3, c/3, c-2*(c/3)))
		}
	}
	var parts []string
	if len(gauges) > 0 {
		parts = append(parts, `{"name":"bench.gauge","gauge":{"dataPoints":[`+strings.Join(gauges, ",")+`]}}`)
	}
	if len(sums) > 0 {
		parts = append(parts, `{"name":"bench.requests","sum":{"aggregationTemporality":2,"isMonotonic":true,"dataPoints":[`+strings.Join(sums, ",")+`]}}`)
	}
	if len(hists) > 0 {
		parts = append(parts, `{"name":"bench.latency","histogram":{"aggregationTemporality":2,"dataPoints":[`+strings.Join(hists, ",")+`]}}`)
	}
	return []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"bench"}}]},"scopeMetrics":[{"metrics":[` + strings.Join(parts, ",") + `]}]}]}`)
}

var logWords = strings.Fields("request handled cache miss upstream timeout retry order payment user session login failed started finished queue worker job scheduled connection pool refused slow query disk memory limit reached")

// otlpLogsBody builds an OTLP protobuf logs request of n records: 60% info,
// 25% debug, 10% warn, 5% error, 20% with a trace id, bodies of a few words
// from a small vocabulary plus an id, stamped now.
func otlpLogsBody(r *mrand.Rand, n int) []byte {
	now := uint64(time.Now().Add(-time.Second).UnixNano())
	res := &logspb.ResourceLogs{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("svc-%d", r.IntN(50))}}},
			{Key: "deployment.environment.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "bench"}}},
		}},
	}
	scope := &logspb.ScopeLogs{}
	for i := 0; i < n; i++ {
		sev := logspb.SeverityNumber_SEVERITY_NUMBER_INFO
		switch v := r.IntN(100); {
		case v < 25:
			sev = logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG
		case v < 35:
			sev = logspb.SeverityNumber_SEVERITY_NUMBER_WARN
		case v < 40:
			sev = logspb.SeverityNumber_SEVERITY_NUMBER_ERROR
		}
		words := make([]string, 4+r.IntN(5))
		for j := range words {
			words[j] = logWords[r.IntN(len(logWords))]
		}
		rec := &logspb.LogRecord{
			TimeUnixNano:   now,
			SeverityNumber: sev,
			Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: strings.Join(words, " ") + fmt.Sprintf(" id=%d", r.IntN(1_000_000))}},
			Attributes: []*commonpb.KeyValue{
				{Key: "http.route", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("/r/%d", r.IntN(40))}}},
				{Key: "attempt", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: int64(r.IntN(5))}}},
			},
		}
		if r.IntN(5) == 0 {
			tid := make([]byte, 16)
			for j := range tid {
				tid[j] = byte(r.IntN(255) + 1)
			}
			rec.TraceId = tid
		}
		scope.LogRecords = append(scope.LogRecords, rec)
	}
	res.ScopeLogs = []*logspb.ScopeLogs{scope}
	b, _ := proto.Marshal(res)
	return protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), b)
}
