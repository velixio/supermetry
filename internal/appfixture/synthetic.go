package appfixture

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"time"

	"supermetry/internal/appproduct"
)

// SyntheticBase is the first UTC day of SyntheticProduct datasets.
var SyntheticBase = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// syntheticIdleGap is the product session idle timeout the oracle assumes
// (30 minutes). SyntheticProduct keeps the hits of one session within a few
// minutes and separate sessions of one install at least two hours apart, so
// the datasets never sit near the boundary.
const syntheticIdleGap = 30 * time.Minute

// SyntheticProduct builds a deterministic product dataset for site 7: the given
// number of installs over days UTC days from SyntheticBase. Each install joins
// on a random day, is active on a random share of the following days, and an
// active day holds one or two sessions of 1 to 4 hits: screens "home",
// "paywall" and "settings" and the event "purchase", in random order and
// sometimes repeated, so ordered funnels face skips, repeats and wrong order.
//
// Every session starts at minute 2 to 20 of an hour and lasts under five
// minutes, so it never straddles a half-hour boundary and therefore never a
// local midnight of a zone with a whole or half hour UTC offset (Asia/Kolkata
// included). Hits are sorted by time, then install.
func SyntheticProduct(seed int64, installs, days int) []appproduct.Hit {
	rng := rand.New(rand.NewPCG(uint64(seed), 0x5eed))
	var hits []appproduct.Hit
	for i := 0; i < installs; i++ {
		id := fmt.Sprintf("syn-install-%04d", i)
		platform, country := "ios", "DE"
		if i%2 == 1 {
			platform, country = "android", "US"
		}
		join := rng.IntN(max(days/2, 1))
		activity := 0.15 + rng.Float64()*0.7
		for d := join; d < days; d++ {
			if rng.Float64() > activity {
				continue
			}
			sessions := 1
			if rng.IntN(4) == 0 {
				sessions = 2
			}
			firstHour := rng.IntN(11)
			for s := 0; s < sessions; s++ {
				hour := firstHour + s*(2+rng.IntN(8))
				start := SyntheticBase.AddDate(0, 0, d).Add(time.Duration(hour)*time.Hour + time.Duration(2+rng.IntN(19))*time.Minute)
				steps := 1 + rng.IntN(4)
				for k := 0; k < steps; k++ {
					h := appproduct.Hit{
						SiteID: 7, TS: start.Add(time.Duration(k) * time.Minute), InstallID: id,
						Release: "1.0.0", Platform: platform, OSName: "OS", Country: country,
					}
					if k == 0 && rng.IntN(5) != 0 {
						h.Screen = "home"
					} else {
						switch rng.IntN(5) {
						case 0, 1:
							h.Screen = "paywall"
						case 2:
							h.Screen = "settings"
						case 3:
							h.Screen = "home"
						default:
							h.Name = "purchase"
						}
					}
					hits = append(hits, h)
				}
			}
		}
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if !hits[a].TS.Equal(hits[b].TS) {
			return hits[a].TS.Before(hits[b].TS)
		}
		return hits[a].InstallID < hits[b].InstallID
	})
	return hits
}

// ExpectedCohort is one retention cohort: the installs whose first active
// local day in the range is Start, and how many of them were active on
// Start+k days (k is the index of Retained).
type ExpectedCohort struct {
	Start    time.Time
	Size     int64
	Retained []int64
}

// Expected holds the reference answers for one dataset in one time zone.
// DAU, WAU and MAU have one entry per local day from From to To inclusive;
// WAU and MAU count the distinct installs active in the 7 and 30 local days
// ending that day. FunnelInstalls and FunnelSessions answer the funnel
// screen "home" > screen "paywall" > event "purchase": a session completes a
// step when the step occurs after the previous one inside that session, and an
// install completes it when any of its sessions does.
type Expected struct {
	From, To       time.Time
	DAU, WAU, MAU  []int64
	Distinct       int64
	Stickiness     float64
	FunnelInstalls []int64
	FunnelSessions []int64
	Cohorts        []ExpectedCohort
}

// maxRetentionOffsets is the retention API's horizon: cohorts report at most
// this many day offsets (0..9).
const maxRetentionOffsets = 10

