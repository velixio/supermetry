package core

// Aggregator builds daily rollups in memory from sessionized events
// (rollup-stream, tier M-history). Feed it every event with AddEvent and
// every finished session with AddSession (the Cache OnRetire hook), then
// Finalize a (site, day) bucket once no more sessions of it can change.
// Definitions match the SQL recompute in store/rollup.go.
type Aggregator struct {
	buckets map[SaltKey]*bucket
}

type visKey struct {
	val uint32 // interned value index within (bucket, dim); 0 = day total
	dim Dim
	vid int64
}

type bucket struct {
	vals  [DimExit + 1]map[string]uint32
	accs  [DimExit + 1][]Metrics // indexed by interned value; accs[0][0] = total
	names [DimExit + 1][]string
	seen  map[visKey]struct{} // visitor sets of every (dim, value)
}

// NewAggregator returns an empty aggregator.
func NewAggregator() *Aggregator { return &Aggregator{buckets: map[SaltKey]*bucket{}} }

func (a *Aggregator) bucket(site int32, day Day) *bucket {
	k := SaltKey{site, day}
	b := a.buckets[k]
	if b == nil {
		b = &bucket{seen: map[visKey]struct{}{}}
		for i := range b.vals {
			b.vals[i] = map[string]uint32{}
		}
		b.accs[0] = []Metrics{{}}
		b.names[0] = []string{""}
		a.buckets[k] = b
	}
	return b
}

func (b *bucket) acc(d Dim, v string) (*Metrics, uint32) {
	i, ok := b.vals[d][v]
	if !ok {
		i = uint32(len(b.accs[d]))
		b.vals[d][v] = i
		b.accs[d] = append(b.accs[d], Metrics{})
		b.names[d] = append(b.names[d], v)
	}
	return &b.accs[d][i], i
}

func (b *bucket) visit(d Dim, idx uint32, vid int64, m *Metrics) {
	k := visKey{idx, d, vid}
	if _, ok := b.seen[k]; !ok {
		b.seen[k] = struct{}{}
		m.Visitors++
	}
}

// AddEvent counts a pageview for its path (visitors, pageviews).
func (a *Aggregator) AddEvent(ev *Event) {
	if !ev.IsPageview() {
		return
	}
	b := a.bucket(ev.SiteID, ev.Day)
	m, i := b.acc(DimPath, ev.Path)
	m.Pageviews++
	b.visit(DimPath, i, ev.VisitorID, m)
}

var sessionDims = []Dim{DimSource, DimCountry, DimBrowser, DimOS, DimDevice, DimEntry, DimExit}

// AddSession folds a finished session into totals and session dimensions,
// and into visits/bounces/duration of each distinct page it viewed
// (requires CacheConfig.TrackPaths).
func (a *Aggregator) AddSession(s *Session) {
	b := a.bucket(s.SiteID, s.Day())
	dur, bounce := s.DurationMs(), int64(0)
	if s.Bounce() {
		bounce = 1
	}
	fold := func(d Dim, v string) {
		var m *Metrics
		var i uint32
		if d == 0 {
			m = &b.accs[0][0]
		} else {
			m, i = b.acc(d, v)
		}
		m.Visits++
		m.Pageviews += int64(s.Pageviews)
		m.Bounces += bounce
		m.DurationMs += dur
		b.visit(d, i, s.VisitorID, m)
	}
	fold(0, "")
	for _, d := range sessionDims {
		if (d == DimEntry || d == DimExit) && s.Pageviews == 0 {
			continue
		}
		fold(d, s.SessionValue(d))
	}
	for _, p := range s.Paths() {
		m, _ := b.acc(DimPath, p)
		m.Visits++
		m.Bounces += bounce
		m.DurationMs += dur
	}
}

// Finalize removes the bucket and returns its rows (nil if absent).
func (a *Aggregator) Finalize(k SaltKey) []RollupRow {
	b := a.buckets[k]
	if b == nil {
		return nil
	}
	delete(a.buckets, k)
	rows := []RollupRow{{Site: k.Site, Day: k.Day, Metrics: b.accs[0][0]}}
	for d := DimPath; d <= DimExit; d++ {
		for i, m := range b.accs[d] {
			rows = append(rows, RollupRow{Site: k.Site, Day: k.Day, Dim: d, Value: b.names[d][i], Metrics: m})
		}
	}
	return rows
}

// Open returns the keys of all open buckets.
func (a *Aggregator) Open() []SaltKey {
	out := make([]SaltKey, 0, len(a.buckets))
	for k := range a.buckets {
		out = append(out, k)
	}
	return out
}
