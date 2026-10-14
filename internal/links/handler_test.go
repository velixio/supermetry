package links

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/enrich"
	"supermetry/internal/ingest"
	"supermetry/internal/sites"
)

type registry map[int32]sites.Site

func (r registry) ByPublicID(id string) (sites.Site, bool) {
	for _, s := range r {
		if s.PublicID == id {
			return s, true
		}
	}
	return sites.Site{}, false
}
func (r registry) Site(id int32) (sites.Site, bool) { s, ok := r[id]; return s, ok }
func (r registry) All() []sites.Site                { return nil }

const (
	chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	phoneUA  = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
)

type fixture struct {
	handler  *Handler
	recorder *Recorder
	mux      *http.ServeMux
	lookups  *int
	now      *time.Time
	mu       sync.Mutex
	links    map[string]Link
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	enricher, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	kathmandu, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 20, 30, 0, 0, time.UTC) // 02:15 next day in Kathmandu (+5:45)
	lookups := 0
	f := &fixture{recorder: NewRecorder(nil, RecorderOptions{}), now: &now, lookups: &lookups, links: map[string]Link{}}
	f.recorder.Views = NewSiteViews()
	f.handler = &Handler{
		Recorder: f.recorder,
		Sites: registry{
			1: {ID: 1, PublicID: "pubsite0001", TZ: kathmandu},
			2: {ID: 2, PublicID: "pubsite0002", RespectPrivacySignals: true},
		},
		Enricher: enricher,
		Now:      func() time.Time { return now },
		Lookup: func(_ context.Context, slug string) (Link, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			lookups++
			if l, ok := f.links[slug]; ok {
				return l, nil
			}
			return Link{}, ErrNotFound
		},
	}
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("GET /l/{slug}", f.handler.ServeRedirect)
	f.mux.HandleFunc("GET /px/{file}", f.handler.ServePixel)
	return f
}

func (f *fixture) put(l Link) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[l.Slug] = l
}

func (f *fixture) get(path string, headers ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "203.0.113.9:4000"
	request.Header.Set("User-Agent", chromeUA)
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	return response
}

func TestRedirectUsesStoredDestinationAndAppliesUTM(t *testing.T) {
	f := newFixture(t)
	f.put(Link{ID: 1, SiteID: 1, Slug: "sale", Destination: "https://shop.example/offer?x=1", Enabled: true,
		UTM: UTM{Source: "mail", Campaign: "spring"}})
	response := f.get("/l/sale?next=https://evil.example&url=//evil.example&redirect=javascript:alert(1)")
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d", response.Code)
	}
	if got, want := response.Header().Get("Location"), "https://shop.example/offer?x=1&utm_source=mail&utm_campaign=spring"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("redirect must not be cacheable: %q", response.Header().Get("Cache-Control"))
	}
}

func TestRedirectStatuses(t *testing.T) {
	f := newFixture(t)
	past, future := f.now.Add(-time.Hour), f.now.Add(time.Hour)
	limit := int32(5)
	f.put(Link{ID: 1, SiteID: 1, Slug: "off", Destination: "https://a.example", Enabled: false})
	f.put(Link{ID: 2, SiteID: 1, Slug: "old", Destination: "https://a.example", Enabled: true, ExpiresAt: &past})
	f.put(Link{ID: 3, SiteID: 1, Slug: "soon", Destination: "https://a.example", Enabled: true, ExpiresAt: &future})
	f.put(Link{ID: 4, SiteID: 1, Slug: "full", Destination: "https://a.example", Enabled: true, ClickLimit: &limit, Clicks: 5})
	f.put(Link{ID: 5, SiteID: 99, Slug: "orphan", Destination: "https://a.example", Enabled: true})
	for slug, want := range map[string]int{
		"off": http.StatusNotFound, "old": http.StatusGone, "soon": http.StatusFound, "full": http.StatusGone,
		"orphan": http.StatusNotFound, "missing": http.StatusNotFound, "UPPER": http.StatusNotFound,
		"bad.slug": http.StatusNotFound,
	} {
		if got := f.get("/l/" + slug).Code; got != want {
			t.Errorf("/l/%s = %d, want %d", slug, got, want)
		}
	}
}

