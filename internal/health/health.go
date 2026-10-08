// Package health exposes process-local analytics health metrics.
package health

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Counter is a monotonically increasing metric.
type Counter struct {
	value atomic.Uint64
}

// Add increments the counter by delta.
func (c *Counter) Add(delta uint64) { c.value.Add(delta) }

// Load returns the current counter value.
func (c *Counter) Load() uint64 { return c.value.Load() }

// Gauge is a concurrently accessible floating-point metric.
type Gauge struct {
	bits atomic.Uint64
}

// ExclusionReason is a bounded label value for intentionally filtered traffic.
type ExclusionReason uint8

const (
	ExclusionNone ExclusionReason = iota
	ExclusionIP
	ExclusionPath
)

// Store replaces the current gauge value.
func (g *Gauge) Store(value float64) { g.bits.Store(math.Float64bits(value)) }

// Observe records the latest observed value.
func (g *Gauge) Observe(value float64) { g.Store(value) }

// Load returns the current gauge value.
func (g *Gauge) Load() float64 { return math.Float64frombits(g.bits.Load()) }

var (
	IngestAccepted           Counter
	IngestInvalid            Counter
	IngestRevenueInvalid     Counter
	IngestProfilesSuppressed Counter
	IngestDropped            Counter

	// BotsFiltered is indexed by enrich.BotReason. Index zero is the
	// unfiltered case and is intentionally not exported as a metric.
	BotsFiltered [5]Counter
	// ExclusionsFiltered is indexed by ExclusionReason. Index zero is unused.
	ExclusionsFiltered [3]Counter

	WriterQueueDepth           Gauge
	CopySeconds                Gauge
	CloseLagSeconds            Gauge
	CloseDurationSeconds       Gauge
	GeoIPUpdateLastSuccessUnix Gauge
	GeoIPUpdateFailures        Counter
	AlertDeliveryFailures      Counter

	QuerySeconds = newRouteHistogram()

	QueryCacheHits   Counter
	QueryCacheMisses Counter

	geoIPSource atomic.Uint32
)

const (
	geoIPSourceDBIP uint32 = iota
	geoIPSourceMaxMind
)

var botReasons = [...]string{"", "ua", "headless", "datacenter", "spam_referrer"}
var exclusionReasons = [...]string{"", "ip", "path"}

var querySecondBuckets = [...]float64{
	0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25,
	0.5, 1, 2.5, 5, 10, 30,
}

// SetGeoIPSource records the active GeoIP database provider. Unsupported
// values leave the current provider unchanged.
func SetGeoIPSource(source string) {
	switch source {
	case "dbip":
		geoIPSource.Store(geoIPSourceDBIP)
	case "maxmind":
		geoIPSource.Store(geoIPSourceMaxMind)
	}
}

// GeoIPSource returns the active GeoIP database provider.
func GeoIPSource() string {
	if geoIPSource.Load() == geoIPSourceMaxMind {
		return "maxmind"
	}
	return "dbip"
}

type bucketHistogram struct {
	buckets [len(querySecondBuckets) + 1]atomic.Uint64
	count   atomic.Uint64
}

// RouteHistogram keeps a bounded fixed-bucket latency histogram for every
// query planner route. Snapshot reports the upper bound of its p95 bucket.
type RouteHistogram struct {
	routes sync.Map // map[string]*bucketHistogram
}

func newRouteHistogram() *RouteHistogram { return &RouteHistogram{} }

// Observe adds a query duration in seconds for route.
func (h *RouteHistogram) Observe(route string, seconds float64) {
	if h == nil || route == "" || math.IsNaN(seconds) || seconds < 0 {
		return
	}
	value, _ := h.routes.LoadOrStore(route, &bucketHistogram{})
	histogram := value.(*bucketHistogram)
	index := sort.SearchFloat64s(querySecondBuckets[:], seconds)
	histogram.buckets[index].Add(1)
	histogram.count.Add(1)
}

func (h *RouteHistogram) snapshot() map[string]float64 {
	result := make(map[string]float64)
	if h == nil {
		return result
	}
	h.routes.Range(func(key, value any) bool {
		histogram := value.(*bucketHistogram)
		count := histogram.count.Load()
		if count == 0 {
			return true
		}
		target := (count*95 + 99) / 100
		var cumulative uint64
		p95 := math.Inf(1)
		for i := range histogram.buckets {
			cumulative += histogram.buckets[i].Load()
			if cumulative >= target {
				if i < len(querySecondBuckets) {
					p95 = querySecondBuckets[i]
				}
				break
			}
		}
		result[key.(string)] = p95
		return true
	})
	return result
}

