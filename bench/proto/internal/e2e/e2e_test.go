// Package e2e tests the full prototype against the reference evaluator:
// NDJSON → pipeline → PG → rollups / raw queries.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"

	"supermetry/bench/proto/internal/core"
	"supermetry/bench/proto/internal/ingest"
	"supermetry/bench/proto/internal/query"
	"supermetry/bench/proto/internal/ref"
	"supermetry/bench/proto/internal/store"
	"supermetry/bench/proto/internal/testdb"
	"supermetry/bench/proto/internal/verify"
)

const (
	fixture      = "../../testdata/fixture.ndjson"
	fixtureSites = "../../testdata/fixture_sites.json"
)

type env struct {
	st    *store.Store
	sites []core.Site
	eng   *query.Engine // closed tier fanned out over 2 chunks, GROUPING SETS
	eng1  *query.Engine // closed tier, one statement per panel, no fan-out
	ref   *ref.Ref
	days  []core.Day
}

func setup(t *testing.T, ndjson, sitesFile string, rollup func(*env)) *env {
	t.Helper()
	ctx := context.Background()
	st := testdb.Open(t)
	sites := testdb.Sites(t, st, sitesFile)
	reg := ingest.NewSites(sites)
	f, err := os.Open(ndjson)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := ingest.Load(ctx, st, reg, f, ingest.LoadOptions{ParseWorkers: 3}); err != nil {
		t.Fatal(err)
	}
	recs, err := ref.ReadNDJSON(ndjson)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, sites: sites, ref: ref.Build(recs, sites),
		eng:  &query.Engine{Pool: st.Pool, Site: reg.ByID, Fanout: 2},
		eng1: &query.Engine{Pool: st.Pool, Site: reg.ByID, NoGrouping: true}}
	seen := map[core.Day]bool{}
	for _, sd := range e.ref.Days() {
		if d := core.Day(sd[1]); !seen[d] {
			seen[d] = true
			e.days = append(e.days, d)
		}
	}
	if rollup == nil {
		rollup = func(e *env) {
			ids := make([]int32, len(sites))
			for i, s := range sites {
				ids[i] = s.ID
			}
			for _, d := range e.days {
				if err := st.RecomputeDay(ctx, d, ids); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	rollup(e)
	return e
}

func toMap(rows []core.DimRow, rawPath bool) map[string]core.Metrics {
	m := map[string]core.Metrics{}
	for _, r := range rows {
		if rawPath {
			r.Bounces, r.DurationMs = 0, 0
		}
		m[r.Value] = r.Metrics
	}
	return m
}

func refMap(m map[string]core.Metrics, d core.Dim, rawPath bool) map[string]core.Metrics {
	out := map[string]core.Metrics{}
	for k, v := range m {
		if rawPath {
			v.Bounces, v.DurationMs = 0, 0
		}
		if (d == core.DimEntry || d == core.DimExit) && k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func (e *env) check(t *testing.T, q ref.Query, mode string) {
	t.Helper()
	e.checkWith(t, e.eng, q, mode)
	if mode == query.ModeRaw {
		e.checkWith(t, e.eng1, q, mode)
		e.checkDashboard(t, q)
	}
}

// checkDashboard compares the combined dashboard with the oracle.
func (e *env) checkDashboard(t *testing.T, q ref.Query) {
	t.Helper()
	p := query.Params{Site: q.Site, From: q.From, To: q.To, Filters: q.Filters, Mode: query.ModeRaw, Limit: -1}
	label := fmt.Sprintf("dashboard site=%d %s..%s %v", q.Site, q.From, q.To, q.Filters)
	db, err := e.eng.Dashboard(context.Background(), p)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if want := e.ref.Overview(q); db.Overview.Metrics != want {
		t.Errorf("%s overview = %+v, want %+v", label, db.Overview.Metrics, want)
	}
	wantTS := e.ref.Timeseries(q)
	for _, pt := range db.Timeseries {
		if pt.Metrics != wantTS[pt.Day] {
			t.Errorf("%s timeseries %s = %+v, want %+v", label, pt.Day, pt.Metrics, wantTS[pt.Day])
		}
	}
	for _, d := range core.AllDims {
		got, want := toMap(db.Breakdowns[d.String()], false), refMap(e.ref.Breakdown(q, d), d, false)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s breakdown %s\n got  %+v\n want %+v", label, d, got, want)
		}
	}
}

func (e *env) checkWith(t *testing.T, eng *query.Engine, q ref.Query, mode string) {
	t.Helper()
	ctx := context.Background()
	p := query.Params{Site: q.Site, From: q.From, To: q.To, Filters: q.Filters, Mode: mode, Limit: -1}
	label := fmt.Sprintf("%s site=%d %s..%s %v", mode, q.Site, q.From, q.To, q.Filters)
	ov, err := eng.Overview(ctx, p)
	if err != nil {
		t.Fatalf("%s overview: %v", label, err)
	}
	if want := e.ref.Overview(q); ov.Metrics != want {
		t.Errorf("%s overview = %+v, want %+v", label, ov.Metrics, want)
	}
	ts, err := eng.Timeseries(ctx, p)
	if err != nil {
		t.Fatalf("%s timeseries: %v", label, err)
	}
	wantTS := e.ref.Timeseries(q)
	for _, pt := range ts {
		if pt.Metrics != wantTS[pt.Day] {
			t.Errorf("%s timeseries %s = %+v, want %+v", label, pt.Day, pt.Metrics, wantTS[pt.Day])
		}
	}
	for _, d := range core.AllDims {
		rows, err := eng.Breakdown(ctx, d, p)
		if err != nil {
			t.Fatalf("%s %s: %v", label, d, err)
		}
		rawPath := mode == query.ModeHot && d == core.DimPath // hot pages carry no bounces/duration
		got, want := toMap(rows, rawPath), refMap(e.ref.Breakdown(q, d), d, rawPath)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s breakdown %s\n got  %+v\n want %+v", label, d, got, want)
		}
	}
	if mode == query.ModeRaw || mode == query.ModeHot {
		fn, err := eng.Funnel(ctx, p)
		if err != nil {
			t.Fatalf("%s funnel: %v", label, err)
		}
		var got []int64
		for _, s := range fn {
			got = append(got, s.Sessions)
		}
		if mode == query.ModeHot && (hasPath(q.Filters) || hasEntryExit(q.Filters)) {
			// the hot funnel restricts events (not sessions) to a page filter: not part of the contract
		} else if want := e.ref.Funnel(q); !reflect.DeepEqual(got, want) {
			t.Errorf("%s funnel = %v, want %v", label, got, want)
		}
		p.PropName, p.PropKey = "signup", "plan"
		props, err := eng.Props(ctx, p)
		if err != nil {
			t.Fatalf("%s props: %v", label, err)
		}
		gotP := map[string]ref.PropRow{}
		for _, r := range props {
			gotP[r.Value] = ref.PropRow{Events: r.Events, Visitors: r.Visitors}
		}
		if want := e.ref.Props(q, "signup", "plan"); !reflect.DeepEqual(gotP, want) && !(len(gotP) == 0 && len(want) == 0) {
			t.Errorf("%s props = %+v, want %+v", label, gotP, want)
		}
	}
}

func hasPath(fs []core.Filter) bool {
	for _, f := range fs {
		if f.Key == "path" {
			return true
		}
	}
	return false
}

func hasEntryExit(fs []core.Filter) bool {
	for _, f := range fs {
		if f.Key == "entry" || f.Key == "exit" {
			return true
		}
	}
	return false
}

// filterCombos returns every single filter value seen, plus pairs across a
// session dimension and the page dimension (and two session dimensions).
func (e *env) filterCombos(site int32) [][]core.Filter {
	vals := map[string]map[string]bool{}
	add := func(k, v string) {
		if vals[k] == nil {
			vals[k] = map[string]bool{}
		}
		vals[k][v] = true
	}
	for _, s := range e.ref.Sessions {
		if s.Site != site {
			continue
		}
		for _, k := range []string{"source", "country", "browser", "os", "device", "entry", "exit", "utm_source"} {
			add(k, s.Dims[k])
		}
		for _, ev := range s.Events {
			if ev := ev; true {
				_ = ev
			}
		}
	}
	for p := range e.ref.Breakdown(ref.Query{Site: site, From: -1 << 30, To: 1 << 30}, core.DimPath) {
		add("path", p)
	}
	var out [][]core.Filter
	for k, vs := range vals {
		for v := range vs {
			out = append(out, []core.Filter{{Key: k, Value: v}})
		}
	}
	for c := range vals["country"] {
		for p := range vals["path"] {
			out = append(out, []core.Filter{{Key: "country", Value: c}, {Key: "path", Value: p}})
		}
		for s := range vals["source"] {
			out = append(out, []core.Filter{{Key: "country", Value: c}, {Key: "source", Value: s}})
		}
	}
	for d := range vals["device"] {
		for en := range vals["entry"] {
			out = append(out, []core.Filter{{Key: "device", Value: d}, {Key: "entry", Value: en}})
		}
	}
	// Two session-level dimensions (the closed tier's split de-duplication).
	for s := range vals["source"] {
		for p := range vals["path"] {
			out = append(out, []core.Filter{{Key: "source", Value: s}, {Key: "path", Value: p}})
		}
		for en := range vals["entry"] {
			out = append(out, []core.Filter{{Key: "source", Value: s}, {Key: "entry", Value: en}})
		}
	}
	for en := range vals["entry"] {
		for ex := range vals["exit"] {
			out = append(out, []core.Filter{{Key: "entry", Value: en}, {Key: "exit", Value: ex}})
		}
	}
	// Unknown values and two different paths match nothing.
	out = append(out, []core.Filter{{Key: "country", Value: "nowhere"}},
		[]core.Filter{{Key: "path", Value: "/"}, {Key: "path", Value: "/pricing"}})
	return out
}

func TestFixtureEndToEnd(t *testing.T) {
	e := setup(t, fixture, fixtureSites, nil)
	var completedDays int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM rollup_block_done WHERE level = 0`).Scan(&completedDays); err != nil {
		t.Fatal(err)
	}
	if want := len(e.days) * len(e.sites); completedDays != want {
		t.Fatalf("completed level-0 blocks = %d, want %d", completedDays, want)
	}
	e.verifyAll(t)
	// Idempotent rollups: recomputing changes nothing.
	ctx := context.Background()
	if err := e.st.RecomputeDay(ctx, e.days[0], []int32{1, 2}); err != nil {
		t.Fatal(err)
	}
	e.check(t, ref.Query{Site: 1, From: e.days[0], To: e.days[0]}, query.ModeRollup)
	// Monthly rollups: a range covering July reads the monthly rows.
	july := core.CivilDay(2026, 7, 1)
	if err := e.st.RecomputeMonth(ctx, july, []int32{1, 2}); err != nil {
		t.Fatal(err)
	}
	for _, s := range e.sites {
		e.check(t, ref.Query{Site: s.ID, From: july, To: core.CivilDay(2026, 7, 31)}, query.ModeRollup)
		e.check(t, ref.Query{Site: s.ID, From: july - 3, To: core.CivilDay(2026, 8, 2)}, query.ModeRollup)
		// Partial months read the monthly rows minus the excluded days.
		e.check(t, ref.Query{Site: s.ID, From: july + 1, To: core.CivilDay(2026, 7, 31)}, query.ModeRollup)
		e.check(t, ref.Query{Site: s.ID, From: july, To: core.CivilDay(2026, 7, 20)}, query.ModeRollup)
	}
	var n int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != len(e.ref.Sessions) {
		t.Fatalf("sessions rows = %d (%v), want %d", n, err, len(e.ref.Sessions))
	}
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions_closed`).Scan(&n); err != nil || n != len(e.ref.Sessions) {
		t.Fatalf("sessions_closed rows = %d (%v), want %d", n, err, len(e.ref.Sessions))
	}
	// Retention of the hot tier: once every day is closed, the hot sessions
	// partitions can be dropped and the raw tier still answers exactly.
	last := e.days[len(e.days)-1]
	dropped, err := e.st.DropCompactedSessions(ctx, last+1)
	if err != nil || len(dropped) == 0 {
		t.Fatalf("drop hot sessions: %v %v", dropped, err)
	}
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("hot sessions left after drop: %d (%v)", n, err)
	}
	for _, s := range e.sites {
		e.check(t, ref.Query{Site: s.ID, From: e.days[0], To: last}, query.ModeRaw)
		for _, fs := range e.filterCombos(s.ID) {
			e.check(t, ref.Query{Site: s.ID, From: e.days[0], To: last, Filters: fs}, query.ModeRaw)
		}
	}
}

