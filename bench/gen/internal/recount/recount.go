// Package recount recomputes ground truth from an emitted NDJSON event stream.
//
// It is deliberately independent of the generator: it imports nothing from
// internal/synth, parses the stream with encoding/json, derives visitors from
// (ip, ua) per site per site-local day and sessions from a 30-minute idle
// timeout, exactly as a system under test would. It also checks the stream
// invariants the CONTRACT promises (global sort, gap rules, midnight rule,
// vk/sk consistency). Comparing its output with the generator's truth.json is
// the main correctness test of the generator.
package recount

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	idleTimeoutMs = 30 * 60 * 1000
	maxIntraMs    = 25 * 60 * 1000
	minInterMs    = 35 * 60 * 1000
	topN          = 50
	tsLayout      = "2006-01-02T15:04:05.000Z"
)

// Dims lists the breakdown dimensions of truth.json, in output order.
var Dims = []string{"path", "country", "source", "browser", "os", "device", "entry", "exit"}

// Plans lists the signup.plan values.
var Plans = []string{"free", "pro", "team"}

// fieldOrder is the exact key order of a CONTRACT event line.
var fieldOrder = []string{"ts", "site", "host", "path", "ref", "utm_source", "utm_medium", "utm_campaign",
	"name", "props", "ip", "ua", "screen_w", "country", "region", "city", "browser", "os", "device", "revenue", "vk", "sk"}

// Site is one entry of sites.json.
type Site struct {
	ID       int     `json:"id"`
	Host     string  `json:"host"`
	Timezone string  `json:"timezone"`
	Share    float64 `json:"share"`
}

// TopEntry is one value of a breakdown.
type TopEntry struct {
	Value     string `json:"value"`
	Visitors  int64  `json:"visitors"`
	Pageviews int64  `json:"pageviews"`
}

// Row is the truth for one (site, local date).
type Row struct {
	Site       int
	Date       string
	Visitors   int64
	Visits     int64
	Pageviews  int64
	Bounces    int64
	DurationMs int64
	Top        map[string][]TopEntry
	Funnel     [3]int64
	Plan       map[string]int64
}

// Options tunes Run.
type Options struct {
	// CheckKeyOrderEvery checks the exact JSON key order on every n-th line
	// (1 = every line, 0 = never).
	CheckKeyOrderEvery int
	// MaxViolations stops recording violations after this many (0 = 100).
	MaxViolations int
}

// Result is the outcome of Run.
type Result struct {
	Rows       []Row // sorted by (date, site)
	Violations []string
	Events     int64
}

// LoadSites parses sites.json.
func LoadSites(r io.Reader) ([]Site, error) {
	var s []Site
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return nil, fmt.Errorf("sites.json: %w", err)
	}
	return s, nil
}

type rawEvent struct {
	TS          string            `json:"ts"`
	Site        int               `json:"site"`
	Host        string            `json:"host"`
	Path        string            `json:"path"`
	Ref         string            `json:"ref"`
	UtmSource   string            `json:"utm_source"`
	UtmMedium   string            `json:"utm_medium"`
	UtmCampaign string            `json:"utm_campaign"`
	Name        string            `json:"name"`
	Props       map[string]string `json:"props"`
	IP          string            `json:"ip"`
	UA          string            `json:"ua"`
	ScreenW     int               `json:"screen_w"`
	Country     string            `json:"country"`
	Region      string            `json:"region"`
	City        string            `json:"city"`
	Browser     string            `json:"browser"`
	OS          string            `json:"os"`
	Device      string            `json:"device"`
	Revenue     json.RawMessage   `json:"revenue"`
	VK          string            `json:"vk"`
	SK          string            `json:"sk"`
}

// ev is the compact per-event state kept until a group is finalized.
type ev struct {
	ms       int64
	pageview bool
	signup   bool
	path     string
	source   string // only meaningful for a session's first event
	plan     string
	sk       string
}

type visitor struct {
	attrs  [4]string // country, browser, os, device
	screen int
	vk     string
	events []ev
}

type group struct {
	site     int
	date     string
	endMs    int64
	visitors map[string]*visitor
	order    []string // visitor keys in first-seen order (deterministic iteration)
}

type siteInfo struct {
	Site
	loc *time.Location
}

