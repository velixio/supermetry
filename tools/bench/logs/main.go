// Command logs loads a synthetic log dataset into a scratch database and
// benchmarks log search against it.
//
//	go run ./tools/bench/logs load   -admin-dsn $DSN -db supermetry_bench_logs
//	go run ./tools/bench/logs search -admin-dsn $DSN -db supermetry_bench_logs
//
// The dataset: 7 days, 10,000,000 rows for site 1 and 2,000,000 across 9 other
// sites, 50 services, severities 60% info / 25% debug / 10% warn / 5% error,
// 20% of rows with trace ids (about 5 rows per trace), bodies from a 5,000-word
// Zipf vocabulary plus ids. "error" appears in about 30% of bodies.
//
// load drops the body trigram index while it copies and rebuilds it afterwards
// (much faster than maintaining it row by row), then runs ANALYZE. It is a
// benchmark tool: it drops and recreates the named database.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/obslogs"
	"supermetry/internal/obslogs/search"
)

const (
	vocabSize   = 5000
	rareRank    = 4200 // a word seen in a few hundred rows
	mainSite    = 1
	otherSites  = 9
	services    = 50
	mainRows    = 10_000_000
	otherRows   = 2_000_000
	batchRows   = 20_000
	warmRuns    = 30
	windowDays  = 7
	benchPhrase = "payment failed"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: logs load|search [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	admin := fs.String("admin-dsn", os.Getenv("SUPERMETRY_DATABASE_URL"), "DSN of a database on the server (used to create the bench database)")
	name := fs.String("db", "supermetry_bench_logs", "bench database name")
	scale := fs.Float64("scale", 1, "fraction of the full dataset to load (load only)")
	_ = fs.Parse(os.Args[2:])
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "load":
		err = load(ctx, *admin, *name, *scale)
	case "search":
		err = runSearch(ctx, *admin, *name)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func dsnFor(admin, name string) (string, error) {
	u, err := url.Parse(admin)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

func vocabulary() []string {
	syl := []string{"ba", "ko", "ri", "tu", "me", "sa", "no", "li", "pe", "dar", "mon", "vek", "tis", "gor", "lan", "fu"}
	words := make([]string, vocabSize)
	for i := range words {
		n, w := i+17, ""
		for len(w) < 4 || n > 0 {
			w += syl[n%len(syl)]
			n /= len(syl)
		}
		words[i] = w
	}
	return words
}

func load(ctx context.Context, admin, name string, scale float64) error {
	root, err := pgxpool.New(ctx, admin)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Exec(ctx, `drop database if exists `+pgx.Identifier{name}.Sanitize()+` with (force)`); err != nil {
		return err
	}
	if _, err := root.Exec(ctx, `create database `+pgx.Identifier{name}.Sanitize()); err != nil {
		return err
	}
	dsn, err := dsnFor(admin, name)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Bench','bench')`); err != nil {
		return err
	}
	for s := 1; s <= mainSite+otherSites; s++ {
		if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values($1,1,$2,'Bench','UTC',30,'app')`, s, fmt.Sprintf("benchlogs%03d", s)); err != nil {
			return err
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-windowDays * 24 * time.Hour)
	if err := db.EnsureDailyPartitions(ctx, pool, "logs", start.Add(-24*time.Hour), now.Add(48*time.Hour)); err != nil {
		return err
	}
	// Maintaining the trigram index per row is the slow part of a bulk load.
	if _, err := pool.Exec(ctx, `drop index logs_body_trgm`); err != nil {
		return err
	}
	total := int(float64(mainRows+otherRows) * scale)
	mainShare := float64(mainRows) / float64(mainRows+otherRows)
	words := vocabulary()
	r := mrand.New(mrand.NewPCG(42, 7))
	zipf := mrand.NewZipf(r, 1.1, 1, vocabSize-1)
	sevPick := func() int16 {
		switch x := r.Float64(); {
		case x < 0.60:
			return 9
		case x < 0.85:
			return 5
		case x < 0.95:
			return 13
		}
		return 17
	}
	step := float64(windowDays*24*time.Hour) / float64(total)
	var trace []byte
	traceLeft := 0
	rows := make([][]any, 0, batchRows)
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		_, err := pool.CopyFrom(ctx, pgx.Identifier{"logs"},
			[]string{"site_id", "ts", "rid", "observed_at", "severity", "service", "environment", "release", "trace_id", "span_id", "body", "truncated", "attributes", "source"},
			pgx.CopyFromRows(rows))
		rows = rows[:0]
		return err
	}
	began := time.Now()
	for i := 0; i < total; i++ {
		site := mainSite
		if r.Float64() >= mainShare {
			site = mainSite + 1 + r.IntN(otherSites)
		}
		ts := start.Add(time.Duration(float64(i) * step))
		sev := sevPick()
		var b strings.Builder
		n := 5 + r.IntN(5)
		for w := 0; w < n; w++ {
			b.WriteString(words[zipf.Uint64()])
			b.WriteByte(' ')
		}
		if r.Float64() < 0.30 {
			b.WriteString("error ")
		}
		if sev >= 13 && r.Float64() < 0.05 {
			b.WriteString(benchPhrase + " ")
		}
		fmt.Fprintf(&b, "id=%08x", r.Uint32())
		var tid, sid any
		if r.Float64() < 0.20 {
			if traceLeft == 0 {
				trace = make([]byte, 16)
				_, _ = rand.Read(trace)
				traceLeft = 1 + r.IntN(8)
			}
			traceLeft--
			tid = trace
			sp := make([]byte, 8)
			_, _ = rand.Read(sp)
			sid = sp
		}
		env := "prod"
		if r.Float64() < 0.2 {
			env = "staging"
		}
		rows = append(rows, []any{int32(site), ts, r.Int64(), ts, sev, fmt.Sprintf("svc-%02d", r.IntN(services)), env, "1." + fmt.Sprint(r.IntN(5)), tid, sid, b.String(), false,
			fmt.Sprintf(`{"http.route":"/r%d","host.name":"web-%d"}`, r.IntN(200), r.IntN(20)), "otlp"})
		if len(rows) == batchRows {
			if err := flush(); err != nil {
				return err
			}
			if (i+1)%(batchRows*25) == 0 {
				fmt.Printf("loaded %d / %d rows (%s)\n", i+1, total, time.Since(began).Round(time.Second))
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	fmt.Printf("copied %d rows in %s\n", total, time.Since(began).Round(time.Second))
	began = time.Now()
	if _, err := pool.Exec(ctx, `set maintenance_work_mem='512MB'; create index logs_body_trgm on logs using gin (body gin_trgm_ops) with (fastupdate = on, gin_pending_list_limit = 4096)`); err != nil {
		return err
	}
	fmt.Printf("trigram index built in %s\n", time.Since(began).Round(time.Second))
	if _, err := pool.Exec(ctx, `analyze logs`); err != nil {
		return err
	}
	var size string
	_ = pool.QueryRow(ctx, `select pg_size_pretty(sum(pg_total_relation_size(relid))) from pg_partition_tree('logs') where isleaf`).Scan(&size)
	fmt.Println("analyzed; total size (heap + indexes):", size)
	return nil
}

type result struct {
	name       string
	p50, p95   time.Duration
	max        time.Duration
	rows       int
	first, all []time.Duration
}

func runSearch(ctx context.Context, admin, name string) error {
	dsn, err := dsnFor(admin, name)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	now := time.Now().UTC()
	to, from := now, now.Add(-windowDays*24*time.Hour+time.Minute)

	// A trace with several rows, for the lookup cases.
	var traceID []byte
	var traceTS time.Time
	if err := pool.QueryRow(ctx, `select trace_id, ts from logs where site_id=1 and trace_id is not null and ts > now() - interval '3 days' limit 1`).Scan(&traceID, &traceTS); err != nil {
		return fmt.Errorf("pick a trace: %w", err)
	}
	words := vocabulary()
	mustParse := func(s string) search.Query {
		q, err := search.Parse(s)
		if err != nil {
			panic(err)
		}
		return q
	}
	type run func() (int, error)
	searchRun := func(q string, f, t time.Time, limit int, pages int) run {
		return func() (int, error) {
			var cur *obslogs.Cursor
			n := 0
			for p := 0; p < pages; p++ {
				page, err := obslogs.Search(ctx, pool, mainSite, now, mustParse(q), f, t, limit, cur)
				if err != nil {
					return 0, err
				}
				n = len(page.Rows)
				if page.Next == nil {
					break
				}
				cur = page.Next
			}
			return n, nil
		}
	}
	cases := []struct {
		name string
		fn   run
	}{
		{"rare word over 7d (" + words[rareRank] + ")", searchRun(words[rareRank], from, to, 100, 1)},
		{"absent word over 7d (no matches, full walk)", searchRun("zzqxjv", from, to, 100, 1)},
		{"common word 'error' over 7d, page 1", searchRun("error", from, to, 100, 1)},
		{"common word 'error' over 7d, page 3", searchRun("error", from, to, 100, 3)},
		{`phrase "payment failed" severity:>=warn over 24h`, searchRun(`"`+benchPhrase+`" severity:>=warn`, now.Add(-24*time.Hour), to, 100, 1)},
		{"service: + env: over 7d, no text", searchRun("service:svc-07 env:prod", from, to, 100, 1)},
		{"trace: search over 7d", searchRun("trace:"+hex.EncodeToString(traceID), from, to, 100, 1)},
		{"trace logs lookup (TraceLogs)", func() (int, error) {
			rows, err := obslogs.TraceLogs(ctx, pool, mainSite, now, traceID, traceTS, 100)
			return len(rows), err
		}},
	}
	fmt.Printf("%-52s %8s %8s %8s %6s\n", "case ("+fmt.Sprint(warmRuns)+" warm runs after 1 cold)", "p50", "p95", "max", "rows")
	failed := false
	for _, c := range cases {
		cold := time.Now()
		if _, err := c.fn(); err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
		coldDur := time.Since(cold)
		var ds []time.Duration
		rows := 0
		for i := 0; i < warmRuns; i++ {
			t0 := time.Now()
			n, err := c.fn()
			if err != nil {
				return fmt.Errorf("%s: %w", c.name, err)
			}
			rows = n
			ds = append(ds, time.Since(t0))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		p95 := ds[int(math.Ceil(0.95*float64(len(ds))))-1]
		fmt.Printf("%-52s %8s %8s %8s %6d   (cold %s)\n", c.name, ds[len(ds)/2].Round(time.Millisecond), p95.Round(time.Millisecond), ds[len(ds)-1].Round(time.Millisecond), rows, coldDur.Round(time.Millisecond))
		if p95 >= time.Second {
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("at least one case has p95 >= 1s")
	}
	return nil
}
