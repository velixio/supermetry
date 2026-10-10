// Package synth generates a deterministic synthetic web-analytics event
// stream with ground truth, per tools/bench/CONTRACT.md.
//
// Every random decision is keyed by (seed, purpose, site, date, visitor, ...)
// rather than by generation order, so output is byte-identical for the same
// flags regardless of worker count, and a site-local day's events do not
// depend on --start or --days.
package synth

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embedded zone database: same offsets on every machine
)

// Config selects what to generate.
type Config struct {
	Tier         string    // tiny, S, M, M-history
	Start        time.Time // first site-local date (UTC midnight of that date)
	Days         int       // number of site-local days per site
	Seed         uint64
	Workers      int  // 0 = GOMAXPROCS
	Zstd         bool // compress the event stream (concatenated zstd frames)
	EventsPerDay int  // 0 = tier default
}

// DefaultEnd is the last site-local date of every tier by default.
var DefaultEnd = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

// DefaultStart returns the start date so that days end on DefaultEnd.
func DefaultStart(days int) time.Time { return DefaultEnd.AddDate(0, 0, 1-days) }

// TierDefaults returns events per day and default days for a tier.
func TierDefaults(tier string) (eventsPerDay, days int, err error) {
	switch tier {
	case "tiny":
		return 10_000, 3, nil
	case "S":
		return 1_000_000, 90, nil
	case "M":
		return 10_000_000, 14, nil
	case "M-history":
		return 10_000_000, 365, nil
	}
	return 0, 0, fmt.Errorf("unknown tier %q (want tiny, S, M or M-history)", tier)
}

const numSites = 100

var epoch2000 = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// dayNum is the number of days since 2000-01-01 of a civil date.
func dayNum(t time.Time) int { return int(t.Sub(epoch2000).Hours() / 24) }

func dateOf(day int) time.Time { return epoch2000.AddDate(0, 0, day) }

type site struct {
	id       int
	host     string
	tz       string
	loc      *time.Location
	share    float64
	home     int // country index, -1 for none
	paths    []string
	pathZipf ftab

	ipK1, ipK2, ipMulA, ipMulB uint32

	prefix      []byte // `","site":1,"host":"site1.test","path":"`
	internalRef []byte // `","ref":"https://site1.test`
}

// Generator holds the immutable model for one configuration.
type Generator struct {
	cfg          Config
	seed         uint64
	eventsPerDay int
	firstDay     int
	workers      int
	idKey        uint64

	sites []*site

	countryTab wtab
	uaTab      wtab
	uaScreen   []wtab
	uaWidths   [][]int64
	channelTab wtab
	hourTab    wtab
	sessTab    wtab
	pageGap    piecewise
	customGap  piecewise
	interGap   piecewise
	propTabs   []wtab
	customPric wtab
	customElse wtab

	// truth label tables
	sources       []string
	channelSource []int
	browsers      []string
	oses          []string
	devices       []string
	uaBrowser     []int
	uaOS          []int
	uaDevice      []int

	// formatting chunks
	channelChunk [][]byte
	uaChunk      [][]byte
	uaTail       [][]byte
	nameChunk    [][]byte
}

// New validates the config and builds the model.
func New(cfg Config) (*Generator, error) {
	epd, days, err := TierDefaults(cfg.Tier)
	if err != nil {
		return nil, err
	}
	if cfg.EventsPerDay == 0 {
		cfg.EventsPerDay = epd
	}
	if cfg.Days == 0 {
		cfg.Days = days
	}
	if cfg.Start.IsZero() {
		cfg.Start = DefaultStart(cfg.Days)
	}
	if cfg.Days < 1 || cfg.EventsPerDay < 1 {
		return nil, fmt.Errorf("days and events per day must be positive")
	}
	if !cfg.Start.Equal(time.Date(cfg.Start.Year(), cfg.Start.Month(), cfg.Start.Day(), 0, 0, 0, 0, time.UTC)) {
		return nil, fmt.Errorf("start must be a UTC date")
	}
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.GOMAXPROCS(0)
	}
	g := &Generator{cfg: cfg, seed: cfg.Seed, eventsPerDay: cfg.EventsPerDay, firstDay: dayNum(cfg.Start), workers: cfg.Workers}
	g.idKey = key(g.seed, tagVK, 0, 0, 0)
	if err := g.buildTables(); err != nil {
		return nil, err
	}
	if err := g.buildSites(); err != nil {
		return nil, err
	}
	// sk packs the visitor index in 28 bits and IPs pack it in 28 bits too.
	if n := float64(g.eventsPerDay) * g.sites[0].share * 1.2 * 1.081 / eventsPerVisitor; n >= 1<<28 {
		return nil, fmt.Errorf("events per day too large for the id encodings")
	}
	return g, nil
}

func weights[T any](xs []T, w func(T) int64) []int64 {
	out := make([]int64, len(xs))
	for i, x := range xs {
		out[i] = w(x)
	}
	return out
}

func indexOf(list *[]string, v string) int {
	for i, x := range *list {
		if x == v {
			return i
		}
	}
	*list = append(*list, v)
	return len(*list) - 1
}

