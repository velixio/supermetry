// Command harness times the Supermetry prototype's query set (bench/proto/NOTES.md,
// proto-queries-*.json) through each incumbent's own HTTP API, and checks
// overview numbers against gen's truth.json (C1). Throwaway benchmark code.
//
//	harness bench -product umami|plausible|goatcounter [-runs 30 -warmup 3] -out FILE
//	harness c1    -product ... -truth truth.json -samples 1:2026-09-15,3:2026-09-10
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// Filter is one dashboard filter in the prototype's vocabulary.
type Filter struct{ Key, Value string }

// Req is one HTTP request a dashboard panel makes.
type Req struct {
	Method, URL, Host string
	Body              []byte
	Header            map[string]string
}

// Product builds requests for the prototype's panels.
type Product interface {
	Name() string
	// Supermetry returns the request for a panel over local dates [from, to], or
	// ok=false if the product lacks it. note explains substitutions.
	Supermetry(panel string, from, to string, f []Filter) (r Req, ok bool, note string)
	// Overview fetches the single-day overview for C1.
	Overview(ctx context.Context, site int, date string) (Overview, error)
	Info() map[string]any
}

// Overview is a product's overview in truth units.
type Overview struct {
	Visitors, Visits, Pageviews, Bounces, DurationSumS *float64
	Raw                                                json.RawMessage `json:"raw"`
}

var client = &http.Client{Timeout: 10 * time.Minute, Transport: &http.Transport{MaxIdleConnsPerHost: 64, MaxConnsPerHost: 0}}

func do(ctx context.Context, r Req) ([]byte, int, error) {
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return nil, 0, err
	}
	if r.Host != "" {
		req.Host = r.Host
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return b, resp.StatusCode, fmt.Errorf("%s %s: HTTP %d: %.300s", r.Method, r.URL, resp.StatusCode, b)
	}
	return b, resp.StatusCode, err
}

// Dist is a latency distribution in ms.
type Dist struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
	Max float64 `json:"max_ms"`
}

func distOf(d []time.Duration) Dist {
	if len(d) == 0 {
		return Dist{}
	}
	s := slices.Clone(d)
	slices.Sort(s)
	ms := func(x time.Duration) float64 { return float64(x.Microseconds()) / 1000 }
	q := func(p float64) float64 {
		i := int(p*float64(len(s))+0.999999) - 1
		return ms(s[max(0, min(len(s)-1, i))])
	}
	return Dist{N: len(s), P50: q(0.50), P95: q(0.95), P99: q(0.99), Max: ms(s[len(s)-1])}
}

// Case is one dashboard (or single-panel) case, like the prototype's.
type Case struct {
	Name     string            `json:"name"`
	Line     string            `json:"scorecard_line"`
	From     string            `json:"from"`
	To       string            `json:"to"`
	Filters  []Filter          `json:"filters,omitempty"`
	Runs     int               `json:"runs"`
	Warmup   int               `json:"warmup"`
	Wall     Dist              `json:"wall"`
	Panels   map[string]Dist   `json:"panels,omitempty"`
	Missing  []string          `json:"missing_panels,omitempty"`
	Notes    map[string]string `json:"notes,omitempty"`
	Samples  map[string]string `json:"sample_responses,omitempty"`
	NA       string            `json:"na,omitempty"`
	Error    string            `json:"error,omitempty"`
	RunsNote string            `json:"runs_note,omitempty"`
}

var dashboard = []string{"overview", "timeseries", "pages", "sources", "countries", "browsers", "os", "devices", "entry", "exit"}

