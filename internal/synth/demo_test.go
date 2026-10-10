package synth

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDemoVisitsDeterministicAndPartitioned(t *testing.T) {
	tz, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, tz)
	d := NewDemo(DemoConfig{Seed: 7, TZ: tz, Visitors: 150, Anchor: anchor})
	from := anchor.AddDate(0, 0, -3)
	mid := anchor.AddDate(0, 0, -1).Add(5*time.Hour + 17*time.Minute)
	to := anchor.AddDate(0, 0, 1)
	all := d.Visits(from, to)
	a, b := d.Visits(from, mid), d.Visits(mid, to)
	if len(a)+len(b) != len(all) {
		t.Fatalf("partition: %d+%d != %d", len(a), len(b), len(all))
	}
	if !reflect.DeepEqual(append(a, b...), all) {
		t.Fatal("windows differ from whole")
	}
	if !reflect.DeepEqual(all, NewDemo(DemoConfig{Seed: 7, TZ: tz, Visitors: 150, Anchor: anchor}).Visits(from, to)) {
		t.Fatal("not deterministic")
	}
	for i := 1; i < len(all); i++ {
		if all[i].Start.Before(all[i-1].Start) {
			t.Fatal("not in start order")
		}
	}
}

func TestDemoCoversEveryFeature(t *testing.T) {
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	d := NewDemo(DemoConfig{Seed: 1, TZ: time.UTC, Visitors: 150, Anchor: anchor})
	visits := d.Visits(anchor.AddDate(0, 0, -DemoWindowDays), anchor.Add(12*time.Hour))
	if len(visits) < 9_000 {
		t.Fatalf("visits = %d, want at least 9000", len(visits))
	}

	var utm [5]bool
	countries, devices, names, hosts := map[string]bool{}, map[int16]bool{}, map[string]bool{}, map[string]bool{}
	profiles := map[uint64]DemoProfile{}
	seenVisitors := map[uint64]bool{}
	returning, replay := 0, 0
	for _, visit := range visits {
		utm[0] = utm[0] || visit.UTMSource != ""
		utm[1] = utm[1] || visit.UTMMedium != ""
		utm[2] = utm[2] || visit.UTMCampaign != ""
		utm[3] = utm[3] || visit.UTMContent != ""
		utm[4] = utm[4] || visit.UTMTerm != ""
		if visit.City != "" {
			countries[visit.Country] = true
		}
		devices[visit.Device] = true
		hosts[visit.Host] = true
		if visit.Language == "" {
			t.Fatal("visit has no language")
		}
		if seenVisitors[visit.VisitorKey] {
			returning++
		}
		seenVisitors[visit.VisitorKey] = true
		if visit.Profile != nil {
			if visit.Profile.Traits["country"] != visit.Country {
				t.Fatalf("profile country %q differs from visit country %q", visit.Profile.Traits["country"], visit.Country)
			}
			if previous, ok := profiles[visit.VisitorKey]; ok && !reflect.DeepEqual(previous, *visit.Profile) {
				t.Fatalf("profile for visitor %d changed", visit.VisitorKey)
			}
			profiles[visit.VisitorKey] = *visit.Profile
		}
		if visit.Replay {
			replay++
			if visit.Start.Before(anchor.AddDate(0, 0, -14)) {
				t.Fatalf("replay outside recent window: %s", visit.Start)
			}
		}
		for _, hit := range visit.Hits {
			if hit.Kind == 1 {
				names[hit.Name] = true
			}
			if hit.RevenueMicros > 0 && hit.Name != "Purchase" {
				t.Fatalf("revenue on %q", hit.Name)
			}
			if hit.Name == "Web Vitals" {
				assertIntPropAtMost(t, hit.Props, "lcp", 60_000)
				assertIntPropAtMost(t, hit.Props, "cls", 10_000)
				assertIntPropAtMost(t, hit.Props, "inp", 60_000)
			}
			if hit.Kind == 2 && strings.HasPrefix(hit.Path, "/blog/") && hit.ScrollPct >= 75 {
				names["deep-scroll"] = true
			}
			if len(hit.Clicks) > 0 && hit.Path != "/" && hit.Path != "/pricing" && !strings.HasPrefix(hit.Path, "/features/") {
				t.Fatalf("clicks on unsupported path %q", hit.Path)
			}
		}
	}

	if !slices.Equal(utm[:], []bool{true, true, true, true, true}) {
		t.Fatalf("UTM coverage = %v", utm)
	}
	if len(countries) < 6 {
		t.Fatalf("countries with cities = %d, want at least 6", len(countries))
	}
	for _, device := range []int16{0, 1, 2} {
		if !devices[device] {
			t.Errorf("device %d missing", device)
		}
	}
	for _, name := range []string{"Signup", "Purchase", "Outbound Link: Click", "File Download", "404", "Form: Submit", "Web Vitals", "deep-scroll"} {
		if !names[name] {
			t.Errorf("event %q missing", name)
		}
	}
	if len(profiles) == 0 {
		t.Error("no profiles")
	}
	if float64(returning)/float64(len(visits)) < 0.20 {
		t.Errorf("returning visits = %.1f%%, want at least 20%%", 100*float64(returning)/float64(len(visits)))
	}
	if replay == 0 || replay > 60 {
		t.Errorf("replay visits = %d, want 1..60", replay)
	}
	for _, host := range DemoHosts {
		if !hosts[host] {
			t.Errorf("host %q missing", host)
		}
	}
}