// jsonSafe reports whether s can be emitted between quotes without escaping.
func jsonSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == '"' || c == '\\' || c >= 0x7f {
			return false
		}
	}
	return true
}

func (g *Generator) buildTables() error {
	g.countryTab = newWtab(weights(countries, func(c country) int64 { return c.weight }))
	g.uaTab = newWtab(weights(userAgents, func(u uaDef) int64 { return u.weight }))
	for _, u := range userAgents {
		if !jsonSafe(u.ua) {
			return fmt.Errorf("ua needs escaping: %q", u.ua)
		}
		k := u.device
		if u.device != "desktop" {
			if u.os == "iOS" {
				k += "/iOS"
			} else {
				k += "/other"
			}
		}
		sc := screens[k]
		if sc == nil {
			return fmt.Errorf("no screens for %s", k)
		}
		g.uaScreen = append(g.uaScreen, newWtab(weights(sc, func(x [2]int64) int64 { return x[1] })))
		g.uaWidths = append(g.uaWidths, weights(sc, func(x [2]int64) int64 { return x[0] }))
		g.uaBrowser = append(g.uaBrowser, indexOf(&g.browsers, u.browser))
		g.uaOS = append(g.uaOS, indexOf(&g.oses, u.os))
		g.uaDevice = append(g.uaDevice, indexOf(&g.devices, u.device))
		g.uaChunk = append(g.uaChunk, []byte(`","ua":"`+u.ua+`","screen_w":`))
		g.uaTail = append(g.uaTail, []byte(`","browser":"`+u.browser+`","os":"`+u.os+`","device":"`+u.device+`","vk":"`))
	}
	if len(channels) > 255 || len(userAgents) > 255 || len(countries) > 255 {
		return fmt.Errorf("table too large for uint8 indexes")
	}
	g.channelTab = newWtab(weights(channels, func(c channelDef) int64 { return c.weight }))
	for _, c := range channels {
		src := "(direct)"
		if c.src != "" {
			src = c.src
		} else if c.ref != "" {
			u, err := url.Parse(c.ref)
			if err != nil {
				return err
			}
			src = strings.ToLower(u.Host)
		}
		g.channelSource = append(g.channelSource, indexOf(&g.sources, src))
		g.channelChunk = append(g.channelChunk, []byte(`","ref":"`+c.ref+`","utm_source":"`+c.src+
			`","utm_medium":"`+c.medium+`","utm_campaign":"`+c.campaign+`","name":"`))
	}
	g.hourTab = newWtab(hourly)
	g.sessTab = newWtab(sessionsPerVisitor)
	g.pageGap = newPiecewise(pageGapBuckets)
	g.customGap = newPiecewise(customGapBuckets)
	g.interGap = newPiecewise(interGapBuckets)
	g.propTabs = make([]wtab, len(eventNames))
	for i := 1; i < len(eventNames); i++ {
		if len(propCombos[i]) > 255 {
			return fmt.Errorf("too many prop combos for %s", eventNames[i])
		}
		g.propTabs[i] = newWtab(weights(propCombos[i], func(p propCombo) int64 { return p.weight }))
	}
	g.customPric = newWtab(customOnPricing)
	g.customElse = newWtab(customElsewhere)
	for _, n := range eventNames {
		g.nameChunk = append(g.nameChunk, []byte(n+`","props":`))
	}
	return nil
}

// publicOctets are 64 first octets of public unicast IPv4 space.
var publicOctets = func() []byte {
	var o []byte
	for i := 2; i <= 9; i++ {
		o = append(o, byte(i))
	}
	for i := 11; len(o) < 64; i++ {
		o = append(o, byte(i))
	}
	return o
}()

func (g *Generator) buildSites() error {
	var sum float64
	for k := 1; k <= numSites; k++ {
		sum += zipfWeight(k, siteZipfS)
	}
	homeIdx := func(code string) int {
		for i, c := range countries {
			if c.code == code {
				return i
			}
		}
		return -1
	}
	for k := 1; k <= numSites; k++ {
		z := zoneFirst[0]
		if k <= len(zoneFirst) {
			z = zoneFirst[k-1]
		} else {
			z = zoneCycle[(k-len(zoneFirst)-1)%len(zoneCycle)]
		}
		loc, err := time.LoadLocation(z.name)
		if err != nil {
			return err
		}
		s := &site{id: k, host: fmt.Sprintf("site%d.test", k), tz: z.name, loc: loc,
			share: zipfWeight(k, siteZipfS) / sum, home: homeIdx(z.home)}
		s.prefix = []byte(`","site":` + strconv.Itoa(k) + `,"host":"` + s.host + `","path":"`)
		s.internalRef = []byte(`","ref":"https://` + s.host)
		r := rng{key(g.seed, tagIP, uint64(k), 0, 0)}
		s.ipK1, s.ipK2 = uint32(r.u64()), uint32(r.u64())
		s.ipMulA, s.ipMulB = uint32(r.u64())|1, uint32(r.u64())|1
		g.sites = append(g.sites, s)
	}
	top := g.sites[0].share
	for _, s := range g.sites {
		n := int(math.Round(maxPaths * math.Pow(s.share/top, 0.75)))
		n = max(minPaths, min(maxPaths, n))
		s.paths = g.vocabulary(s, n)
		s.pathZipf = newZipf(n, pathZipfS)
	}
	return nil
}

