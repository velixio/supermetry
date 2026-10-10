package synth

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DemoConfig controls the deterministic demo traffic model.
type DemoConfig struct {
	Seed     int64
	TZ       *time.Location
	Visitors float64
	Anchor   time.Time
}

// DemoVisit is one synthetic session. It contains no IP address or user agent.
type DemoVisit struct {
	Start       time.Time
	VisitorKey  uint64
	Host        string
	Referrer    string
	UTMSource   string
	UTMMedium   string
	UTMCampaign string
	UTMContent  string
	UTMTerm     string
	Country     string
	Region      string
	City        string
	Language    string
	Browser     string
	BrowserVer  string
	OS          string
	OSVer       string
	Device      int16
	ScreenW     int
	Profile     *DemoProfile
	Hits        []DemoHit
	Replay      bool
}

// DemoHit is one pageview, custom event, or engagement event in a demo visit.
type DemoHit struct {
	Offset        time.Duration
	Kind          int16
	Path          string
	Name          string
	Props         map[string]string
	RevenueMicros int64
	EngagementMS  int32
	ScrollPct     int16
	Clicks        [][2]int
}

// DemoProfile is the stable identity attached after a synthetic visitor signs up.
type DemoProfile struct {
	ExternalID string
	Traits     map[string]string
}

// Demo generates deterministic traffic for one demo site.
type Demo struct {
	cfg       DemoConfig
	anchorDay int
}

const (
	demoTagDay        uint64 = 1001
	demoTagVisitorKey uint64 = 1002
	demoTagPerson     uint64 = 1003
	demoTagBusyVisit  uint64 = 1004
)

// NewDemo builds a demo model. A zero Anchor means the current site-local day.
func NewDemo(cfg DemoConfig) *Demo {
	if cfg.TZ == nil {
		cfg.TZ = time.UTC
	}
	if cfg.Visitors <= 0 {
		cfg.Visitors = 150
	}
	if cfg.Anchor.IsZero() {
		cfg.Anchor = time.Now().In(cfg.TZ)
	}
	cfg.Anchor = localMidnight(cfg.Anchor, cfg.TZ)
	return &Demo{cfg: cfg, anchorDay: civilDay(cfg.Anchor)}
}

