// Command umami bulk-loads the gen NDJSON stream (stdin) into Umami 3.4.0's PG
// tables website_event, session and event_data via COPY, bypassing HTTP (the
// scorecard allows this for history). Throwaway benchmark code.
//
// Identity, emulating what Umami's own /api/send would store:
//   - session_id (Umami's "visitor"): a UUID from (website_id, ip, ua, UTC month),
//     like Umami's monthly-salted session hash. One session row per session_id.
//   - visit_id: a UUID from the ground-truth session key sk (CONTRACT allows sk
//     for direct loaders). Umami's live ingest would split visits 30 min after
//     the visit start / at clock hours instead; see NOTES.md.
//   - event_id: a UUID from (sk, ts), uniformly distributed like Umami's v4 ids.
//
// Value formats were probed through the real /api/send (NOTES.md): referrer_domain
// is the host without "www." and NULL for internal/none, url_query is ” when
// empty, utm_* are NULL when empty, browser/os labels are Umami's.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"panel/bench/load/internal/pgcopy"
	"panel/bench/load/internal/rec"
)

type site struct {
	Site int    `json:"site"`
	Host string `json:"host"`
	ID   string `json:"id"`
}

func uuidFrom(version byte, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	var s [32]byte
	h.Sum(s[:0])
	b := s[:16]
	b[6] = (b[6] & 0x0f) | version<<4
	b[8] = (b[8] & 0x3f) | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}

// Umami (detect-browser) labels, as probed via /api/send.
func browser(r *rec.Record) string {
	ios := r.OS == "iOS"
	switch r.Browser {
	case "Chrome":
		if ios {
			return "crios"
		}
		return "chrome"
	case "Safari":
		if ios {
			return "ios"
		}
		return "safari"
	case "Firefox":
		if ios {
			return "fxios"
		}
		return "firefox"
	case "Edge":
		if ios {
			return "edge-ios"
		}
		return "edge-chromium"
	case "Samsung Internet":
		return "samsung"
	case "Opera":
		return "opera"
	}
	return strings.ToLower(r.Browser)
}

func osName(s string) string {
	switch s {
	case "Android":
		return "Android OS"
	case "Windows":
		return "Windows 10"
	case "macOS":
		return "Mac OS"
	}
	return s
}

func screen(r *rec.Record) string {
	h := r.ScreenW * 9 / 16
	switch r.Device {
	case "mobile":
		h = r.ScreenW * 216 / 100
	case "tablet":
		h = r.ScreenW * 4 / 3
	}
	return fmt.Sprintf("%dx%d", r.ScreenW, h)
}