func runCase(ctx context.Context, p Product, c *Case, panels []string, runs, warm int, budget time.Duration) {
	type pr struct {
		name string
		req  Req
	}
	var reqs []pr
	c.Notes = map[string]string{}
	c.Samples = map[string]string{}
	for _, n := range panels {
		r, ok, note := p.Supermetry(n, c.From, c.To, c.Filters)
		if note != "" {
			c.Notes[n] = note
		}
		if !ok {
			c.Missing = append(c.Missing, n)
			continue
		}
		reqs = append(reqs, pr{n, r})
	}
	if len(reqs) == 0 {
		c.NA = "not supported by " + p.Name()
		return
	}
	per := map[string][]time.Duration{}
	var walls []time.Duration
	c.Warmup, c.Runs = warm, runs
	for i := 0; i < c.Warmup+c.Runs; i++ {
		var mu sync.Mutex
		var wg sync.WaitGroup
		var firstErr error
		start := time.Now()
		for _, r := range reqs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t := time.Now()
				b, _, err := do(ctx, r.req)
				el := time.Since(t)
				mu.Lock()
				defer mu.Unlock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", r.name, err)
				}
				if i == 0 {
					c.Samples[r.name] = string(b[:min(len(b), 400)])
				}
				if i >= c.Warmup {
					per[r.name] = append(per[r.name], el)
				}
			}()
		}
		wg.Wait()
		wall := time.Since(start)
		if firstErr != nil {
			c.Error = firstErr.Error()
			log.Printf("%s: %v", c.Name, firstErr)
			return
		}
		if i >= c.Warmup {
			walls = append(walls, wall)
		}
		// Adaptive: keep a case within the time budget (documented in the results).
		if i == c.Warmup-1 && budget > 0 && wall*time.Duration(c.Runs) > budget {
			n := max(5, int(budget/wall))
			if n < c.Runs {
				c.RunsNote = fmt.Sprintf("reduced from %d to %d runs: warm-up wall %.1f s exceeds the %s case budget", c.Runs, n, wall.Seconds(), budget)
				c.Runs = n
			}
		}
	}
	c.Wall = distOf(walls)
	if len(reqs) > 1 {
		c.Panels = map[string]Dist{}
		for n, d := range per {
			c.Panels[n] = distOf(d)
		}
	}
	log.Printf("%-48s p50 %9.1f ms  p95 %9.1f ms  max %9.1f ms (n=%d)", c.Name, c.Wall.P50, c.Wall.P95, c.Wall.Max, c.Wall.N)
}

type sitesFile []struct {
	ID       int    `json:"id"`
	Host     string `json:"host"`
	Timezone string `json:"timezone"`
}

func loadSites(path string) map[int]string {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	var s sitesFile
	if err := json.Unmarshal(b, &s); err != nil {
		log.Fatal(err)
	}
	m := map[int]string{}
	for _, x := range s {
		m[x.ID] = x.Timezone
	}
	return m
}