// Visits returns sessions starting in [from, to), ordered by start time.
// Each overlapping local day is generated in full before the range is filtered.
func (d *Demo) Visits(from, to time.Time) []DemoVisit {
	if !to.After(from) {
		return nil
	}
	day := localMidnight(from, d.cfg.TZ)
	visits := make([]DemoVisit, 0, int(to.Sub(from).Hours()/24*d.cfg.Visitors)+16)
	for day.Before(to) {
		for _, visit := range d.dayVisits(day) {
			if !visit.Start.Before(from) && visit.Start.Before(to) {
				visits = append(visits, visit)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	slices.SortStableFunc(visits, func(a, b DemoVisit) int { return a.Start.Compare(b.Start) })
	return visits
}

func (d *Demo) dayVisits(day time.Time) []DemoVisit {
	dayNumber := civilDay(day)
	delta := dayNumber - d.anchorDay
	n := d.visitCount(dayNumber)
	r := rng{key(uint64(d.cfg.Seed), demoTagDay, uint64(dayNumber), 0, 0)}
	visits := make([]DemoVisit, 0, n)
	returnCount := n * 30 / 100
	poolBefore := d.poolSizeBefore(dayNumber)
	if poolBefore == 0 {
		returnCount = 0
	}
	for i := 0; i < n; i++ {
		poolIndex := poolBefore + int64(i-returnCount)
		if i < returnCount {
			poolIndex = r.intn(poolBefore)
		}
		visitorKey := key(uint64(d.cfg.Seed), demoTagVisitorKey, uint64(poolIndex), 0, 0)
		visit := d.makeVisit(day, dayNumber, delta, i, visitorKey, poolIndex, &r)
		visit.VisitorKey &^= uint64(1) << 62
		visits = append(visits, visit)
	}
	slices.SortStableFunc(visits, func(a, b DemoVisit) int { return a.Start.Compare(b.Start) })
	return visits
}

// BusyVisit returns one deterministic long-running session for watched demos.
func (d *Demo) BusyVisit(start time.Time, busyKey uint64) DemoVisit {
	r := rng{key(uint64(d.cfg.Seed), demoTagBusyVisit, busyKey, 0, 0)}
	visitorKey := key(uint64(d.cfg.Seed), demoTagBusyVisit, busyKey, 1, 0)
	visitorKey = visitorKey&^((uint64(1)<<63)|(uint64(1)<<62)) | (uint64(1) << 62)
	geo := demoGeos[pickDemoWeighted(&r, len(demoGeos), func(i int) int64 { return demoGeos[i].weight })]
	place := geo.places[r.intn(int64(len(geo.places)))]
	device := demoDevices[pickDemoWeighted(&r, len(demoDevices), func(i int) int64 { return int64(demoDevices[i].weight) })]
	channel := demoChannels[pickDemoWeighted(&r, len(demoChannels), func(i int) int64 { return demoChannels[i].weight })]
	hits := demoBusyJourney(&r, device.screenW)
	return DemoVisit{
		Start: start, VisitorKey: visitorKey, Host: hostForPath(hits[0].Path),
		Referrer: channel.referrer, UTMSource: channel.source, UTMMedium: channel.medium,
		UTMCampaign: channel.campaign, UTMContent: channel.content, UTMTerm: channel.term,
		Country: geo.country, Region: place[0], City: place[1], Language: geo.language,
		Browser: device.browser, BrowserVer: device.browserVer, OS: device.os, OSVer: device.osVer,
		Device: device.kind, ScreenW: device.screenW, Hits: hits, Replay: r.chance(20_000),
	}
}

// BusyTarget returns the desired watched-demo concurrency at the local time.
func BusyTarget(local time.Time) int {
	minWeight, maxWeight := demoHourly[0], demoHourly[0]
	for _, weight := range demoHourly[1:] {
		minWeight = min(minWeight, weight)
		maxWeight = max(maxWeight, weight)
	}
	shape := float64(demoHourly[local.Hour()]-minWeight) / float64(maxWeight-minWeight)
	return 10 + int(math.Round(10*shape))
}

func demoBusyJourney(r *rng, screenW int) []DemoHit {
	duration := time.Duration(8*60+r.intn(17*60+1)) * time.Second
	hits := make([]DemoHit, 0, int(duration/time.Minute)*2)
	lastPath := "/"
	for offset := time.Duration(0); offset < duration; {
		path := demoRandomPath(r)
		lastPath = path
		page := DemoHit{Offset: offset, Kind: 0, Path: path}
		if path == "/" || path == "/pricing" || strings.HasPrefix(path, "/features/") {
			page.Clicks = [][2]int{{max(1, screenW/2+int(r.intn(81))-40), 180 + int(r.intn(360))}}
		}
		hits = append(hits, page)
		engagementAt := min(duration, offset+time.Duration(8+r.intn(20))*time.Second)
		scroll := int16(35 + r.intn(66))
		if strings.HasPrefix(path, "/blog/") {
			scroll = int16(75 + r.intn(26))
		}
		hits = append(hits, DemoHit{Offset: engagementAt, Kind: 2, Path: path, EngagementMS: int32(8_000 + r.intn(70_000)), ScrollPct: scroll})
		if customAt := engagementAt + time.Second; customAt < duration {
			if custom := demoBusyCustomHit(r, path, customAt); custom != nil {
				hits = append(hits, *custom)
			}
		}
		offset += time.Duration(40+r.intn(111)) * time.Second
	}
	if hits[len(hits)-1].Offset < duration {
		hits = append(hits, DemoHit{Offset: duration, Kind: 2, Path: lastPath, EngagementMS: 1_000})
	}
	slices.SortStableFunc(hits, func(a, b DemoHit) int { return cmp.Compare(a.Offset, b.Offset) })
	return hits
}

func demoBusyCustomHit(r *rng, path string, offset time.Duration) *DemoHit {
	var hit DemoHit
	switch {
	case r.chance(25_000):
		plan := r.intn(int64(len(demoPlans)))
		hit = DemoHit{Offset: offset, Kind: 1, Path: "/checkout/success", Name: demoEvents[demoPurchaseEvent], Props: map[string]string{"plan": demoPlans[plan]}, RevenueMicros: []int64{19, 49, 199}[plan] * 1_000_000}
	case r.chance(80_000):
		hit = DemoHit{Offset: offset, Kind: 1, Path: path, Name: demoEvents[demoVitalsEvent], Props: map[string]string{
			"lcp": strconv.FormatInt(900+r.intn(4_500), 10), "cls": strconv.FormatInt(r.intn(450), 10), "inp": strconv.FormatInt(40+r.intn(700), 10),
		}}
	case strings.HasPrefix(path, "/blog/") && r.chance(90_000):
		hit = DemoHit{Offset: offset, Kind: 1, Path: path, Name: demoEvents[demoOutboundEvent], Props: map[string]string{"url": "https://github.com/demo/example"}}
	case strings.HasPrefix(path, "/docs/") && r.chance(100_000):
		hit = DemoHit{Offset: offset, Kind: 1, Path: path, Name: demoEvents[demoDownloadEvent], Props: map[string]string{"url": "/downloads/acme-cloud-guide.pdf"}}
	case r.chance(60_000):
		hit = DemoHit{Offset: offset, Kind: 1, Path: path, Name: demoEvents[demoFormEvent], Props: map[string]string{"form": "request-demo"}}
	default:
		return nil
	}
	return &hit
}

func demoRandomPath(r *rng) string {
	switch roll := r.intn(100); {
	case roll < 25:
		return []string{"/", "/pricing", "/signup", "/account"}[r.intn(4)]
	case roll < 55:
		return demoBlogPages[r.intn(int64(len(demoBlogPages)))]
	case roll < 78:
		return demoDocPages[r.intn(int64(len(demoDocPages)))]
	default:
		return demoFeaturePages[r.intn(int64(len(demoFeaturePages)))]
	}
}

func (d *Demo) poolSizeBefore(dayNumber int) int64 {
	start := d.anchorDay - DemoWindowDays
	if dayNumber <= start {
		return 0
	}
	var size int64
	for day := start; day < dayNumber; day++ {
		count := d.visitCount(day)
		returns := count * 30 / 100
		if day == start {
			returns = 0
		}
		size += int64(count - returns)
	}
	return size
}

func (d *Demo) poolOriginDay(poolIndex int64) int {
	start := d.anchorDay - DemoWindowDays
	var offset int64
	for day := start; ; day++ {
		count := d.visitCount(day)
		returns := count * 30 / 100
		if day == start {
			returns = 0
		}
		newVisitors := int64(count - returns)
		if poolIndex < offset+newVisitors {
			return day
		}
		offset += newVisitors
	}
}

func (d *Demo) visitCount(dayNumber int) int {
	delta := dayNumber - d.anchorDay
	progress := float64(delta+DemoWindowDays) / DemoWindowDays
	progress = max(0, min(1, progress))
	factor := 0.60 + 0.40*progress
	date := dateOf(dayNumber)
	factor *= demoWeekly[date.Weekday()]
	r := rng{key(uint64(d.cfg.Seed), demoTagDay, uint64(dayNumber), 1, 0)}
	factor *= 0.94 + 0.12*r.f64()
	if delta == -DemoCampaignDaysAgo || delta == -DemoCampaignDaysAgo+1 {
		factor *= 3.2
	}
	if delta == -DemoDipDaysAgo {
		factor *= 0.45
	}
	return max(1, int(d.cfg.Visitors*factor+0.5))
}

func (d *Demo) makeVisit(day time.Time, dayNumber, delta, ordinal int, visitorKey uint64, poolIndex int64, r *rng) DemoVisit {
	person := rng{key(uint64(d.cfg.Seed), demoTagPerson, visitorKey, 0, 0)}
	geo := demoGeos[pickDemoWeighted(&person, len(demoGeos), func(i int) int64 { return demoGeos[i].weight })]
	place := geo.places[person.intn(int64(len(geo.places)))]
	device := demoDevices[pickDemoWeighted(&person, len(demoDevices), func(i int) int64 { return int64(demoDevices[i].weight) })]
	channel := demoChannels[pickDemoWeighted(r, len(demoChannels), func(i int) int64 { return demoChannels[i].weight })]
	if delta == -DemoCampaignDaysAgo || delta == -DemoCampaignDaysAgo+1 {
		channel = demoChannel{source: "google", medium: "cpc", campaign: DemoCampaign, content: "launch-hero", term: "cloud analytics"}
	}

	hour := pickDemoWeighted(r, len(demoHourly), func(i int) int64 { return demoHourly[i] })
	start := time.Date(day.Year(), day.Month(), day.Day(), hour, int(r.intn(60)), int(r.intn(60)), 0, d.cfg.TZ)
	visit := DemoVisit{
		Start: start, VisitorKey: visitorKey,
		Referrer: channel.referrer, UTMSource: channel.source, UTMMedium: channel.medium,
		UTMCampaign: channel.campaign, UTMContent: channel.content, UTMTerm: channel.term,
		Country: geo.country, Region: place[0], City: place[1], Language: geo.language,
		Browser: device.browser, BrowserVer: device.browserVer, OS: device.os, OSVer: device.osVer,
		Device: device.kind, ScreenW: device.screenW,
		Replay: delta >= -13 && ordinal < 4,
	}
	profile, signupNow := d.profileFor(visitorKey, poolIndex, dayNumber, visit.Country)
	var signup *DemoProfile
	if signupNow {
		signup = profile
	}
	visit.Hits = demoJourney(r, device.screenW, signup)
	visit.Host = hostForPath(visit.Hits[0].Path)
	if profile != nil {
		visit.Profile = profile
	}
	return visit
}

func (d *Demo) profileFor(visitorKey uint64, poolIndex int64, dayNumber int, country string) (*DemoProfile, bool) {
	r := rng{key(uint64(d.cfg.Seed), demoTagPerson, visitorKey, 1, 0)}
	if !r.chance(50_000) {
		return nil, false
	}
	signupDay := d.poolOriginDay(poolIndex)
	if dayNumber < signupDay {
		return nil, false
	}
	profile := &DemoProfile{
		ExternalID: fmt.Sprintf("user_%x", visitorKey),
		Traits: map[string]string{
			"plan":         demoPlans[r.intn(int64(len(demoPlans)))],
			"company_size": demoCompanySizes[r.intn(int64(len(demoCompanySizes)))],
			"country":      country,
		},
	}
	return profile, dayNumber == signupDay
}

func demoJourney(r *rng, screenW int, signup *DemoProfile) []DemoHit {
	hits := make([]DemoHit, 0, 10)
	offset := time.Duration(0)
	addPage := func(path string, scroll int16) {
		page := DemoHit{Offset: offset, Kind: 0, Path: path}
		if path == "/" || path == "/pricing" || strings.HasPrefix(path, "/features/") {
			page.Clicks = [][2]int{{max(1, screenW/2+int(r.intn(81))-40), 180 + int(r.intn(360))}}
		}
		hits = append(hits, page)
		offset += time.Duration(18+r.intn(75)) * time.Second
		hits = append(hits, DemoHit{Offset: offset, Kind: 2, Path: path, EngagementMS: int32(8_000 + r.intn(70_000)), ScrollPct: scroll})
		offset += time.Duration(2+r.intn(20)) * time.Second
	}
	addCustom := func(path, name string, props map[string]string, revenue int64) {
		hits = append(hits, DemoHit{Offset: offset, Kind: 1, Path: path, Name: name, Props: props, RevenueMicros: revenue})
		offset += time.Duration(2+r.intn(12)) * time.Second
	}

	if signup != nil {
		addPage("/", int16(45+r.intn(50)))
		addPage("/pricing", int16(55+r.intn(41)))
		addPage("/signup", int16(70+r.intn(26)))
		addCustom("/signup", demoEvents[demoSignupEvent], map[string]string{
			"plan": signup.Traits["plan"], "method": demoSignupMethods[r.intn(int64(len(demoSignupMethods)))],
		}, 0)
		if r.chance(600_000) {
			addPage("/checkout", 90)
			if r.chance(800_000) {
				plan := slices.Index(demoPlans, signup.Traits["plan"])
				addPage("/checkout/success", 100)
				addCustom("/checkout/success", demoEvents[demoPurchaseEvent], map[string]string{"plan": demoPlans[plan]}, []int64{19, 49, 199}[plan]*1_000_000)
			}
		}
		return hits
	}

	switch roll := r.intn(100); {
	case roll < 22:
		addPage("/", int16(45+r.intn(50)))
		if r.chance(400_000) {
			addPage("/pricing", int16(55+r.intn(41)))
		}
	case roll < 47:
		path := demoBlogPages[r.intn(int64(len(demoBlogPages)))]
		addPage(path, int16(75+r.intn(26)))
		if r.chance(350_000) {
			addCustom(path, demoEvents[demoOutboundEvent], map[string]string{"url": "https://github.com/demo/example"}, 0)
		}
	case roll < 62:
		path := demoDocPages[r.intn(int64(len(demoDocPages)))]
		addPage(path, int16(50+r.intn(46)))
		if r.chance(450_000) {
			addCustom(path, demoEvents[demoDownloadEvent], map[string]string{"url": "/downloads/acme-cloud-guide.pdf"}, 0)
		}
	case roll < 77:
		path := demoFeaturePages[r.intn(int64(len(demoFeaturePages)))]
		addPage(path, int16(55+r.intn(41)))
		addCustom(path, demoEvents[demoFormEvent], map[string]string{"form": "request-demo"}, 0)
	case roll < 90:
		addPage("/pricing", int16(60+r.intn(36)))
		addCustom("/pricing", demoEvents[demoVitalsEvent], map[string]string{
			"lcp": strconv.FormatInt(900+r.intn(4_500), 10),
			"cls": strconv.FormatInt(r.intn(450), 10),
			"inp": strconv.FormatInt(40+r.intn(700), 10),
		}, 0)
	case roll < 96:
		addPage("/old-pricing", int16(20+r.intn(40)))
		addCustom("/old-pricing", demoEvents[demoNotFoundEvent], map[string]string{"path": "/old-pricing"}, 0)
	default:
		addPage("/account", int16(40+r.intn(50)))
	}
	return hits
}

func pickDemoWeighted(r *rng, length int, weight func(int) int64) int {
	var total int64
	for i := 0; i < length; i++ {
		total += weight(i)
	}
	x := r.intn(total)
	for i := 0; i < length; i++ {
		x -= weight(i)
		if x < 0 {
			return i
		}
	}
	return length - 1
}

func hostForPath(path string) string {
	if strings.HasPrefix(path, "/docs/") {
		return DemoHosts[1]
	}
	return DemoHosts[0]
}

func civilDay(t time.Time) int {
	y, m, d := t.Date()
	return dayNum(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

func localMidnight(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}
