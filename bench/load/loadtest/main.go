// Command loadtest replays gen NDJSON (stdin) through an incumbent's real HTTP
// tracking API at a fixed open-loop rate, like the prototype's loadtest
// (bench/proto/internal/benchq/loadtest.go): scheduled send times, latency
// measured from the scheduled time (no coordinated omission), client backlog
// counted as overflow. Timestamps in the stream are ignored (the APIs use server
// time); ip/ua/path/ref/props come from the stream. Throwaway benchmark code.
//
//	gen --tier S --days 1 --start 2026-10-01 --out - | loadtest -product umami -rate 1000 -duration 5m -out f.json
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"

	"sync"
	"sync/atomic"
	"time"

	"panel/bench/load/internal/rec"
)

type siteMap []struct {
	Site int    `json:"site"`
	Host string `json:"host"`
	ID   any    `json:"id"`
}

func screenH(r *rec.Record) int {
	switch r.Device {
	case "mobile":
		return r.ScreenW * 216 / 100
	case "tablet":
		return r.ScreenW * 4 / 3
	}
	return r.ScreenW * 9 / 16
}

func utmQuery(r *rec.Record) string {
	v := url.Values{}
	if r.UTMSource != "" {
		v.Set("utm_source", r.UTMSource)
	}
	if r.UTMMedium != "" {
		v.Set("utm_medium", r.UTMMedium)
	}
	if r.UTMCampaign != "" {
		v.Set("utm_campaign", r.UTMCampaign)
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// build returns the request for one event.
type builder func(r *rec.Record) (*http.Request, error)

func main() {
	product := flag.String("product", "", "umami|plausible|goatcounter")
	base := flag.String("url", "", "base URL (default per product)")
	rate := flag.Int("rate", 1000, "target events/s")
	dur := flag.Duration("duration", 60*time.Second, "test length")
	conc := flag.Int("concurrency", 256, "client workers")
	timeout := flag.Duration("timeout", 5*time.Second, "per-request timeout")
	sitesF := flag.String("sites", "", "product site map (bench/rig/*-sites.json)")
	out := flag.String("out", "", "output JSON")
	label := flag.String("label", "", "label")
	pagesOnly := flag.Bool("pageviews-only", false, "skip custom events (GoatCounter counts only pageviews)")
	flag.Parse()
	var sm siteMap
	b, err := os.ReadFile(*sitesF)
	if err != nil {
		log.Fatal(err)
	}
	json.Unmarshal(b, &sm)
	ids := map[int]string{}
	for _, s := range sm {
		ids[s.Site] = fmt.Sprint(s.ID)
	}
	var build builder
	switch *product {
	case "umami":
		if *base == "" {
			*base = "http://127.0.0.1:3030"
		}
		build = func(r *rec.Record) (*http.Request, error) {
			p := map[string]any{"website": ids[r.Site], "hostname": r.Host, "url": r.Path + utmQuery(r), "referrer": r.Ref,
				"title": "", "screen": fmt.Sprintf("%dx%d", r.ScreenW, screenH(r)), "language": "en-US"}
			if !r.IsPageview() {
				p["name"] = r.Name
				p["data"] = r.Props
			}
			body, _ := json.Marshal(map[string]any{"type": "event", "payload": p})
			req, err := http.NewRequest("POST", *base+"/api/send", bytes.NewReader(body))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
			}
			return req, err
		}
	case "plausible":
		if *base == "" {
			*base = "http://127.0.0.1:8000"
		}
		build = func(r *rec.Record) (*http.Request, error) {
			p := map[string]any{"n": r.Name, "u": "https://" + r.Host + r.Path + utmQuery(r), "d": r.Host, "r": r.Ref, "w": r.ScreenW}
			if !r.IsPageview() {
				p["p"] = r.Props
			}
			body, _ := json.Marshal(p)
			req, err := http.NewRequest("POST", *base+"/api/event", bytes.NewReader(body))
			if err == nil {
				req.Header.Set("Content-Type", "text/plain")
			}
			return req, err
		}
	case "goatcounter":
		if *base == "" {
			*base = "http://127.0.0.1:8083"
		}
		build = func(r *rec.Record) (*http.Request, error) {
			q := url.Values{}
			if r.IsPageview() {
				q.Set("p", r.Path+utmQuery(r))
			} else {
				q.Set("p", r.Name)
				q.Set("e", "true")
			}
			q.Set("r", r.Ref)
			q.Set("s", fmt.Sprintf("%d,%d,1", r.ScreenW, screenH(r)))
			q.Set("t", "")
			req, err := http.NewRequest("GET", *base+"/count?"+q.Encode(), nil)
			if err == nil {
				req.Host = r.Host
			}
			return req, err
		}
	default:
		log.Fatalf("unknown product %q", *product)
	}

	// Feed: decoded records in stream order.
	feed := make(chan *rec.Record, 1<<16)
	go func() {
		defer close(feed)
		rec.Decode(os.Stdin, 4, func(rs []rec.Record) error {
			for i := range rs {
				if *pagesOnly && !rs[i].IsPageview() {
					continue
				}
				feed <- &rs[i]
			}
			return nil
		})
	}()

	tr := &http.Transport{MaxIdleConns: *conc, MaxIdleConnsPerHost: *conc, IdleConnTimeout: 30 * time.Second,
		DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}
	client := &http.Client{Transport: tr, Timeout: *timeout}
	type job struct {
		sched time.Time
		r     *rec.Record
	}
	jobs := make(chan job, *rate) // ≤ 1 s of backlog
	var codes sync.Map
	var mu sync.Mutex
	var lats, svc []time.Duration
	var sent, overflow, feedEmpty atomic.Int64
	var errSample atomic.Value
	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	var wg sync.WaitGroup
	start := time.Now()
	for range *conc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]time.Duration, 0, 4096)
			lsvc := make([]time.Duration, 0, 4096)
			for j := range jobs {
				req, err := build(j.r)
				if err != nil {
					log.Fatal(err)
				}
				req.Header.Set("User-Agent", j.r.UA)
				req.Header.Set("X-Forwarded-For", j.r.IP)
				t0 := time.Now()
				resp, err := client.Do(req)
				code := 0
				if err == nil {
					body, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					code = resp.StatusCode
					if code/100 != 2 && errSample.Load() == nil {
						errSample.Store(fmt.Sprintf("HTTP %d: %.200s", code, body))
					}
				} else if errSample.Load() == nil {
					errSample.Store(err.Error())
				}
				now := time.Now()
				local = append(local, now.Sub(j.sched))
				lsvc = append(lsvc, now.Sub(t0))
				c, _ := codes.LoadOrStore(code, new(atomic.Int64))
				c.(*atomic.Int64).Add(1)
				sent.Add(1)
			}
			mu.Lock()
			lats = append(lats, local...)
			svc = append(svc, lsvc...)
			mu.Unlock()
		}()
	}
	interval := time.Second / time.Duration(*rate)
	next := start
	// per-second progress for steady-state checks
	var perSec []int64
	var last int64
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
loop:
	for {
		now := time.Now()
		for !next.After(now) {
			r, ok := <-feed
			if !ok {
				feedEmpty.Add(1)
				break loop
			}
			select {
			case jobs <- job{next, r}:
			default:
				overflow.Add(1)
			}
			next = next.Add(interval)
		}
		select {
		case <-ctx.Done():
			break loop
		case <-tick.C:
			s := sent.Load()
			perSec = append(perSec, s-last)
			last = s
		case <-time.After(time.Until(next)):
		}
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	cm := map[string]int64{}
	var ok int64
	codes.Range(func(k, v any) bool {
		n := v.(*atomic.Int64).Load()
		cm[fmt.Sprint(k)] = n
		if c := k.(int); c/100 == 2 {
			ok += n
		}
		return true
	})
	dist := func(d []time.Duration) map[string]float64 {
		if len(d) == 0 {
			return nil
		}
		slices.Sort(d)
		q := func(p float64) float64 {
			i := int(p*float64(len(d))+0.999999) - 1
			return float64(d[max(0, min(len(d)-1, i))].Microseconds()) / 1000
		}
		return map[string]float64{"p50_ms": q(0.5), "p95_ms": q(0.95), "p99_ms": q(0.99), "max_ms": float64(d[len(d)-1].Microseconds()) / 1000}
	}
	res := map[string]any{"kind": "incumbent http ingest loadtest", "product": *product, "url": *base, "label": *label,
		"target_rate": *rate, "duration_s": elapsed, "concurrency": *conc, "timeout_s": timeout.Seconds(),
		"sent": sent.Load(), "ok_2xx": ok, "codes": cm, "client_overflow": overflow.Load(),
		"achieved_rate": float64(sent.Load()) / elapsed, "ok_rate": float64(ok) / elapsed,
		"latency_from_schedule": dist(lats), "latency_service": dist(svc), "per_second_sent": perSec,
		"measured_at": time.Now().UTC().Format(time.RFC3339), "feed_exhausted": feedEmpty.Load() > 0}
	if s := errSample.Load(); s != nil {
		res["error_sample"] = s
	}
	j, _ := json.MarshalIndent(res, "", " ")
	if *out != "" {
		os.WriteFile(*out, j, 0o644)
	}
	l := res["latency_from_schedule"].(map[string]float64)
	fmt.Fprintf(os.Stderr, "%s rate=%d achieved=%.0f ok=%.0f/s overflow=%d codes=%v p50=%.1f p99=%.1f max=%.1f ms\n",
		*product, *rate, res["achieved_rate"], res["ok_rate"], overflow.Load(), cm, l["p50_ms"], l["p99_ms"], l["max_ms"])

}