// vocabulary builds n distinct paths for a site: the fixed head, then a
// deterministic long tail.
func (g *Generator) vocabulary(s *site, n int) []string {
	paths := append([]string(nil), pathHead...)
	seen := map[string]bool{}
	for _, p := range paths {
		seen[p] = true
	}
	tw := newWtab(weights(pathTemplates, func(t struct {
		prefix string
		words  int
		weight int64
	}) int64 {
		return t.weight
	}))
	r := rng{key(g.seed, tagPaths, uint64(s.id), 0, 0)}
	for len(paths) < n {
		t := pathTemplates[tw.pick(&r)]
		var b strings.Builder
		b.WriteString(t.prefix)
		if t.prefix == "/docs/" {
			b.WriteString(docSections[r.intn(int64(len(docSections)))])
			b.WriteByte('/')
		}
		prev := -1
		for i := 0; i < t.words; i++ {
			if i > 0 {
				b.WriteByte('-')
			}
			w := int(r.intn(int64(len(words))))
			if w == prev {
				w = (w + 1) % len(words) // no "churn-churn"
			}
			b.WriteString(words[w])
			prev = w
		}
		p := b.String()
		if seen[p] {
			// Disambiguate collisions with a numeric suffix (keeps n reachable).
			p = fmt.Sprintf("%s-%d", p, r.intn(1000))
			if seen[p] {
				continue
			}
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths[:n]
}

// Config returns the effective configuration (tier defaults applied).
func (g *Generator) Config() Config { return g.cfg }

// SiteInfo is one entry of sites.json.
type SiteInfo struct {
	ID       int     `json:"id"`
	Host     string  `json:"host"`
	Timezone string  `json:"timezone"`
	Share    float64 `json:"share"`
}

// Sites lists the sites.
func (g *Generator) Sites() []SiteInfo {
	var out []SiteInfo
	for _, s := range g.sites {
		out = append(out, SiteInfo{ID: s.id, Host: s.host, Timezone: s.tz, Share: math.Round(s.share*1e6) / 1e6})
	}
	return out
}

// WriteSites writes sites.json: one site per line inside a JSON array.
func (g *Generator) WriteSites(w io.Writer) error {
	var b strings.Builder
	b.WriteString("[\n")
	for i, s := range g.Sites() {
		j, err := json.Marshal(s)
		if err != nil {
			return err
		}
		b.Write(j)
		if i < numSites-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("]\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// visitorsOn is the number of visitors of a site on a day. It is a closed
// form of (seed, tier volume, site, date) so any day can be generated alone.
func (g *Generator) visitorsOn(s *site, day int) int64 {
	wd := (day + 6) % 7 // 2000-01-01 was a Saturday
	noise := 920 + int64(key(g.seed, tagVolume, uint64(s.id), uint64(day), 0)%161)
	e := float64(g.eventsPerDay) * s.share * weekly[wd] * float64(noise) / 1000
	n := int64(e/eventsPerVisitor + 0.5)
	return max(n, 1)
}

// returners is how many of a day's visitors were also there the day before.
// They are visitors [0, R) of the day and are visitors [R', R'+R) of the day
// before, where R' is that day's own returner count. The bound by half of the
// day's visitors keeps both ranges disjoint inside the previous day.
func (g *Generator) returners(s *site, day int) int64 {
	return min(g.visitorsOn(s, day-1)*returnPerMille/1000, g.visitorsOn(s, day)/2)
}

const m30 = 1<<30 - 1

// ip encodes (origin day mod 4, origin index) bijectively into public IPv4
// space. Everyone present on a site on day d or d+1 originates on d-1..d+1,
// so their IPs are distinct and (ip, ua) is unique per site-day.
func (s *site) ip(originDay int, idx int64) uint32 {
	x := (uint32(originDay&3)<<28 | uint32(idx)) & m30
	x = (x ^ s.ipK1) & m30
	x = (x * s.ipMulA) & m30
	x ^= x >> 13
	x = (x * s.ipMulB) & m30
	x ^= x >> 16
	x = (x ^ s.ipK2) & m30
	return uint32(publicOctets[x>>24])<<24 | x&0xFFFFFF
}

// vk is the person key: stable across days for a returning visitor.
func (g *Generator) vk(s *site, originDay int, idx int64) uint64 {
	return mix64((uint64(s.id)<<48 | uint64(originDay&0xFFFF)<<32 | uint64(idx)) ^ g.idKey)
}

// sk is the session key; the top bit separates it from every vk.
func (g *Generator) sk(s *site, day int, visitor uint32, ord uint8) uint64 {
	return mix64((1<<63 | uint64(s.id)<<48 | uint64(day&0xFFFF)<<32 | uint64(visitor)<<4 | uint64(ord)) ^ g.idKey)
}
