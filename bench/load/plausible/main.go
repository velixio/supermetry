// Command plausible bulk-loads the gen NDJSON stream (stdin) into Plausible CE
// v3.2.1's ClickHouse tables events_v2 and sessions_v2, bypassing /api/event
// (which uses server time; the scorecard allows direct history inserts).
// Throwaway benchmark code. No Plausible code or DDL is copied: only column names
// (the insert interface) and value formats probed through the real /api/event
// (see bench/load/NOTES.md).
//
//   - user_id = hash(site, vk, site-local date): a daily-rotating visitor id,
//     like Plausible's daily salt, but rotated at the site-local midnight so it
//     lines up with the truth's per-local-day visitors.
//   - session_id = hash(sk): the ground-truth session (CONTRACT allows sk for
//     direct loaders). One sessions_v2 row per session with sign = 1 and final
//     values (what a fully collapsed VersionedCollapsingMergeTree holds).
//   - Every event carries its session's referrer, source and UTMs (observed: the
//     live ingest copies them onto later events).
//   - Timestamps are whole seconds (DateTime), as Plausible stores them.
//
// Output: TabSeparated streams piped into `clickhouse-client` inside the CH container.
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"panel/bench/load/internal/rec"
)

var sourceNames = map[string]string{ // probed via /api/event on CE v3.2.1
	"www.google.com": "Google", "www.bing.com": "Bing", "duckduckgo.com": "DuckDuckGo",
	"l.facebook.com": "Facebook", "m.facebook.com": "Facebook", "t.co": "Twitter",
	"www.reddit.com": "Reddit", "www.linkedin.com": "LinkedIn", "news.ycombinator.com": "Hacker News",
	"github.com": "GitHub", "www.youtube.com": "Youtube", "stackoverflow.com": "StackOverflow",
	"l.instagram.com": "Instagram", "www.ecosia.org": "Ecosia", "www.perplexity.ai": "Perplexity",
	"www.baidu.com": "Baidu", "yandex.ru": "Yandex",
	// chatgpt.com, dev.to, medium.com and the blogs keep their host
}

var browsers = map[string]string{"Chrome": "Chrome", "Safari": "Safari", "Firefox": "Firefox",
	"Edge": "Microsoft Edge", "Samsung Internet": "Samsung Browser", "Opera": "Opera"}
var oses = map[string]string{"Android": "Android", "iOS": "iOS", "Windows": "Windows", "macOS": "Mac", "Linux": "GNU/Linux"}

func screenSize(w int) string {
	switch {
	case w < 576:
		return "Mobile"
	case w < 992:
		return "Tablet"
	case w < 1440:
		return "Laptop"
	}
	return "Desktop"
}

func h64(parts ...string) uint64 {
	h := fnv.New128a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	var b [16]byte
	h.Sum(b[:0])
	return binary.BigEndian.Uint64(b[:8]) ^ binary.BigEndian.Uint64(b[8:])
}

func esc(s string) string {
	if !strings.ContainsAny(s, "\\\t\n'") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "'", `\'`)
	return r.Replace(s)
}

func arr(xs []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range xs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('\'')
		b.WriteString(esc(x))
		b.WriteByte('\'')
	}
	b.WriteByte(']')
	return b.String()
}

type sess struct {
	site                             int
	host, user                       string
	uid, sid                         uint64
	start, last                      time.Time
	entry, exit                      string
	pageviews, events, customs       int
	ref, src, utmS, utmM, utmC       string
	country, screen, osName, browser string
}

