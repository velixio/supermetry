// Command proto is the throwaway Panel prototype for the go/no-go benchmark
// (bench/CONTRACT.md). See NOTES.md.
package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // served only when PANEL_PPROF is set
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"panel/bench/proto/internal/benchq"
	"panel/bench/proto/internal/core"
	"panel/bench/proto/internal/enrich"
	"panel/bench/proto/internal/ingest"
	"panel/bench/proto/internal/query"
	"panel/bench/proto/internal/store"
	"panel/bench/proto/internal/verify"
)

const usage = `usage: proto <command> [flags]

commands:
  serve          HTTP ingest (POST /api/event) + query API on :8090
  migrate        apply schema migrations
  sites          import sites.json (--file)
  load           bulk-load NDJSON from stdin through the ingest pipeline
  retention      drop expired raw-event partitions (--raw-days N [--as-of YYYY-MM-DD])
  rollup         close days: rebuild closed-day tables + rollups (--from --to [--site] [--parallel] [--drop-hot])
  rollup-months  rebuild monthly rollups of complete months (--from --to [--site])
  rollup-blocks  rebuild dyadic day blocks over the complete closed span (--from --to [--site])
  rollup-stream  build rollups straight from NDJSON on stdin (no raw rows)
  query          run a query: proto query <name> --site --from --to [--filter k=v ...]
  verify         compare query results with truth.json (--truth)
  bench          time S2/S3/S4/F4 queries, write JSON to bench/results/
  copybench      COPY throughput micro-benchmark (scratch schema)
  loadtest       HTTP ingest load test against a running server
  reset          drop all raw partitions and rollups (keeps sites)

env: PANEL_DSN (default ` + store.DefaultDSN + `)
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if addr := os.Getenv("PANEL_PPROF"); addr != "" {
		go http.ListenAndServe(addr, nil)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(ctx, args)
	case "migrate":
		_, err = openStore(ctx, 0)
	case "sites":
		err = cmdSites(ctx, args)
	case "load":
		err = cmdLoad(ctx, args)
	case "retention":
		err = cmdRetention(ctx, args)
	case "rollup":
		err = cmdRollup(ctx, args)
	case "rollup-months":
		err = cmdRollupMonths(ctx, args)
	case "rollup-blocks":
		err = cmdRollupBlocks(ctx, args)
	case "rollup-stream":
		err = cmdRollupStream(ctx, args)
	case "query":
		err = cmdQuery(ctx, args)
	case "verify":
		err = cmdVerify(ctx, args)
	case "bench":
		err = benchq.CmdBench(ctx, args, openStore)
	case "copybench":
		err = benchq.CmdCopyBench(ctx, args)
	case "loadtest":
		err = benchq.CmdLoadTest(ctx, args)
	case "reset":
		err = cmdReset(ctx)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "proto "+cmd+":", err)
		os.Exit(1)
	}
}

func openStore(ctx context.Context, maxConns int32) (*store.Store, error) {
	st, err := store.Open(ctx, store.DSN(), store.Options{MaxConns: maxConns, RawPartition: strings.ToLower(os.Getenv("PANEL_RAW_PARTITION"))})
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func retentionCutoff(asOf time.Time, rawDays int) (time.Time, error) {
	if rawDays <= 0 {
		return time.Time{}, errors.New("--raw-days must be positive")
	}
	asOf = asOf.UTC()
	today := time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 0, 0, 0, 0, time.UTC)
	return today.AddDate(0, 0, -(rawDays - 1)), nil
}

func cmdRetention(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("retention", flag.ExitOnError)
	rawDays := fs.Int("raw-days", 0, "number of UTC raw-event days to keep, including --as-of")
	asOfText := fs.String("as-of", "", "retention clock YYYY-MM-DD (default: today UTC)")
	fs.Parse(args)
	asOf := time.Now()
	var err error
	if *asOfText != "" {
		asOf, err = time.Parse("2006-01-02", *asOfText)
		if err != nil {
			return fmt.Errorf("--as-of: %w", err)
		}
	}
	cutoff, err := retentionCutoff(asOf, *rawDays)
	if err != nil {
		return err
	}
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	dropped, err := st.DropRawEventPartitions(ctx, cutoff)
	if err != nil {
		return err
	}
	printJSON(map[string]any{"raw_days": *rawDays, "as_of": asOf.UTC().Format("2006-01-02"), "cutoff": cutoff.Format("2006-01-02"), "dropped": dropped})
	return nil
}

// loadSites imports file (if given) and returns all sites from the DB.
func loadSites(ctx context.Context, st *store.Store, file string) (*ingest.Sites, error) {
	if file != "" {
		sites, err := core.LoadSitesFile(file)
		if err != nil {
			return nil, err
		}
		if err := st.UpsertSites(ctx, sites); err != nil {
			return nil, err
		}
	}
	sites, err := st.LoadSites(ctx)
	if err != nil {
		return nil, err
	}
	if len(sites) == 0 {
		return nil, errors.New("no sites: pass --sites sites.json")
	}
	return ingest.NewSites(sites), nil
}

func cmdSites(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sites", flag.ExitOnError)
	file := fs.String("file", "", "sites.json from the generator")
	fs.Parse(args)
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	s, err := loadSites(ctx, st, *file)
	if err != nil {
		return err
	}
	fmt.Printf("%d sites\n", len(s.All()))
	return nil
}

func pipelineFlags(fs *flag.FlagSet) *ingest.Config {
	c := &ingest.Config{}
	fs.IntVar(&c.QueueSize, "queue", 1<<16, "bounded queue size (backpressure limit)")
	fs.IntVar(&c.BatchSize, "batch", 5000, "events per COPY")
	fs.IntVar(&c.Writers, "writers", 2, "concurrent COPY batchers")
	fs.DurationVar(&c.FlushEvery, "flush", time.Second, "max partial-batch age")
	fs.DurationVar(&c.SessionFlushEvery, "session-flush", 10*time.Second, "dirty-session upsert interval")
	return c
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8090", "listen address")
	geo := fs.String("geoip", "data/dbip-country-lite.mmdb", "country mmdb (empty: no GeoIP)")
	sitesFile := fs.String("sites", "", "import sites.json first")
	xff := fs.Bool("trust-xff", true, "take client IP from X-Forwarded-For (load tests)")
	memLimit := fs.Int64("memlimit-mb", 100, "Go soft memory limit in MiB unless GOMEMLIMIT is set (0 = none); R2")
	pc := pipelineFlags(fs)
	fs.Parse(args)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if os.Getenv("GOMEMLIMIT") == "" && *memLimit > 0 {
		// A soft limit keeps the heap near the live set instead of 2x it
		// (GOGC=100), which is what decides RSS under sustained load.
		debug.SetMemoryLimit(*memLimit << 20)
	}
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	sites, err := loadSites(ctx, st, *sitesFile)
	if err != nil {
		return err
	}
	en, err := enrich.Open(*geo)
	if err != nil {
		return err
	}
	defer en.Close()
	persist := make(chan core.SaltKey, 4096)
	salts := core.NewSalts(func(k core.SaltKey, _ []byte) {
		select {
		case persist <- k:
		default:
			log.Warn("salt persist queue full", "site", k.Site, "day", k.Day)
		}
	})
	known, err := st.LoadSalts(ctx, core.DayOf(time.Now(), time.UTC)-2)
	if err != nil {
		return err
	}
	for k, v := range known {
		salts.Put(k, v)
	}
	pc.Log = log
	cache := core.NewCache(core.CacheConfig{})
	open, err := st.LoadOpenSessions(ctx, time.Now(), core.SessionTimeout)
	if err != nil {
		return fmt.Errorf("load open sessions: %w", err)
	}
	log.Info("rehydrated open sessions", "count", restoreSessions(cache, open))
	pipe := ingest.New(*pc, st, cache)
	srv := &ingest.Server{Pipe: pipe, Builder: &ingest.Builder{Sites: sites, Salts: salts, Enrich: en},
		Query: &query.Engine{Pool: st.Pool, Site: sites.ByID}, Log: log, TrustXFF: *xff}
	schedCtx, cancelSched := context.WithCancel(context.Background())
	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		(&ingest.Scheduler{Store: st, Sites: sites, Salts: salts, Persist: persist, Log: log}).Run(schedCtx)
	}()
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("serving", "addr", *addr, "sites", len(sites.All()))
	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	log.Info("shutting down: draining HTTP, then flushing the pipeline")
	sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	perr := pipe.Close(sctx)
	cancelSched()
	<-schedDone
	log.Info("stopped", "stats", fmt.Sprintf("%+v", pipe.Stats()))
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, perr)
}

func restoreSessions(cache *core.Cache, sessions []core.Session) int {
	n := 0
	for _, session := range sessions {
		if cache.Restore(session) {
			n++
		}
	}
	return n
}

func cmdLoad(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("load", flag.ExitOnError)
	sitesFile := fs.String("sites", "", "import sites.json first")
	mode := fs.String("enrich", "truth", "truth: use the record's parsed dims; real: UA parser + GeoIP")
	geo := fs.String("geoip", "data/dbip-country-lite.mmdb", "mmdb for --enrich real")
	workers := fs.Int("parse-workers", 0, "parallel NDJSON parsers (0 = GOMAXPROCS-2)")
	pc := pipelineFlags(fs)
	fs.Parse(args)
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	sites, err := loadSites(ctx, st, *sitesFile)
	if err != nil {
		return err
	}
	o := ingest.LoadOptions{ParseWorkers: *workers, Pipeline: *pc}
	if *mode == "real" {
		if o.Enrich, err = enrich.Open(*geo); err != nil {
			return err
		}
	}
	stats, err := ingest.Load(ctx, st, sites, bufio.NewReaderSize(os.Stdin, 1<<20), o)
	printJSON(stats)
	if err != nil {
		return err
	}
	return st.AnalyzePartitions(ctx)
}

func dayFlags(fs *flag.FlagSet) (*string, *string) {
	return fs.String("from", "", "first local date YYYY-MM-DD"), fs.String("to", "", "last local date YYYY-MM-DD")
}

func cmdRollup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rollup", flag.ExitOnError)
	from, to := dayFlags(fs)
	site := fs.Int("site", 0, "only this site (0 = all)")
	parallel := fs.Int("parallel", 1, "days recomputed concurrently")
	dropHot := fs.Bool("drop-hot", false, "after each day, drop hot sessions partitions whose local days are all compacted (needs --from = first day with data, all sites)")
	fs.Parse(args)
	f, err := core.ParseDay(*from)
	if err != nil {
		return err
	}
	t, err := core.ParseDay(*to)
	if err != nil {
		return err
	}
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	sites, err := loadSites(ctx, st, "")
	if err != nil {
		return err
	}
	start := time.Now()
	now := time.Now()
	// Days run on --parallel workers; hot partitions are dropped behind the
	// prefix of days that are done for every site.
	type res struct {
		d   core.Day
		all bool
		err error
	}
	days := make(chan core.Day)
	results := make(chan res)
	for range max(*parallel, 1) {
		go func() {
			for d := range days {
				var ids []int32
				for _, s := range sites.All() {
					if (*site == 0 || int32(*site) == s.ID) && core.DayOf(now, s.Loc) > d { // closed days only
						ids = append(ids, s.ID)
					}
				}
				ds := time.Now()
				err := st.RecomputeDay(ctx, d, ids)
				if err == nil {
					fmt.Fprintf(os.Stderr, "rollup %s: %d sites in %s\n", d, len(ids), time.Since(ds).Round(time.Millisecond))
				}
				results <- res{d, *site == 0 && len(ids) == len(sites.All()), err}
			}
		}()
	}
	go func() {
		for d := f; d <= t; d++ {
			days <- d
		}
		close(days)
	}()
	doneDays := map[core.Day]bool{}
	prefix := f - 1 // all days in [f, prefix] are done for every site
	var firstErr error
	for range int(t-f) + 1 {
		r := <-results
		if r.err != nil {
			firstErr = cmp.Or(firstErr, r.err)
			continue
		}
		doneDays[r.d] = r.all
		for doneDays[prefix+1] {
			prefix++
		}
		if *dropHot && prefix >= f {
			dropped, err := st.DropCompactedSessions(ctx, prefix)
			if err != nil {
				return err
			}
			if len(dropped) > 0 {
				fmt.Fprintf(os.Stderr, "  dropped %v\n", dropped)
			}
		}
	}
	if firstErr != nil {
		return firstErr
	}
	// Monthly rollups for every month the run covered completely.
	var all []int32
	for _, s := range sites.All() {
		if *site == 0 || int32(*site) == s.ID {
			all = append(all, s.ID)
		}
	}
	ft := f.Time()
	for first := time.Date(ft.Year(), ft.Month(), 1, 0, 0, 0, 0, time.UTC); ; first = first.AddDate(0, 1, 0) {
		if first.Before(ft) {
			continue
		}
		last := core.Day(first.AddDate(0, 1, -1).Unix() / 86400)
		if last > t || last >= core.DayOf(now, time.UTC)-1 {
			break
		}
		if err := st.RecomputeMonth(ctx, core.Day(first.Unix()/86400), all); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "monthly rollup %s\n", first.Format("2006-01"))
	}
	fmt.Fprintf(os.Stderr, "rollup done in %s\n", time.Since(start).Round(time.Millisecond))
	return nil
}

// cmdRollupMonths (re)builds the monthly rollups of every complete month in
// [from, to] from the daily rollups.
func cmdRollupMonths(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rollup-months", flag.ExitOnError)
	from, to := dayFlags(fs)
	site := fs.Int("site", 0, "only this site (0 = all)")
	fs.Parse(args)
	f, err := core.ParseDay(*from)
	if err != nil {
		return err
	}
	t, err := core.ParseDay(*to)
	if err != nil {
		return err
	}
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	sites, err := loadSites(ctx, st, "")
	if err != nil {
		return err
	}
	var ids []int32
	for _, s := range sites.All() {
		if *site == 0 || int32(*site) == s.ID {
			ids = append(ids, s.ID)
		}
	}
	ft := f.Time()
	for first := time.Date(ft.Year(), ft.Month(), 1, 0, 0, 0, 0, time.UTC); ; first = first.AddDate(0, 1, 0) {
		if first.Before(ft) {
			continue
		}
		if core.Day(first.AddDate(0, 1, -1).Unix()/86400) > t {
			break
		}
		start := time.Now()
		if err := st.RecomputeMonth(ctx, core.Day(first.Unix()/86400), ids); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "monthly rollup %s: %d sites in %s\n", first.Format("2006-01"), len(ids), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// cmdRollupBlocks backfills exact dyadic blocks from existing daily rollups.
// The range must be the complete retained/known span for the selected sites;
// dates without daily rows are marked as completed zero days.
func cmdRollupBlocks(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rollup-blocks", flag.ExitOnError)
	from, to := dayFlags(fs)
	site := fs.Int("site", 0, "only this site (0 = all)")
	fs.Parse(args)
	f, err := core.ParseDay(*from)
	if err != nil {
		return err
	}
	t, err := core.ParseDay(*to)
	if err != nil {
		return err
	}
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	sites, err := loadSites(ctx, st, "")
	if err != nil {
		return err
	}
	var ids []int32
	for _, s := range sites.All() {
		if *site == 0 || int32(*site) == s.ID {
			ids = append(ids, s.ID)
		}
	}
	start := time.Now()
	if err := st.RebuildDayBlocks(ctx, f, t, ids); err != nil {
		return err
	}
	if _, err := st.Pool.Exec(ctx, `ANALYZE rollup_block_dim; ANALYZE rollup_block_done`); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "dyadic rollups %s..%s: %d sites in %s\n", f, t, len(ids), time.Since(start).Round(time.Millisecond))
	return nil
}

func cmdRollupStream(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rollup-stream", flag.ExitOnError)
	sitesFile := fs.String("sites", "", "import sites.json first")
	workers := fs.Int("parse-workers", 0, "parallel NDJSON parsers")
	fs.Parse(args)
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	sites, err := loadSites(ctx, st, *sitesFile)
	if err != nil {
		return err
	}
	stats, err := ingest.Stream(ctx, sites, bufio.NewReaderSize(os.Stdin, 1<<20), ingest.StreamOptions{ParseWorkers: *workers},
		func(b []core.SaltKey, rows []core.RollupRow) error { return st.ReplaceRollups(ctx, b, rows) })
	printJSON(stats)
	return err
}

func cmdQuery(ctx context.Context, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("query name required: %s, realtime", strings.Join(query.Names, ", "))
	}
	name := args[0]
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	from, to := dayFlags(fs)
	var filters multiFlag
	fs.Var(&filters, "filter", "k=v (repeatable): path, source, country, browser, os, device, entry, exit, utm_*")
	limit := fs.String("limit", "", "rows for breakdowns (default 50, -1 = all)")
	mode := fs.String("mode", "auto", "auto | rollup | raw")
	event := fs.String("event", "signup", "props: custom event name")
	key := fs.String("key", "plan", "props: prop key")
	server := fs.String("server", "http://127.0.0.1:8090", "realtime: running server")
	fanout := fs.Int("fanout", 8, "closed tier: day chunks run concurrently")
	fs.Parse(args[1:])
	if name == "realtime" {
		resp, err := http.Get(*server + "/api/realtime?site=" + *site)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	}
	vals := map[string]string{"site": *site, "from": *from, "to": *to, "limit": *limit, "mode": *mode, "event": *event, "key": *key}
	p, err := ingest.ParseParams(func(k string) string { return vals[k] }, filters)
	if err != nil {
		return err
	}
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	sites, err := loadSites(ctx, st, "")
	if err != nil {
		return err
	}
	e := &query.Engine{Pool: st.Pool, Site: sites.ByID, Fanout: *fanout}
	start := time.Now()
	var res any
	if name == "dashboard" {
		res, err = e.Dashboard(ctx, p)
	} else {
		res, err = e.Run(ctx, name, p)
	}
	if err != nil {
		return err
	}
	printJSON(map[string]any{"query": name, "params": p, "result": res, "took_ms": float64(time.Since(start).Microseconds()) / 1000})
	return nil
}

func cmdVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	truth := fs.String("truth", "", "truth.json from the generator")
	raw := fs.Bool("raw", true, "also check raw-tier totals, funnel, props, filtered overviews")
	every := fs.Int("raw-every", 1, "raw checks on every n-th truth row")
	workers := fs.Int("workers", 8, "truth rows checked concurrently")
	fanout := fs.Int("fanout", 1, "closed-tier day chunks per query")
	fs.Parse(args)
	f, err := os.Open(*truth)
	if err != nil {
		return err
	}
	defer f.Close()
	rows, err := verify.LoadTruth(bufio.NewReader(f))
	if err != nil {
		return err
	}
	st, err := openStore(ctx, int32(*workers+4))
	if err != nil {
		return err
	}
	defer st.Close()
	sites, err := loadSites(ctx, st, "")
	if err != nil {
		return err
	}
	rep, err := verify.Run(ctx, &query.Engine{Pool: st.Pool, Site: sites.ByID, Fanout: *fanout}, rows,
		verify.Options{Raw: *raw, RawEvery: *every, Workers: *workers})
	if err != nil {
		return err
	}
	printJSON(rep)
	fmt.Fprintln(os.Stderr, rep.Summary())
	if !rep.Pass {
		return errors.New("verification failed")
	}
	return nil
}

func cmdReset(ctx context.Context) error {
	st, err := openStore(ctx, 0)
	if err != nil {
		return err
	}
	defer st.Close()
	_, err = st.Pool.Exec(ctx, `
DO $$ DECLARE r record; BEGIN
  FOR r IN SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
           JOIN pg_class p ON p.oid = i.inhparent
           WHERE p.relname IN ('events', 'sessions', 'sessions_closed', 'cube_day', 'custom_closed')
             AND p.relnamespace = current_schema()::regnamespace LOOP
    EXECUTE format('DROP TABLE %I', r.relname);
  END LOOP;
END $$;
TRUNCATE rollup_day, rollup_day_dim, rollup_month_dim, rollup_month_done, rollup_block_dim, rollup_block_done, salts, dict;
INSERT INTO dict (id, value) VALUES (0, '');`)
	return err
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