// ExpectedProduct computes the reference numbers with maps and sorting only:
// no SQL, no query or rollup code. Local days follow loc. hits must be sorted
// by time, as SyntheticProduct returns them.
func ExpectedProduct(hits []appproduct.Hit, loc *time.Location) Expected {
	var exp Expected
	if len(hits) == 0 {
		return exp
	}
	dayOf := func(t time.Time) time.Time {
		y, m, d := t.In(loc).Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	byInstall := map[string][]appproduct.Hit{}
	exp.From, exp.To = dayOf(hits[0].TS), dayOf(hits[0].TS)
	for _, h := range hits {
		byInstall[h.InstallID] = append(byInstall[h.InstallID], h)
		d := dayOf(h.TS)
		if d.Before(exp.From) {
			exp.From = d
		}
		if d.After(exp.To) {
			exp.To = d
		}
	}
	index := func(d time.Time) int { return int(d.Sub(exp.From) / (24 * time.Hour)) }
	n := index(exp.To) + 1
	active := map[string]map[int]bool{} // install -> active day indexes
	for id, hs := range byInstall {
		active[id] = map[int]bool{}
		for _, h := range hs {
			active[id][index(dayOf(h.TS))] = true
		}
	}
	exp.DAU, exp.WAU, exp.MAU = make([]int64, n), make([]int64, n), make([]int64, n)
	var dauSum int64
	for _, days := range active {
		for d := 0; d < n; d++ {
			if days[d] {
				exp.DAU[d]++
			}
			for back := 0; back < 30; back++ {
				if days[d-back] {
					exp.MAU[d]++
					break
				}
			}
			for back := 0; back < 7; back++ {
				if days[d-back] {
					exp.WAU[d]++
					break
				}
			}
		}
	}
	for _, v := range exp.DAU {
		dauSum += v
	}
	exp.Distinct = int64(len(active))
	exp.Stickiness = float64(dauSum) / float64(n) / float64(exp.Distinct)

	// Funnel: a session ends at an idle gap or a local day change.
	exp.FunnelInstalls, exp.FunnelSessions = make([]int64, 3), make([]int64, 3)
	for _, hs := range byInstall {
		reached := 0
		var session []appproduct.Hit
		flush := func() {
			depth := funnelDepth(session)
			for step := 0; step < depth; step++ {
				exp.FunnelSessions[step]++
			}
			reached = max(reached, depth)
			session = session[:0]
		}
		for i, h := range hs {
			if i > 0 && (h.TS.Sub(hs[i-1].TS) > syntheticIdleGap || !dayOf(h.TS).Equal(dayOf(hs[i-1].TS))) {
				flush()
			}
			session = append(session, h)
		}
		flush()
		for step := 0; step < reached; step++ {
			exp.FunnelInstalls[step]++
		}
	}

	// Day retention: cohort = first active local day, retained[k] = active k days later.
	first := map[string]int{}
	widest := 0
	for id, days := range active {
		f := n
		for d := range days {
			f = min(f, d)
		}
		first[id] = f
		for d := range days {
			widest = max(widest, d-f)
		}
	}
	width := min(widest, maxRetentionOffsets-1) + 1
	cohorts := map[int]*ExpectedCohort{}
	for id, f := range first {
		c := cohorts[f]
		if c == nil {
			c = &ExpectedCohort{Start: exp.From.AddDate(0, 0, f), Retained: make([]int64, width)}
			cohorts[f] = c
		}
		c.Size++
		for d := range active[id] {
			if k := d - f; k < width {
				c.Retained[k]++
			}
		}
	}
	for f := 0; f < n; f++ {
		if c := cohorts[f]; c != nil {
			exp.Cohorts = append(exp.Cohorts, *c)
		}
	}
	return exp
}

// funnelDepth is how many steps of home > paywall > purchase one session
// completes, each step after the previous one.
func funnelDepth(session []appproduct.Hit) int {
	depth := 0
	for _, h := range session {
		switch {
		case depth == 0 && h.Screen == "home":
			depth = 1
		case depth == 1 && h.Screen == "paywall":
			depth = 2
		case depth == 2 && h.Name == "purchase":
			depth = 3
		}
	}
	return depth
}
