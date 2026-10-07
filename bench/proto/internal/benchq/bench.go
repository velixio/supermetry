// Package benchq holds the prototype's measurement commands: the query
// benchmark (scorecard S2/S3/S4/F4), a COPY throughput micro-benchmark and an
// HTTP ingest load tester.
package benchq

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"panel/bench/proto/internal/core"
	"panel/bench/proto/internal/ingest"
	"panel/bench/proto/internal/query"
	"panel/bench/proto/internal/store"
)

// Dist is a latency distribution in milliseconds.
type Dist struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms,omitempty"`
	Max float64 `json:"max_ms"`
}

// DistOf summarises durations.
func DistOf(ds []time.Duration) Dist {
	if len(ds) == 0 {
		return Dist{}
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	q := func(p float64) float64 {
		i := int(float64(len(s))*p+0.999999) - 1
		return ms(s[max(0, min(i, len(s)-1))])
	}
	return Dist{N: len(s), P50: q(0.50), P95: q(0.95), P99: q(0.99), Max: ms(s[len(s)-1])}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Case is one benchmark case.
type Case struct {
	Name    string          `json:"name"`
	Line    string          `json:"scorecard_line"`
	From    core.Day        `json:"from"`
	To      core.Day        `json:"to"`
	Filters []core.Filter   `json:"filters,omitempty"`
	Wall    Dist            `json:"wall"`
	Panels  map[string]Dist `json:"panels,omitempty"`
	Target  string          `json:"target"`
	Mode    string          `json:"mode,omitempty"`
	// ApproxError (sampling mode only): relative error against the exact
	// dashboard, overview and the top 10 rows of every breakdown.
	ApproxError map[string]float64 `json:"approx_error,omitempty"`
}

// runDashboard times one Dashboard call (all panels, shared scans) per run.
func runDashboard(ctx context.Context, e *query.Engine, site int32, c *Case, warm, runs int) error {
	var walls []time.Duration
	for i := range warm + runs {
		start := time.Now()
		if _, err := e.Dashboard(ctx, query.Params{Site: site, From: c.From, To: c.To, Filters: c.Filters, Mode: c.Mode}); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		if i >= warm {
			walls = append(walls, time.Since(start))
		}
	}
	c.Wall = DistOf(walls)
	fmt.Fprintf(os.Stderr, "%-55s p50 %8.1f ms  p95 %8.1f ms  max %8.1f ms\n", c.Name, c.Wall.P50, c.Wall.P95, c.Wall.Max)
	if e.Sample > 1 {
		ex := &query.Engine{Pool: e.Pool, Site: e.Site, Fanout: e.Fanout}
		p := query.Params{Site: site, From: c.From, To: c.To, Filters: c.Filters, Mode: c.Mode}
		want, err := ex.Dashboard(ctx, p)
		if err != nil {
			return err
		}
		got, err := e.Dashboard(ctx, p)
		if err != nil {
			return err
		}
		c.ApproxError = approxError(want, got)
		fmt.Fprintf(os.Stderr, "    approx error vs exact: %v\n", c.ApproxError)
	}
	return nil
}

func relErr(want, got int64) float64 {
	if want == 0 {
		if got == 0 {
			return 0
		}
		return 1
	}
	return math.Abs(float64(got-want)) / float64(want)
}

// approxError summarises the sampled dashboard's error: overview visitors
// and pageviews, and over the exact top 10 of every breakdown the median and
// max relative visitor error plus the share of the exact top 10 found in
// the sampled top 10.
func approxError(want, got *query.Dashboard) map[string]float64 {
	out := map[string]float64{
		"overview_visitors":  relErr(want.Overview.Visitors, got.Overview.Visitors),
		"overview_pageviews": relErr(want.Overview.Pageviews, got.Overview.Pageviews),
	}
	var all []float64
	var worst float64
	var overlap, total int
	for d, rows := range want.Breakdowns {
		g := map[string]int64{}
		top := map[string]bool{}
		for i, r := range got.Breakdowns[d] {
			g[r.Value] = r.Visitors
			if i < 10 {
				top[r.Value] = true
			}
		}
		for i, r := range rows {
			if i >= 10 {
				break
			}
			e := relErr(r.Visitors, g[r.Value])
			all = append(all, e)
			worst = max(worst, e)
			total++
			if top[r.Value] {
				overlap++
			}
		}
	}
	slices.Sort(all)
	if len(all) > 0 {
		out["top10_visitors_median"] = all[len(all)/2]
		out["top10_visitors_max"] = worst
		out["top10_overlap"] = float64(overlap) / float64(total)
	}
	return out
}

// panels of the dashboard (all fetched concurrently, like a browser).
var panels = []string{"overview", "timeseries", "pages", "sources", "countries", "browsers", "os", "devices", "entry", "exit"}

// CmdBench runs the query benchmark.
func CmdBench(ctx context.Context, args []string, open func(context.Context, int32) (*store.Store, error)) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	siteF := fs.Int("site", 0, "site (0 = biggest by pageviews)")
	toF := fs.String("to", "", "last day (default: last rolled-up day)")
	s2days := fs.String("s2-days", "30,90", "S2 ranges in days")
	s3days := fs.Int("s3-days", 30, "S3 range in days (raw tier)")
	s4days := fs.Int("s4-days", 30, "S4/F4 range in days")
	runs := fs.Int("runs", 30, "measured runs per case")
	warm := fs.Int("warmup", 3, "warm-up runs per case")
	out := fs.String("out", "../results", "results directory")
	label := fs.String("label", "", "label for the results file")
	kind := fs.String("kind", "proto-queries", "results file prefix")
	tier := fs.String("tier", "", "dataset tier label (S, M, ...)")
	mode := fs.String("mode", "auto", "query mode for S3/S4: auto (closed tier) | hot (hot tables, as before)")
	fanout := fs.Int("fanout", 8, "closed tier: day chunks run concurrently")
	noGrouping := fs.Bool("no-grouping", false, "closed tier: one statement per panel instead of GROUPING SETS")
	noMonthly := fs.Bool("no-monthly", false, "rollups: pure dyadic blocks (monthly+dyadic is default)")
	sample := fs.Int("sample", 0, "APPROXIMATE opt-in mode: 1/N visitor-day sample on sessions_closed (0 = exact); reports the error vs exact")
	dash := fs.Bool("dashboard", true, "S2/S3: one Dashboard call (shared scans) instead of 10 concurrent panel calls")
	only := fs.String("cases", "s2,s3,s4,f4", "cases to run")
	fs.Parse(args)
	startLoad, _ := os.ReadFile("/proc/loadavg")
	want := map[string]bool{}
	for _, c := range strings.Split(*only, ",") {
		want[strings.TrimSpace(c)] = true
	}
	st, err := open(ctx, 24)
	if err != nil {
		return err
	}
	defer st.Close()
	list, err := st.LoadSites(ctx)
	if err != nil {
		return err
	}
	sites := ingest.NewSites(list)
	e := &query.Engine{Pool: st.Pool, Site: sites.ByID, Fanout: *fanout, NoGrouping: *noGrouping, NoMonthly: *noMonthly, Sample: *sample}
	site := int32(*siteF)
	if site == 0 {
		if err := st.Pool.QueryRow(ctx, `SELECT site_id FROM rollup_day GROUP BY 1 ORDER BY sum(pageviews) DESC LIMIT 1`).Scan(&site); err != nil {
			return fmt.Errorf("no rollups to pick the biggest site: %w", err)
		}
	}
	var to core.Day
	if *toF != "" {
		if to, err = core.ParseDay(*toF); err != nil {
			return err
		}
	} else {
		var t time.Time
		if err := st.Pool.QueryRow(ctx, `SELECT max(day) FROM rollup_day WHERE site_id = $1`, site).Scan(&t); err != nil {
			return err
		}
		to = core.Day(t.Unix() / 86400)
	}
	// Filter values: the site's top country, its top source, top page.
	top := func(name string, days int, f []core.Filter) string {
		rows, err := e.Breakdown(ctx, mustDim(name), query.Params{Site: site, From: to - core.Day(days) + 1, To: to, Limit: 1, Filters: f})
		if err != nil || len(rows) == 0 {
			return ""
		}
		return rows[0].Value
	}
	country := top("countries", *s3days, nil)
	source := top("sources", *s3days, []core.Filter{{Key: "country", Value: country}})
	page := top("pages", *s3days, nil)

	var cases []*Case
	for _, ds := range strings.Split(*s2days, ",") {
		n, _ := strconv.Atoi(ds)
		if n > 0 && want["s2"] {
			cases = append(cases, &Case{Name: fmt.Sprintf("S2 dashboard, rollups, %dd", n), Line: "S2", From: to - core.Day(n) + 1, To: to, Target: "p95 < 300 ms"})
		}
	}
	s3from := to - core.Day(*s3days) + 1
	s3 := [][]core.Filter{
		{{Key: "country", Value: country}},
		{{Key: "country", Value: country}, {Key: "source", Value: source}},
		{{Key: "country", Value: country}, {Key: "path", Value: page}},
	}
	if !want["s3"] {
		s3 = nil
	}
	for _, f := range s3 {
		var keys []string
		for _, x := range f {
			keys = append(keys, x.Key)
		}
		cases = append(cases, &Case{Name: fmt.Sprintf("S3 dashboard, raw, %dd, filter %s", *s3days, strings.Join(keys, "+")), Line: "S3", From: s3from, To: to, Filters: f, Target: "p95 < 1 s (S, 90d) / < 3 s (M, 14d)", Mode: *mode})
	}
	s4from := to - core.Day(*s4days) + 1
	funnel := &Case{Name: fmt.Sprintf("S4 funnel F, raw, %dd", *s4days), Line: "S4", From: s4from, To: to, Target: "p95 < 2 s", Mode: *mode}
	props := &Case{Name: fmt.Sprintf("F4 signup.plan breakdown, raw, %dd, filtered", *s4days), Line: "F4", From: s4from, To: to,
		Filters: []core.Filter{{Key: "country", Value: country}}, Target: "< 1 s (S, 30d)"}
	propsU := &Case{Name: fmt.Sprintf("F4 signup.plan breakdown, raw, %dd, unfiltered", *s4days), Line: "F4", From: s4from, To: to, Target: "< 1 s (S, 30d)"}

	runCase := func(c *Case, names []string) error {
		if *dash && len(names) > 1 {
			return runDashboard(ctx, e, site, c, *warm, *runs)
		}
		c.Panels = map[string]Dist{}
		per := map[string][]time.Duration{}
		var walls []time.Duration
		for i := range *warm + *runs {
			var mu sync.Mutex
			var wg sync.WaitGroup
			errs := make(chan error, len(names))
			start := time.Now()
			for _, n := range names {
				wg.Go(func() {
					p := query.Params{Site: site, From: c.From, To: c.To, Filters: c.Filters, Mode: c.Mode}
					if n == "props" {
						p.PropName, p.PropKey = "signup", "plan"
					}
					t := time.Now()
					if _, err := e.Run(ctx, n, p); err != nil {
						errs <- fmt.Errorf("%s %s: %w", c.Name, n, err)
						return
					}
					mu.Lock()
					if i >= *warm {
						per[n] = append(per[n], time.Since(t))
					}
					mu.Unlock()
				})
			}
			wg.Wait()
			close(errs)
			if err := <-errs; err != nil {
				return err
			}
			if i >= *warm {
				walls = append(walls, time.Since(start))
			}
		}
		c.Wall = DistOf(walls)
		if len(names) > 1 {
			for n, d := range per {
				c.Panels[n] = DistOf(d)
			}
		}
		fmt.Fprintf(os.Stderr, "%-55s p50 %8.1f ms  p95 %8.1f ms  max %8.1f ms\n", c.Name, c.Wall.P50, c.Wall.P95, c.Wall.Max)
		return nil
	}
	for _, c := range cases {
		if err := runCase(c, panels); err != nil {
			return err
		}
	}
	var single []*Case
	if want["s4"] {
		single = append(single, funnel)
	}
	if want["f4"] {
		single = append(single, props, propsU)
	}
	for _, c := range single {
		n := "funnel"
		if c.Line == "F4" {
			n = "props"
		}
		if err := runCase(c, []string{n}); err != nil {
			return err
		}
		cases = append(cases, c)
	}

	meta, err := rigState(ctx, st, site, to)
	if err != nil {
		return err
	}
	res := map[string]any{"kind": "panel-proto query bench", "tier": *tier, "site": site, "to": to,
		"filter_values": map[string]string{"country": country, "source": source, "path": page},
		"runs":          *runs, "warmup": *warm, "rig": meta, "cases": cases, "loadavg_at_start": strings.TrimSpace(string(startLoad)),
		"engine": map[string]any{"mode": *mode, "fanout": *fanout, "grouping_sets": !*noGrouping, "dashboard_call": *dash,
			"monthly_rollups": !*noMonthly, "rollup_strategy": map[bool]string{true: "dyadic", false: "monthly+dyadic"}[*noMonthly]}}
	return writeResult(*out, *kind, *label, res)
}

func mustDim(n string) core.Dim { d, _ := core.ParseDim(n); return d }

// rigState records PG settings and data volume next to the numbers.
func rigState(ctx context.Context, st *store.Store, site int32, to core.Day) (map[string]any, error) {
	m := map[string]any{"measured_at": time.Now().UTC().Format(time.RFC3339), "go": runtime.Version(),
		"cpus": runtime.NumCPU(), "commit": "n/a (not a git repo)"}
	settings := map[string]string{}
	for _, s := range []string{"server_version", "shared_buffers", "work_mem", "effective_cache_size", "jit",
		"max_parallel_workers_per_gather", "hash_mem_multiplier", "random_page_cost"} {
		var v string
		if err := st.Pool.QueryRow(ctx, "SELECT current_setting($1)", s).Scan(&v); err != nil {
			return nil, err
		}
		settings[s] = v
	}
	m["pg"] = settings
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		m["loadavg_at_end"] = strings.TrimSpace(string(b)) // the box is shared: record how busy it was
	}
	var events, sessions, siteEvents, blockRows, blockDoneRows int64
	var evBytes, sessBytes, dailyRollBytes, monthRollBytes, blockRollBytes int64
	q := `SELECT (SELECT count(*) FROM events), (SELECT count(*) FROM sessions), (SELECT count(*) FROM events WHERE site_id = $1),
	  coalesce((SELECT sum(pg_total_relation_size(inhrelid)) FROM pg_inherits WHERE inhparent = 'events'::regclass), 0),
	  coalesce((SELECT sum(pg_total_relation_size(inhrelid)) FROM pg_inherits WHERE inhparent = 'sessions'::regclass), 0),
	  pg_total_relation_size('rollup_day') + pg_total_relation_size('rollup_day_dim'),
	  pg_total_relation_size('rollup_month_dim') + pg_total_relation_size('rollup_month_done'),
	  pg_total_relation_size('rollup_block_dim') + pg_total_relation_size('rollup_block_done'),
	  (SELECT count(*) FROM rollup_block_dim), (SELECT count(*) FROM rollup_block_done)`
	if err := st.Pool.QueryRow(ctx, q, site).Scan(&events, &sessions, &siteEvents, &evBytes, &sessBytes,
		&dailyRollBytes, &monthRollBytes, &blockRollBytes, &blockRows, &blockDoneRows); err != nil {
		return nil, err
	}
	rollBytes := dailyRollBytes + monthRollBytes + blockRollBytes
	m["data"] = map[string]any{"events": events, "sessions": sessions, "site_events": siteEvents,
		"events_bytes": evBytes, "sessions_bytes": sessBytes, "rollup_bytes": rollBytes,
		"daily_rollup_bytes": dailyRollBytes, "monthly_rollup_bytes": monthRollBytes,
		"block_rollup_rows": blockRows, "block_done_rows": blockDoneRows, "block_rollup_bytes": blockRollBytes,
		"bytes_per_event_total": float64(evBytes+sessBytes+rollBytes) / float64(max(events, 1))}
	return m, nil
}

func writeResult(dir, kind, label string, v any) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := kind + "-" + time.Now().Format("20060102-150405")
	if label != "" {
		name += "-" + label
	}
	path := filepath.Join(dir, name+".json")
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "wrote", path)
	return nil
}
