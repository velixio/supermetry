// Package obsmetrics is the metrics half of OTLP observability intake: the
// series registry, cumulative-to-delta conversion, point normalisation and the
// batching writer for metric_series and metric_points.
package obsmetrics

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"sync"
	"time"
)

// DefaultSeriesCap is the series limit per site.
const DefaultSeriesCap = 10_000

// Stats receives drop counters by name; nil is allowed everywhere.
type Stats interface{ Add(name string, n int) }

// SeriesKey identifies a series within a site.
type SeriesKey struct {
	Name, Kind, Unit string
	Bounds           []float64
	Attrs            map[string]string
}

// Hash is sha256 over the name, kind, unit, bucket bounds and the attributes
// in sorted key order, each field length-prefixed so fields cannot run into
// one another. Changed bounds therefore give a new series.
func (k SeriesKey) Hash() []byte {
	h := sha256.New()
	var buf [8]byte
	str := func(s string) {
		binary.BigEndian.PutUint32(buf[:4], uint32(len(s)))
		h.Write(buf[:4])
		h.Write([]byte(s))
	}
	str(k.Name)
	str(k.Kind)
	str(k.Unit)
	binary.BigEndian.PutUint32(buf[:4], uint32(len(k.Bounds)))
	h.Write(buf[:4])
	for _, b := range k.Bounds {
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(b))
		h.Write(buf[:])
	}
	keys := make([]string, 0, len(k.Attrs))
	for key := range k.Attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	binary.BigEndian.PutUint32(buf[:4], uint32(len(keys)))
	h.Write(buf[:4])
	for _, key := range keys {
		str(key)
		str(k.Attrs[key])
	}
	return h.Sum(nil)
}

// Point is one normalised data point. Cumulative points carry the running
// total in Value (counters) or Count/Sum/Buckets (histograms) and are turned
// into deltas by the writer; Start is the cumulative window start.
type Point struct {
	SiteID     int32
	SeriesKey  SeriesKey
	Hash       []byte // SeriesKey.Hash(), set by Normalise
	TS         time.Time
	Cumulative bool
	Start      time.Time
	Value      *float64
	Count      *int64
	Sum        *float64
	Min, Max   *float64
	Buckets    []int64
}

// size estimates the bytes a queued point holds.
func (p *Point) size() int {
	n := 192 + len(p.Hash) + len(p.SeriesKey.Name) + len(p.SeriesKey.Unit) + 8*len(p.SeriesKey.Bounds) + 8*len(p.Buckets)
	for k, v := range p.SeriesKey.Attrs {
		n += len(k) + len(v) + 32
	}
	return n
}

type regKey struct {
	site int32
	hash [32]byte
}

type regEntry struct {
	id      int64 // 0 while reserved but not yet inserted
	touched time.Time
}

// Registry is the in-memory view of metric_series used for cardinality checks
// on the request path (no database access) and id lookup in the writer.
type Registry struct {
	mu     sync.Mutex
	cap    int
	series map[regKey]regEntry
	count  map[int32]int
}

// NewRegistry builds an empty registry with a per-site series cap.
func NewRegistry(seriesCap int) *Registry {
	if seriesCap <= 0 {
		seriesCap = DefaultSeriesCap
	}
	return &Registry{cap: seriesCap, series: map[regKey]regEntry{}, count: map[int32]int{}}
}

func makeKey(site int32, h []byte) (regKey, bool) {
	var k regKey
	if len(h) != len(k.hash) {
		return k, false
	}
	k.site = site
	copy(k.hash[:], h)
	return k, true
}

// Admit reports whether a series may be written: it is known, or the site is
// under its cap, in which case the series is reserved in memory.
func (r *Registry) Admit(siteID int32, h []byte) bool {
	k, ok := makeKey(siteID, h)
	if !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, known := r.series[k]; known {
		return true
	}
	if r.count[siteID] >= r.cap {
		return false
	}
	r.series[k] = regEntry{}
	r.count[siteID]++
	return true
}

// ID returns the database id of a known, inserted series.
func (r *Registry) ID(siteID int32, h []byte) (int64, bool) {
	k, ok := makeKey(siteID, h)
	if !ok {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.series[k]
	return e.id, e.id > 0
}

func (r *Registry) set(siteID int32, h []byte, id int64, touched time.Time) {
	k, ok := makeKey(siteID, h)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, known := r.series[k]; !known {
		r.count[siteID]++
	}
	r.series[k] = regEntry{id: id, touched: touched}
}

// dueTouch returns the id of a series whose last_seen was last written
// before cutoff (and marks it written at now); 0 when no touch is due.
func (r *Registry) dueTouch(siteID int32, h []byte, cutoff, now time.Time) int64 {
	k, ok := makeKey(siteID, h)
	if !ok {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.series[k]
	if e.id == 0 || e.touched.After(cutoff) {
		return 0
	}
	e.touched = now
	r.series[k] = e
	return e.id
}

// Len returns the number of series (reserved or inserted) of a site.
func (r *Registry) Len(siteID int32) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count[siteID]
}
