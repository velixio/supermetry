package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"supermetry/bench/gen/internal/recount"
	"supermetry/bench/gen/internal/synth"
)

type output struct {
	events, truth, sites []byte
	stats                synth.Stats
}

func generate(t *testing.T, cfg synth.Config) output {
	t.Helper()
	g, err := synth.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var ev, tr, si bytes.Buffer
	if err := g.WriteSites(&si); err != nil {
		t.Fatal(err)
	}
	st, err := g.Run(&ev, &tr)
	if err != nil {
		t.Fatal(err)
	}
	return output{ev.Bytes(), tr.Bytes(), si.Bytes(), st}
}

func tinyConfig(workers int) synth.Config {
	return synth.Config{Tier: "tiny", Days: 3, Start: synth.DefaultStart(3), Seed: 42, Workers: workers}
}

var tinyCache *output

func tiny(t *testing.T) output {
	if tinyCache == nil {
		o := generate(t, tinyConfig(4))
		tinyCache = &o
	}
	return *tinyCache
}

// checkAgainstRecount recounts the stream independently (visitors from ip+ua,
// sessions from a 30-minute timeout) and requires exact equality with the
// generator's truth.json, plus zero stream-invariant violations.
func checkAgainstRecount(t *testing.T, o output, wantRows int) {
	t.Helper()
	sites, err := recount.LoadSites(bytes.NewReader(o.sites))
	if err != nil {
		t.Fatal(err)
	}
	res, err := recount.Run(bytes.NewReader(o.events), sites, recount.Options{CheckKeyOrderEvery: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) > 0 {
		t.Fatalf("%d stream violations, first: %s", len(res.Violations), strings.Join(res.Violations[:min(5, len(res.Violations))], "\n"))
	}
	if res.Events != o.stats.Events {
		t.Errorf("recount saw %d events, generator reports %d", res.Events, o.stats.Events)
	}
	want, err := recount.LoadTruth(bytes.NewReader(o.truth))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != wantRows {
		t.Errorf("truth has %d rows, want %d", len(want), wantRows)
	}
	if d := recount.Compare(res.Rows, want, 20); len(d) > 0 {
		t.Fatalf("truth differs from recount:\n%s", strings.Join(d, "\n"))
	}
}

// TestTruthEqualsIndependentRecount is the main correctness test.
func TestTruthEqualsIndependentRecount(t *testing.T) {
	o := tiny(t)
	if o.stats.Events < 25_000 || o.stats.Events > 35_000 {
		t.Errorf("tiny has %d events, want about 30K", o.stats.Events)
	}
	checkAgainstRecount(t, o, 100*3)
}

func TestTinyFixtureCoversDashboardDimensions(t *testing.T) {
	o := tiny(t)
	type fixtureEvent struct {
		Country     string            `json:"country"`
		Region      string            `json:"region"`
		City        string            `json:"city"`
		Ref         string            `json:"ref"`
		UTMSource   string            `json:"utm_source"`
		UTMMedium   string            `json:"utm_medium"`
		UTMCampaign string            `json:"utm_campaign"`
		Name        string            `json:"name"`
		Props       map[string]string `json:"props"`
		Revenue     *struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"revenue"`
	}

	countries := make(map[string]struct{})
	var regions, cities, referrers, campaigns, customProperties, revenue int
	scanner := bufio.NewScanner(bytes.NewReader(o.events))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var event fixtureEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		countries[event.Country] = struct{}{}
		if event.Region != "" {
			regions++
		}
		if event.City != "" {
			cities++
		}
		if event.Ref != "" {
			referrers++
		}
		if event.UTMSource != "" && event.UTMMedium != "" && event.UTMCampaign != "" {
			campaigns++
		}
		if event.Name != "pageview" && len(event.Props) > 0 {
			customProperties++
		}
		if event.Revenue != nil && event.Revenue.Amount != "" && event.Revenue.Currency == "USD" {
			revenue++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(countries) < 40 {
		t.Errorf("tiny fixture has %d countries, want at least 40", len(countries))
	}
	for name, count := range map[string]int{
		"regions": regions, "cities": cities, "referrers": referrers, "campaigns": campaigns,
		"custom events with properties": customProperties, "revenue events": revenue,
	} {
		if count == 0 {
			t.Errorf("tiny fixture has no %s", name)
		}
	}
}

// TestTruthAcrossDSTChanges covers 23- and 25-hour local days: EU and US
// fall-back (2025-10-26, 2025-11-02) and spring-forward (2026-03-08, 03-29),
// plus Sydney/Auckland (2026-04-05).
func TestTruthAcrossDSTChanges(t *testing.T) {
	for _, r := range []struct {
		start string
		days  int
	}{{"2025-10-25", 9}, {"2026-03-07", 2}, {"2026-03-28", 2}, {"2026-04-04", 2}} {
		start, _ := time.Parse("2006-01-02", r.start)
		o := generate(t, synth.Config{Tier: "tiny", Start: start, Days: r.days, Seed: 42, Workers: 4})
		checkAgainstRecount(t, o, 100*r.days)
	}
}

func TestDeterministicAcrossWorkerCounts(t *testing.T) {
	a := tiny(t)
	b := generate(t, tinyConfig(1))
	c := generate(t, tinyConfig(11))
	for name, o := range map[string]output{"workers=1": b, "workers=11": c} {
		if !bytes.Equal(a.events, o.events) {
			t.Errorf("%s: events differ", name)
		}
		if !bytes.Equal(a.truth, o.truth) {
			t.Errorf("%s: truth differs", name)
		}
		if !bytes.Equal(a.sites, o.sites) {
			t.Errorf("%s: sites differ", name)
		}
	}
	d := tinyConfig(4)
	d.Seed = 43
	if bytes.Equal(a.events, generate(t, d).events) {
		t.Error("seed 43 produced the same events as seed 42")
	}
}

func TestZstdIsDeterministicAndRoundTrips(t *testing.T) {
	plain := tiny(t)
	cfg := tinyConfig(3)
	cfg.Zstd = true
	z1 := generate(t, cfg)
	cfg.Workers = 8
	z2 := generate(t, cfg)
	if !bytes.Equal(z1.events, z2.events) {
		t.Fatal("zstd output differs across worker counts")
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := dec.DecodeAll(z1.events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, plain.events) {
		t.Fatal("decompressed zstd output differs from plain output")
	}
	if len(z1.events)*4 > len(raw) {
		t.Errorf("zstd ratio only %.1fx", float64(len(raw))/float64(len(z1.events)))
	}
}

// TestLaterStartIsSuffix: a day's data depends only on (seed, tier, site,
// date), so a run starting later reproduces the same site-local days. This is
// what makes tier M the last 14 days of M-history.
func TestLaterStartIsSuffix(t *testing.T) {
	full := tiny(t)
	cfg := tinyConfig(4)
	cfg.Start = cfg.Start.AddDate(0, 0, 1)
	cfg.Days = 2
	part := generate(t, cfg)
	sites, _ := recount.LoadSites(bytes.NewReader(full.sites))
	locs := map[int]*time.Location{}
	for _, s := range sites {
		locs[s.ID], _ = time.LoadLocation(s.Timezone)
	}
	first := cfg.Start.Format("2006-01-02")
	var kept bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(full.events))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e struct {
			TS   string `json:"ts"`
			Site int    `json:"site"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		ts, _ := time.Parse(time.RFC3339Nano, e.TS)
		if ts.In(locs[e.Site]).Format("2006-01-02") >= first {
			kept.Write(sc.Bytes())
			kept.WriteByte('\n')
		}
	}
	if !bytes.Equal(kept.Bytes(), part.events) {
		t.Fatalf("later-start run is not a suffix: %d vs %d bytes", kept.Len(), len(part.events))
	}
}

// TestFixturesUpToDate guards the committed fixtures in testdata/.
// Regenerate with: go run . --tier tiny --seed 42 --out testdata/tiny.ndjson --sites testdata/tiny.sites.json --truth testdata/tiny.truth.json
func TestFixturesUpToDate(t *testing.T) {
	o := tiny(t)
	for name, want := range map[string][]byte{"tiny.ndjson": o.events, "tiny.truth.json": o.truth, "tiny.sites.json": o.sites} {
		got, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("testdata/%s is stale; regenerate it (see comment)", name)
		}
	}
}