func TestDemoReturningVisitorsSpanLongIntervals(t *testing.T) {
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	visits := NewDemo(DemoConfig{Seed: 3, TZ: time.UTC, Visitors: 150, Anchor: anchor}).Visits(
		anchor.AddDate(0, 0, -DemoWindowDays), anchor.AddDate(0, 0, 1),
	)
	firstSeen := map[uint64]time.Time{}
	var longest time.Duration
	for _, visit := range visits {
		if first, ok := firstSeen[visit.VisitorKey]; ok {
			longest = max(longest, visit.Start.Sub(first))
		} else {
			firstSeen[visit.VisitorKey] = visit.Start
		}
	}
	if longest < 60*24*time.Hour {
		t.Fatalf("longest return interval = %s, want at least 60 days", longest)
	}
}

func TestDemoProfilesComeFromSignupEvents(t *testing.T) {
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	visits := NewDemo(DemoConfig{Seed: 4, TZ: time.UTC, Visitors: 150, Anchor: anchor}).Visits(
		anchor.AddDate(0, 0, -DemoWindowDays), anchor.AddDate(0, 0, 1),
	)
	signedUp, profiled := map[uint64]bool{}, map[uint64]bool{}
	for _, visit := range visits {
		if visit.Profile != nil {
			profiled[visit.VisitorKey] = true
		}
		for _, hit := range visit.Hits {
			if hit.Name == "Signup" {
				signedUp[visit.VisitorKey] = true
				if visit.Profile == nil {
					t.Fatalf("signup visitor %d has no profile", visit.VisitorKey)
				}
			}
		}
	}
	if !reflect.DeepEqual(signedUp, profiled) {
		t.Fatalf("signup visitors (%d) differ from profiled visitors (%d)", len(signedUp), len(profiled))
	}
}

func TestDemoSignupFunnelConversions(t *testing.T) {
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	visits := NewDemo(DemoConfig{Seed: 5, TZ: time.UTC, Visitors: 150, Anchor: anchor}).Visits(
		anchor.AddDate(0, 0, -DemoWindowDays), anchor.AddDate(0, 0, 1),
	)
	var home, pricing, signup, checkout, purchase int
	for _, visit := range visits {
		stage := 0
		for _, hit := range visit.Hits {
			switch {
			case stage == 0 && hit.Kind == 0 && hit.Path == "/":
				home++
				stage = 1
			case stage == 1 && hit.Kind == 0 && hit.Path == "/pricing":
				pricing++
				stage = 2
			case stage == 2 && hit.Kind == 1 && hit.Name == "Signup":
				signup++
				stage = 3
			case stage == 3 && hit.Kind == 0 && hit.Path == "/checkout":
				checkout++
				stage = 4
			case stage == 4 && hit.Kind == 1 && hit.Name == "Purchase":
				purchase++
				stage = 5
			}
		}
	}
	assertRatioBetween(t, "home to pricing", pricing, home, 0.35, 0.55)
	assertRatioBetween(t, "pricing to signup", signup, pricing, 0.18, 0.38)
	assertRatioBetween(t, "signup to checkout", checkout, signup, 0.50, 0.70)
	assertRatioBetween(t, "checkout to purchase", purchase, checkout, 0.70, 0.90)
}

func TestDemoReplayContinuesAfterAnchor(t *testing.T) {
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	visits := NewDemo(DemoConfig{Seed: 3, TZ: time.UTC, Visitors: 150, Anchor: anchor}).Visits(
		anchor.AddDate(0, 0, 1), anchor.AddDate(0, 0, 3),
	)
	replays := 0
	for _, visit := range visits {
		if visit.Replay {
			replays++
		}
	}
	if replays == 0 || replays > 8 {
		t.Fatalf("future replay visits = %d, want 1..8", replays)
	}
}

