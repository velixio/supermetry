package synth

import (
	"fmt"
	"math"
)

var sprintf = fmt.Sprintf

// Behaviour model. All tunables live here; NOTES.md documents the targets.

const (
	minute = int64(60_000)
	hour   = 60 * minute

	maxIntraGap   = 25 * minute // CONTRACT: intra-session gaps <= 25 min
	minInterGap   = 35 * minute // CONTRACT: inter-session gaps >= 35 min
	midnightGuard = 20 * minute // keeps returning visitors >= 40 min apart across midnight

	returnPerMille = 300 // 30% of a day's visitors come back the next day

	// eventsPerVisitor converts the event budget into a visitor count. It is
	// measured, not derived (see TestEventsPerVisitorCalibration).
	eventsPerVisitor = 3.302

	singlePageviewPPM = 618_000 // P(session has 1 pageview); rest is 2 + geometric
	geomContinuePPM   = 745_500 // P(one more pageview) in the geometric tail
	maxPageviews      = 60

	entryHomePPM     = 300_000 // P(entry page is "/") before the Zipf draw
	homeToPricingPPM = 220_000 // P(next page is /pricing | current is /)
	customPricingPPM = 300_000 // P(custom event after a /pricing pageview)
	customOtherPPM   = 100_000 // P(custom event after any other pageview)

	pathZipfS = 1.0
	siteZipfS = 1.25
	maxPaths  = 20_000 // distinct paths on the biggest site
	minPaths  = 40
)

func zipfWeight(k int, s float64) float64 {
	if s == 1 {
		return 1 / float64(k)
	}
	return 1 / math.Pow(float64(k), s)
}

// Weekly factors, Sunday first; their mean is 1.
var weekly = [7]float64{0.88, 1.06, 1.10, 1.10, 1.06, 0.98, 0.82}

// Session starts by site-local hour (relative weights).
var hourly = []int64{30, 20, 14, 10, 9, 12, 22, 38, 55, 68, 75, 77, 74, 76, 77, 74, 70, 64, 60, 62, 64, 60, 50, 40}

var sessionsPerVisitor = []int64{820, 130, 40, 10} // 1..4 sessions per visitor-day

// Time on page before the next pageview, ms buckets {lo, hi, weight}.
var pageGapBuckets = [][3]int64{
	{1_000, 5_000, 80}, {5_000, 15_000, 170}, {15_000, 30_000, 180}, {30_000, 60_000, 200},
	{60_000, 120_000, 160}, {120_000, 300_000, 120}, {300_000, 600_000, 60}, {600_000, maxIntraGap + 1, 30},
}

// Delay from a pageview to a custom event on that page.
var customGapBuckets = [][3]int64{{1_000, 10_000, 40}, {10_000, 60_000, 40}, {60_000, 180_000, 20}}

// Extra idle time between sessions, on top of minInterGap.
var interGapBuckets = [][3]int64{{0, 30 * minute, 25}, {30 * minute, 2 * hour, 35}, {2 * hour, 6 * hour, 30}, {6 * hour, 12 * hour, 10}}

type country struct {
	code   string
	weight int64
}

type location struct {
	region string
	city   string
}

var countryLocations = map[string][]location{
	"US": {{"California", "San Francisco"}, {"New York", "New York"}, {"Texas", "Austin"}, {"Washington", "Seattle"}},
	"IN": {{"Karnataka", "Bengaluru"}, {"Maharashtra", "Mumbai"}, {"Delhi", "New Delhi"}},
	"DE": {{"Berlin", "Berlin"}, {"Bavaria", "Munich"}},
	"GB": {{"England", "London"}, {"Scotland", "Edinburgh"}},
	"BR": {{"Sao Paulo", "Sao Paulo"}, {"Rio de Janeiro", "Rio de Janeiro"}},
	"FR": {{"Ile-de-France", "Paris"}, {"Auvergne-Rhone-Alpes", "Lyon"}},
	"CA": {{"Ontario", "Toronto"}, {"British Columbia", "Vancouver"}},
	"JP": {{"Tokyo", "Tokyo"}, {"Osaka", "Osaka"}},
	"AU": {{"New South Wales", "Sydney"}, {"Victoria", "Melbourne"}},
	"NP": {{"Bagmati", "Kathmandu"}},
}

func countryLocation(code string, selector uint64) location {
	locations := countryLocations[code]
	if len(locations) == 0 {
		return location{region: code + " Region", city: code + " City"}
	}
	return locations[selector%uint64(len(locations))]
}