type state struct {
	opt      Options
	sites    map[int]*siteInfo
	groups   map[string]*group
	rows     []Row
	viol     []string
	nviol    int
	lastMs   int64
	lastSeen map[string]lastSeen // site|ip|ua -> last event, pruned
	skDay    map[string]string   // sk -> group key, pruned with groups
	skGroups map[string][]string // group key -> sks, for pruning
}

type lastSeen struct {
	ms int64
	sk string
}

// Run reads an NDJSON stream and recomputes the truth.
func Run(r io.Reader, sites []Site, opt Options) (*Result, error) {
	if opt.MaxViolations == 0 {
		opt.MaxViolations = 100
	}
	st := &state{
		opt:      opt,
		sites:    map[int]*siteInfo{},
		groups:   map[string]*group{},
		lastMs:   -1 << 62,
		lastSeen: map[string]lastSeen{},
		skDay:    map[string]string{},
		skGroups: map[string][]string{},
	}
	for _, s := range sites {
		loc, err := time.LoadLocation(s.Timezone)
		if err != nil {
			return nil, fmt.Errorf("site %d: %w", s.ID, err)
		}
		st.sites[s.ID] = &siteInfo{Site: s, loc: loc}
	}
	br := bufio.NewReaderSize(r, 1<<20)
	var n int64
	for {
		line, err := br.ReadBytes('\n')
		if len(line) == 0 && err == io.EOF {
			break
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		n++
		if line[len(line)-1] != '\n' {
			st.violate("line %d: missing trailing newline", n)
		} else {
			line = line[:len(line)-1]
		}
		if err := st.event(line, n); err != nil {
			return nil, err
		}
		if n%4096 == 0 {
			st.flush(st.lastMs, false)
		}
	}
	st.flush(0, true)
	sort.Slice(st.rows, func(i, j int) bool {
		if st.rows[i].Date != st.rows[j].Date {
			return st.rows[i].Date < st.rows[j].Date
		}
		return st.rows[i].Site < st.rows[j].Site
	})
	return &Result{Rows: st.rows, Violations: st.viol, Events: n}, nil
}

func (st *state) violate(format string, a ...any) {
	st.nviol++
	if len(st.viol) < st.opt.MaxViolations {
		st.viol = append(st.viol, fmt.Sprintf(format, a...))
	}
}

func checkKeyOrder(line []byte) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("not an object")
	}
	for i := 0; ; i++ {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if t == json.Delim('}') {
			if i != len(fieldOrder) {
				return fmt.Errorf("got %d keys, want %d", i, len(fieldOrder))
			}
			return nil
		}
		k, _ := t.(string)
		if i >= len(fieldOrder) || k != fieldOrder[i] {
			return fmt.Errorf("key %d is %q", i, k)
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
		}
	}
}

