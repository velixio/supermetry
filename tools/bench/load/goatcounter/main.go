// Command goatcounter imports the gen NDJSON stream (stdin) into GoatCounter
// v2.7.0 through its supported import path, the /api/v0/count API (the same API
// `goatcounter import` uses), with created_at set to the event time.
// GoatCounter then aggregates the hits itself (memstore, persisted every 10 s).
// Throwaway benchmark code; no GoatCounter code is copied.
//
//   - Host: siteN.test selects the site; each site has its own user, so each
//     needs its own API token (-tokens file: {"N": "token"}).
//   - location = the record's country (GoatCounter skips GeoIP when set).
//   - session = hash(site, vk, site-local date): a visitor-day key, so
//     GoatCounter's unique-visitor logic sees the truth's daily visitors. With
//     ip + user_agent instead, GoatCounter would derive sessions from its own
//     current salt (real time, not created_at).
//   - Custom events are sent as events (path = event name, event = true); props
//     are dropped (GoatCounter has no event properties).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supermetry/tools/bench/load/internal/rec"
)

type hit struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	Event     bool   `json:"event,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Query     string `json:"query,omitempty"`
	Size      string `json:"size,omitempty"` // "width" (the API's Floats type takes a string)
	UserAgent string `json:"user_agent"`
	Location  string `json:"location,omitempty"`
	CreatedAt string `json:"created_at"`
	Session   string `json:"session"`
}

type batch struct {
	site int
	hits []hit
}

func main() {
	base := flag.String("url", "http://127.0.0.1:8083", "GoatCounter base URL")
	tokensF := flag.String("tokens", "data/goatcounter-tokens.json", "site -> API token")
	sitesF := flag.String("sites", "data/sites.json", "gen sites.json (time zones)")
	per := flag.Int("batch", 100, "hits per /api/v0/count request")
	workers := flag.Int("workers", 1, "concurrent requests (v2.7.0 panics with >1)")
	onlySites := flag.String("only-site", "0", "import only these sites, comma-separated (0 = all)")
	flag.Parse()
	only := map[int]bool{}
	for _, f := range strings.Split(*onlySites, ",") {
		var n int
		fmt.Sscan(f, &n)
		if n != 0 {
			only[n] = true
		}
	}
	var toks map[string]string
	b, err := os.ReadFile(*tokensF)
	if err != nil {
		log.Fatal(err)
	}
	json.Unmarshal(b, &toks)
	var sites []struct {
		ID       int    `json:"id"`
		Timezone string `json:"timezone"`
	}
	b, _ = os.ReadFile(*sitesF)
	json.Unmarshal(b, &sites)
	locs := map[int]*time.Location{}
	for _, s := range sites {
		locs[s.ID], _ = time.LoadLocation(s.Timezone)
	}
	cl := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{MaxIdleConnsPerHost: *workers}}
	ch := make(chan batch, *workers*2)
	var sent, reqs, fails atomic.Int64
	var wg sync.WaitGroup
	for range *workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for bt := range ch {
				body, _ := json.Marshal(map[string]any{"hits": bt.hits, "filter": []string{}})
				for attempt := 0; ; attempt++ {
					req, _ := http.NewRequest("POST", *base+"/api/v0/count", bytes.NewReader(body))
					req.Host = fmt.Sprintf("site%d.test", bt.site)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer "+toks[fmt.Sprint(bt.site)])
					resp, err := cl.Do(req)
					if err == nil {
						rb, _ := io.ReadAll(resp.Body)
						resp.Body.Close()
						if resp.StatusCode/100 == 2 {
							break
						}
						err = fmt.Errorf("HTTP %d: %.300s", resp.StatusCode, rb)
					}
					if attempt >= 5 {
						log.Printf("site %d: giving up: %v", bt.site, err)
						fails.Add(int64(len(bt.hits)))
						break
					}
					log.Printf("site %d: retry %d: %v", bt.site, attempt+1, err)
					time.Sleep(time.Duration(attempt+1) * time.Second)
				}
				reqs.Add(1)
				sent.Add(int64(len(bt.hits)))
			}
		}()
	}
	pending := map[int][]hit{}
	start := time.Now()
	var n int64
	err = rec.Decode(os.Stdin, 0, func(rs []rec.Record) error {
		for i := range rs {
			r := &rs[i]
			if len(only) > 0 && !only[r.Site] {
				continue
			}
			day := r.Time.In(locs[r.Site]).Format("2006-01-02")
			sh := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", r.Site, r.VK, day)))
			h := hit{Title: "", UserAgent: r.UA, Location: r.Country, CreatedAt: r.TS, Session: hex.EncodeToString(sh[:12])}
			if r.IsPageview() {
				h.Path = r.Path
				h.Size = fmt.Sprint(r.ScreenW)
				if host, _, _ := rec.SplitURL(r.Ref); host != "" && host != r.Host {
					h.Ref = r.Ref
				}
				q := url.Values{}
				if r.UTMSource != "" {
					q.Set("utm_source", r.UTMSource)
				}
				if r.UTMMedium != "" {
					q.Set("utm_medium", r.UTMMedium)
				}
				if r.UTMCampaign != "" {
					q.Set("utm_campaign", r.UTMCampaign)
				}
				h.Query = q.Encode()
			} else {
				h.Path, h.Event = r.Name, true
			}
			pending[r.Site] = append(pending[r.Site], h)
			if len(pending[r.Site]) >= *per {
				ch <- batch{r.Site, pending[r.Site]}
				pending[r.Site] = nil
			}
			n++
			if n%1_000_000 == 0 {
				log.Printf("%d events read, %d sent (%.0f/s)", n, sent.Load(), float64(sent.Load())/time.Since(start).Seconds())
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	for s, hs := range pending {
		if len(hs) > 0 {
			ch <- batch{s, hs}
		}
	}
	close(ch)
	wg.Wait()
	el := time.Since(start).Seconds()
	fmt.Printf("{\"events\":%d,\"requests\":%d,\"failed_hits\":%d,\"seconds\":%.1f,\"events_per_s\":%.0f}\n", sent.Load(), reqs.Load(), fails.Load(), el, float64(sent.Load())/el)
}