func TestClickLimitCountsServedAndPendingClicks(t *testing.T) {
	f := newFixture(t)
	limit := int32(3)
	f.put(Link{ID: 7, SiteID: 1, Slug: "capped", Destination: "https://a.example", Enabled: true, ClickLimit: &limit, Clicks: 1})
	var codes []int
	for range 4 {
		codes = append(codes, f.get("/l/capped").Code)
	}
	want := []int{302, 302, 410, 410}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v, want %v", codes, want)
		}
	}
	if got := f.recorder.Pending(7); got != 2 {
		t.Fatalf("pending = %d, want 2", got)
	}
}

func TestClicksAggregateInSiteTimeZoneWithoutIdentifiers(t *testing.T) {
	f := newFixture(t)
	f.put(Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://a.example", Enabled: true})
	f.get("/l/go", "Referer", "https://www.News.example/path?token=secret")
	f.get("/l/go", "Referer", "https://www.News.example/other")
	f.get("/l/go", "User-Agent", phoneUA)
	clicks, impressions := f.recorder.snapshot()
	if len(impressions) != 0 {
		t.Fatalf("unexpected impressions %v", impressions)
	}
	want := map[clickKey]int64{
		{1, "2026-10-13", "", "news.example", "desktop"}: 2, // 20:30 UTC is already the 13th in Kathmandu
		{1, "2026-10-13", "", "", "mobile"}:              1,
	}
	if len(clicks) != len(want) {
		t.Fatalf("clicks = %v, want %v", clicks, want)
	}
	for key, n := range want {
		if clicks[key] != n {
			t.Fatalf("clicks[%v] = %d, want %d (all: %v)", key, clicks[key], n, clicks)
		}
	}
}

func TestBotsAndPrivacySignalsAreRedirectedButNotCounted(t *testing.T) {
	f := newFixture(t)
	f.put(Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://a.example", Enabled: true})
	f.put(Link{ID: 2, SiteID: 2, Slug: "private", Destination: "https://a.example", Enabled: true})
	for _, ua := range []string{"Googlebot/2.1 (+http://www.google.com/bot.html)", "", "Mozilla/5.0 HeadlessChrome/120.0"} {
		if got := f.get("/l/go", "User-Agent", ua).Code; got != http.StatusFound {
			t.Fatalf("bot UA %q status = %d", ua, got)
		}
	}
	f.get("/l/private", "DNT", "1")
	f.get("/l/private", "Sec-GPC", "1")
	if clicks, _ := f.recorder.snapshot(); len(clicks) != 0 {
		t.Fatalf("bots and opted-out visitors were counted: %v", clicks)
	}
	f.get("/l/private")
	if clicks, _ := f.recorder.snapshot(); len(clicks) != 1 {
		t.Fatalf("normal visitor on a respecting site not counted: %v", clicks)
	}
}

func TestHeadRequestsAreNotCounted(t *testing.T) {
	f := newFixture(t)
	f.put(Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://a.example", Enabled: true})
	request := httptest.NewRequest(http.MethodHead, "/l/go", nil)
	request.Header.Set("User-Agent", chromeUA)
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("status = %d", response.Code)
	}
	if clicks, _ := f.recorder.snapshot(); len(clicks) != 0 {
		t.Fatalf("HEAD counted: %v", clicks)
	}
}

func TestRedirectCachesLookupsAndForgetApplies(t *testing.T) {
	f := newFixture(t)
	f.put(Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://a.example", Enabled: true})
	for range 5 {
		f.get("/l/go")
		f.get("/l/nope")
	}
	if *f.lookups != 2 {
		t.Fatalf("lookups = %d, want 2 (one hit and one miss, then cached)", *f.lookups)
	}
	f.put(Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://b.example", Enabled: true})
	if got := f.get("/l/go").Header().Get("Location"); got != "https://a.example" {
		t.Fatalf("expected cached destination, got %q", got)
	}
	f.handler.Forget("go")
	if got := f.get("/l/go").Header().Get("Location"); got != "https://b.example" {
		t.Fatalf("Forget did not apply: %q", got)
	}
	*f.now = f.now.Add(positiveTTL + time.Second)
	f.put(Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://c.example", Enabled: true})
	if got := f.get("/l/go").Header().Get("Location"); got != "https://c.example" {
		t.Fatalf("expired cache entry not refreshed: %q", got)
	}
}

func TestRateLimitsRedirectsAndMisses(t *testing.T) {
	f := newFixture(t)
	f.handler.Limiter = ingest.NewTokenBuckets(0, 3)
	f.put(Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://a.example", Enabled: true})
	var codes []int
	for range 5 {
		codes = append(codes, f.get("/l/go").Code)
	}
	if codes[2] != 302 || codes[3] != 429 || codes[4] != 429 {
		t.Fatalf("codes = %v", codes)
	}
	if f.get("/l/go").Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}

	g := newFixture(t)
	g.handler.MissLimiter = ingest.NewTokenBuckets(0, 2)
	var misses []int
	for i := range 4 {
		misses = append(misses, g.get("/l/unknown"+string(rune('a'+i))).Code)
	}
	if misses[0] != 404 || misses[1] != 404 || misses[2] != 429 {
		t.Fatalf("miss codes = %v", misses)
	}
}

func TestLookupFailureIsServiceUnavailableAndNotCached(t *testing.T) {
	f := newFixture(t)
	failing := true
	f.handler.Lookup = func(context.Context, string) (Link, error) {
		if failing {
			return Link{}, context.DeadlineExceeded
		}
		return Link{ID: 1, SiteID: 1, Slug: "go", Destination: "https://a.example", Enabled: true}, nil
	}
	if got := f.get("/l/go").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", got)
	}
	failing = false
	if got := f.get("/l/go").Code; got != http.StatusFound {
		t.Fatalf("failure was cached: %d", got)
	}
}

func TestPixelAlwaysReturnsGIFAndRecordsImpressions(t *testing.T) {
	f := newFixture(t)
	f.recorder.Views.Register(1, "spring-2026")
	for _, target := range []string{"/px/pubsite0001.gif?c=spring-2026", "/px/pubsite0001.gif?c=unregistered", "/px/pubsite0001.gif?c=%3Cscript%3E", "/px/pubsite0001.gif"} {
		response := f.get(target)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/gif" || response.Body.Len() != 43 {
			t.Fatalf("%s: status=%d type=%q len=%d", target, response.Code, response.Header().Get("Content-Type"), response.Body.Len())
		}
		if response.Header().Get("Set-Cookie") != "" {
			t.Fatal("pixel must not set cookies")
		}
	}
	// Unknown site, wrong extension, bots and opted-out visitors still get an image but no count.
	f.get("/px/nosuchsite.gif")
	f.get("/px/pubsite0001.png")
	f.get("/px/pubsite0001.gif", "User-Agent", "Googlebot/2.1")
	f.get("/px/pubsite0002.gif", "DNT", "1")
	_, impressions := f.recorder.snapshot()
	want := map[impressionKey]int64{
		{1, "2026-10-13", "spring-2026", Place{Source: SourceOther}}: 1,
	}
	if len(impressions) != len(want) {
		t.Fatalf("impressions = %v, want %v", impressions, want)
	}
	for key, n := range want {
		if impressions[key] != n {
			t.Fatalf("impressions = %v, want %v", impressions, want)
		}
	}
	if total, _, ok := f.recorder.Views.Snapshot(1, "spring-2026", "2026-10-13", 1); !ok || total != 1 {
		t.Fatalf("registered cache total=%d ok=%v, want 1", total, ok)
	}
	if _, _, ok := f.recorder.Views.Snapshot(1, "unregistered", "2026-10-13", 1); ok {
		t.Fatal("an unregistered campaign entered the cache")
	}
}

func TestPixelRateLimitStillServesImage(t *testing.T) {
	f := newFixture(t)
	f.recorder.Views.Register(1, "limited")
	f.handler.Limiter = ingest.NewTokenBuckets(0, 1)
	for range 3 {
		if response := f.get("/px/pubsite0001.gif?c=limited"); response.Code != http.StatusOK || response.Body.Len() != 43 {
			t.Fatalf("status=%d len=%d", response.Code, response.Body.Len())
		}
	}
	if _, impressions := f.recorder.snapshot(); len(impressions) != 1 {
		t.Fatalf("impressions = %v", impressions)
	}
	var total int64
	_, impressions := f.recorder.snapshot()
	for _, n := range impressions {
		total += n
	}
	if total != 1 {
		t.Fatalf("rate-limited hits were recorded: total %d", total)
	}
}

func TestRecorderRejectsEmptyAndUnregisteredCampaigns(t *testing.T) {
	recorder := NewRecorder(nil, RecorderOptions{})
	recorder.Impression(1, "2026-10-13", "known", Place{Country: ""})
	recorder.Impression(1, "2026-10-13", "", Place{Country: ""})
	if _, impressions := recorder.snapshot(); len(impressions) != 0 {
		t.Fatalf("a recorder without a registry counted impressions: %v", impressions)
	}

	recorder.Views = NewSiteViews()
	recorder.Views.Register(1, "known")
	if !recorder.Views.Registered(1, "known") || recorder.Views.Registered(1, "unknown") {
		t.Fatal("registered lookup does not match the registry")
	}
	recorder.Impression(1, "2026-10-13", "known", Place{Country: ""})
	recorder.Impression(1, "2026-10-13", "unknown", Place{Country: ""})
	_, impressions := recorder.snapshot()
	if len(impressions) != 1 || impressions[impressionKey{1, "2026-10-13", "known", Place{Source: SourceOther}}] != 1 {
		t.Fatalf("impressions = %v, want only the registered campaign", impressions)
	}
}

func TestBadgeStatsCountsAndReportsSiteViews(t *testing.T) {
	f := newFixture(t)
	f.recorder.Views = NewSiteViews()
	f.recorder.Views.Register(1, "spring")
	f.recorder.Views.Add(1, "spring", "2026-10-12", 5) // history already stored
	request := func(target string) (int64, []int64, bool) {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.RemoteAddr = "203.0.113.9:4000"
		r.Header.Set("User-Agent", chromeUA)
		return f.handler.BadgeStats(r, r.URL.Path[len("/px/"):len(r.URL.Path)-len(".svg")], 3)
	}
	// 20:30 UTC is already 2026-10-13 in Kathmandu.
	total, series, ok := request("/px/pubsite0001.svg?c=spring")
	if !ok || total != 6 || len(series) != 3 || series[2] != 1 || series[1] != 5 || series[0] != 0 {
		t.Fatalf("total=%d series=%v ok=%v", total, series, ok)
	}
	if total, _, ok := request("/px/pubsite0001.svg?c=spring&preview=1"); !ok || total != 6 {
		t.Fatalf("a preview must not count, total=%d", total)
	}
	if _, _, ok := request("/px/pubsite0001.svg?c=arbitrary"); ok {
		t.Fatal("an unregistered tag must not expose badge counts")
	}
	if _, _, ok := request("/px/pubsite0001.svg"); ok {
		t.Fatal("a missing tag must not expose site-wide badge counts")
	}
	if _, _, ok := request("/px/nosuchsite.svg"); ok {
		t.Fatal("an unknown site must not produce badge numbers")
	}
	if _, _, ok := request("/px/pubsite0002.svg?c=spring"); ok {
		t.Fatal("the same tag on another site must not share a registration")
	}
	_, impressions := f.recorder.snapshot()
	var arbitrary int64
	for key, n := range impressions {
		if key.siteID == 1 && key.campaign == "arbitrary" {
			arbitrary += n
		}
	}
	if arbitrary != 0 {
		t.Fatalf("unregistered impression was buffered: %v", impressions)
	}
}

type fakeProxyLimiter struct{ allow bool }

func (l fakeProxyLimiter) Allow(int64, string) bool { return l.allow }

func TestPixelCountsImageProxyHitsForRegisteredTags(t *testing.T) {
	f := newFixture(t)
	f.recorder.Views.Register(1, "readme")
	f.handler.DetectProxy = func(ua string) (string, bool) { return "camo", strings.Contains(ua, "github-camo") }
	f.handler.ProxyLimiter = fakeProxyLimiter{allow: true}
	camo := []string{"User-Agent", "github-camo (876de43e)"}
	// GitHub's image proxy is not a bot: it counts, with no country, on a registered tag only.
	f.get("/px/pubsite0001.gif?c=readme", camo...)
	f.get("/px/pubsite0001.gif?c=readme", camo...)
	f.get("/px/pubsite0001.gif?c=other", camo...)
	f.get("/px/pubsite0001.gif", camo...)
	f.handler.ProxyLimiter = fakeProxyLimiter{allow: false}
	f.get("/px/pubsite0001.gif?c=readme", camo...) // over the proxy limit: served, not counted
	_, impressions := f.recorder.snapshot()
	if len(impressions) != 1 || impressions[impressionKey{1, "2026-10-13", "readme", Place{Source: SourceGitHub}}] != 2 {
		t.Fatalf("impressions = %v, want 2 for readme", impressions)
	}
}