func (e *env) verifyAll(t *testing.T) {
	first, last := e.days[0], e.days[len(e.days)-1]
	for _, s := range e.sites {
		for _, d := range e.days {
			e.check(t, ref.Query{Site: s.ID, From: d, To: d}, query.ModeRollup)
			e.check(t, ref.Query{Site: s.ID, From: d, To: d}, query.ModeRaw)
			e.check(t, ref.Query{Site: s.ID, From: d, To: d}, query.ModeHot)
		}
		e.check(t, ref.Query{Site: s.ID, From: first, To: last}, query.ModeRollup)
		e.check(t, ref.Query{Site: s.ID, From: first, To: last}, query.ModeRaw)
		e.check(t, ref.Query{Site: s.ID, From: first, To: last}, query.ModeHot)
		if testing.Short() {
			continue
		}
		for _, fs := range e.filterCombos(s.ID) {
			e.check(t, ref.Query{Site: s.ID, From: first, To: last, Filters: fs}, query.ModeRaw)
			e.check(t, ref.Query{Site: s.ID, From: first, To: last, Filters: fs}, query.ModeHot)
		}
	}
}

func streamRollup(t *testing.T, ndjson string) func(*env) {
	return func(e *env) {
		f, err := os.Open(ndjson)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, err = ingest.Stream(context.Background(), ingest.NewSites(e.sites), f, ingest.StreamOptions{ParseWorkers: 2},
			func(b []core.SaltKey, rows []core.RollupRow) error {
				return e.st.ReplaceRollups(context.Background(), b, rows)
			})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// rollup-stream builds the same rollups straight from NDJSON (no raw rows read).
func TestFixtureRollupStream(t *testing.T) {
	e := setup(t, fixture, fixtureSites, streamRollup(t, fixture))
	var completedDays int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM rollup_block_done WHERE level = 0`).Scan(&completedDays); err != nil {
		t.Fatal(err)
	}
	if completedDays == 0 {
		t.Fatal("rollup-stream did not maintain completed day blocks")
	}
	first, last := e.days[0], e.days[len(e.days)-1]
	for _, s := range e.sites {
		for _, d := range e.days {
			e.check(t, ref.Query{Site: s.ID, From: d, To: d}, query.ModeRollup)
		}
		e.check(t, ref.Query{Site: s.ID, From: first, To: last}, query.ModeRollup)
	}
}

// truthFromRef renders the oracle in the generator's truth.json format.
func truthFromRef(e *env) []verify.Row {
	var rows []verify.Row
	for _, sd := range e.ref.Days() {
		q := ref.Query{Site: sd[0], From: core.Day(sd[1]), To: core.Day(sd[1])}
		m := e.ref.Overview(q)
		fn := e.ref.Funnel(q)
		plan := map[string]int64{"free": 0, "pro": 0, "team": 0}
		for k, v := range e.ref.Props(q, "signup", "plan") {
			plan[k] = v.Events
		}
		row := verify.Row{Site: sd[0], Date: core.Day(sd[1]).String(), Visitors: m.Visitors, Visits: m.Visits,
			Pageviews: m.Pageviews, Bounces: m.Bounces, DurationSumS: json.Number(fmt.Sprintf("%.3f", float64(m.DurationMs)/1000)),
			Top: map[string][]verify.TopEntry{}, FunnelF: fn, SignupPlan: plan}
		for _, d := range core.AllDims {
			var top []core.DimRow
			for v, mm := range refMap(e.ref.Breakdown(q, d), d, false) {
				top = append(top, core.DimRow{Value: v, Metrics: mm})
			}
			sort.Slice(top, func(i, j int) bool {
				a, b := top[i], top[j]
				if a.Visitors != b.Visitors {
					return a.Visitors > b.Visitors
				}
				if a.Pageviews != b.Pageviews {
					return a.Pageviews > b.Pageviews
				}
				return a.Value < b.Value
			})
			for _, t := range top {
				v := t.Value
				if d == core.DimSource && v == "" {
					v = verify.DirectLabel
				}
				row.Top[d.String()] = append(row.Top[d.String()], verify.TopEntry{Value: v, Visitors: t.Visitors, Pageviews: t.Pageviews})
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func TestVerifyAgainstTruthFormat(t *testing.T) {
	e := setup(t, fixture, fixtureSites, nil)
	rows := truthFromRef(e)
	rep, err := verify.Run(context.Background(), e.eng, rows, verify.Options{Raw: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Pass || rep.FilteredChecks == 0 {
		t.Fatalf("%s\n%v", rep.Summary(), rep.Examples)
	}
	// A wrong truth must fail: one visitor off, and 2% duration off.
	rows[0].Visitors++
	rows[1].DurationSumS = json.Number(fmt.Sprintf("%.3f", float64(e.ref.Overview(ref.Query{Site: rows[1].Site, From: mustDay(rows[1].Date), To: mustDay(rows[1].Date)}).DurationMs)/1000*1.02))
	rep, err = verify.Run(context.Background(), e.eng, rows, verify.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pass || rep.Mismatches < 2 {
		t.Fatalf("expected failures: %s %v", rep.Summary(), rep.Examples)
	}
}

func mustDay(s string) core.Day { d, _ := core.ParseDay(s); return d }

// The generator's tiny fixture (bench/gen/testdata) must pass verify exactly.
func TestGenTinyVerify(t *testing.T) {
	dir := "../../../gen/testdata/"
	if d := os.Getenv("SUPERMETRY_TINY_DIR"); d != "" {
		dir = d + "/"
	}
	ndjson, truthFile, sitesFile := dir+"tiny.ndjson", dir+"tiny.truth.json", dir+"tiny.sites.json"
	for _, f := range []string{ndjson, truthFile, sitesFile} {
		if _, err := os.Stat(f); err != nil {
			t.Skipf("%s not generated yet", f)
		}
	}
	e := setup(t, ndjson, sitesFile, nil)
	f, err := os.Open(truthFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := verify.LoadTruth(f)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := verify.Run(context.Background(), e.eng, rows, verify.Options{Raw: true, RawEvery: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(rep.Summary())
	if !rep.Pass {
		t.Fatalf("%s\n%v", rep.Summary(), rep.Examples)
	}
	// rollup-stream must produce the same rollups.
	streamRollup(t, ndjson)(e)
	rep, err = verify.Run(context.Background(), e.eng, rows, verify.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Pass {
		t.Fatalf("rollup-stream: %s\n%v", rep.Summary(), rep.Examples)
	}
}
