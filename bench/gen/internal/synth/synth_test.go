package synth

import (
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func newGen(t *testing.T, cfg Config) *Generator {
	t.Helper()
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestSites(t *testing.T) {
	g := newGen(t, Config{Tier: "S", Seed: 42})
	if len(g.sites) != 100 {
		t.Fatalf("%d sites", len(g.sites))
	}
	zones := map[string]bool{}
	var sum float64
	for _, s := range g.sites {
		zones[s.tz] = true
		sum += s.share
		if s.paths[homeIdx] != "/" || s.paths[pricingIdx] != "/pricing" {
			t.Fatalf("site %d: head paths %v", s.id, s.paths[:3])
		}
		seen := map[string]bool{}
		for _, p := range s.paths {
			if seen[p] || !strings.HasPrefix(p, "/") || !jsonSafe(p) {
				t.Fatalf("site %d: bad or duplicate path %q", s.id, p)
			}
			seen[p] = true
		}
	}
	for _, z := range []string{"UTC", "Europe/Berlin", "America/New_York", "Asia/Kolkata", "Asia/Kathmandu"} {
		if !zones[z] {
			t.Errorf("zone %s missing", z)
		}
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("shares sum to %f", sum)
	}
	if sh := g.sites[0].share; sh < 0.28 || sh > 0.32 {
		t.Errorf("biggest share %.3f, want about 0.30", sh)
	}
	if n := len(g.sites[0].paths); n != 20_000 {
		t.Errorf("biggest site has %d paths, want 20000", n)
	}
	if n := len(g.sites[99].paths); n > 1000 {
		t.Errorf("smallest site has %d paths, want far fewer", n)
	}
	t.Logf("shares: %.4f %.4f ... %.5f; paths: %d %d ... %d", g.sites[0].share, g.sites[1].share, g.sites[99].share,
		len(g.sites[0].paths), len(g.sites[1].paths), len(g.sites[99].paths))
}

// Everyone who can appear on a site on day d or d+1 originates on d-1..d+1;
// their IPs must be pairwise distinct.
func TestIPsDistinctAcrossAdjacentDays(t *testing.T) {
	g := newGen(t, Config{Tier: "M", Seed: 42})
	s := g.sites[0]
	seen := make(map[uint32]struct{}, 3*300_000)
	for day := 9700; day < 9703; day++ {
		for i := int64(0); i < 300_000; i++ {
			ip := s.ip(day, i)
			if _, dup := seen[ip]; dup {
				t.Fatalf("duplicate ip for day %d index %d", day, i)
			}
			seen[ip] = struct{}{}
			if o := byte(ip >> 24); o < 2 || o == 10 || o > 66 {
				t.Fatalf("ip first octet %d outside the public range used", o)
			}
		}
	}
}

func TestKeysDistinct(t *testing.T) {
	g := newGen(t, Config{Tier: "S", Seed: 42})
	seen := map[uint64]bool{}
	for _, s := range g.sites[:3] {
		for day := 9700; day < 9702; day++ {
			for i := uint32(0); i < 20_000; i++ {
				for _, k := range []uint64{g.vk(s, day, int64(i)), g.sk(s, day, i, 0), g.sk(s, day, i, 1)} {
					if seen[k] {
						t.Fatalf("duplicate key %x", k)
					}
					seen[k] = true
				}
			}
		}
	}
}

func TestReturnerRangesFit(t *testing.T) {
	g := newGen(t, Config{Tier: "tiny", Seed: 7})
	for _, s := range g.sites {
		for day := 9700; day < 9800; day++ {
			n, rt, rn := g.visitorsOn(s, day), g.returners(s, day), g.returners(s, day+1)
			if rt+rn > n {
				t.Fatalf("site %d day %d: %d returners + %d leavers > %d visitors", s.id, day, rt, rn, n)
			}
		}
	}
}

// TestEventsPerVisitorCalibration keeps the eventsPerVisitor constant honest:
// over a full week the event volume must hit the tier's budget.
func TestEventsPerVisitorCalibration(t *testing.T) {
	if testing.Short() {
		t.Skip("calibration in -short mode")
	}
	g := newGen(t, Config{Tier: "S", Days: 7, Seed: 42, EventsPerDay: 300_000})
	st, err := g.Run(io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	epv := float64(st.Events) / float64(st.Visitors)
	t.Logf("events/visitor %.4f (constant %.4f), events/day %.0f", epv, eventsPerVisitor, float64(st.Events)/7)
	if math.Abs(epv/eventsPerVisitor-1) > 0.015 {
		t.Errorf("events per visitor is %.4f, constant says %.4f", epv, eventsPerVisitor)
	}
}

func TestSortEventsStable(t *testing.T) {
	r := rng{1}
	ev := make([]eventRec, 50_000)
	for i := range ev {
		ev[i] = eventRec{off: uint32(r.intn(90_000_000)), sess: uint32(i)}
		if i%7 == 0 && i > 0 {
			ev[i].off = ev[i-1].off // ties
		}
	}
	sortEvents(ev)
	for i := 1; i < len(ev); i++ {
		if ev[i].off < ev[i-1].off || ev[i].off == ev[i-1].off && ev[i].sess < ev[i-1].sess {
			t.Fatalf("not stably sorted at %d", i)
		}
	}
}

func TestAppendTS(t *testing.T) {
	f := formatter{}
	r := rng{3}
	for i := 0; i < 10_000; i++ {
		ms := int64(1_700_000_000_000) + r.intn(400*86_400_000)
		got := string(f.appendTS(nil, ms))
		want := time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
		if got != want {
			t.Fatalf("appendTS(%d) = %s, want %s", ms, got, want)
		}
	}
}

func TestLocalDayBounds(t *testing.T) {
	g := newGen(t, Config{Tier: "S", Seed: 42, Start: time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), Days: 1})
	// 2026-03-08 is the US spring-forward day: 23 hours in New York.
	u := g.newUnit(g.sites[0], 0, 0)
	if g.sites[0].tz != "America/New_York" || u.end-u.start != 23*hour {
		t.Fatalf("%s day length %d h", g.sites[0].tz, (u.end-u.start)/hour)
	}
	k := g.newUnit(g.sites[4], 4, 0) // Asia/Kathmandu, +05:45
	if want := time.Date(2026, 3, 7, 18, 15, 0, 0, time.UTC).UnixMilli(); k.start != want {
		t.Fatalf("Kathmandu midnight at %s", time.UnixMilli(k.start).UTC())
	}
}