func (st *state) event(line []byte, n int64) error {
	if st.opt.CheckKeyOrderEvery > 0 && n%int64(st.opt.CheckKeyOrderEvery) == 0 {
		if err := checkKeyOrder(line); err != nil {
			st.violate("line %d: key order: %v", n, err)
		}
	}
	var e rawEvent
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return fmt.Errorf("line %d: %w", n, err)
	}
	t, err := time.Parse(tsLayout, e.TS)
	if err != nil || t.UTC().Format(tsLayout) != e.TS {
		st.violate("line %d: ts %q is not canonical", n, e.TS)
		return nil
	}
	ms := t.UnixMilli()
	if ms < st.lastMs {
		st.violate("line %d: ts %s goes backwards", n, e.TS)
	}
	st.lastMs = ms
	si := st.sites[e.Site]
	if si == nil {
		st.violate("line %d: unknown site %d", n, e.Site)
		return nil
	}
	if e.Host != si.Host {
		st.violate("line %d: host %q, want %q", n, e.Host, si.Host)
	}
	if e.Name == "" || e.Path == "" || e.IP == "" || e.UA == "" || e.VK == "" || e.SK == "" {
		st.violate("line %d: empty required field", n)
	}
	if e.Name == "pageview" && len(e.Props) != 0 {
		st.violate("line %d: pageview with props", n)
	}
	if e.Name != "pageview" && (len(e.Props) == 0 || len(e.Props) > 3) {
		st.violate("line %d: custom event with %d props", n, len(e.Props))
	}
	local := t.In(si.loc)
	date := local.Format("2006-01-02")
	gk := fmt.Sprintf("%d|%s", e.Site, date)
	g := st.groups[gk]
	if g == nil {
		y, m, d := local.Date()
		end := time.Date(y, m, d+1, 0, 0, 0, 0, si.loc).UnixMilli()
		g = &group{site: e.Site, date: date, endMs: end, visitors: map[string]*visitor{}}
		st.groups[gk] = g
	}
	vkey := e.IP + "\x00" + e.UA
	v := g.visitors[vkey]
	attrs := [4]string{e.Country, e.Browser, e.OS, e.Device}
	if v == nil {
		v = &visitor{attrs: attrs, screen: e.ScreenW, vk: e.VK}
		g.visitors[vkey] = v
		g.order = append(g.order, vkey)
	} else if v.attrs != attrs || v.screen != e.ScreenW || v.vk != e.VK {
		st.violate("line %d: visitor attributes or vk change within a day (site %d %s)", n, e.Site, date)
	}
	source := "(direct)"
	if e.UtmSource != "" {
		source = e.UtmSource
	} else if e.Ref != "" {
		u, err := url.Parse(e.Ref)
		if err != nil || u.Host == "" {
			st.violate("line %d: bad ref %q", n, e.Ref)
		} else {
			source = strings.ToLower(u.Host)
		}
	}
	x := ev{ms: ms, pageview: e.Name == "pageview", signup: e.Name == "signup", path: e.Path, source: source, sk: e.SK}
	if x.signup {
		x.plan = e.Props["plan"]
	}
	v.events = append(v.events, x)

	// Cross-day gap rule: the same (ip, ua) on the same site must respect the
	// gap rules even across local midnight, or a 30-minute timeout would merge.
	lk := fmt.Sprintf("%d|%s", e.Site, vkey)
	if ls, ok := st.lastSeen[lk]; ok {
		gap := ms - ls.ms
		if ls.sk == e.SK && gap > maxIntraMs {
			st.violate("line %d: intra-session gap %d ms > 25 min (sk %s)", n, gap, e.SK)
		}
		if ls.sk != e.SK && gap < minInterMs {
			st.violate("line %d: inter-session gap %d ms < 35 min (sk %s)", n, gap, e.SK)
		}
		if gap <= 0 {
			st.violate("line %d: non-increasing ts within one visitor", n)
		}
	}
	st.lastSeen[lk] = lastSeen{ms: ms, sk: e.SK}
	if prev, ok := st.skDay[e.SK]; !ok {
		st.skDay[e.SK] = gk
		st.skGroups[gk] = append(st.skGroups[gk], e.SK)
	} else if prev != gk {
		st.violate("line %d: session %s crosses site-local midnight (%s -> %s)", n, e.SK, prev, gk)
	}
	return nil
}

// flush finalizes groups whose local day has ended before nowMs.
func (st *state) flush(nowMs int64, all bool) {
	var done []string
	for k, g := range st.groups {
		if all || g.endMs <= nowMs {
			done = append(done, k)
		}
	}
	sort.Strings(done)
	for _, k := range done {
		g := st.groups[k]
		st.rows = append(st.rows, st.finalize(g))
		delete(st.groups, k)
	}
	// The sk of a closed day is kept for two more hours, so a session that
	// continued past local midnight would still be caught.
	for gk, sks := range st.skGroups {
		if _, open := st.groups[gk]; open {
			continue
		}
		if all || nowMs-st.closedAt(gk) > 2*60*60*1000 {
			for _, sk := range sks {
				delete(st.skDay, sk)
			}
			delete(st.skGroups, gk)
		}
	}
	for k, ls := range st.lastSeen {
		if all || nowMs-ls.ms > 2*60*60*1000 {
			delete(st.lastSeen, k)
		}
	}
}

// closedAt returns the end of the local day of a group key ("site|date").
func (st *state) closedAt(gk string) int64 {
	var site int
	var date string
	if i := strings.IndexByte(gk, '|'); i > 0 {
		fmt.Sscanf(gk[:i], "%d", &site)
		date = gk[i+1:]
	}
	si := st.sites[site]
	if si == nil {
		return 0
	}
	d, err := time.ParseInLocation("2006-01-02", date, si.loc)
	if err != nil {
		return 0
	}
	return d.AddDate(0, 0, 1).UnixMilli()
}

type dimAcc struct {
	visitors  map[string]map[string]struct{}
	pageviews map[string]int64
}

