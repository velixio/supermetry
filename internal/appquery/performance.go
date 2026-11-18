package appquery

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"supermetry/internal/spanrollup"
	"supermetry/internal/spans"
)

// Performance reads. Transaction statistics come from the hourly span rollup
// (span_hourly) for hours that are rolled up and from raw spans for the others
// (open or dirty hours), so closed and open hours agree: both use the shared
// log-scale histogram and nearest-rank percentiles (spans.Percentiles).

const (
	// MaxTransactions is the longest transaction list a read returns.
	MaxTransactions = 100
	// regressionMinSamples is the sample count a release needs on each side.
	regressionMinSamples = 30
	// regressionMinDelta is the smallest p95 increase that is listed.
	regressionMinDelta = 0.2
	// rawRetentionDays bounds raw span reads: daily partitions are dropped
	// after the longest project retention (14 days) plus a day.
	rawRetentionDays = 15
	// detailSegmentSample is how many recent segments feed the child span
	// breakdown of a transaction.
	detailSegmentSample = 5000
	// maxNetworkRows bounds the network failure list.
	maxNetworkRows = 50
)

// errorPredicate mirrors spanrollup's error definition over raw spans: a
// non-ok status, an HTTP 5xx, or (http.client children) an HTTP 4xx. Columns
// carry the alias prefix %s.
func errorPredicate(p string) string {
	return `(` + p + `status not in ('', 'ok', 'cancelled') or coalesce(` + p + `http_status, 0) >= 500 or (not ` + p + `is_segment and ` + p + `op = 'http.client' and coalesce(` + p + `http_status, 0) >= 400))`
}

type spanKind int

const (
	kindSegment  spanKind = iota // transactions
	kindChild                    // child span groups
	kindAppStart                 // app.start.cold / app.start.warm rows
)

type histKey struct {
	day     string // local YYYY-MM-DD, or "" when not grouped by day
	name    string
	op      string
	group   int64
	release string
}

type agg struct {
	count, errors, sumUS int64
	hist                 map[int16]int64
}

func newAgg() *agg { return &agg{hist: map[int16]int64{}} }

func (a *agg) add(o *agg) {
	a.count += o.count
	a.errors += o.errors
	a.sumUS += o.sumUS
	for b, c := range o.hist {
		a.hist[b] += c
	}
}

type loadOpts struct {
	kind       spanKind
	op         string   // exact op filter (children), "" for none
	name       string   // transaction name filter, "" for none
	releases   []string // release filter, nil for none
	byDay      bool
	byRelease  bool
	filterDims bool // apply the Filter's release/environment/platform
}

// hourRange is the local-hour range [lo, hi) a window covers.
func (w window) hourRange() (time.Time, time.Time) {
	loc := w.scope.loc()
	return spanrollup.Wall(w.start.In(loc)), spanrollup.Wall(w.end.In(loc))
}

// splitHours returns the rolled-up (done) hours of the window and the open
// ones, with future hours and hours older than the raw retention dropped from
// the open set.
func (q *Querier) splitHours(ctx context.Context, w window) (done, open []time.Time, err error) {
	lo, hi := w.hourRange()
	all, err := spanrollup.OpenHours(ctx, q.pool, w.scope.SiteID, lo, hi)
	if err != nil {
		return nil, nil, err
	}
	isOpen := make(map[time.Time]struct{}, len(all))
	for _, h := range all {
		isOpen[h] = struct{}{}
	}
	loc := w.scope.loc()
	now := q.now()
	rawFloor := spanrollup.Wall(now.AddDate(0, 0, -rawRetentionDays).In(loc))
	rawCeil := spanrollup.Wall(now.In(loc))
	for h := lo; h.Before(hi); h = h.Add(time.Hour) {
		if _, ok := isOpen[h]; !ok {
			done = append(done, h)
			continue
		}
		if !h.Before(rawFloor) && !h.After(rawCeil) {
			open = append(open, h)
		}
	}
	return done, open, nil
}

