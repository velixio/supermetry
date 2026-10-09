// Package verify compares prototype query results with the generator's
// truth.json (scorecard C1: exact counts, duration within 1%).
package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"supermetry/bench/proto/internal/core"
	"supermetry/bench/proto/internal/query"
)

// TopEntry is one breakdown entry of truth.json.
type TopEntry struct {
	Value     string `json:"value"`
	Visitors  int64  `json:"visitors"`
	Pageviews int64  `json:"pageviews"`
}

// Row is one (site, local date) of truth.json.
type Row struct {
	Site         int32                 `json:"site"`
	Date         string                `json:"date"`
	Visitors     int64                 `json:"visitors"`
	Visits       int64                 `json:"visits"`
	Pageviews    int64                 `json:"pageviews"`
	Bounces      int64                 `json:"bounces"`
	DurationSumS json.Number           `json:"duration_sum_s"`
	Top          map[string][]TopEntry `json:"top"`
	FunnelF      []int64               `json:"funnel_f"`
	SignupPlan   map[string]int64      `json:"signup_plan"`
}

// LoadTruth parses truth.json ({"rows": [...]}).
func LoadTruth(r io.Reader) ([]Row, error) {
	var f struct {
		Rows []Row `json:"rows"`
	}
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("truth.json: %w", err)
	}
	return f.Rows, nil
}

// DirectLabel is the truth's name for "no referrer"; Supermetry stores "".
const DirectLabel = "(direct)"

// Options control which tiers are checked.
type Options struct {
	Raw        bool // also check raw-tier overview, funnel, props and filtered queries
	RawEvery   int  // check the raw tier on every n-th row (default 1)
	MaxReports int  // mismatches to keep in the report (default 50)
	Workers    int  // truth rows checked concurrently (default 1)
}

// Report is the verify outcome.
type Report struct {
	Rows           int      `json:"rows"`
	Checks         int      `json:"checks"`
	Mismatches     int      `json:"mismatches"`
	MaxDurationErr float64  `json:"max_duration_rel_err"`
	Pass           bool     `json:"pass"`
	Examples       []string `json:"examples,omitempty"`
	RangeChecks    int      `json:"range_checks"`
	FilteredChecks int      `json:"filtered_checks"`
	RawRowsChecked int      `json:"raw_rows_checked"`
}

type checker struct {
	mu  sync.Mutex
	r   *Report
	max int
}

func (c *checker) fail(format string, a ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failLocked(format, a...)
}

func (c *checker) failLocked(format string, a ...any) {
	c.r.Mismatches++
	if len(c.r.Examples) < c.max {
		c.r.Examples = append(c.r.Examples, fmt.Sprintf(format, a...))
	}
}

func (c *checker) eq(what string, got, want int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.r.Checks++
	if got != want {
		c.failLocked("%s: got %d want %d", what, got, want)
	}
}

func (c *checker) count(f func(r *Report)) {
	c.mu.Lock()
	f(c.r)
	c.mu.Unlock()
}

func (c *checker) dur(what string, gotMs, wantMs int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.r.Checks++
	rel := 0.0
	if wantMs != 0 {
		rel = math.Abs(float64(gotMs-wantMs)) / float64(wantMs)
	} else if gotMs != 0 {
		rel = 1
	}
	c.r.MaxDurationErr = max(c.r.MaxDurationErr, rel)
	if rel > 0.01 {
		c.failLocked("%s: duration got %dms want %dms (%.3f%%)", what, gotMs, wantMs, rel*100)
	}
}

func durMs(n json.Number) (int64, error) {
	f, err := strconv.ParseFloat(string(n), 64)
	return int64(math.Round(f * 1000)), err
}

func label(dim, v string) string {
	if dim == "source" && v == "" {
		return DirectLabel
	}
	return v
}

func unlabel(dim, v string) string {
	if dim == "source" && v == DirectLabel {
		return ""
	}
	return v
}