func newProduct(name string, site int, tz map[int]string) Product {
	switch name {
	case "umami":
		return newUmami(site, tz)
	case "plausible":
		return newPlausible(site, tz)
	case "goatcounter":
		return newGoat(site, tz)
	}
	log.Fatalf("unknown product %q", name)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: harness bench|c1 ...")
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	product := fs.String("product", "", "umami|plausible|goatcounter")
	site := fs.Int("site", 1, "site number (1 = biggest)")
	from := fs.String("from", "2026-09-01", "first local day")
	to := fs.String("to", "2026-09-30", "last local day")
	runs := fs.Int("runs", 30, "measured runs per case")
	warm := fs.Int("warmup", 3, "warm-up runs per case")
	budget := fs.Duration("budget", 15*time.Minute, "per-case time budget (runs are reduced, min 5, if exceeded)")
	sitesF := fs.String("sites", "data/sites.json", "gen sites.json (time zones)")
	out := fs.String("out", "", "output JSON")
	truthF := fs.String("truth", "data/truth-S30.json", "gen truth.json")
	samples := fs.String("samples", "1:2026-09-15,3:2026-09-10,5:2026-09-20", "site:date list for C1")
	country := fs.String("country", "US", "S3 country filter value (proto: the site's top country)")
	source := fs.String("source", "", "S3 source filter value (proto: (direct) = \"\")")
	page := fs.String("page", "/", "S3 path filter value (proto: top page)")
	only := fs.String("only", "", "comma-separated case prefixes to run (default all)")
	label := fs.String("label", "", "label stored in the output")
	fs.Parse(os.Args[2:])
	tz := loadSites(*sitesF)
	p := newProduct(*product, *site, tz)
	ctx := context.Background()
	res := map[string]any{"kind": "incumbent query bench", "product": p.Name(), "info": p.Info(), "site": *site,
		"measured_at": time.Now().UTC().Format(time.RFC3339), "label": *label, "go": runtime.Version()}
	switch cmd {
	case "bench":
		cases := []*Case{
			{Name: "S2 dashboard, 30d", Line: "S2", From: *from, To: *to},
			{Name: "S3 dashboard, 30d, filter country", Line: "S3", From: *from, To: *to, Filters: []Filter{{"country", *country}}},
			{Name: "S3 dashboard, 30d, filter country+source", Line: "S3", From: *from, To: *to, Filters: []Filter{{"country", *country}, {"source", *source}}},
			{Name: "S3 dashboard, 30d, filter country+path", Line: "S3", From: *from, To: *to, Filters: []Filter{{"country", *country}, {"path", *page}}},
			{Name: "S4 funnel F, 30d", Line: "S4", From: *from, To: *to},
			{Name: "F4 signup.plan breakdown, 30d, filtered", Line: "F4", From: *from, To: *to, Filters: []Filter{{"country", *country}}},
			{Name: "F4 signup.plan breakdown, 30d, unfiltered", Line: "F4", From: *from, To: *to},
		}
		if p.Name() == "goatcounter" { // INFO: its only filter
			cases = append(cases, &Case{Name: "S3-info dashboard, 30d, filter path only", Line: "S3 (INFO)", From: *from, To: *to, Filters: []Filter{{"path", *page}}})
		}
		var ran []*Case
		for _, c := range cases {
			if *only != "" {
				keep := false
				for _, o := range strings.Split(*only, ",") {
					keep = keep || strings.HasPrefix(c.Name, o)
				}
				if !keep {
					continue
				}
			}
			panels := dashboard
			switch c.Line {
			case "S4":
				panels = []string{"funnel"}
			case "F4":
				panels = []string{"props"}
			}
			runCase(ctx, p, c, panels, *runs, *warm, *budget)
			ran = append(ran, c)
		}
		res["cases"] = ran
		res["filter_values"] = map[string]string{"country": *country, "source": *source, "path": *page}
	case "c1":
		truth := loadTruth(*truthF)
		var rows []map[string]any
		for _, s := range strings.Split(*samples, ",") {
			var n int
			var d string
			parts := strings.SplitN(s, ":", 2)
			fmt.Sscan(parts[0], &n)
			d = parts[1]
			t, ok := truth[fmt.Sprintf("%d|%s", n, d)]
			if !ok {
				log.Fatalf("no truth row for %s", s)
			}
			o, err := p.Overview(ctx, n, d)
			row := map[string]any{"site": n, "date": d, "tz": tz[n], "truth": t}
			if err != nil {
				row["error"] = err.Error()
			} else {
				got := map[string]any{}
				errs := map[string]any{}
				cmp := func(k string, v *float64, want float64) {
					if v == nil {
						got[k] = "n/a"
						return
					}
					got[k] = *v
					e := map[string]float64{"abs": *v - want}
					if want != 0 {
						e["pct"] = 100 * (*v - want) / want
					}
					errs[k] = e
				}
				cmp("visitors", o.Visitors, t.Visitors)
				cmp("visits", o.Visits, t.Visits)
				cmp("pageviews", o.Pageviews, t.Pageviews)
				cmp("bounces", o.Bounces, t.Bounces)
				cmp("duration_sum_s", o.DurationSumS, t.DurationSumS)
				row["got"], row["error_vs_truth"], row["raw"] = got, errs, o.Raw
			}
			rows = append(rows, row)
		}
		res["c1"] = rows
	default:
		log.Fatalf("unknown command %q", cmd)
	}
	j, _ := json.MarshalIndent(res, "", " ")
	if *out == "" {
		fmt.Println(string(j))
		return
	}
	if err := os.WriteFile(*out, j, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", *out)
}

type truthRow struct {
	Site         int     `json:"site"`
	Date         string  `json:"date"`
	Visitors     float64 `json:"visitors"`
	Visits       float64 `json:"visits"`
	Pageviews    float64 `json:"pageviews"`
	Bounces      float64 `json:"bounces"`
	DurationSumS float64 `json:"duration_sum_s"`
	FunnelF      []int   `json:"funnel_f"`
}

func loadTruth(path string) map[string]truthRow {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	m := map[string]truthRow{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		l := strings.TrimSuffix(strings.TrimSpace(sc.Text()), ",")
		if !strings.HasPrefix(l, `{"site"`) {
			continue
		}
		var r truthRow
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			continue
		}
		m[fmt.Sprintf("%d|%s", r.Site, r.Date)] = r
	}
	return m
}

func f64(v float64) *float64 { return &v }

// localRange returns [start of from, end of to] in the zone, as UTC instants.
func localRange(tz, from, to string) (time.Time, time.Time) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Fatal(err)
	}
	a, _ := time.ParseInLocation("2006-01-02", from, loc)
	b, _ := time.ParseInLocation("2006-01-02", to, loc)
	return a, b.AddDate(0, 0, 1).Add(-time.Millisecond)
}
