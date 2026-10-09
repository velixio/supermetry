// Command gcexpect computes what GoatCounter's hit_counts total should be per
// site for the imported stream: distinct (visitor-day session, path or event
// name) pairs, i.e. GoatCounter's "first visit" per session and path, given the
// session key the importer sends. Used to quantify lost aggregate batches.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"supermetry/bench/load/internal/rec"
)

func main() {
	sitesF := flag.String("sites", "data/sites.json", "gen sites.json")
	only := flag.String("only-site", "1,3,5", "sites")
	flag.Parse()
	var sites []struct {
		ID       int    `json:"id"`
		Timezone string `json:"timezone"`
	}
	b, _ := os.ReadFile(*sitesF)
	json.Unmarshal(b, &sites)
	locs := map[int]*time.Location{}
	for _, s := range sites {
		locs[s.ID], _ = time.LoadLocation(s.Timezone)
	}
	want := map[int]bool{}
	for _, f := range strings.Split(*only, ",") {
		var n int
		fmt.Sscan(f, &n)
		want[n] = true
	}
	seen := map[string]struct{}{}
	pages, events := map[int]int{}, map[int]int{}
	rec.Decode(os.Stdin, 0, func(rs []rec.Record) error {
		for i := range rs {
			r := &rs[i]
			if !want[r.Site] {
				continue
			}
			day := r.Time.In(locs[r.Site]).Format("2006-01-02")
			p := r.Path
			if !r.IsPageview() {
				p = "\x01" + r.Name
			}
			k := fmt.Sprintf("%d|%s|%s|%s", r.Site, r.VK, day, p)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			if r.IsPageview() {
				pages[r.Site]++
			} else {
				events[r.Site]++
			}
		}
		return nil
	})
	out := map[string]any{}
	for s := range want {
		out[fmt.Sprint(s)] = map[string]int{"page_visitors": pages[s], "event_visitors": events[s], "total": pages[s] + events[s]}
	}
	j, _ := json.Marshal(out)
	fmt.Println(string(j))
}