// Run checks every truth row: rollup totals and top-50 lists exactly,
// funnel and signup.plan from raw, and (Options.Raw) raw unfiltered totals
// plus filtered overviews (each of the top 3 country/source/path values)
// against the truth breakdown entry. It also checks each site's whole range.
func Run(ctx context.Context, e *query.Engine, rows []Row, o Options) (*Report, error) {
	if o.RawEvery <= 0 {
		o.RawEvery = 1
	}
	if o.MaxReports <= 0 {
		o.MaxReports = 50
	}
	rep := &Report{}
	c := &checker{r: rep, max: o.MaxReports}
	type span struct {
		from, to core.Day
		m        core.Metrics
	}
	spans := map[int32]*span{}
	for _, tr := range rows {
		d, err := core.ParseDay(tr.Date)
		if err != nil {
			return nil, err
		}
		wantDur, err := durMs(tr.DurationSumS)
		if err != nil {
			return nil, err
		}
		rep.Rows++
		sp := spans[tr.Site]
		if sp == nil {
			sp = &span{from: d, to: d}
			spans[tr.Site] = sp
		}
		sp.from, sp.to = min(sp.from, d), max(sp.to, d)
		sp.m.Add(core.Metrics{Visitors: tr.Visitors, Visits: tr.Visits, Pageviews: tr.Pageviews, Bounces: tr.Bounces, DurationMs: wantDur})
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(o.Workers, 1))
	for i, tr := range rows {
		g.Go(func() error { return checkRow(gctx, e, c, o, i, tr) })
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	for site, sp := range spans {
		ov, err := e.Overview(ctx, query.Params{Site: site, From: sp.from, To: sp.to, Mode: query.ModeRollup})
		if err != nil {
			return nil, err
		}
		rep.RangeChecks++
		id := fmt.Sprintf("site %d %s..%s", site, sp.from, sp.to)
		c.eq(id+" range visitors", ov.Visitors, sp.m.Visitors)
		c.eq(id+" range visits", ov.Visits, sp.m.Visits)
		c.eq(id+" range pageviews", ov.Pageviews, sp.m.Pageviews)
		c.eq(id+" range bounces", ov.Bounces, sp.m.Bounces)
		c.dur(id+" range", ov.DurationMs, sp.m.DurationMs)
	}
	rep.Pass = rep.Mismatches == 0
	return rep, nil
}

// checkRow checks one truth row.
func checkRow(ctx context.Context, e *query.Engine, c *checker, o Options, i int, tr Row) error {
	{
		d, err := core.ParseDay(tr.Date)
		if err != nil {
			return err
		}
		wantDur, err := durMs(tr.DurationSumS)
		if err != nil {
			return err
		}

		id := fmt.Sprintf("site %d %s", tr.Site, tr.Date)
		p := query.Params{Site: tr.Site, From: d, To: d, Mode: query.ModeRollup}
		checkTotals := func(tier string, p query.Params) error {
			ov, err := e.Overview(ctx, p)
			if err != nil {
				return err
			}
			c.eq(id+" "+tier+" visitors", ov.Visitors, tr.Visitors)
			c.eq(id+" "+tier+" visits", ov.Visits, tr.Visits)
			c.eq(id+" "+tier+" pageviews", ov.Pageviews, tr.Pageviews)
			c.eq(id+" "+tier+" bounces", ov.Bounces, tr.Bounces)
			c.dur(id+" "+tier, ov.DurationMs, wantDur)
			return nil
		}
		if err := checkTotals("rollup", p); err != nil {
			return err
		}
		for dim, want := range tr.Top {
			dd, err := core.ParseDim(dim)
			if err != nil {
				return err
			}
			p := p
			p.Limit = len(want)
			got, err := e.Breakdown(ctx, dd, p)
			if err != nil {
				return err
			}
			c.count(func(r *Report) { r.Checks++ })
			if len(got) != len(want) {
				c.fail("%s top %s: %d rows, want %d", id, dim, len(got), len(want))
				continue
			}
			for j := range want {
				g := got[j]
				if label(dim, g.Value) != want[j].Value || g.Visitors != want[j].Visitors || g.Pageviews != want[j].Pageviews {
					c.fail("%s top %s #%d: got %q v=%d pv=%d want %q v=%d pv=%d", id, dim, j,
						label(dim, g.Value), g.Visitors, g.Pageviews, want[j].Value, want[j].Visitors, want[j].Pageviews)
					break
				}
			}
		}
		if !o.Raw || i%o.RawEvery != 0 {
			return nil
		}
		c.count(func(r *Report) { r.RawRowsChecked++ })
		rp := p
		rp.Mode = query.ModeRaw
		if err := checkTotals("raw", rp); err != nil {
			return err
		}
		fn, err := e.Funnel(ctx, rp)
		if err != nil {
			return err
		}
		for j, s := range fn {
			if j < len(tr.FunnelF) {
				c.eq(fmt.Sprintf("%s funnel step %d", id, j+1), s.Sessions, tr.FunnelF[j])
			}
		}
		rp.PropName, rp.PropKey, rp.Limit = "signup", "plan", -1
		props, err := e.Props(ctx, rp)
		if err != nil {
			return err
		}
		gotPlan := map[string]int64{}
		for _, r := range props {
			gotPlan[r.Value] = r.Events
		}
		for k, v := range tr.SignupPlan {
			c.eq(id+" signup.plan="+k, gotPlan[k], v)
		}
		for k, v := range gotPlan {
			if _, ok := tr.SignupPlan[k]; !ok {
				c.eq(id+" signup.plan="+k, v, 0)
			}
		}
		// Filtered (raw) overviews must equal the truth's breakdown entries.
		for _, dim := range []string{"country", "source", "path", "entry"} {
			for j, te := range tr.Top[dim] {
				if j >= 3 {
					break
				}
				fp := rp
				fp.PropName, fp.PropKey, fp.Limit = "", "", 0
				fp.Filters = []core.Filter{{Key: dim, Value: unlabel(dim, te.Value)}}
				ov, err := e.Overview(ctx, fp)
				if err != nil {
					return err
				}
				c.count(func(r *Report) { r.FilteredChecks++ })
				c.eq(fmt.Sprintf("%s filter %s=%q visitors", id, dim, te.Value), ov.Visitors, te.Visitors)
				c.eq(fmt.Sprintf("%s filter %s=%q pageviews", id, dim, te.Value), ov.Pageviews, te.Pageviews)
			}
		}
	}
	return nil
}

// Summary is a one-line result.
func (r *Report) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "verify: %d rows, %d checks (%d filtered, %d range), %d mismatches, max duration err %.4f%% => ",
		r.Rows, r.Checks, r.FilteredChecks, r.RangeChecks, r.Mismatches, r.MaxDurationErr*100)
	if r.Pass {
		b.WriteString("PASS")
	} else {
		b.WriteString("FAIL")
	}
	return b.String()
}