func TestDemoCampaignSpikeAndDip(t *testing.T) {
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	visits := NewDemo(DemoConfig{Seed: 9, TZ: time.UTC, Visitors: 150, Anchor: anchor}).Visits(
		anchor.AddDate(0, 0, -DemoWindowDays), anchor.AddDate(0, 0, 1),
	)
	counts := map[string]int{}
	for _, visit := range visits {
		counts[visit.Start.Format(time.DateOnly)]++
	}
	ordinary := make([]int, 0, len(counts)-3)
	for day, count := range counts {
		date, err := time.ParseInLocation(time.DateOnly, day, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		delta := int(date.Sub(anchor).Hours() / 24)
		if delta != -DemoCampaignDaysAgo && delta != -DemoCampaignDaysAgo+1 && delta != -DemoDipDaysAgo {
			ordinary = append(ordinary, count)
		}
	}
	slices.Sort(ordinary)
	median := ordinary[len(ordinary)/2]
	for _, ago := range []int{DemoCampaignDaysAgo, DemoCampaignDaysAgo - 1} {
		day := anchor.AddDate(0, 0, -ago).Format(time.DateOnly)
		if counts[day] <= 2*median {
			t.Errorf("campaign day %s = %d, want > 2x median %d", day, counts[day], median)
		}
	}
	dipDay := anchor.AddDate(0, 0, -DemoDipDaysAgo).Format(time.DateOnly)
	if float64(counts[dipDay]) >= 0.7*float64(median) {
		t.Errorf("dip day %s = %d, want < 0.7x median %d", dipDay, counts[dipDay], median)
	}
}

func TestBusyVisitShape(t *testing.T) {
	tz, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 11, 15, 4, 5, 0, tz)
	model := NewDemo(DemoConfig{Seed: 17, TZ: tz, Visitors: 150, Anchor: start})
	visit := model.BusyVisit(start, 77)
	if got := model.BusyVisit(start, 77); !reflect.DeepEqual(got, visit) {
		t.Fatal("BusyVisit is not deterministic for the same seed and key")
	}
	if !visit.Start.Equal(start) {
		t.Fatalf("start = %s, want %s", visit.Start, start)
	}
	if visit.VisitorKey&(uint64(1)<<62) == 0 {
		t.Fatalf("visitor key %d does not have bit 62 set", visit.VisitorKey)
	}
	if visit.Host == "" || visit.Country == "" || visit.Region == "" || visit.City == "" || visit.Language == "" ||
		visit.Browser == "" || visit.BrowserVer == "" || visit.OS == "" || visit.OSVer == "" || visit.ScreenW == 0 {
		t.Fatalf("busy visit fields are incomplete: %+v", visit)
	}
	if len(visit.Hits) == 0 {
		t.Fatal("busy visit has no hits")
	}
	duration := visit.Hits[len(visit.Hits)-1].Offset
	if duration < 8*time.Minute || duration > 25*time.Minute {
		t.Fatalf("duration = %s, want 8m..25m", duration)
	}
	var previous time.Duration
	pageviews, engagement := 0, 0
	for _, hit := range visit.Hits {
		if hit.Kind == 2 {
			engagement++
		}
		if hit.Kind != 0 {
			continue
		}
		if pageviews > 0 {
			gap := hit.Offset - previous
			if gap < 40*time.Second || gap > 150*time.Second {
				t.Fatalf("pageview gap = %s, want 40s..150s", gap)
			}
		}
		previous = hit.Offset
		pageviews++
	}
	if pageviews < 4 || engagement == 0 {
		t.Fatalf("pageviews=%d engagement=%d, want several pageviews and engagement", pageviews, engagement)
	}
	for key := uint64(0); key < 128; key++ {
		candidate := model.BusyVisit(start, key)
		candidateDuration := candidate.Hits[len(candidate.Hits)-1].Offset
		if candidateDuration < 8*time.Minute || candidateDuration > 25*time.Minute {
			t.Fatalf("key %d duration = %s, want 8m..25m", key, candidateDuration)
		}
	}
}

func TestBusyTargetRange(t *testing.T) {
	day := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	for hour := range 24 {
		got := BusyTarget(day.Add(time.Duration(hour) * time.Hour))
		if got < 10 || got > 20 {
			t.Errorf("BusyTarget at %02d:00 = %d, want 10..20", hour, got)
		}
	}
	if afternoon, night := BusyTarget(day.Add(15*time.Hour)), BusyTarget(day.Add(3*time.Hour)); afternoon <= night {
		t.Fatalf("15:00 target = %d, want above 03:00 target %d", afternoon, night)
	}
}

