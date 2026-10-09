package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
)

// Umami 3.4.0: the dashboard's stats / pageviews / metrics endpoints, the
// funnel report and event-data values (bench/load/NOTES.md for probing).
type umami struct {
	base, token, wid, tz string
	site                 int
	tzs                  map[int]string
	ids                  map[int]string
	srcSubst             string
}

func newUmami(site int, tz map[int]string) Product {
	u := &umami{base: env("UMAMI_URL", "http://127.0.0.1:3030"), site: site, tzs: tz, tz: tz[site], ids: map[int]string{},
		srcSubst: env("UMAMI_DIRECT_SUBST", "google.com")}
	b, err := os.ReadFile(env("UMAMI_SITES", "../rig/umami-sites.json"))
	if err != nil {
		log.Fatal(err)
	}
	var s []struct {
		Site int    `json:"site"`
		ID   string `json:"id"`
	}
	json.Unmarshal(b, &s)
	for _, x := range s {
		u.ids[x.Site] = x.ID
	}
	u.wid = u.ids[site]
	body, _ := json.Marshal(map[string]string{"username": os.Getenv("UMAMI_ADMIN_USER"), "password": os.Getenv("UMAMI_ADMIN_PASSWORD")})
	resp, _, err := do(context.Background(), Req{Method: "POST", URL: u.base + "/api/auth/login", Body: body, Header: map[string]string{"Content-Type": "application/json"}})
	if err != nil {
		log.Fatalf("umami login: %v", err)
	}
	var t struct{ Token string }
	json.Unmarshal(resp, &t)
	u.token = t.Token
	return u
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (u *umami) Name() string { return "umami" }
func (u *umami) Info() map[string]any {
	return map[string]any{"base": u.base, "website_id": u.wid, "timezone": u.tz, "version": "3.4.0"}
}

func (u *umami) hdr() map[string]string {
	return map[string]string{"Authorization": "Bearer " + u.token, "Content-Type": "application/json"}
}

func (u *umami) query(wid, tz, from, to string, f []Filter) (url.Values, string) {
	a, b := localRange(tz, from, to)
	q := url.Values{}
	q.Set("startAt", fmt.Sprint(a.UnixMilli()))
	q.Set("endAt", fmt.Sprint(b.UnixMilli()))
	q.Set("timezone", tz)
	var notes []string
	for _, x := range f {
		switch x.Key {
		case "country":
			q.Set("country", "eq."+x.Value)
		case "path":
			q.Set("path", "eq."+x.Value)
		case "source":
			v := strings.TrimPrefix(x.Value, "www.")
			if v == "" {
				v = u.srcSubst
				notes = append(notes, "Umami cannot filter direct traffic (no referrer filter for empty/none: eq. and ns. fail or match nothing); substituted referrer=eq."+v)
			}
			q.Set("referrer", "eq."+v)
		default:
			q.Set(x.Key, "eq."+x.Value)
		}
	}
	return q, strings.Join(notes, "; ")
}

func (u *umami) Supermetry(p, from, to string, f []Filter) (Req, bool, string) {
	q, note := u.query(u.wid, u.tz, from, to, f)
	w := u.base + "/api/websites/" + u.wid
	get := func(path string) Req { return Req{Method: "GET", URL: w + path + "?" + q.Encode(), Header: u.hdr()} }
	metric := func(t string) Req {
		q.Set("type", t)
		q.Set("limit", "50")
		return get("/metrics")
	}
	switch p {
	case "overview":
		return get("/stats"), true, note
	case "timeseries":
		q.Set("unit", "day")
		return get("/pageviews"), true, note
	case "pages":
		return metric("path"), true, note
	case "sources":
		return metric("referrer"), true, note
	case "countries":
		return metric("country"), true, note
	case "browsers":
		return metric("browser"), true, note
	case "os":
		return metric("os"), true, note
	case "devices":
		return metric("device"), true, note
	case "entry":
		return metric("entry"), true, note
	case "exit":
		return metric("exit"), true, note
	case "funnel":
		a, b := localRange(u.tz, from, to)
		body, _ := json.Marshal(map[string]any{"websiteId": u.wid, "type": "funnel", "filters": map[string]any{},
			"timezone": u.tz,
			"parameters": map[string]any{"startDate": a.UTC().Format("2006-01-02T15:04:05.000Z"), "endDate": b.UTC().Format("2006-01-02T15:04:05.000Z"),
				"window": 60, "steps": []map[string]string{{"type": "path", "value": "/"}, {"type": "path", "value": "/pricing"}, {"type": "event", "value": "signup"}}}})
		return Req{Method: "POST", URL: u.base + "/api/reports/funnel", Body: body, Header: u.hdr()}, true,
			"Umami funnel report, window 60 min (UI default) from the first step, not 'within one session'"
	case "props":
		q.Set("eventName", "signup")
		q.Set("propertyName", "plan")
		return get("/event-data/values"), true, note
	}
	return Req{}, false, ""
}

func (u *umami) Overview(ctx context.Context, site int, date string) (Overview, error) {
	q, _ := u.query(u.ids[site], u.tzs[site], date, date, nil)
	b, _, err := do(ctx, Req{Method: "GET", URL: u.base + "/api/websites/" + u.ids[site] + "/stats?" + q.Encode(), Header: u.hdr()})
	if err != nil {
		return Overview{}, err
	}
	var s struct{ Pageviews, Visitors, Visits, Bounces, Totaltime float64 }
	if err := json.Unmarshal(b, &s); err != nil {
		return Overview{}, err
	}
	return Overview{Visitors: f64(s.Visitors), Visits: f64(s.Visits), Pageviews: f64(s.Pageviews), Bounces: f64(s.Bounces),
		DurationSumS: f64(s.Totaltime), Raw: b}, nil
}