// utcRuns turns open local hours into merged UTC intervals.
func utcRuns(open []time.Time, loc *time.Location) [][2]time.Time {
	var iv [][2]time.Time
	for _, h := range open {
		iv = append(iv, spanrollup.LocalHourBounds(h, loc)...)
	}
	sort.Slice(iv, func(i, j int) bool { return iv[i][0].Before(iv[j][0]) })
	var out [][2]time.Time
	for _, x := range iv {
		if n := len(out); n > 0 && !x[0].After(out[n-1][1]) {
			if x[1].After(out[n-1][1]) {
				out[n-1][1] = x[1]
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

func (o loadOpts) rollupCond() string {
	switch o.kind {
	case kindSegment:
		return `h.is_segment`
	case kindChild:
		return `not h.is_segment and h.op not like 'app.start.%'`
	default:
		return `not h.is_segment and h.op like 'app.start.%'`
	}
}

// loadHist reads span statistics for the window, merging rolled-up and open
// hours into one map. The done/open split is taken once, so an hour is read
// from exactly one side even if the rollup job finishes it meanwhile.
func (q *Querier) loadHist(ctx context.Context, w window, o loadOpts) (map[histKey]*agg, error) {
	done, open, err := q.splitHours(ctx, w)
	if err != nil {
		return nil, err
	}
	out := map[histKey]*agg{}
	lo, hi := w.hourRange()
	if len(done) > 0 {
		if err := q.loadRollup(ctx, w, o, lo, hi, done, out); err != nil {
			return nil, err
		}
	}
	for _, run := range utcRuns(open, w.scope.loc()) {
		if err := q.loadRaw(ctx, w, o, run[0], run[1], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// dims returns the dimension conditions (column prefix p) and their args,
// numbering placeholders after the existing args. When rawNames is false the
// name and op filters are included.
func (o loadOpts) dims(w window, p string, args []any, withNameOp bool) (string, []any) {
	var sb strings.Builder
	add := func(format string, v any) {
		args = append(args, v)
		fmt.Fprintf(&sb, " and "+format, p, len(args))
	}
	if o.filterDims {
		if w.f.Release != "" {
			add("%srelease = $%d", w.f.Release)
		}
		if w.f.Environment != "" {
			add("%senvironment = $%d", w.f.Environment)
		}
		if w.f.Platform != "" {
			add("%splatform = $%d", w.f.Platform)
		}
	}
	if withNameOp && o.name != "" {
		add("%sname = $%d", o.name)
	}
	if withNameOp && o.op != "" {
		add("%sop = $%d", o.op)
	}
	if o.releases != nil {
		add("%srelease = any($%d)", o.releases)
	}
	return sb.String(), args
}

func get(m map[histKey]*agg, k histKey) *agg {
	a := m[k]
	if a == nil {
		a = newAgg()
		m[k] = a
	}
	return a
}

func (q *Querier) loadRollup(ctx context.Context, w window, o loadOpts, lo, hi time.Time, done []time.Time, out map[histKey]*agg) error {
	dayExpr, relExpr := `''`, `''`
	if o.byDay {
		dayExpr = `to_char(h.local_hour, 'YYYY-MM-DD')`
	}
	if o.byRelease {
		relExpr = `h.release`
	}
	args := []any{w.scope.SiteID, lo, hi, done}
	extra, args := o.dims(w, "h.", args, true)
	where := `h.site_id = $1 and h.local_hour >= $2 and h.local_hour < $3 and h.local_hour = any($4::timestamp[]) and ` + o.rollupCond() + extra
	const group = `1, 2, 3, 4, 5`

	rows, err := q.pool.Query(ctx, `select `+dayExpr+`, h.name, h.op, h.group_hash, `+relExpr+`, sum(h.count)::bigint, sum(h.errors)::bigint, sum(h.sum_us)::bigint
from span_hourly h where `+where+` group by `+group, args...)
	if err != nil {
		return fmt.Errorf("read span rollup: %w", err)
	}
	for rows.Next() {
		var k histKey
		var c, e, s int64
		if err := rows.Scan(&k.day, &k.name, &k.op, &k.group, &k.release, &c, &e, &s); err != nil {
			rows.Close()
			return err
		}
		a := get(out, k)
		a.count += c
		a.errors += e
		a.sumUS += s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = q.pool.Query(ctx, `select `+dayExpr+`, h.name, h.op, h.group_hash, `+relExpr+`, u.b, sum(u.c)::bigint
from span_hourly h cross join lateral unnest(h.buckets, h.bucket_counts) as u(b, c)
where `+where+` group by `+group+`, u.b`, args...)
	if err != nil {
		return fmt.Errorf("read span rollup histogram: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k histKey
		var b int16
		var c int64
		if err := rows.Scan(&k.day, &k.name, &k.op, &k.group, &k.release, &b, &c); err != nil {
			return err
		}
		get(out, k).hist[b] += c
	}
	return rows.Err()
}

func (q *Querier) loadRaw(ctx context.Context, w window, o loadOpts, from, to time.Time, out map[histKey]*agg) error {
	loc := w.scope.loc()
	var cond, nameE, opE, groupE, bucketE, sumE string
	switch o.kind {
	case kindSegment:
		cond, nameE, opE, groupE, bucketE, sumE = `is_segment`, `name`, `op`, `0::bigint`, `bucket`, `duration_us`
	case kindChild:
		cond, nameE, opE, groupE, bucketE, sumE = `not is_segment`, `''::text`, `op`, `group_hash`, `bucket`, `duration_us`
	default:
		cond, nameE, opE, groupE, bucketE, sumE = `is_segment and app_start_type is not null and app_start_bucket is not null`,
			`name`, `'app.start.' || app_start_type`, `0::bigint`, `app_start_bucket`, `app_start_ms::bigint * 1000`
	}
	errE := `count(*) filter (where ` + errorPredicate("") + `)`
	if o.kind == kindAppStart {
		errE = `0::bigint`
	}
	dayExpr, relExpr := `null::timestamptz`, `''::text`
	if o.byDay {
		// Every UTC offset is a multiple of 15 minutes, so a 15-minute UTC bin
		// always falls inside one local day.
		dayExpr = `date_bin('15 minutes', start_ts, timestamptz '2000-01-01')`
	}
	if o.byRelease {
		relExpr = `release`
	}
	args := []any{w.scope.SiteID, from, to}
	extra, args := o.dims(w, "", args, false)
	// Name and op filters apply to the stored columns (app start: segment name).
	if o.name != "" {
		args = append(args, o.name)
		extra += fmt.Sprintf(" and name = $%d", len(args))
	}
	if o.op != "" {
		args = append(args, o.op)
		extra += fmt.Sprintf(" and op = $%d", len(args))
	}
	rows, err := q.pool.Query(ctx, `select `+dayExpr+`, `+nameE+`, `+opE+`, `+groupE+`, `+relExpr+`, `+bucketE+`, count(*), `+errE+`, sum(`+sumE+`)::bigint
from spans where site_id = $1 and start_ts >= $2 and start_ts < $3 and `+cond+extra+`
group by 1, 2, 3, 4, 5, 6`, args...)
	if err != nil {
		return fmt.Errorf("read raw spans: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k histKey
		var day *time.Time
		var b int16
		var c, e, s int64
		if err := rows.Scan(&day, &k.name, &k.op, &k.group, &k.release, &b, &c, &e, &s); err != nil {
			return err
		}
		if day != nil {
			k.day = day.In(loc).Format(time.DateOnly)
		}
		a := get(out, k)
		a.count += c
		a.errors += e
		a.sumUS += s
		a.hist[b] += c
	}
	return rows.Err()
}

func ms(us float64) float64 { return us / 1000 }

// pcts returns percentile estimates in milliseconds (0 for an empty histogram,
// so the result always encodes as JSON).
func pcts(h map[int16]int64, ps ...float64) []float64 {
	v := spans.Percentiles(h, ps...)
	for i := range v {
		if math.IsNaN(v[i]) {
			v[i] = 0
		} else {
			v[i] = ms(v[i])
		}
	}
	return v
}

func ptr(v float64) *float64 { return &v }

// TransactionRow is one transaction of the list. Percentiles are in
// milliseconds and are histogram estimates within 4.9 % of the exact value.
type TransactionRow struct {
	Name       string   `json:"name"`
	Op         string   `json:"op"`
	Count      int64    `json:"count"`
	Errors     int64    `json:"errors"`
	Throughput float64  `json:"throughput_per_min"`
	ErrorRate  *float64 `json:"error_rate"`
	P50        float64  `json:"p50_ms"`
	P75        float64  `json:"p75_ms"`
	P90        float64  `json:"p90_ms"`
	P95        float64  `json:"p95_ms"`
	P99        float64  `json:"p99_ms"`
}

// minutes is the length of the window that has already happened, for the
// throughput denominator (at least one minute).
func (q *Querier) minutes(w window) float64 {
	end := w.end
	if now := q.now(); now.Before(end) {
		end = now
	}
	return math.Max(end.Sub(w.start).Minutes(), 1)
}

func rowFrom(name, op string, a *agg, minutes float64) TransactionRow {
	p := pcts(a.hist, 0.5, 0.75, 0.9, 0.95, 0.99)
	r := TransactionRow{Name: name, Op: op, Count: a.count, Errors: a.errors, Throughput: float64(a.count) / minutes,
		P50: p[0], P75: p[1], P90: p[2], P95: p[3], P99: p[4]}
	if a.count > 0 {
		r.ErrorRate = ptr(float64(a.errors) / float64(a.count))
	}
	return r
}

// mergeNameOp folds histograms of one (name, op) pair across days, releases
// and groups.
func mergeNameOp(m map[histKey]*agg) map[[2]string]*agg {
	out := map[[2]string]*agg{}
	for k, a := range m {
		kk := [2]string{k.name, k.op}
		if out[kk] == nil {
			out[kk] = newAgg()
		}
		out[kk].add(a)
	}
	return out
}

func sortRows(rows []TransactionRow, sortBy string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		var x, y float64
		switch sortBy {
		case "count":
			x, y = float64(a.Count), float64(b.Count)
		case "errors":
			x, y = float64(a.Errors), float64(b.Errors)
		case "p50":
			x, y = a.P50, b.P50
		default:
			x, y = a.P95, b.P95
		}
		if x != y {
			return x > y
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Name < b.Name
	})
}

func validSort(s string) (string, error) {
	switch s {
	case "":
		return "p95", nil
	case "p95", "count", "errors", "p50":
		return s, nil
	}
	return "", invalid("sort must be p95, count, errors or p50")
}

// Transactions lists the transactions of the range with throughput, error
// rate and p50-p99 durations, sorted by sortBy (p95, count, errors or p50),
// largest first.
func (q *Querier) Transactions(ctx context.Context, scope Scope, f Filter, sortBy string, limit int) ([]TransactionRow, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return nil, err
	}
	if sortBy, err = validSort(sortBy); err != nil {
		return nil, err
	}
	if limit < 1 || limit > MaxTransactions {
		limit = MaxTransactions
	}
	m, err := q.loadHist(ctx, w, loadOpts{kind: kindSegment, filterDims: true})
	if err != nil {
		return nil, err
	}
	minutes := q.minutes(w)
	rows := []TransactionRow{}
	for k, a := range mergeNameOp(m) {
		if a.count > 0 {
			rows = append(rows, rowFrom(k[0], k[1], a, minutes))
		}
	}
	sortRows(rows, sortBy)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// SpanGroupRow is a group of child spans (same op and parameterised
// description). The group hash is internal and never serialised.
type SpanGroupRow struct {
	Op          string   `json:"op"`
	Description string   `json:"description"`
	Count       int64    `json:"count"`
	Errors      int64    `json:"errors"`
	ErrorRate   *float64 `json:"error_rate"`
	TotalMS     float64  `json:"total_ms"`
	AvgMS       float64  `json:"avg_ms"`
	P95         float64  `json:"p95_ms"`
}

// SlowSample is one slow transaction occurrence, linking to its trace.
type SlowSample struct {
	TraceID    string    `json:"trace_id"`
	Start      time.Time `json:"start"`
	DurationMS float64   `json:"duration_ms"`
	Release    string    `json:"release"`
}

// DayPoint is one local day of a transaction's series; the percentiles are
// absent on days without samples.
type DayPoint struct {
	Date   string   `json:"date"`
	Count  int64    `json:"count"`
	Errors int64    `json:"errors"`
	P50    *float64 `json:"p50_ms"`
	P95    *float64 `json:"p95_ms"`
}

// AppStartStat is the p50/p95 of cold or warm app starts, in milliseconds.
type AppStartStat struct {
	Type  string  `json:"type"`
	Count int64   `json:"count"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
}

// TransactionDetail is one transaction's series, child span breakdown and
// slowest samples.
type TransactionDetail struct {
	Name     string         `json:"name"`
	Summary  TransactionRow `json:"summary"`
	Series   []DayPoint     `json:"series"`
	Spans    []SpanGroupRow `json:"span_groups"`
	Slowest  []SlowSample   `json:"slowest"`
	AppStart []AppStartStat `json:"app_start"`
}

func appStartStats(m map[histKey]*agg) []AppStartStat {
	byOp := map[string]*agg{}
	for k, a := range m {
		if byOp[k.op] == nil {
			byOp[k.op] = newAgg()
		}
		byOp[k.op].add(a)
	}
	out := []AppStartStat{}
	for _, t := range []string{"cold", "warm"} {
		if a := byOp["app.start."+t]; a != nil && a.count > 0 {
			p := pcts(a.hist, 0.5, 0.95)
			out = append(out, AppStartStat{Type: t, Count: a.count, P50: p[0], P95: p[1]})
		}
	}
	return out
}

// Transaction returns the detail of one transaction by name, or ErrNotFound
// when it has no samples in the range.
func (q *Querier) Transaction(ctx context.Context, scope Scope, f Filter, name string) (TransactionDetail, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return TransactionDetail{}, err
	}
	if name == "" || len(name) > 200 {
		return TransactionDetail{}, invalid("name is required and at most 200 bytes")
	}
	d := TransactionDetail{Name: name}
	byDay, err := q.loadHist(ctx, w, loadOpts{kind: kindSegment, name: name, byDay: true, filterDims: true})
	if err != nil {
		return d, err
	}
	perDay := map[string]*agg{}
	total := newAgg()
	op := ""
	for k, a := range byDay {
		if perDay[k.day] == nil {
			perDay[k.day] = newAgg()
		}
		perDay[k.day].add(a)
		total.add(a)
		if op == "" || k.op < op {
			op = k.op
		}
	}
	if total.count == 0 {
		return d, ErrNotFound
	}
	d.Summary = rowFrom(name, op, total, q.minutes(w))
	d.Series = []DayPoint{}
	for _, day := range w.days {
		pt := DayPoint{Date: day}
		if a := perDay[day]; a != nil && a.count > 0 {
			p := pcts(a.hist, 0.5, 0.95)
			pt.Count, pt.Errors, pt.P50, pt.P95 = a.count, a.errors, ptr(p[0]), ptr(p[1])
		}
		d.Series = append(d.Series, pt)
	}
	start, err := q.loadHist(ctx, w, loadOpts{kind: kindAppStart, name: name, filterDims: true})
	if err != nil {
		return d, err
	}
	d.AppStart = appStartStats(start)
	if d.Spans, err = q.transactionChildren(ctx, w, name); err != nil {
		return d, err
	}
	if d.Slowest, err = q.slowSegments(ctx, w, name); err != nil {
		return d, err
	}
	return d, nil
}

// rawWindow is the UTC range raw spans can still cover for the window.
func (q *Querier) rawWindow(w window) (time.Time, time.Time) {
	lo := w.start
	if floor := q.now().AddDate(0, 0, -rawRetentionDays); floor.After(lo) {
		lo = floor
	}
	return lo, w.end
}

// dimConds are the filter's dimension conditions, placeholders from $5.
func dimConds(w window, args []any) (string, []any) {
	var sb strings.Builder
	for _, d := range []struct{ col, v string }{{"release", w.f.Release}, {"environment", w.f.Environment}, {"platform", w.f.Platform}} {
		if d.v != "" {
			args = append(args, d.v)
			fmt.Fprintf(&sb, " and %s = $%d", d.col, len(args))
		}
	}
	return sb.String(), args
}

// transactionChildren breaks the transaction down by child span group over its
// most recent segments (raw spans, joined through the trace index), top 20 by
// total time.
func (q *Querier) transactionChildren(ctx context.Context, w window, name string) ([]SpanGroupRow, error) {
	lo, hi := q.rawWindow(w)
	out := []SpanGroupRow{}
	if !lo.Before(hi) {
		return out, nil
	}
	dims, args := dimConds(w, []any{w.scope.SiteID, name, lo, hi})
	rows, err := q.pool.Query(ctx, `
with seg as (
    select trace_id, span_id from spans
    where site_id = $1 and is_segment and name = $2 and start_ts >= $3 and start_ts < $4`+dims+`
    order by start_ts desc limit `+fmt.Sprint(detailSegmentSample)+`)
select c.op, c.group_hash, max(c.description), c.bucket, count(*), count(*) filter (where `+errorPredicate("c.")+`), sum(c.duration_us)::bigint
from seg join spans c on c.site_id = $1 and c.trace_id = seg.trace_id and c.segment_id = seg.span_id
where not c.is_segment and c.start_ts >= $3 and c.start_ts < $4 + interval '1 hour'
group by c.op, c.group_hash, c.bucket`, args...)
	if err != nil {
		return nil, fmt.Errorf("read transaction spans: %w", err)
	}
	defer rows.Close()
	type gk struct {
		op string
		h  int64
	}
	type gv struct {
		desc string
		a    *agg
	}
	groups := map[gk]*gv{}
	for rows.Next() {
		var k gk
		var desc string
		var b int16
		var c, e, s int64
		if err := rows.Scan(&k.op, &k.h, &desc, &b, &c, &e, &s); err != nil {
			return nil, err
		}
		g := groups[k]
		if g == nil {
			g = &gv{desc: desc, a: newAgg()}
			groups[k] = g
		}
		g.a.count += c
		g.a.errors += e
		g.a.sumUS += s
		g.a.hist[b] += c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for k, g := range groups {
		out = append(out, groupRow(k.op, g.desc, g.a))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMS != out[j].TotalMS {
			return out[i].TotalMS > out[j].TotalMS
		}
		return out[i].Description < out[j].Description
	})
	if len(out) > 20 {
		out = out[:20]
	}
	return out, nil
}

func groupRow(op, desc string, a *agg) SpanGroupRow {
	r := SpanGroupRow{Op: op, Description: desc, Count: a.count, Errors: a.errors,
		TotalMS: ms(float64(a.sumUS)), P95: pcts(a.hist, 0.95)[0]}
	if a.count > 0 {
		r.ErrorRate = ptr(float64(a.errors) / float64(a.count))
		r.AvgMS = r.TotalMS / float64(a.count)
	}
	return r
}

func (q *Querier) slowSegments(ctx context.Context, w window, name string) ([]SlowSample, error) {
	lo, hi := q.rawWindow(w)
	out := []SlowSample{}
	if !lo.Before(hi) {
		return out, nil
	}
	dims, args := dimConds(w, []any{w.scope.SiteID, name, lo, hi})
	rows, err := q.pool.Query(ctx, `select trace_id, start_ts, duration_us, release from spans
where site_id = $1 and is_segment and name = $2 and start_ts >= $3 and start_ts < $4`+dims+`
order by duration_us desc limit 20`, args...)
	if err != nil {
		return nil, fmt.Errorf("read slow segments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s SlowSample
		var tid []byte
		var dur int64
		if err := rows.Scan(&tid, &s.Start, &dur, &s.Release); err != nil {
			return nil, err
		}
		s.TraceID = fmt.Sprintf("%x", tid)
		s.DurationMS = ms(float64(dur))
		out = append(out, s)
	}
	return out, rows.Err()
}

// RegressionRow is a transaction whose p95 grew between two releases.
type RegressionRow struct {
	Name        string  `json:"name"`
	Op          string  `json:"op"`
	BaseP95     float64 `json:"base_p95_ms"`
	TargetP95   float64 `json:"target_p95_ms"`
	BaseCount   int64   `json:"base_count"`
	TargetCount int64   `json:"target_count"`
	Delta       float64 `json:"delta"`
}

// Regressions compares p95 per transaction between two releases and lists the
// ones with at least 30 samples on each side and a p95 up by 20 % or more,
// worst first. The Filter's release is ignored; environment and platform apply.
func (q *Querier) Regressions(ctx context.Context, scope Scope, f Filter, base, target string) ([]RegressionRow, error) {
	if base == "" || target == "" || base == target || len(base) > 64 || len(target) > 64 {
		return nil, invalid("base and target must be two different releases")
	}
	w, err := q.window(scope, f)
	if err != nil {
		return nil, err
	}
	w.f.Release = ""
	m, err := q.loadHist(ctx, w, loadOpts{kind: kindSegment, byRelease: true, filterDims: true, releases: []string{base, target}})
	if err != nil {
		return nil, err
	}
	type pair struct{ base, target *agg }
	by := map[[2]string]*pair{}
	for k, a := range m {
		kk := [2]string{k.name, k.op}
		if by[kk] == nil {
			by[kk] = &pair{newAgg(), newAgg()}
		}
		if k.release == base {
			by[kk].base.add(a)
		} else {
			by[kk].target.add(a)
		}
	}
	out := []RegressionRow{}
	for k, p := range by {
		if p.base.count < regressionMinSamples || p.target.count < regressionMinSamples {
			continue
		}
		b, t := pcts(p.base.hist, 0.95)[0], pcts(p.target.hist, 0.95)[0]
		if b <= 0 {
			continue
		}
		if delta := t/b - 1; delta >= regressionMinDelta {
			out = append(out, RegressionRow{Name: k[0], Op: k[1], BaseP95: b, TargetP95: t, BaseCount: p.base.count, TargetCount: p.target.count, Delta: delta})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Delta != out[j].Delta {
			return out[i].Delta > out[j].Delta
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// NetworkFailures lists the http.client span groups with errors (a 4xx or 5xx
// answer, or a failed status), worst first.
func (q *Querier) NetworkFailures(ctx context.Context, scope Scope, f Filter) ([]SpanGroupRow, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return nil, err
	}
	m, err := q.loadHist(ctx, w, loadOpts{kind: kindChild, op: "http.client", filterDims: true})
	if err != nil {
		return nil, err
	}
	merged := map[int64]*agg{}
	for k, a := range m {
		if merged[k.group] == nil {
			merged[k.group] = newAgg()
		}
		merged[k.group].add(a)
	}
	var hashes []int64
	for h, a := range merged {
		if a.errors > 0 {
			hashes = append(hashes, h)
		}
	}
	out := []SpanGroupRow{}
	if len(hashes) == 0 {
		return out, nil
	}
	desc := map[int64]string{}
	rows, err := q.pool.Query(ctx, `select group_hash, description from span_groups where site_id = $1 and group_hash = any($2)`, scope.SiteID, hashes)
	if err != nil {
		return nil, fmt.Errorf("read span groups: %w", err)
	}
	for rows.Next() {
		var h int64
		var d string
		if err := rows.Scan(&h, &d); err != nil {
			rows.Close()
			return nil, err
		}
		desc[h] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, h := range hashes {
		out = append(out, groupRow("http.client", desc[h], merged[h]))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Errors != out[j].Errors {
			return out[i].Errors > out[j].Errors
		}
		return out[i].Description < out[j].Description
	})
	if len(out) > maxNetworkRows {
		out = out[:maxNetworkRows]
	}
	return out, nil
}

// VitalsLink points to the web site linked to the app project; the p75 values
// are present when the querier has a vitals source.
type VitalsLink struct {
	SiteID int32    `json:"site_id"`
	LCPp75 *float64 `json:"lcp_p75,omitempty"`
	INPp75 *float64 `json:"inp_p75,omitempty"`
	CLSp75 *float64 `json:"cls_p75,omitempty"`
}

// VitalsFunc reads the 75th percentile Web Vitals of a web site for a range.
type VitalsFunc func(ctx context.Context, siteID int32, f Filter) (lcp, inp, cls *float64, err error)

// WithVitals returns a Querier that fills VitalsLink with real values.
func (q *Querier) WithVitals(fn VitalsFunc) *Querier {
	c := *q
	c.vitals = fn
	return &c
}

// PerformanceOverview is the performance landing view.
type PerformanceOverview struct {
	Transactions int64            `json:"transactions"`
	Errors       int64            `json:"errors"`
	ErrorRate    *float64         `json:"error_rate"`
	P50          float64          `json:"p50_ms"`
	P95          float64          `json:"p95_ms"`
	Slowest      []TransactionRow `json:"slowest"`
	MostFailing  []TransactionRow `json:"most_failing"`
	AppStart     []AppStartStat   `json:"app_start"`
	VitalsLink   *VitalsLink      `json:"vitals_link,omitempty"`
}

// PerformanceOverview totals the range, lists the five slowest transactions
// by p95 and the five with the highest error rate, the app start percentiles,
// and the linked web site's Web Vitals when there is one.
func (q *Querier) PerformanceOverview(ctx context.Context, scope Scope, f Filter) (PerformanceOverview, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return PerformanceOverview{}, err
	}
	o := PerformanceOverview{Slowest: []TransactionRow{}, MostFailing: []TransactionRow{}, AppStart: []AppStartStat{}}
	m, err := q.loadHist(ctx, w, loadOpts{kind: kindSegment, filterDims: true})
	if err != nil {
		return o, err
	}
	total := newAgg()
	minutes := q.minutes(w)
	var rows []TransactionRow
	for k, a := range mergeNameOp(m) {
		total.add(a)
		if a.count > 0 {
			rows = append(rows, rowFrom(k[0], k[1], a, minutes))
		}
	}
	o.Transactions, o.Errors = total.count, total.errors
	if total.count > 0 {
		o.ErrorRate = ptr(float64(total.errors) / float64(total.count))
		p := pcts(total.hist, 0.5, 0.95)
		o.P50, o.P95 = p[0], p[1]
	}
	sortRows(rows, "p95")
	o.Slowest = append(o.Slowest, rows[:min(5, len(rows))]...)
	var failing []TransactionRow
	for _, r := range rows {
		if r.Errors > 0 {
			failing = append(failing, r)
		}
	}
	sort.SliceStable(failing, func(i, j int) bool {
		a, b := *failing[i].ErrorRate, *failing[j].ErrorRate
		if a != b {
			return a > b
		}
		return failing[i].Count > failing[j].Count
	})
	o.MostFailing = append(o.MostFailing, failing[:min(5, len(failing))]...)

	start, err := q.loadHist(ctx, w, loadOpts{kind: kindAppStart, filterDims: true})
	if err != nil {
		return o, err
	}
	o.AppStart = appStartStats(start)

	var web int32
	err = q.pool.QueryRow(ctx, `select id from sites where kind = 'web' and deleted_at is null and org_id = $1 and settings->>'app_link' = $2::text order by id limit 1`,
		scope.OrgID, fmt.Sprint(scope.SiteID)).Scan(&web)
	switch {
	case err == nil:
		link := &VitalsLink{SiteID: web}
		if q.vitals != nil {
			if lcp, inp, cls, verr := q.vitals(ctx, web, f); verr == nil {
				link.LCPp75, link.INPp75, link.CLSp75 = lcp, inp, cls
			}
		}
		o.VitalsLink = link
	case isNoRows(err):
	default:
		return o, fmt.Errorf("read app link: %w", err)
	}
	return o, nil
}