// Snapshot returns a consistent-enough point-in-time view of every metric.
// Individual values are loaded atomically; the map is independent of future
// updates.
func Snapshot() map[string]float64 {
	snapshot := map[string]float64{
		"ingest_accepted_total":            float64(IngestAccepted.Load()),
		"ingest_invalid_total":             float64(IngestInvalid.Load()),
		"ingest_revenue_invalid_total":     float64(IngestRevenueInvalid.Load()),
		"ingest_profiles_suppressed_total": float64(IngestProfilesSuppressed.Load()),
		"ingest_dropped_total":             float64(IngestDropped.Load()),
		"writer_queue_depth":               WriterQueueDepth.Load(),
		"copy_seconds":                     CopySeconds.Load(),
		"close_lag_seconds":                CloseLagSeconds.Load(),
		"close_duration_seconds":           CloseDurationSeconds.Load(),
		"geoip_update_last_success_unix":   GeoIPUpdateLastSuccessUnix.Load(),
		"geoip_update_failures_total":      float64(GeoIPUpdateFailures.Load()),
		"alert_delivery_failures_total":    float64(AlertDeliveryFailures.Load()),
		"query_cache_hit_ratio":            cacheHitRatio(),
	}
	snapshot[`geoip_source{source="dbip"}`] = 0
	snapshot[`geoip_source{source="maxmind"}`] = 0
	snapshot[fmt.Sprintf(`geoip_source{source=%q}`, GeoIPSource())] = 1
	for reason := 1; reason < len(BotsFiltered); reason++ {
		snapshot[fmt.Sprintf(`bots_filtered_total{reason=%q}`, botReasons[reason])] = float64(BotsFiltered[reason].Load())
	}
	for reason := 1; reason < len(ExclusionsFiltered); reason++ {
		snapshot[fmt.Sprintf(`ingest_excluded_total{reason=%q}`, exclusionReasons[reason])] = float64(ExclusionsFiltered[reason].Load())
	}
	for route, p95 := range QuerySeconds.snapshot() {
		snapshot[fmt.Sprintf(`query_seconds{route=%q}`, escapeLabel(route))] = p95
	}
	return snapshot
}

func cacheHitRatio() float64 {
	hits := QueryCacheHits.Load()
	total := hits + QueryCacheMisses.Load()
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// WritePrometheus writes metrics in the Prometheus text exposition format.
func WritePrometheus(w io.Writer) {
	snapshot := Snapshot()
	writeFamily(w, snapshot, "ingest_accepted_total", "counter")
	writeFamily(w, snapshot, "ingest_invalid_total", "counter")
	writeFamily(w, snapshot, "ingest_revenue_invalid_total", "counter")
	writeFamily(w, snapshot, "ingest_profiles_suppressed_total", "counter")
	writeFamily(w, snapshot, "ingest_dropped_total", "counter")
	writeFamily(w, snapshot, "bots_filtered_total", "counter")
	writeFamily(w, snapshot, "ingest_excluded_total", "counter")
	writeFamily(w, snapshot, "writer_queue_depth", "gauge")
	writeFamily(w, snapshot, "copy_seconds", "gauge")
	writeFamily(w, snapshot, "close_lag_seconds", "gauge")
	writeFamily(w, snapshot, "close_duration_seconds", "gauge")
	writeFamily(w, snapshot, "geoip_update_last_success_unix", "gauge")
	writeFamily(w, snapshot, "geoip_update_failures_total", "counter")
	writeFamily(w, snapshot, "alert_delivery_failures_total", "counter")
	writeFamily(w, snapshot, "geoip_source", "gauge")
	writeFamily(w, snapshot, "query_seconds", "gauge")
	writeFamily(w, snapshot, "query_cache_hit_ratio", "gauge")
}

func writeFamily(w io.Writer, snapshot map[string]float64, name, metricType string) {
	_, _ = fmt.Fprintf(w, "# TYPE panel_%s %s\n", name, metricType)
	keys := make([]string, 0)
	for key := range snapshot {
		if key == name || strings.HasPrefix(key, name+"{") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = fmt.Fprintf(w, "panel_%s %s\n", key, strconv.FormatFloat(snapshot[key], 'g', -1, 64))
	}
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
