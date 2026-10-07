package benchq

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"panel/bench/proto/internal/core"
)

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0",
	"Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
}

type visitor struct{ ip, ua, host string }

// CmdLoadTest drives POST /api/event at a fixed open-loop rate and reports
// accepted events/s, latency (measured from the scheduled send time, so
// queueing in the client counts), status codes and server RSS.
func CmdLoadTest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("loadtest", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8090", "server")
	rate := fs.Int("rate", 10000, "target events/s")
	dur := fs.Duration("duration", 30*time.Second, "test length")
	conc := fs.Int("concurrency", 256, "client workers")
	nvis := fs.Int("visitors", 50000, "distinct visitors (ip+ua); with --churn the pool size")
	churn := fs.Bool("churn", false, "realistic churn: new visitors arrive at rate/3.3 per s, each active for ~3 min")
	sitesFile := fs.String("sites", "testdata/fixture_sites.json", "sites.json for hosts")
	out := fs.String("out", "../results", "results directory")
	label := fs.String("label", "", "results label")
	fs.Parse(args)
	sites, err := core.LoadSitesFile(*sitesFile)
	if err != nil {
		return err
	}
	r := rand.New(rand.NewPCG(1, 2))
	vis := make([]visitor, *nvis)
	for i := range vis {
		// Random public-looking IPv4 so GeoIP does real lookups.
		ip := fmt.Sprintf("%d.%d.%d.%d", 1+r.IntN(222), r.IntN(256), r.IntN(256), 1+r.IntN(254))
		if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "127.") {
			ip = "81" + ip[strings.Index(ip, "."):]
		}
		vis[i] = visitor{ip: ip, ua: userAgents[r.IntN(len(userAgents))], host: sites[r.IntN(len(sites))].Host}
	}
	tr := &http.Transport{MaxIdleConns: *conc, MaxIdleConnsPerHost: *conc, IdleConnTimeout: 30 * time.Second,
		DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	tokens := make(chan time.Time, *rate) // scheduled send times; ≤1 s of backlog
	var codes sync.Map
	var lats []time.Duration
	var mu sync.Mutex
	var sent, overflow atomic.Int64
	ctx, cancel := context.WithTimeout(ctx, *dur)
	defer cancel()

	var rss []int64
	var rssWG sync.WaitGroup
	rssWG.Go(func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if v := serverRSS(*url); v > 0 {
					rss = append(rss, v)
				}
			}
		}
	})
	var wg sync.WaitGroup
	start := time.Now()
	for w := range *conc {
		wg.Go(func() {
			rr := rand.New(rand.NewPCG(uint64(w), 3))
			body := &bytes.Buffer{}
			local := make([]time.Duration, 0, 4096)
			for sched := range tokens {
				v := vis[rr.IntN(len(vis))]
				if *churn {
					// Visitor index advances with time; pick among those who arrived in the last 3 min.
					arrivals := float64(*rate) / 3.3
					idx := int(sched.Sub(start).Seconds()*arrivals) - rr.IntN(int(180*arrivals))
					v = vis[((idx%len(vis))+len(vis))%len(vis)]
				}
				body.Reset()
				name, props := "pageview", ""
				if rr.IntN(10) == 0 {
					name, props = "signup", `,"p":{"plan":"pro"}`
				}
				fmt.Fprintf(body, `{"n":%q,"u":"https://%s/blog/post-%d","d":%q,"r":"https://www.google.com/"%s}`,
					name, v.host, rr.IntN(2000), v.host, props)
				req, _ := http.NewRequest("POST", *url+"/api/event", body)
				req.Header.Set("User-Agent", v.ua)
				req.Header.Set("X-Forwarded-For", v.ip)
				req.Header.Set("Content-Type", "text/plain")
				resp, err := client.Do(req)
				code := 0
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					code = resp.StatusCode
				}
				local = append(local, time.Since(sched))
				c, _ := codes.LoadOrStore(code, new(atomic.Int64))
				c.(*atomic.Int64).Add(1)
				sent.Add(1)
			}
			mu.Lock()
			lats = append(lats, local...)
			mu.Unlock()
		})
	}
	interval := time.Second / time.Duration(*rate)
	next := start
loop:
	for {
		now := time.Now()
		for !next.After(now) {
			select {
			case tokens <- next:
			default:
				overflow.Add(1) // client can't keep up: counted, not silently dropped
			}
			next = next.Add(interval)
		}
		select {
		case <-ctx.Done():
			break loop
		case <-time.After(time.Until(next)):
		}
	}
	close(tokens)
	wg.Wait()
	rssWG.Wait()
	elapsed := time.Since(start).Seconds()
	cm := map[string]int64{}
	codes.Range(func(k, v any) bool { cm[fmt.Sprint(k)] = v.(*atomic.Int64).Load(); return true })
	var maxRSS int64
	for _, v := range rss {
		maxRSS = max(maxRSS, v)
	}
	res := map[string]any{"kind": "panel-proto HTTP ingest load test", "measured_at": time.Now().UTC().Format(time.RFC3339),
		"target_rate": *rate, "duration_s": elapsed, "concurrency": *conc, "visitors": *nvis,
		"sent": sent.Load(), "client_overflow": overflow.Load(), "codes": cm,
		"accepted_per_s": float64(cm["202"]) / elapsed, "latency": DistOf(lats),
		"server_rss_kb_max": maxRSS, "server_rss_kb_samples": rss, "server_stats_after": serverStats(*url)}
	b, _ := json.MarshalIndent(map[string]any{"accepted_per_s": res["accepted_per_s"], "codes": cm, "latency": res["latency"], "rss_kb_max": maxRSS}, "", "  ")
	fmt.Fprintln(os.Stderr, string(b))
	return writeResult(*out, "proto-loadtest", *label, res)
}

func serverStats(url string) map[string]any {
	resp, err := http.Get(url + "/api/stats")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return m
}

func serverRSS(url string) int64 {
	m := serverStats(url)
	if v, ok := m["rss_kb"].(float64); ok {
		return int64(v)
	}
	return -1
}