func main() {
	sitesF := flag.String("sites", "../rig/umami-sites.json", "umami site map")
	dsn := flag.String("dsn", os.Getenv("UMAMI_DSN"), "PG DSN for the umami db")
	conns := flag.Int("conns", 4, "COPY connections for website_event")
	batch := flag.Int("batch", 50000, "rows per COPY statement")
	flag.Parse()
	var sites []site
	b, err := os.ReadFile(*sitesF)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, &sites); err != nil {
		log.Fatal(err)
	}
	wid := map[int]string{}
	for _, s := range sites {
		wid[s.Site] = s.ID
	}
	ctx := context.Background()
	ev, err := pgcopy.NewWriter(ctx, *dsn, "website_event",
		"event_id,website_id,session_id,visit_id,created_at,url_path,url_query,referrer_path,referrer_query,referrer_domain,page_title,event_type,event_name,utm_source,utm_medium,utm_campaign,hostname", *conns)
	if err != nil {
		log.Fatal(err)
	}
	se, err := pgcopy.NewWriter(ctx, *dsn, "session",
		"session_id,website_id,browser,os,device,screen,language,country,created_at", 1)
	if err != nil {
		log.Fatal(err)
	}
	ed, err := pgcopy.NewWriter(ctx, *dsn, "event_data",
		"event_data_id,website_id,website_event_id,data_key,string_value,data_type,created_at", 1)
	if err != nil {
		log.Fatal(err)
	}
	seen := make(map[[16]byte]struct{}, 8<<20)
	evb, seb, edb := ev.Get(), se.Get(), ed.Get()
	start := time.Now()
	var n, nsess, ndata int64
	err = rec.Decode(os.Stdin, 0, func(rs []rec.Record) error {
		for i := range rs {
			r := &rs[i]
			w, ok := wid[r.Site]
			if !ok {
				return fmt.Errorf("site %d not in %s", r.Site, *sitesF)
			}
			month := r.TS[:7] // Umami's salt is per (UTC) month
			sid := uuidFrom(5, "s", w, r.IP, r.UA, month)
			var key [16]byte
			hex.Decode(key[:], []byte(strings.ReplaceAll(sid, "-", "")))
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				seb.Begin().Str(true, sid).Str(false, w).Str(false, browser(r)).Str(false, osName(r.OS)).
					Str(false, r.Device).Str(false, screen(r)).Str(false, "en-US").Str(false, r.Country).Str(false, r.TS).End()
				nsess++
				if seb.Rows >= *batch {
					se.Send(seb)
					seb = se.Get()
				}
			}
			eid := uuidFrom(4, "e", r.SK, r.TS)
			vid := uuidFrom(5, "v", r.SK)
			host, rpath, rquery := r.RefParts()
			internal := host == r.Host
			host = strings.TrimPrefix(host, "www.")
			var q string
			if r.UTMSource != "" || r.UTMMedium != "" || r.UTMCampaign != "" {
				var parts []string
				if r.UTMSource != "" {
					parts = append(parts, "utm_source="+r.UTMSource)
				}
				if r.UTMMedium != "" {
					parts = append(parts, "utm_medium="+r.UTMMedium)
				}
				if r.UTMCampaign != "" {
					parts = append(parts, "utm_campaign="+r.UTMCampaign)
				}
				q = strings.Join(parts, "&")
			}
			evType := int64(1)
			evName := ""
			if !r.IsPageview() {
				evType, evName = 2, r.Name
			}
			evb.Begin().Str(true, eid).Str(false, w).Str(false, sid).Str(false, vid).Str(false, r.TS).
				Str(false, r.Path).Str(false, q)
			if r.Ref == "" {
				evb.Null(false).Null(false).Null(false)
			} else {
				evb.Str(false, rpath).Str(false, rquery)
				if internal || host == "" {
					evb.Null(false)
				} else {
					evb.Str(false, host)
				}
			}
			evb.Null(false).Int(false, evType).StrN(false, evName).
				StrN(false, r.UTMSource).StrN(false, r.UTMMedium).StrN(false, r.UTMCampaign).Str(false, r.Host).End()
			if evb.Rows >= *batch {
				ev.Send(evb)
				evb = ev.Get()
			}
			for k, v := range r.Props {
				edb.Begin().Str(true, uuidFrom(4, "d", eid, k)).Str(false, w).Str(false, eid).Str(false, k).
					Str(false, v).Int(false, 1).Str(false, r.TS).End()
				ndata++
			}
			if edb.Rows >= *batch {
				ed.Send(edb)
				edb = ed.Get()
			}
			n++
			if n%5_000_000 == 0 {
				el := time.Since(start).Seconds()
				log.Printf("%d events (%.0f/s), %d sessions, %d event_data", n, float64(n)/el, nsess, ndata)
			}
		}
		return ev.Err()
	})
	if err != nil {
		log.Fatal(err)
	}
	ev.Send(evb)
	se.Send(seb)
	ed.Send(edb)
	for _, w := range []*pgcopy.Writer{ev, se, ed} {
		if err := w.Close(); err != nil {
			log.Fatal(err)
		}
	}
	el := time.Since(start).Seconds()
	out := map[string]any{"events": ev.Rows.Load(), "sessions": se.Rows.Load(), "event_data": ed.Rows.Load(),
		"seconds": el, "events_per_s": float64(ev.Rows.Load()) / el}
	j, _ := json.Marshal(out)
	fmt.Println(string(j))
}
