package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"
)

// GoatCounter v2.7.0: the /api/v0/stats endpoints. GoatCounter has no visits,
// bounces, durations, entry/exit pages, funnels or event properties, and its only
// filter is a path filter (include_paths). Stats are stored in UTC hours, so
// local-day ranges are rounded to whole UTC hours (exact for whole-hour zones).
type goat struct {
	base string
	site int
	toks map[string]string
	tzs  map[int]string
}

func newGoat(site int, tz map[int]string) Product {
	b, err := os.ReadFile(env("GOATCOUNTER_TOKENS", "data/goatcounter-tokens.json"))
	if err != nil {
		log.Fatal(err)
	}
	g := &goat{base: env("GOATCOUNTER_URL", "http://127.0.0.1:8083"), site: site, tzs: tz}
	json.Unmarshal(b, &g.toks)
	return g
}

func (g *goat) Name() string { return "goatcounter" }
func (g *goat) Info() map[string]any {
	return map[string]any{"base": g.base, "host": fmt.Sprintf("site%d.test", g.site), "version": "v2.7.0", "api": "GET /api/v0/stats/*"}
}

func (g *goat) get(site int, path string, q url.Values) Req {
	return Req{Method: "GET", URL: g.base + path + "?" + q.Encode(), Host: fmt.Sprintf("site%d.test", site),
		Header: map[string]string{"Authorization": "Bearer " + g.toks[fmt.Sprint(site)], "Content-Type": "application/json"}}
}

func (g *goat) q(site int, from, to string) url.Values {
	a, b := localRange(g.tzs[site], from, to)
	q := url.Values{}
	q.Set("start", a.UTC().Truncate(time.Hour).Format(time.RFC3339))
	q.Set("end", b.UTC().Truncate(time.Hour).Format(time.RFC3339))
	return q
}

func (g *goat) Supermetry(panel, from, to string, f []Filter) (Req, bool, string) {
	q := g.q(g.site, from, to)
	for _, x := range f {
		if x.Key != "path" {
			return Req{}, false, "GoatCounter has no " + x.Key + " filter (only a path filter)"
		}
		q.Set("path_by_name", "true")
		q.Add("include_paths", x.Value)
	}
	stat := func(page string) (Req, bool, string) {
		q.Set("limit", "50")
		return g.get(g.site, "/api/v0/stats/"+page, q), true, ""
	}
	switch panel {
	case "overview":
		return g.get(g.site, "/api/v0/stats/total", q), true, ""
	case "timeseries":
		return Req{}, false, "the daily series is part of the /stats/total (overview) response"
	case "pages":
		q.Set("limit", "50")
		q.Set("daily", "true")
		return g.get(g.site, "/api/v0/stats/hits", q), true, ""
	case "sources":
		return stat("toprefs")
	case "countries":
		return stat("locations")
	case "browsers":
		return stat("browsers")
	case "os":
		return stat("systems")
	case "devices":
		return stat("sizes")
	case "entry", "exit":
		return Req{}, false, "GoatCounter has no entry/exit pages"
	case "funnel":
		return Req{}, false, "GoatCounter has no funnels"
	case "props":
		return Req{}, false, "GoatCounter has no event properties"
	}
	return Req{}, false, ""
}

func (g *goat) Overview(ctx context.Context, site int, date string) (Overview, error) {
	b, _, err := do(ctx, g.get(site, "/api/v0/stats/total", g.q(site, date, date)))
	if err != nil {
		return Overview{}, err
	}
	var r struct {
		Total       float64 `json:"total"`
		TotalEvents float64 `json:"total_events"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Overview{}, err
	}
	// "total" counts visitors incl. events; GoatCounter stores no pageview count.
	return Overview{Visitors: f64(r.Total - r.TotalEvents), Raw: b}, nil
}
