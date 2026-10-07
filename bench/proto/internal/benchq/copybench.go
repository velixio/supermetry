package benchq

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/bench/proto/internal/core"
	"panel/bench/proto/internal/store"
)

// synthEvents makes n realistic-looking, already-sessionized events spread
// over one day (for COPY throughput only; not for correctness).
func synthEvents(n int, seed uint64) []*core.Event {
	r := rand.New(rand.NewPCG(seed, 7))
	paths := make([]string, 20000)
	for i := range paths {
		paths[i] = fmt.Sprintf("/blog/post-%d-some-slug", i)
	}
	paths[0], paths[1] = "/", "/pricing"
	zp := rand.NewZipf(r, 1.1, 1, uint64(len(paths)-1))
	zs := rand.NewZipf(r, 1.2, 1, 99)
	sources := []string{"", "www.google.com", "news.ycombinator.com", "t.co", "www.bing.com", "duckduckgo.com", "newsletter", "github.com"}
	countries := []string{"US", "IN", "DE", "GB", "FR", "BR", "CA", "JP", "NP", "NL"}
	browsers := []string{"Chrome", "Safari", "Firefox", "Edge", "Samsung Browser", "Opera"}
	oss := []string{"Windows", "Android", "iOS", "macOS", "Linux"}
	devices := []string{"desktop", "mobile", "tablet"}
	day := core.CivilDay(2026, 7, 1)
	base := day.Time().UnixMicro()
	out := make([]*core.Event, n)
	for i := range out {
		e := &core.Event{SiteID: int32(zs.Uint64() + 1), Day: day, TS: base + int64(i)*86_400_000_000/int64(n),
			VisitorID: r.Int64(), SessionID: r.Int64(), Name: "pageview", Path: paths[zp.Uint64()],
			Source: sources[r.IntN(len(sources))], Country: countries[r.IntN(len(countries))],
			Browser: browsers[r.IntN(len(browsers))], OS: oss[r.IntN(len(oss))], Device: devices[r.IntN(len(devices))]}
		if r.IntN(10) == 0 {
			e.Name = "signup"
			e.Props = []byte(`{"plan":"pro"}`)
		}
		out[i] = e
	}
	return out
}

// CmdCopyBench measures raw COPY throughput into a scratch schema.
func CmdCopyBench(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("copybench", flag.ExitOnError)
	rows := fs.Int("rows", 2_000_000, "events per run")
	writers := fs.String("writers", "1,2,4", "concurrent COPY streams to try")
	batch := fs.Int("batch", 5000, "rows per COPY")
	indexes := fs.String("indexes", "none,site_ts,custom", "extra index variants: none, site_ts (btree (site_id, ts)), custom (partial btree (site_id, ts) WHERE name <> 'pageview')")
	out := fs.String("out", "../results", "results directory")
	fs.Parse(args)
	evs := synthEvents(*rows, 42)
	type result struct {
		Writers     int     `json:"writers"`
		Batch       int     `json:"batch"`
		ExtraIndex  string  `json:"extra_index"`
		Rows        int     `json:"rows"`
		Seconds     float64 `json:"seconds"`
		RowsPerSec  float64 `json:"rows_per_s"`
		BytesPerRow float64 `json:"table_bytes_per_row"`
	}
	var results []result
	for _, withIdx := range strings.Split(*indexes, ",") {
		for _, ws := range strings.Split(*writers, ",") {
			w, err := strconv.Atoi(ws)
			if err != nil {
				return err
			}
			r, err := copyRun(ctx, evs, w, *batch, withIdx)
			if err != nil {
				return err
			}
			results = append(results, result{w, *batch, withIdx, *rows, r.secs, float64(*rows) / r.secs, r.bytesPerRow})
			fmt.Fprintf(os.Stderr, "COPY writers=%d batch=%d index=%s: %.0f rows/s, %.0f B/row\n", w, *batch, withIdx, float64(*rows)/r.secs, r.bytesPerRow)
		}
	}
	return writeResult(*out, "proto-copybench", "", map[string]any{"kind": "panel-proto COPY micro-benchmark",
		"measured_at": time.Now().UTC().Format(time.RFC3339),
		"note":        "synthetic pre-sessionized events, one day/one partition, stock PG config, BRIN(ts) always present", "results": results})
}

type copyResult struct{ secs, bytesPerRow float64 }

var extraIndex = map[string]string{"none": "", "site_ts": `CREATE INDEX ON events (site_id, ts)`,
	"custom": `CREATE INDEX ON events (site_id, ts) WHERE name <> 'pageview'`}

func copyRun(ctx context.Context, evs []*core.Event, writers, batch int, withIdx string) (copyResult, error) {
	admin, err := pgx.Connect(ctx, store.DSN())
	if err != nil {
		return copyResult{}, err
	}
	defer admin.Close(ctx)
	schema := fmt.Sprintf("copybench_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		return copyResult{}, err
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	st, err := store.Open(ctx, store.DSN(), store.Options{SearchPath: schema, MaxConns: int32(writers + 1)})
	if err != nil {
		return copyResult{}, err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return copyResult{}, err
	}
	ddl, ok := extraIndex[withIdx]
	if !ok {
		return copyResult{}, fmt.Errorf("unknown index variant %q", withIdx)
	}
	if ddl != "" {
		if _, err := st.Pool.Exec(ctx, ddl); err != nil {
			return copyResult{}, err
		}
	}
	if err := st.EnsureMonths(ctx, evs[0].Time()); err != nil {
		return copyResult{}, err
	}
	work := make(chan []*core.Event)
	errc := make(chan error, writers)
	var wg sync.WaitGroup
	start := time.Now()
	for range writers {
		wg.Go(func() {
			failed := false
			for b := range work { // keep draining after a failure so the producer never blocks
				if failed {
					continue
				}
				if err := st.CopyEvents(ctx, b); err != nil {
					errc <- err
					failed = true
				}
			}
		})
	}
	for i := 0; i < len(evs); i += batch {
		work <- evs[i:min(i+batch, len(evs))]
	}
	close(work)
	wg.Wait()
	secs := time.Since(start).Seconds()
	close(errc)
	if err := <-errc; err != nil {
		return copyResult{}, err
	}
	var size int64
	if err := st.Pool.QueryRow(ctx, `SELECT sum(pg_total_relation_size(inhrelid)) FROM pg_inherits WHERE inhparent = 'events'::regclass`).Scan(&size); err != nil {
		return copyResult{}, err
	}
	return copyResult{secs, float64(size) / float64(len(evs))}, nil
}