func newDimAcc() *dimAcc {
	return &dimAcc{visitors: map[string]map[string]struct{}{}, pageviews: map[string]int64{}}
}

func (d *dimAcc) add(value, vkey string, pv int64) {
	s := d.visitors[value]
	if s == nil {
		s = map[string]struct{}{}
		d.visitors[value] = s
	}
	s[vkey] = struct{}{}
	d.pageviews[value] += pv
}

func (d *dimAcc) top() []TopEntry {
	out := make([]TopEntry, 0, len(d.visitors))
	for v, s := range d.visitors {
		out = append(out, TopEntry{Value: v, Visitors: int64(len(s)), Pageviews: d.pageviews[v]})
	}
	SortTop(out)
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

// SortTop orders breakdown entries: visitors desc, pageviews desc, value asc.
func SortTop(t []TopEntry) {
	sort.Slice(t, func(i, j int) bool {
		if t[i].Visitors != t[j].Visitors {
			return t[i].Visitors > t[j].Visitors
		}
		if t[i].Pageviews != t[j].Pageviews {
			return t[i].Pageviews > t[j].Pageviews
		}
		return t[i].Value < t[j].Value
	})
}

func (st *state) finalize(g *group) Row {
	row := Row{Site: g.site, Date: g.date, Top: map[string][]TopEntry{}, Plan: map[string]int64{}}
	for _, p := range Plans {
		row.Plan[p] = 0
	}
	dims := map[string]*dimAcc{}
	for _, d := range Dims {
		dims[d] = newDimAcc()
	}
	for _, vkey := range g.order {
		v := g.visitors[vkey]
		row.Visitors++
		var vpv int64
		// Sessionize by idle timeout, independent of sk.
		var sessions [][]ev
		for i, x := range v.events {
			if i == 0 || x.ms-v.events[i-1].ms > idleTimeoutMs {
				sessions = append(sessions, nil)
			}
			sessions[len(sessions)-1] = append(sessions[len(sessions)-1], x)
		}
		seenSK := map[string]bool{}
		for _, s := range sessions {
			sk := s[0].sk
			if seenSK[sk] {
				st.violate("site %d %s: sk %s spans two timeout sessions", g.site, g.date, sk)
			}
			seenSK[sk] = true
			var pv, custom int64
			var entry, exit string
			step := 0
			for _, x := range s {
				if x.sk != sk {
					st.violate("site %d %s: timeout session mixes sk %s and %s", g.site, g.date, sk, x.sk)
				}
				if x.pageview {
					pv++
					if entry == "" {
						entry = x.path
					}
					exit = x.path
					dims["path"].add(x.path, vkey, 1)
					if step == 0 && x.path == "/" {
						step = 1
					} else if step == 1 && x.path == "/pricing" {
						step = 2
					}
				} else {
					custom++
					if x.signup {
						if step == 2 {
							step = 3
						}
						if _, ok := row.Plan[x.plan]; ok {
							row.Plan[x.plan]++
						} else {
							st.violate("site %d %s: signup with plan %q", g.site, g.date, x.plan)
						}
					}
				}
			}
			if !s[0].pageview {
				st.violate("site %d %s: session %s starts with a custom event", g.site, g.date, sk)
			}
			row.Visits++
			row.Pageviews += pv
			vpv += pv
			if pv == 1 && custom == 0 {
				row.Bounces++
			}
			row.DurationMs += s[len(s)-1].ms - s[0].ms
			for i := 0; i < step; i++ {
				row.Funnel[i]++
			}
			dims["source"].add(s[0].source, vkey, pv)
			if entry != "" {
				dims["entry"].add(entry, vkey, pv)
				dims["exit"].add(exit, vkey, pv)
			}
		}
		for i, d := range []string{"country", "browser", "os", "device"} {
			dims[d].add(v.attrs[i], vkey, vpv)
		}
	}
	for _, d := range Dims {
		row.Top[d] = dims[d].top()
	}
	return row
}

// truth.json parsing.

type truthFile struct {
	Rows []truthRow `json:"rows"`
}

type truthRow struct {
	Site         int                   `json:"site"`
	Date         string                `json:"date"`
	Visitors     int64                 `json:"visitors"`
	Visits       int64                 `json:"visits"`
	Pageviews    int64                 `json:"pageviews"`
	Bounces      int64                 `json:"bounces"`
	DurationSumS json.Number           `json:"duration_sum_s"`
	Top          map[string][]TopEntry `json:"top"`
	FunnelF      []int64               `json:"funnel_f"`
	SignupPlan   map[string]int64      `json:"signup_plan"`
}

// LoadTruth parses truth.json.
func LoadTruth(r io.Reader) ([]Row, error) {
	var tf truthFile
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(&tf); err != nil {
		return nil, fmt.Errorf("truth.json: %w", err)
	}
	rows := make([]Row, 0, len(tf.Rows))
	for _, tr := range tf.Rows {
		ms, err := secondsToMs(string(tr.DurationSumS))
		if err != nil {
			return nil, fmt.Errorf("site %d %s: %w", tr.Site, tr.Date, err)
		}
		if len(tr.FunnelF) != 3 {
			return nil, fmt.Errorf("site %d %s: funnel_f has %d steps", tr.Site, tr.Date, len(tr.FunnelF))
		}
		rows = append(rows, Row{Site: tr.Site, Date: tr.Date, Visitors: tr.Visitors, Visits: tr.Visits,
			Pageviews: tr.Pageviews, Bounces: tr.Bounces, DurationMs: ms, Top: tr.Top,
			Funnel: [3]int64{tr.FunnelF[0], tr.FunnelF[1], tr.FunnelF[2]}, Plan: tr.SignupPlan})
	}
	return rows, nil
}

// secondsToMs parses a decimal seconds value with at most 3 decimals exactly.
func secondsToMs(s string) (int64, error) {
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 3 {
		return 0, fmt.Errorf("duration %q has more than 3 decimals", s)
	}
	for len(frac) < 3 {
		frac += "0"
	}
	var w, f int64
	if _, err := fmt.Sscanf(whole+" "+frac, "%d %d", &w, &f); err != nil {
		return 0, fmt.Errorf("duration %q: %w", s, err)
	}
	return w*1000 + f, nil
}

// Compare reports differences between two row sets (keyed by site and date).
func Compare(got, want []Row, max int) []string {
	var diffs []string
	add := func(format string, a ...any) {
		if len(diffs) < max {
			diffs = append(diffs, fmt.Sprintf(format, a...))
		}
	}
	key := func(r Row) string { return fmt.Sprintf("site %d %s", r.Site, r.Date) }
	gm := map[string]Row{}
	for _, r := range got {
		gm[key(r)] = r
	}
	seen := map[string]bool{}
	for _, w := range want {
		k := key(w)
		seen[k] = true
		g, ok := gm[k]
		if !ok {
			add("%s: missing from recount", k)
			continue
		}
		if g.Visitors != w.Visitors || g.Visits != w.Visits || g.Pageviews != w.Pageviews ||
			g.Bounces != w.Bounces || g.DurationMs != w.DurationMs {
			add("%s: totals recount v=%d s=%d pv=%d b=%d dur=%d, truth v=%d s=%d pv=%d b=%d dur=%d", k,
				g.Visitors, g.Visits, g.Pageviews, g.Bounces, g.DurationMs,
				w.Visitors, w.Visits, w.Pageviews, w.Bounces, w.DurationMs)
		}
		if g.Funnel != w.Funnel {
			add("%s: funnel recount %v, truth %v", k, g.Funnel, w.Funnel)
		}
		for _, p := range Plans {
			if g.Plan[p] != w.Plan[p] {
				add("%s: plan %s recount %d, truth %d", k, p, g.Plan[p], w.Plan[p])
			}
		}
		if len(w.Plan) != len(Plans) {
			add("%s: truth signup_plan has %d keys", k, len(w.Plan))
		}
		for _, d := range Dims {
			gt, wt := g.Top[d], w.Top[d]
			if len(gt) != len(wt) {
				add("%s: %s has %d entries in recount, %d in truth", k, d, len(gt), len(wt))
				continue
			}
			for i := range gt {
				if gt[i] != wt[i] {
					add("%s: %s[%d] recount %+v, truth %+v", k, d, i, gt[i], wt[i])
					break
				}
			}
		}
		if len(w.Top) != len(Dims) {
			add("%s: truth has %d dimensions", k, len(w.Top))
		}
	}
	for _, g := range got {
		if !seen[key(g)] {
			add("%s: in recount but not in truth", key(g))
		}
	}
	return diffs
}