var countries = []country{
	{"US", 220}, {"IN", 120}, {"DE", 70}, {"GB", 60}, {"BR", 50}, {"FR", 40}, {"CA", 40}, {"JP", 30},
	{"ES", 30}, {"IT", 30}, {"NL", 25}, {"AU", 25}, {"PL", 20}, {"MX", 20}, {"ID", 20}, {"TR", 15},
	{"KR", 15}, {"SE", 10}, {"CH", 10}, {"NP", 10}, {"PK", 10}, {"NG", 10}, {"PH", 10}, {"VN", 10},
	{"AR", 10}, {"ZA", 10}, {"UA", 10}, {"BD", 10}, {"EG", 10}, {"SG", 8}, {"IE", 7}, {"AT", 7},
	{"BE", 7}, {"MY", 5}, {"TH", 5}, {"SA", 5}, {"AE", 5}, {"DK", 5}, {"NO", 5}, {"FI", 5},
	{"PT", 5}, {"CZ", 5}, {"RO", 5}, {"IL", 5}, {"CO", 5}, {"CL", 5}, {"GR", 4}, {"HU", 4},
	{"NZ", 3}, {"KE", 3},
}

const homeCountryPPM = 300_000 // P(visitor is from the site's home country)

type zoneDef struct {
	name string
	home string // home country code, "" for none
}

// The first five sites get the CONTRACT's required zones; the rest cycle.
var zoneFirst = []zoneDef{
	{"America/New_York", "US"}, {"Europe/Berlin", "DE"}, {"Asia/Kolkata", "IN"}, {"UTC", ""}, {"Asia/Kathmandu", "NP"},
}

var zoneCycle = []zoneDef{
	{"Europe/London", "GB"}, {"America/Los_Angeles", "US"}, {"Asia/Kolkata", "IN"}, {"Europe/Berlin", "DE"},
	{"America/New_York", "US"}, {"UTC", ""}, {"America/Sao_Paulo", "BR"}, {"Asia/Tokyo", "JP"},
	{"Australia/Sydney", "AU"}, {"Europe/Paris", "FR"}, {"America/Chicago", "US"}, {"Asia/Kathmandu", "NP"},
	{"Asia/Singapore", "SG"}, {"Europe/Madrid", "ES"}, {"Pacific/Auckland", "NZ"},
}

type uaDef struct {
	ua, browser, os, device string
	weight                  int64
}

const (
	winChrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36"
	macChrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36"
	andChrome = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Mobile Safari/537.36"
	iosSafari = "Mozilla/5.0 (iPhone; CPU iPhone OS %s like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/%s Mobile/15E148 Safari/604.1"
)

