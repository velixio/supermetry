package obsmetrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/spanrollup"
)

// Query limits.
const (
	MaxQueryGroups  = 50
	MaxQueryGroupBy = 2
	MaxQueryBuckets = 1500
	MaxQueryFilters = 16
	maxNameBytes    = 255
	maxKeyBytes     = 128
	maxValueBytes   = 256
	rawMaxRange     = 48 * time.Hour
	otherGroupName  = "other"
)

// ErrInvalid wraps a problem with a MetricQuery the caller can fix.
var ErrInvalid = errors.New("invalid metric query")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// MetricQuery selects one metric name of a site. In a dashboard widget the
// From and To are filled by the dashboard's time range.
type MetricQuery struct {
	Name     string            `json:"name"`
	Agg      string            `json:"agg"` // avg|sum|min|max|last|rate|p50|p75|p90|p95|p99
	GroupBy  []string          `json:"group_by,omitempty"`
	Filters  map[string]string `json:"filters,omitempty"`
	From     time.Time         `json:"from"`
	To       time.Time         `json:"to"`
	Interval string            `json:"interval"` // 1m|5m|1h|1d
}

// SeriesPoint is one bucket of a result series; Value is nil when the bucket
// has no value for the aggregation (an empty histogram has no percentile).
type SeriesPoint struct {
	TS    time.Time `json:"ts"`
	Value *float64  `json:"value"`
}

// Series is one group of the result.
type Series struct {
	Group  map[string]string `json:"group"`
	Points []SeriesPoint     `json:"points"`
}

var aggs = map[string]bool{"avg": true, "sum": true, "min": true, "max": true, "last": true, "rate": true,
	"p50": true, "p75": true, "p90": true, "p95": true, "p99": true}

var percentiles = map[string]float64{"p50": 0.50, "p75": 0.75, "p90": 0.90, "p95": 0.95, "p99": 0.99}

var intervals = map[string]time.Duration{"1m": time.Minute, "5m": 5 * time.Minute, "1h": time.Hour, "24h": 24 * time.Hour, "1d": 24 * time.Hour}

// ValidateShape checks the parts of a query that do not depend on a time
// range, so a dashboard can validate a widget before it has From and To.
func (q MetricQuery) ValidateShape() error {
	if q.Name == "" || len(q.Name) > maxNameBytes {
		return invalid("name must be 1 to %d bytes", maxNameBytes)
	}
	if !aggs[q.Agg] {
		return invalid("unknown aggregation %q", q.Agg)
	}
	if _, ok := intervals[q.Interval]; !ok || q.Interval == "24h" {
		return invalid("interval must be 1m, 5m, 1h or 1d")
	}
	if len(q.GroupBy) > MaxQueryGroupBy {
		return invalid("group_by takes at most %d attribute keys", MaxQueryGroupBy)
	}
	seen := map[string]bool{}
	for _, k := range q.GroupBy {
		if k == "" || len(k) > maxKeyBytes || seen[k] {
			return invalid("group_by keys must be distinct and 1 to %d bytes", maxKeyBytes)
		}
		seen[k] = true
	}
	if len(q.Filters) > MaxQueryFilters {
		return invalid("at most %d filters", MaxQueryFilters)
	}
	for k, v := range q.Filters {
		if k == "" || len(k) > maxKeyBytes || len(v) > maxValueBytes {
			return invalid("filter keys are 1 to %d bytes and values at most %d", maxKeyBytes, maxValueBytes)
		}
	}
	return nil
}

// MetricInfo describes one metric name of a site.
type MetricInfo struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Unit          string   `json:"unit"`
	Series        int      `json:"series"`
	AttributeKeys []string `json:"attribute_keys"`
}

