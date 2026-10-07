package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// Plausible CE v3.2.1: Stats API v2, POST /api/v2/query, one query per panel.
// Panels map to the dashboard's dimensions; top lists use limit 50 (as the
// prototype). Plausible CE has no funnels (EE only).
type plausible struct {
	base, key string
	site      int
}

func newPlausible(site int, tz map[int]string) Product {
	k, err := os.ReadFile(env("PLAUSIBLE_API_KEY_FILE", "data/plausible-api-key"))
	if err != nil {
		log.Fatal(err)
	}
	return &plausible{base: env("PLAUSIBLE_URL", "http://127.0.0.1:8000"), key: strings.TrimSpace(string(k)), site: site}
}

func (p *plausible) Name() string { return "plausible" }
func (p *plausible) Info() map[string]any {
	return map[string]any{"base": p.base, "site_id": fmt.Sprintf("site%d.test", p.site), "version": "CE v3.2.1", "api": "POST /api/v2/query"}
}

func plFilters(f []Filter) ([]any, string) {
	var out []any
	var notes []string
	for _, x := range f {
		switch x.Key {
		case "country":
			out = append(out, []any{"is", "visit:country", []string{x.Value}})
		case "source":
			v := x.Value
			if v == "" {
				v = "Direct / None"
			}
			out = append(out, []any{"is", "visit:source", []string{v}})
		case "path":
			out = append(out, []any{"is", "event:page", []string{x.Value}})
		default:
			out = append(out, []any{"is", "visit:" + x.Key, []string{x.Value}})
		}
	}
	return out, strings.Join(notes, "; ")
}

func (p *plausible) req(site int, q map[string]any) Req {
	q["site_id"] = fmt.Sprintf("site%d.test", site)
	b, _ := json.Marshal(q)
	return Req{Method: "POST", URL: p.base + "/api/v2/query", Body: b,
		Header: map[string]string{"Authorization": "Bearer " + p.key, "Content-Type": "application/json"}}
}

func (p *plausible) Panel(panel, from, to string, f []Filter) (Req, bool, string) {
	filters, note := plFilters(f)
	q := map[string]any{"date_range": []string{from, to}}
	if len(filters) > 0 {
		q["filters"] = filters
	}
	top := func(dim string) (Req, bool, string) {
		q["dimensions"] = []string{dim}
		q["metrics"] = []string{"visitors", "pageviews"}
		if dim == "visit:entry_page" || dim == "visit:exit_page" {
			// pageviews can't be combined with entry/exit dimensions; the dashboard
			// shows unique entrances/exits (visits) there.
			q["metrics"] = []string{"visitors", "visits"}
		}
		q["pagination"] = map[string]int{"limit": 50}
		return p.req(p.site, q), true, note
	}
	switch panel {
	case "overview":
		q["metrics"] = []string{"visitors", "visits", "pageviews", "bounce_rate", "visit_duration"}
		return p.req(p.site, q), true, note
	case "timeseries":
		q["metrics"] = []string{"visitors", "pageviews"}
		q["dimensions"] = []string{"time:day"}
		return p.req(p.site, q), true, note
	case "pages":
		return top("event:page")
	case "sources":
		return top("visit:source")
	case "countries":
		return top("visit:country")
	case "browsers":
		return top("visit:browser")
	case "os":
		return top("visit:os")
	case "devices":
		return top("visit:device")
	case "entry":
		return top("visit:entry_page")
	case "exit":
		return top("visit:exit_page")
	case "funnel":
		return Req{}, false, "Plausible CE has no funnels (Enterprise/cloud only)"
	case "props":
		// Custom-property breakdown of goal "signup" by prop "plan" (Stats API v2:
		// event:props:plan, filtered by event:goal). Needs the goal to exist.
		q["dimensions"] = []string{"event:props:plan"}
		q["metrics"] = []string{"visitors", "events"}
		q["filters"] = append(filters, []any{"is", "event:goal", []string{"signup"}})
		return p.req(p.site, q), true, note
	}
	return Req{}, false, ""
}

func (p *plausible) Overview(ctx context.Context, site int, date string) (Overview, error) {
	b, _, err := do(ctx, p.req(site, map[string]any{"date_range": []string{date, date},
		"metrics": []string{"visitors", "visits", "pageviews", "bounce_rate", "visit_duration"}}))
	if err != nil {
		return Overview{}, err
	}
	var r struct {
		Results []struct{ Metrics []float64 }
	}
	if err := json.Unmarshal(b, &r); err != nil || len(r.Results) == 0 || len(r.Results[0].Metrics) < 5 {
		return Overview{}, fmt.Errorf("unexpected response %.300s", b)
	}
	m := r.Results[0].Metrics
	// bounce_rate is a rounded %, visit_duration a rounded mean (s): both are
	// converted back to totals, so they carry Plausible's rounding.
	return Overview{Visitors: f64(m[0]), Visits: f64(m[1]), Pageviews: f64(m[2]),
		Bounces: f64(m[3] / 100 * m[1]), DurationSumS: f64(m[4] * m[1]), Raw: b}, nil
}