func TestVisitsUnchangedByBusy(t *testing.T) {
	anchor := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	visits := NewDemo(DemoConfig{Seed: 19, TZ: time.UTC, Visitors: 150, Anchor: anchor}).Visits(
		anchor.AddDate(0, 0, -7), anchor,
	)
	for _, visit := range visits {
		if visit.VisitorKey&(uint64(1)<<62) != 0 {
			t.Fatalf("Visits returned reserved busy visitor key %d", visit.VisitorKey)
		}
	}
}

func TestDemoVariety(t *testing.T) {
	anchor := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	visits := NewDemo(DemoConfig{Seed: 31, TZ: time.UTC, Visitors: 150, Anchor: anchor}).Visits(
		anchor.AddDate(0, 0, -7), anchor,
	)
	countries := map[string]bool{}
	cities := map[string]bool{}
	browsers := map[string]bool{}
	oses := map[string]bool{}
	devices := map[int16]bool{}
	languages := map[string]bool{}
	referrers := map[string]bool{}
	campaigns := map[string]bool{}
	paths := map[string]bool{}
	countryLanguage := map[string]string{}
	for _, visit := range visits {
		countries[visit.Country] = true
		cities[visit.Country+"/"+visit.City] = true
		browsers[visit.Browser] = true
		oses[visit.OS] = true
		devices[visit.Device] = true
		languages[visit.Language] = true
		if visit.Referrer != "" {
			host := strings.TrimPrefix(strings.TrimPrefix(visit.Referrer, "https://"), "http://")
			referrers[strings.TrimSuffix(host, "/")] = true
		}
		if visit.UTMCampaign != "" {
			campaigns[visit.UTMCampaign] = true
		}
		if previous, ok := countryLanguage[visit.Country]; ok && previous != visit.Language {
			t.Fatalf("country %s has inconsistent languages %s and %s", visit.Country, previous, visit.Language)
		}
		countryLanguage[visit.Country] = visit.Language
		if !browserMatchesPlatform(visit.Browser, visit.OS, visit.Device, visit.ScreenW) {
			t.Fatalf("inconsistent device: browser=%s os=%s device=%d screen=%d", visit.Browser, visit.OS, visit.Device, visit.ScreenW)
		}
		for _, hit := range visit.Hits {
			if hit.Path != "" {
				paths[hit.Path] = true
			}
		}
	}
	for name, got := range map[string]int{
		"countries": len(countries), "cities": len(cities), "browsers": len(browsers), "operating systems": len(oses),
		"device kinds": len(devices), "languages": len(languages), "referrer hosts": len(referrers),
		"UTM campaigns": len(campaigns), "paths": len(paths),
	} {
		minimum := map[string]int{"countries": 35, "cities": 100, "browsers": 8, "operating systems": 6, "device kinds": 3, "languages": 15, "referrer hosts": 25, "UTM campaigns": 6, "paths": 30}[name]
		if got < minimum {
			t.Errorf("distinct %s = %d, want at least %d", name, got, minimum)
		}
	}
}

func browserMatchesPlatform(browser, os string, device int16, screen int) bool {
	if device == 0 && screen < 1024 || device == 1 && screen >= 768 || device == 2 && (screen < 768 || screen >= 1400) {
		return false
	}
	switch browser {
	case "Safari":
		return os == "macOS" || os == "iOS" || os == "iPadOS"
	case "Edge":
		return os == "Windows" || os == "Android"
	case "Samsung Internet":
		return os == "Android"
	case "Chrome", "Firefox", "Opera", "Brave", "Vivaldi":
		return os == "Windows" || os == "macOS" || os == "Linux" || os == "Android" || os == "ChromeOS"
	default:
		return false
	}
}

func assertIntPropAtMost(t *testing.T, props map[string]string, name string, maxValue int) {
	t.Helper()
	value, err := strconv.Atoi(props[name])
	if err != nil {
		t.Fatalf("%s property %q is not an integer: %v", name, props[name], err)
	}
	if value < 0 || value > maxValue {
		t.Fatalf("%s = %d, want 0..%d", name, value, maxValue)
	}
}

func assertRatioBetween(t *testing.T, name string, numerator, denominator int, low, high float64) {
	t.Helper()
	if denominator == 0 {
		t.Fatalf("%s denominator is zero", name)
	}
	ratio := float64(numerator) / float64(denominator)
	if ratio < low || ratio > high {
		t.Fatalf("%s = %.3f (%d/%d), want %.2f..%.2f", name, ratio, numerator, denominator, low, high)
	}
}