// ListMetrics returns up to 1,000 metric names of the site with their kind,
// unit, series count and attribute keys. It reads only metric_series.
func ListMetrics(ctx context.Context, pool *pgxpool.Pool, siteID int32) ([]MetricInfo, error) {
	rows, err := pool.Query(ctx, `
select s.name, mode() within group (order by s.kind), mode() within group (order by s.unit), count(*)::int,
       coalesce((select array_agg(distinct k order by k) from metric_series s2, jsonb_object_keys(s2.attrs) k
                 where s2.site_id = s.site_id and s2.name = s.name), '{}')
from metric_series s
where s.site_id = $1
group by s.site_id, s.name
order by s.name
limit 1000`, siteID)
	if err != nil {
		return nil, fmt.Errorf("list metrics: %w", err)
	}
	defer rows.Close()
	out := []MetricInfo{}
	for rows.Next() {
		var m MetricInfo
		if err := rows.Scan(&m.Name, &m.Kind, &m.Unit, &m.Series, &m.AttributeKeys); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type seriesRow struct {
	id     int64
	kind   string
	bounds []float64
	attrs  map[string]any
}

// partial is the mergeable aggregate of one (group, bucket).
type partial struct {
	samples        int64
	vsum           float64
	hasSum         bool
	vmin, vmax     float64
	hasMinMax      bool
	vlast          float64
	vlastTS        time.Time
	hasLast        bool
	count          int64
	hasCount       bool
	hsum           float64
	buckets        []int64
	bucketsMatched bool
}

func (p *partial) merge(o *partial) {
	p.samples += o.samples
	if o.hasSum {
		p.vsum += o.vsum
		p.hasSum = true
	}
	if o.hasMinMax {
		if !p.hasMinMax || o.vmin < p.vmin {
			p.vmin = o.vmin
		}
		if !p.hasMinMax || o.vmax > p.vmax {
			p.vmax = o.vmax
		}
		p.hasMinMax = true
	}
	if o.hasLast && (!p.hasLast || o.vlastTS.After(p.vlastTS)) {
		p.vlast, p.vlastTS, p.hasLast = o.vlast, o.vlastTS, true
	}
	if o.hasCount {
		p.count += o.count
		p.hasCount = true
	}
	p.hsum += o.hsum
	if o.buckets != nil {
		if p.buckets == nil {
			p.buckets = append([]int64(nil), o.buckets...)
		} else if len(p.buckets) == len(o.buckets) {
			for i := range o.buckets {
				p.buckets[i] += o.buckets[i]
			}
		}
	}
}

type group struct {
	labels  map[string]string
	key     string
	idx     int32
	bounds  []float64
	buckets map[time.Time]*partial
	weight  float64
}

const rawPartials = `
select m.grp, %[1]s as b,
       count(*)::bigint,
       sum(p.value), min(coalesce(p.value, p.min)), max(coalesce(p.value, p.max)),
       (array_agg(p.value order by p.ts desc) filter (where p.value is not null))[1],
       max(p.ts) filter (where p.value is not null),
       sum(p.count)::bigint, sum(p.sum)
from metric_points p
join unnest($1::bigint[], $2::int[]) as m(series_id, grp) on m.series_id = p.series_id
where p.site_id = $3 and p.series_id = any($1) and p.ts >= $4 and p.ts < $5
group by 1, 2`

const hourlyPartials = `
select m.grp, %[1]s as b,
       sum(h.samples)::bigint,
       sum(h.value_sum), min(h.value_min), max(h.value_max),
       (array_agg(h.value_last order by h.hour desc) filter (where h.value_last is not null))[1],
       max(h.hour) filter (where h.value_last is not null),
       sum(h.count)::bigint, sum(h.sum)
from metric_hourly h
join unnest($1::bigint[], $2::int[]) as m(series_id, grp) on m.series_id = h.series_id
where h.site_id = $3 and h.series_id = any($1) and h.hour >= $4 and h.hour < $5
group by 1, 2`

const rawBuckets = `
select grp, b, array_agg(total order by i) from (
    select m.grp, %[1]s as b, u.i, sum(u.c)::bigint as total
    from metric_points p
    join unnest($1::bigint[], $2::int[]) as m(series_id, grp) on m.series_id = p.series_id,
         unnest(p.bucket_counts) with ordinality as u(c, i)
    where p.site_id = $3 and p.series_id = any($1) and p.ts >= $4 and p.ts < $5 and p.bucket_counts is not null
    group by 1, 2, 3
) t group by grp, b`

const hourlyBuckets = `
select grp, b, array_agg(total order by i) from (
    select m.grp, %[1]s as b, u.i, sum(u.c)::bigint as total
    from metric_hourly h
    join unnest($1::bigint[], $2::int[]) as m(series_id, grp) on m.series_id = h.series_id,
         unnest(h.bucket_counts) with ordinality as u(c, i)
    where h.site_id = $3 and h.series_id = any($1) and h.hour >= $4 and h.hour < $5 and h.bucket_counts is not null
    group by 1, 2, 3
) t group by grp, b`

type plan struct {
	raw        bool
	step       time.Duration
	bucketExpr string
	from, to   time.Time // raw: UTC instants; hourly: site-local wall clock
	loc        *time.Location
}

func (q MetricQuery) plan(loc *time.Location) (plan, error) {
	if loc == nil {
		loc = time.UTC
	}
	if !q.From.Before(q.To) {
		return plan{}, invalid("from must be before to")
	}
	step := intervals[q.Interval]
	span := q.To.Sub(q.From)
	p := plan{loc: loc}
	if step < time.Hour && span <= rawMaxRange {
		p.raw, p.step = true, step
		p.bucketExpr = `date_bin(interval '` + fmt.Sprintf("%d seconds", int(step.Seconds())) + `', p.ts, timestamptz '2000-01-01 00:00:00+00')`
		p.from, p.to = q.From.UTC(), q.To.UTC()
		if buckets := int(math.Ceil(float64(span) / float64(step))); buckets > MaxQueryBuckets {
			return plan{}, invalid("range and interval give %d buckets, at most %d", buckets, MaxQueryBuckets)
		}
		return p, nil
	}
	// Hourly rollups: whole site-local hours, or whole local days.
	p.from, p.to = spanrollup.Wall(q.From.In(loc)), spanrollup.Wall(q.To.In(loc).Add(-time.Nanosecond)).Add(time.Hour)
	if q.Interval == "1d" {
		p.step = 24 * time.Hour
		p.bucketExpr = `date_trunc('day', h.hour)`
		last := spanrollup.Wall(q.To.In(loc).Add(-time.Nanosecond))
		p.from = time.Date(p.from.Year(), p.from.Month(), p.from.Day(), 0, 0, 0, 0, time.UTC)
		p.to = time.Date(last.Year(), last.Month(), last.Day()+1, 0, 0, 0, 0, time.UTC)
	} else {
		p.step = time.Hour
		p.bucketExpr = `h.hour`
	}
	if buckets := int(p.to.Sub(p.from) / p.step); buckets > MaxQueryBuckets {
		return plan{}, invalid("range and interval give %d buckets, at most %d", buckets, MaxQueryBuckets)
	}
	return p, nil
}

// Query runs q for a site, in its time zone loc: raw points when the range is
// at most 48 hours and the interval under an hour, the hourly rollup
// otherwise (1m and 5m are then served as 1h). A day bucket is a site-local
// day. At most 50 groups are returned; the rest are merged into one "other"
// group. A metric name that holds several kinds is queried as its most common
// kind. Every statement is scoped by site_id.
func Query(ctx context.Context, pool *pgxpool.Pool, siteID int32, loc *time.Location, q MetricQuery) ([]Series, error) {
	if err := q.ValidateShape(); err != nil {
		return nil, err
	}
	pl, err := q.plan(loc)
	if err != nil {
		return nil, err
	}
	rows, err := loadSeries(ctx, pool, siteID, q)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []Series{}, nil
	}
	kind := dominantKind(rows)
	if err := checkAgg(kind, q.Agg); err != nil {
		return nil, err
	}

	groups := map[string]*group{}
	var order []*group
	var ids []int64
	var idx []int32
	var bucketIDs []int64
	var bucketIdx []int32
	for _, r := range rows {
		if r.kind != kind {
			continue
		}
		labels := make(map[string]string, len(q.GroupBy))
		var key strings.Builder
		for _, k := range q.GroupBy {
			v := ""
			if raw, ok := r.attrs[k]; ok {
				if s, isStr := raw.(string); isStr {
					v = s
				} else {
					v = fmt.Sprint(raw)
				}
			}
			labels[k] = v
			key.WriteString(v)
			key.WriteByte(0)
		}
		g := groups[key.String()]
		if g == nil {
			g = &group{labels: labels, key: key.String(), idx: int32(len(order)), bounds: r.bounds, buckets: map[time.Time]*partial{}}
			groups[g.key] = g
			order = append(order, g)
		}
		ids = append(ids, r.id)
		idx = append(idx, g.idx)
		if kind == "histogram" && boundsEqual(g.bounds, r.bounds) {
			bucketIDs = append(bucketIDs, r.id)
			bucketIdx = append(bucketIdx, g.idx)
		}
	}

	scalarSQL, bucketSQL := hourlyPartials, hourlyBuckets
	if pl.raw {
		scalarSQL, bucketSQL = rawPartials, rawBuckets
	}
	scanRows, err := pool.Query(ctx, fmt.Sprintf(scalarSQL, pl.bucketExpr), ids, idx, siteID, pl.from, pl.to)
	if err != nil {
		return nil, fmt.Errorf("query metric: %w", err)
	}
	for scanRows.Next() {
		var gi int32
		var b time.Time
		var samples int64
		var vsum, vmin, vmax, vlast, hsum *float64
		var vlastTS *time.Time
		var count *int64
		if err := scanRows.Scan(&gi, &b, &samples, &vsum, &vmin, &vmax, &vlast, &vlastTS, &count, &hsum); err != nil {
			scanRows.Close()
			return nil, err
		}
		p := &partial{samples: samples}
		if vsum != nil {
			p.vsum, p.hasSum = *vsum, true
		}
		if vmin != nil && vmax != nil {
			p.vmin, p.vmax, p.hasMinMax = *vmin, *vmax, true
		}
		if vlast != nil && vlastTS != nil {
			p.vlast, p.vlastTS, p.hasLast = *vlast, *vlastTS, true
		}
		if count != nil {
			p.count, p.hasCount = *count, true
		}
		if hsum != nil {
			p.hsum = *hsum
		}
		order[gi].buckets[b.UTC()] = p
	}
	scanRows.Close()
	if err := scanRows.Err(); err != nil {
		return nil, err
	}

	if _, isPct := percentiles[q.Agg]; isPct && len(bucketIDs) > 0 {
		br, err := pool.Query(ctx, fmt.Sprintf(bucketSQL, pl.bucketExpr), bucketIDs, bucketIdx, siteID, pl.from, pl.to)
		if err != nil {
			return nil, fmt.Errorf("query metric buckets: %w", err)
		}
		for br.Next() {
			var gi int32
			var b time.Time
			var counts []int64
			if err := br.Scan(&gi, &b, &counts); err != nil {
				br.Close()
				return nil, err
			}
			if p := order[gi].buckets[b.UTC()]; p != nil {
				p.buckets = counts
			}
		}
		br.Close()
		if err := br.Err(); err != nil {
			return nil, err
		}
	}

	return assemble(q, kind, pl, order), nil
}

func loadSeries(ctx context.Context, pool *pgxpool.Pool, siteID int32, q MetricQuery) ([]seriesRow, error) {
	filter := "{}"
	if len(q.Filters) > 0 {
		b, err := json.Marshal(q.Filters)
		if err != nil {
			return nil, err
		}
		filter = string(b)
	}
	rs, err := pool.Query(ctx, `select id, kind, bounds, attrs from metric_series
where site_id = $1 and name = $2 and attrs @> $3::jsonb order by id`, siteID, q.Name, filter)
	if err != nil {
		return nil, fmt.Errorf("find metric series: %w", err)
	}
	defer rs.Close()
	var out []seriesRow
	for rs.Next() {
		var r seriesRow
		var raw []byte
		if err := rs.Scan(&r.id, &r.kind, &r.bounds, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &r.attrs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func dominantKind(rows []seriesRow) string {
	n := map[string]int{}
	for _, r := range rows {
		n[r.kind]++
	}
	best := ""
	for k, c := range n {
		if best == "" || c > n[best] || (c == n[best] && k < best) {
			best = k
		}
	}
	return best
}

func boundsEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func checkAgg(kind, agg string) error {
	if _, isPct := percentiles[agg]; isPct && kind != "histogram" {
		return invalid("%s needs a histogram metric, this one is a %s", agg, kind)
	}
	if agg == "rate" && kind != "counter" && kind != "histogram" {
		return invalid("rate needs a counter or histogram metric, this one is a %s", kind)
	}
	return nil
}

// value derives the aggregation from a bucket's partial; nil when undefined.
func (p *partial) value(kind, agg string, bounds []float64, step time.Duration) *float64 {
	var v float64
	switch agg {
	case "avg":
		switch {
		case kind == "histogram" && p.hasCount && p.count > 0:
			v = p.hsum / float64(p.count)
		case kind != "histogram" && p.hasSum && p.samples > 0:
			v = p.vsum / float64(p.samples)
		default:
			return nil
		}
	case "sum":
		switch {
		case kind == "histogram" && p.hasCount:
			v = p.hsum
		case p.hasSum:
			v = p.vsum
		default:
			return nil
		}
	case "min":
		if !p.hasMinMax {
			return nil
		}
		v = p.vmin
	case "max":
		if !p.hasMinMax {
			return nil
		}
		v = p.vmax
	case "last":
		if !p.hasLast {
			return nil
		}
		v = p.vlast
	case "rate":
		switch {
		case kind == "histogram" && p.hasCount:
			v = float64(p.count) / step.Seconds()
		case p.hasSum:
			v = p.vsum / step.Seconds()
		default:
			return nil
		}
	default:
		q, ok := percentiles[agg]
		if !ok || p.buckets == nil {
			return nil
		}
		var mn, mx *float64
		if p.hasMinMax {
			mn, mx = &p.vmin, &p.vmax
		}
		v = Percentile(bounds, p.buckets, q, mn, mx)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// weight ranks groups for the 50-group cap: total magnitude of the stored values.
func (p *partial) weight() float64 {
	switch {
	case p.hasSum:
		return math.Abs(p.vsum)
	case p.hasCount:
		return float64(p.count)
	}
	return float64(p.samples)
}

func assemble(q MetricQuery, kind string, pl plan, order []*group) []Series {
	for _, g := range order {
		for _, p := range g.buckets {
			g.weight += p.weight()
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].weight != order[j].weight {
			return order[i].weight > order[j].weight
		}
		return order[i].key < order[j].key
	})
	kept := order
	var other *group
	if len(order) > MaxQueryGroups {
		kept = order[:MaxQueryGroups]
		labels := map[string]string{}
		for _, k := range q.GroupBy {
			labels[k] = otherGroupName
		}
		other = &group{labels: labels, bounds: order[0].bounds, buckets: map[time.Time]*partial{}}
		for _, g := range order[MaxQueryGroups:] {
			for b, p := range g.buckets {
				if cur := other.buckets[b]; cur != nil {
					cur.merge(p)
				} else {
					c := *p
					c.buckets = append([]int64(nil), p.buckets...)
					other.buckets[b] = &c
				}
			}
		}
	}
	out := make([]Series, 0, len(kept)+1)
	emit := func(g *group) {
		times := make([]time.Time, 0, len(g.buckets))
		for b := range g.buckets {
			times = append(times, b)
		}
		sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
		s := Series{Group: g.labels, Points: make([]SeriesPoint, 0, len(times))}
		for _, b := range times {
			ts := b
			if !pl.raw { // a wall-clock bucket: the instant that local time starts
				ts = time.Date(b.Year(), b.Month(), b.Day(), b.Hour(), b.Minute(), 0, 0, pl.loc).UTC()
			}
			s.Points = append(s.Points, SeriesPoint{TS: ts, Value: g.buckets[b].value(kind, q.Agg, g.bounds, pl.step)})
		}
		out = append(out, s)
	}
	for _, g := range kept {
		emit(g)
	}
	if other != nil {
		emit(other)
	}
	return out
}
