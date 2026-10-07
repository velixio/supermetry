// Command gen writes the benchmark's synthetic event stream (NDJSON) plus
// sites.json and truth.json, per bench/CONTRACT.md.
//
//	gen --tier S --days 90 --seed 42 --out - --sites sites.json --truth truth.json [--zstd]
//	gen verify --sites sites.json --truth truth.json [--zstd] < events.ndjson
//
//go:generate go run . --tier tiny --seed 42 --out testdata/tiny.ndjson --sites testdata/tiny.sites.json --truth testdata/tiny.truth.json
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"panel/bench/gen/internal/recount"
	"panel/bench/gen/internal/synth"
)

func main() {
	// Keep RSS well under the 2 GB budget at tier M (live heap is ~0.6 GB);
	// GOMEMLIMIT in the environment overrides this.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(1 << 30)
	}
	var err error
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		err = runVerify(os.Args[2:])
	} else {
		err = runGenerate(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func create(path string) (io.WriteCloser, error) {
	if path == "-" {
		return nopCloser{os.Stdout}, nil
	}
	return os.Create(path)
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func runGenerate(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	tier := fs.String("tier", "", "tiny | S | M | M-history (required)")
	days := fs.Int("days", 0, "site-local days per site (default: tier's)")
	start := fs.String("start", "", "first site-local date YYYY-MM-DD (default: so the run ends on 2026-09-30)")
	seed := fs.Uint64("seed", 42, "random seed")
	out := fs.String("out", "-", "event stream path, - for stdout")
	sites := fs.String("sites", "", "write sites.json here")
	truth := fs.String("truth", "", "write truth.json here")
	useZstd := fs.Bool("zstd", false, "zstd-compress the event stream")
	workers := fs.Int("workers", 0, "worker goroutines (default GOMAXPROCS); output does not depend on it")
	epd := fs.Int("events-per-day", 0, "override the tier's events per day (experiments only)")
	quiet := fs.Bool("quiet", false, "no summary on stderr")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tier == "" {
		return fmt.Errorf("--tier is required")
	}
	cfg := synth.Config{Tier: *tier, Days: *days, Seed: *seed, Workers: *workers, Zstd: *useZstd, EventsPerDay: *epd}
	if *start != "" {
		t, err := time.Parse("2006-01-02", *start)
		if err != nil {
			return fmt.Errorf("--start: %w", err)
		}
		cfg.Start = t
	}
	g, err := synth.New(cfg)
	if err != nil {
		return err
	}
	if *sites != "" {
		f, err := os.Create(*sites)
		if err != nil {
			return err
		}
		if err := g.WriteSites(f); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	w, err := create(*out)
	if err != nil {
		return err
	}
	var tw io.Writer
	var tf *os.File
	if *truth != "" {
		if tf, err = os.Create(*truth); err != nil {
			return err
		}
		tw = tf
	}
	t0 := time.Now()
	st, err := g.Run(w, tw)
	if err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	if tf != nil {
		if err := tf.Close(); err != nil {
			return err
		}
	}
	if !*quiet {
		el := time.Since(t0).Seconds()
		fmt.Fprintf(os.Stderr, "gen: tier=%s days=%d events=%d pageviews=%d customs=%d sessions=%d visitors=%d units=%d\n",
			*tier, g.Config().Days, st.Events, st.Pageviews, st.Customs, st.Sessions, st.Visitors, st.Units)
		fmt.Fprintf(os.Stderr, "gen: elapsed=%.1fs rate=%.0f events/s raw=%.1fMB (%.0f B/event) out=%.1fMB maxrss=%s\n",
			el, float64(st.Events)/el, float64(st.RawBytes)/1e6, float64(st.RawBytes)/float64(max(st.Events, 1)),
			float64(st.OutBytes)/1e6, maxRSS())
	}
	return nil
}

func maxRSS() string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return "?"
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmHWM:") {
			return strings.Join(strings.Fields(l)[1:], "")
		}
	}
	return "?"
}

// verify recounts a stream independently and compares it with truth.json.
func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	sitesPath := fs.String("sites", "", "sites.json (required)")
	truthPath := fs.String("truth", "", "truth.json (required)")
	useZstd := fs.Bool("zstd", false, "stdin is zstd-compressed")
	keyEvery := fs.Int("key-order-every", 1000, "check exact JSON key order on every n-th line")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sf, err := os.Open(*sitesPath)
	if err != nil {
		return err
	}
	sites, err := recount.LoadSites(sf)
	sf.Close()
	if err != nil {
		return err
	}
	tf, err := os.Open(*truthPath)
	if err != nil {
		return err
	}
	want, err := recount.LoadTruth(bufio.NewReaderSize(tf, 1<<20))
	tf.Close()
	if err != nil {
		return err
	}
	var in io.Reader = os.Stdin
	if *useZstd {
		d, err := zstd.NewReader(os.Stdin)
		if err != nil {
			return err
		}
		defer d.Close()
		in = d
	}
	res, err := recount.Run(in, sites, recount.Options{CheckKeyOrderEvery: *keyEvery})
	if err != nil {
		return err
	}
	diffs := recount.Compare(res.Rows, want, 50)
	for _, v := range res.Violations {
		fmt.Fprintln(os.Stderr, "violation:", v)
	}
	for _, d := range diffs {
		fmt.Fprintln(os.Stderr, "diff:", d)
	}
	fmt.Fprintf(os.Stderr, "verify: %d events, %d rows recounted, %d truth rows, %d violations, %d diffs\n",
		res.Events, len(res.Rows), len(want), len(res.Violations), len(diffs))
	if len(res.Violations) > 0 || len(diffs) > 0 {
		return fmt.Errorf("verification failed")
	}
	return nil
}