// TestDistributionsTierS checks the realism targets on two days of tier S.
func TestDistributionsTierS(t *testing.T) {
	if testing.Short() {
		t.Skip("tier S sample in -short mode")
	}
	cfg := synth.Config{Tier: "S", Days: 2, Start: synth.DefaultStart(2), Seed: 42, Workers: 0}
	o := generate(t, cfg)
	rows, err := recount.LoadTruth(bytes.NewReader(o.truth))
	if err != nil {
		t.Fatal(err)
	}
	var visits, pv, bounces, siteVisitors [101]int64
	var funnel [3]int64
	var plans = map[string]int64{}
	for _, r := range rows {
		visits[0] += r.Visits
		pv[0] += r.Pageviews
		bounces[0] += r.Bounces
		siteVisitors[r.Site] += r.Visitors
		siteVisitors[0] += r.Visitors
		for i := range funnel {
			funnel[i] += r.Funnel[i]
		}
		for k, v := range r.Plan {
			plans[k] += v
		}
	}
	type stream struct {
		Site      int    `json:"site"`
		Ref       string `json:"ref"`
		UtmSource string `json:"utm_source"`
		Name      string `json:"name"`
		SK        string `json:"sk"`
		Host      string `json:"host"`
		Device    string `json:"device"`
		ScreenW   int    `json:"screen_w"`
		Country   string `json:"country"`
		TS        string `json:"ts"`
		IP        string `json:"ip"`
		UA        string `json:"ua"`
	}
	sites, _ := recount.LoadSites(bytes.NewReader(o.sites))
	locs := map[int]*time.Location{}
	for _, s := range sites {
		locs[s.ID], _ = time.LoadLocation(s.Timezone)
	}
	firstDay := cfg.Start.Format("2006-01-02")
	day0 := map[string]bool{} // site|ip|ua seen on the first local day
	day1 := map[string]bool{} // ... and on the second
	var events, customs, sessions, direct, google, utm int64
	siteEvents := map[int]int64{}
	devices := map[string]int64{}
	countries := map[string]int64{}
	seenSK := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(o.events))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e stream
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		events++
		siteEvents[e.Site]++
		ts, _ := time.Parse(time.RFC3339Nano, e.TS)
		vkey := e.Host + "|" + e.IP + "|" + e.UA
		if ts.In(locs[e.Site]).Format("2006-01-02") == firstDay {
			day0[vkey] = true
		} else {
			day1[vkey] = true
		}
		if e.Name != "pageview" {
			customs++
		}
		if seenSK[e.SK] {
			continue
		}
		seenSK[e.SK] = true // first event of a session: session-level shares
		sessions++
		devices[e.Device]++
		countries[e.Country]++
		switch {
		case e.Ref == "":
			direct++
		case strings.Contains(e.Ref, "google."):
			google++
		}
		if e.UtmSource != "" {
			utm++
		}
		switch e.Device {
		case "mobile":
			if e.ScreenW >= 576 {
				t.Fatalf("mobile with screen_w %d", e.ScreenW)
			}
		case "tablet":
			if e.ScreenW < 576 || e.ScreenW >= 992 {
				t.Fatalf("tablet with screen_w %d", e.ScreenW)
			}
		case "desktop":
			if e.ScreenW < 1024 {
				t.Fatalf("desktop with screen_w %d", e.ScreenW)
			}
		}
	}
	frac := func(a, b int64) float64 { return float64(a) / float64(b) }
	within := func(name string, got, lo, hi float64) {
		t.Helper()
		t.Logf("%-24s %.4f", name, got)
		if got < lo || got > hi {
			t.Errorf("%s = %.4f, want in [%.3f, %.3f]", name, got, lo, hi)
		}
	}
	var returned int64
	for k := range day0 {
		if day1[k] {
			returned++
		}
	}
	// 2026-09-29 and -30 are a Tuesday and a Wednesday: weekly factor 1.10.
	// (The full-week volume is checked by TestEventsPerVisitorCalibration.)
	within("events/day / 1.1M", float64(events)/2/1.1e6, 0.97, 1.03)
	within("next-day return rate", frac(returned, int64(len(day0))), 0.27, 0.33)
	within("bounce rate", frac(bounces[0], visits[0]), 0.52, 0.58)
	within("pageviews/session", frac(pv[0], visits[0]), 2.35, 2.65)
	within("custom event share", frac(customs, events), 0.085, 0.115)
	within("direct share", frac(direct, sessions), 0.37, 0.43)
	within("google share", frac(google, sessions), 0.27, 0.33)
	within("utm share", frac(utm, sessions), 0.08, 0.12)
	within("biggest site share", frac(siteEvents[1], events), 0.27, 0.33)
	within("mobile share", frac(devices["mobile"], sessions), 0.48, 0.62)
	within("US share", frac(countries["US"], sessions), 0.15, 0.30)
	within("funnel step2/step1", frac(funnel[1], funnel[0]), 0.10, 0.50)
	within("funnel step3/step2", frac(funnel[2], funnel[1]), 0.10, 0.50)
	within("plan free share", frac(plans["free"], plans["free"]+plans["pro"]+plans["team"]), 0.5, 0.7)
	if visits[0] != sessions {
		t.Errorf("truth visits %d != distinct sk %d", visits[0], sessions)
	}
}