func dt(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

const evCols = "timestamp,name,site_id,user_id,session_id,hostname,pathname,referrer,referrer_source,country_code,screen_size,operating_system,browser,utm_medium,utm_source,utm_campaign,meta.key,meta.value"
const seCols = "session_id,sign,site_id,user_id,hostname,timestamp,start,is_bounce,entry_page,exit_page,pageviews,events,duration,referrer,referrer_source,country_code,screen_size,operating_system,browser,utm_medium,utm_source,utm_campaign,exit_page_hostname"

func client(container, table, cols string) (*exec.Cmd, io.WriteCloser) {
	q := fmt.Sprintf("INSERT INTO %s (%s) FORMAT TabSeparated", table, cols)
	cmd := exec.Command("sudo", "docker", "exec", "-i", container, "clickhouse-client",
		"--database", "plausible_events_db", "--max_insert_block_size", "1048576", "--query", q)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	w, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
	return cmd, w
}

func main() {
	sitesF := flag.String("sites", "data/sites.json", "gen sites.json (time zones)")
	container := flag.String("container", "plausible-plausible_events_db-1", "ClickHouse container")
	flag.Parse()
	var sites []struct {
		ID       int    `json:"id"`
		Timezone string `json:"timezone"`
	}
	b, err := os.ReadFile(*sitesF)
	if err != nil {
		log.Fatal(err)
	}
	json.Unmarshal(b, &sites)
	locs := map[int]*time.Location{}
	for _, s := range sites {
		if locs[s.ID], err = time.LoadLocation(s.Timezone); err != nil {
			log.Fatal(err)
		}
	}
	evCmd, evPipe := client(*container, "events_v2", evCols)
	seCmd, sePipe := client(*container, "sessions_v2", seCols)
	ev := bufio.NewWriterSize(evPipe, 4<<20)
	se := bufio.NewWriterSize(sePipe, 1<<20)
	live := map[string]*sess{}
	var nev, nse int64
	emit := func(s *sess) {
		bounce := 0
		if s.pageviews == 1 && s.customs == 0 {
			bounce = 1
		}
		st, la := s.start.Truncate(time.Second), s.last.Truncate(time.Second)
		fmt.Fprintf(se, "%d\t1\t%d\t%d\t%s\t%s\t%s\t%d\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.sid, s.site, s.uid, esc(s.host), dt(la), dt(st), bounce, esc(s.entry), esc(s.exit), s.pageviews, s.events,
			int64(la.Sub(st)/time.Second), esc(s.ref), esc(s.src), s.country, s.screen, s.osName, s.browser,
			esc(s.utmM), esc(s.utmS), esc(s.utmC), esc(s.host))
		nse++
	}
	sweep := func(now time.Time) {
		for k, s := range live {
			if now.Sub(s.last) > 31*time.Minute {
				emit(s)
				delete(live, k)
			}
		}
	}
	start := time.Now()
	var lastSweep time.Time
	err = rec.Decode(os.Stdin, 0, func(rs []rec.Record) error {
		for i := range rs {
			r := &rs[i]
			s := live[r.SK]
			if s == nil {
				loc := locs[r.Site]
				day := r.Time.In(loc).Format("2006-01-02")
				s = &sess{site: r.Site, host: r.Host, uid: h64("u", fmt.Sprint(r.Site), r.VK, day), sid: h64("s", r.SK),
					start: r.Time, country: r.Country, screen: screenSize(r.ScreenW), osName: oses[r.OS], browser: browsers[r.Browser]}
				if s.osName == "" {
					s.osName = r.OS
				}
				if s.browser == "" {
					s.browser = r.Browser
				}
				host, path, _ := r.RefParts()
				if host != "" && host != r.Host {
					s.ref = strings.TrimPrefix(host, "www.")
					if path != "" && path != "/" {
						s.ref += path
					}
					s.src = sourceNames[host]
					if s.src == "" {
						s.src = host
					}
				}
				s.utmS, s.utmM, s.utmC = r.UTMSource, r.UTMMedium, r.UTMCampaign
				if r.UTMSource != "" {
					s.src = r.UTMSource
				}
				live[r.SK] = s
			}
			s.last = r.Time
			s.events++
			keys, vals := []string{}, []string{}
			if r.IsPageview() {
				s.pageviews++
				if s.entry == "" {
					s.entry = r.Path
				}
				s.exit = r.Path
			} else {
				s.customs++
				for k := range r.Props {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					vals = append(vals, r.Props[k])
				}
			}
			fmt.Fprintf(ev, "%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				dt(r.Time), esc(r.Name), r.Site, s.uid, s.sid, esc(r.Host), esc(r.Path), esc(s.ref), esc(s.src),
				r.Country, s.screen, s.osName, s.browser, esc(s.utmM), esc(s.utmS), esc(s.utmC), arr(keys), arr(vals))
			nev++
			if r.Time.Sub(lastSweep) > 5*time.Minute {
				sweep(r.Time)
				lastSweep = r.Time
			}
			if nev%5_000_000 == 0 {
				log.Printf("%d events, %d sessions (%.0f ev/s), live %d", nev, nse, float64(nev)/time.Since(start).Seconds(), len(live))
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, s := range live {
		emit(s)
	}
	ev.Flush()
	se.Flush()
	evPipe.Close()
	sePipe.Close()
	if err := evCmd.Wait(); err != nil {
		log.Fatal("events insert: ", err)
	}
	if err := seCmd.Wait(); err != nil {
		log.Fatal("sessions insert: ", err)
	}
	el := time.Since(start).Seconds()
	fmt.Printf("{\"events\":%d,\"sessions\":%d,\"seconds\":%.1f,\"events_per_s\":%.0f}\n", nev, nse, el, float64(nev)/el)
}