// User agents with their parsed ground truth; weights sum to 1000
// (desktop 420, mobile 550, tablet 30).
var userAgents = []uaDef{
	// desktop
	{sprintf(winChrome, "131"), "Chrome", "Windows", "desktop", 70},
	{sprintf(winChrome, "130"), "Chrome", "Windows", "desktop", 35},
	{sprintf(winChrome, "129"), "Chrome", "Windows", "desktop", 15},
	{sprintf(macChrome, "131"), "Chrome", "macOS", "desktop", 35},
	{sprintf(macChrome, "130"), "Chrome", "macOS", "desktop", 15},
	{sprintf(macChrome, "129"), "Chrome", "macOS", "desktop", 10},
	{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36", "Chrome", "Linux", "desktop", 20},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15", "Safari", "macOS", "desktop", 30},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15", "Safari", "macOS", "desktop", 15},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15", "Safari", "macOS", "desktop", 15},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0", "Edge", "Windows", "desktop", 35},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0", "Edge", "Windows", "desktop", 15},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0", "Edge", "Windows", "desktop", 10},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0", "Edge", "macOS", "desktop", 10},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0", "Firefox", "Windows", "desktop", 25},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0", "Firefox", "Windows", "desktop", 10},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0", "Firefox", "Windows", "desktop", 10},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0", "Firefox", "macOS", "desktop", 10},
	{"Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0", "Firefox", "Linux", "desktop", 10},
	{"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0", "Firefox", "Linux", "desktop", 8},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0", "Opera", "Windows", "desktop", 12},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0", "Opera", "macOS", "desktop", 5},
	// mobile
	{sprintf(andChrome, "131"), "Chrome", "Android", "mobile", 120},
	{sprintf(andChrome, "130"), "Chrome", "Android", "mobile", 60},
	{sprintf(andChrome, "129"), "Chrome", "Android", "mobile", 25},
	{sprintf(iosSafari, "18_1", "18.1"), "Safari", "iOS", "mobile", 90},
	{sprintf(iosSafari, "18_0", "18.0"), "Safari", "iOS", "mobile", 50},
	{sprintf(iosSafari, "17_6", "17.6"), "Safari", "iOS", "mobile", 40},
	{sprintf(iosSafari, "17_5", "17.5"), "Safari", "iOS", "mobile", 20},
	{sprintf(iosSafari, "16_7", "16.6"), "Safari", "iOS", "mobile", 20},
	{"Mozilla/5.0 (Linux; Android 14; SAMSUNG SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36", "Samsung Internet", "Android", "mobile", 40},
	{"Mozilla/5.0 (Linux; Android 13; SAMSUNG SM-A546B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0.0.0 Mobile Safari/537.36", "Samsung Internet", "Android", "mobile", 20},
	{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0.6778.73 Mobile/15E148 Safari/604.1", "Chrome", "iOS", "mobile", 30},
	{"Mozilla/5.0 (Android 14; Mobile; rv:133.0) Gecko/133.0 Firefox/133.0", "Firefox", "Android", "mobile", 15},
	{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/133.0 Mobile/15E148 Safari/605.1.15", "Firefox", "iOS", "mobile", 10},
	{"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36 EdgA/131.0.0.0", "Edge", "Android", "mobile", 10},
	// tablet
	{"Mozilla/5.0 (iPad; CPU OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1", "Safari", "iOS", "tablet", 15},
	{"Mozilla/5.0 (iPad; CPU OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1", "Safari", "iOS", "tablet", 5},
	{"Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36", "Chrome", "Android", "tablet", 6},
	{"Mozilla/5.0 (Linux; Android 14; SAMSUNG SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Safari/537.36", "Samsung Internet", "Android", "tablet", 4},
}

// Screen widths by device class (and iOS vs other for phones and tablets),
// chosen so that width-based classifiers (<576 mobile, <992 tablet) agree.
var screens = map[string][][2]int64{
	"desktop":      {{1920, 300}, {1536, 120}, {1440, 110}, {1366, 110}, {2560, 90}, {1280, 80}, {1680, 60}, {1600, 50}, {3440, 20}},
	"mobile/iOS":   {{390, 300}, {393, 200}, {375, 150}, {430, 150}, {414, 100}, {428, 70}, {320, 30}},
	"mobile/other": {{412, 350}, {360, 250}, {384, 150}, {393, 100}, {411, 80}, {400, 70}},
	"tablet/iOS":   {{820, 350}, {810, 300}, {768, 200}, {834, 150}},
	"tablet/other": {{800, 500}, {768, 300}, {962, 200}},
}

// A channel is a session's acquisition: referrer URL plus UTM tags.
type channelDef struct {
	ref, src, medium, campaign string
	weight                     int64 // per 10,000 sessions
}

var channels = func() []channelDef {
	c := []channelDef{
		{"", "", "", "", 3600},
		{"", "newsletter", "email", "weekly-digest", 250},
		{"", "newsletter", "email", "product-update", 150},
		{"https://www.google.com/", "", "", "", 2700},
		{"https://www.google.com/", "google", "cpc", "brand-search", 200},
		{"https://www.google.com/", "google", "cpc", "generic-analytics", 100},
		{"https://www.bing.com/", "", "", "", 290},
		{"https://duckduckgo.com/", "", "", "", 200},
		{"https://www.ecosia.org/", "", "", "", 30},
		{"https://l.facebook.com/", "", "", "", 300},
		{"https://m.facebook.com/", "", "", "", 80},
		{"https://l.facebook.com/", "facebook", "social", "autumn-sale", 150},
		{"https://t.co/", "", "", "", 250},
		{"https://t.co/", "twitter", "social", "launch-week", 100},
		{"https://www.linkedin.com/", "", "", "", 150},
		{"https://www.linkedin.com/", "linkedin", "social", "hiring", 50},
		{"https://www.reddit.com/", "", "", "", 250},
		{"https://news.ycombinator.com/", "", "", "", 150},
		{"https://github.com/", "", "", "", 120},
		{"https://www.youtube.com/", "", "", "", 80},
		{"https://l.instagram.com/", "", "", "", 60},
		{"https://www.producthunt.com/", "producthunt", "referral", "ph-launch", 50},
		{"https://stackoverflow.com/", "", "", "", 60},
		{"https://dev.to/", "", "", "", 40},
		{"https://medium.com/", "", "", "", 40},
		{"https://chatgpt.com/", "", "", "", 80},
		{"https://www.perplexity.ai/", "", "", "", 30},
		{"https://www.baidu.com/", "", "", "", 20},
		{"https://yandex.ru/", "", "", "", 20},
	}
	// A long tail of small referring blogs: 400 per 10,000 over 40 hosts.
	for i := 1; i <= 40; i++ {
		c = append(c, channelDef{ref: sprintf("https://blog%d.example.net/", i), weight: 10})
	}
	return c
}()

// Custom events. Props are pre-serialized with keys in sorted order.
type propCombo struct {
	json   string
	weight int64
	plan   int // index into plans for signup, else -1
}

var plans = []string{"free", "pro", "team"}

const (
	evPageview = iota
	evSignup
	evDownload
	evOutbound
)

var eventNames = []string{"pageview", "signup", "download", "outbound"}

var propCombos = func() [][]propCombo {
	out := make([][]propCombo, len(eventNames))
	planW := []int64{60, 30, 10}
	methods := [][2]any{{"email", int64(50)}, {"google", int64(35)}, {"github", int64(15)}}
	billing := [][2]any{{"monthly", int64(70)}, {"yearly", int64(30)}}
	for pi, p := range plans {
		for _, m := range methods {
			for _, b := range billing {
				out[evSignup] = append(out[evSignup], propCombo{
					json:   sprintf(`{"billing":"%s","method":"%s","plan":"%s"}`, b[0], m[0], p),
					weight: planW[pi] * m[1].(int64) * b[1].(int64), plan: pi})
			}
		}
	}
	files := [][3]any{
		{"/downloads/app-2.4.1.dmg", "dmg", int64(25)}, {"/downloads/app-2.4.1.exe", "exe", int64(30)},
		{"/downloads/app-2.4.1.tar.gz", "tar.gz", int64(10)}, {"/files/whitepaper.pdf", "pdf", int64(20)},
		{"/files/pricing-sheet.pdf", "pdf", int64(10)}, {"/files/case-study.pdf", "pdf", int64(5)},
	}
	positions := [][2]any{{"hero", int64(40)}, {"body", int64(45)}, {"footer", int64(15)}}
	for _, f := range files {
		for _, p := range positions {
			out[evDownload] = append(out[evDownload], propCombo{
				json:   sprintf(`{"file":"%s","position":"%s","type":"%s"}`, f[0], p[0], f[1]),
				weight: f[2].(int64) * p[1].(int64), plan: -1})
		}
	}
	links := [][3]any{
		{"https://github.com/acme/sdk", "github.com", int64(30)}, {"https://twitter.com/acme", "twitter.com", int64(15)},
		{"https://www.linkedin.com/company/acme", "www.linkedin.com", int64(10)}, {"https://docs.partner.example/setup", "docs.partner.example", int64(20)},
		{"https://status.acme.example/", "status.acme.example", int64(10)}, {"https://www.youtube.com/@acme", "www.youtube.com", int64(15)},
	}
	linkPos := [][2]any{{"header", int64(30)}, {"body", int64(50)}, {"footer", int64(20)}}
	for _, l := range links {
		for _, p := range linkPos {
			out[evOutbound] = append(out[evOutbound], propCombo{
				json:   sprintf(`{"domain":"%s","position":"%s","url":"%s"}`, l[1], p[0], l[0]),
				weight: l[2].(int64) * p[1].(int64), plan: -1})
		}
	}
	return out
}()

// Which custom event fires: on /pricing it is mostly signup.
var customOnPricing = []int64{0, 75, 0, 25} // by event index
var customElsewhere = []int64{0, 5, 55, 40}

// Path vocabulary: a fixed head (so "/" and "/pricing" exist on every site,
// at ranks 1 and 3) and a generated long tail.
var pathHead = []string{"/", "/blog", "/pricing", "/docs", "/features", "/about", "/signup", "/login",
	"/contact", "/changelog", "/integrations", "/customers", "/careers", "/privacy", "/terms"}

var pathTemplates = []struct {
	prefix string
	words  int
	weight int64
}{
	{"/blog/", 3, 40}, {"/docs/", 2, 25}, {"/products/", 2, 10}, {"/help/", 2, 10}, {"/news/", 3, 10}, {"/tag/", 1, 5},
}

var docSections = []string{"getting-started", "api", "guides", "reference", "sdk", "self-hosting", "integrations"}

var words = []string{
	"analytics", "privacy", "dashboard", "events", "funnel", "goals", "retention", "cohort", "traffic", "growth",
	"seo", "content", "marketing", "launch", "pricing", "team", "remote", "startup", "product", "design",
	"engineering", "postgres", "golang", "react", "typescript", "docker", "kubernetes", "linux", "cloud", "edge",
	"cache", "latency", "scale", "billing", "invoice", "export", "import", "api", "webhook", "plugin",
	"theme", "mobile", "desktop", "browser", "cookie", "consent", "gdpr", "ccpa", "audit", "security",
	"backup", "restore", "migration", "release", "roadmap", "feedback", "survey", "metrics", "report", "weekly",
	"monthly", "yearly", "guide", "tutorial", "tips", "tricks", "howto", "faq", "story", "case",
	"study", "interview", "podcast", "video", "webinar", "event", "meetup", "conference", "summit", "community",
	"open", "source", "license", "fork", "commit", "deploy", "monitor", "alert", "incident", "uptime",
	"server", "database", "query", "index", "partition", "rollup", "session", "visitor", "referrer", "campaign",
	"newsletter", "email", "social", "search", "ads", "budget", "revenue", "churn", "upgrade", "trial",
	"onboarding", "checklist", "template", "workflow", "automation", "integration", "slack", "github", "stripe", "shopify",
}
