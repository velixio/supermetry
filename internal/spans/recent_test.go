package spans

import (
	"runtime"
	"testing"
	"time"
)

func recentKey(site int32, trace, span byte) SpanKey {
	k := SpanKey{Site: site}
	k.Trace[0], k.Span[0] = trace, span
	return k
}

func TestRecentClaimsOnce(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	r := NewRecent(100, 10*time.Minute)
	a := recentKey(1, 1, 1)
	if !r.Claim(a, now) {
		t.Fatal("first claim should win")
	}
	if r.Claim(a, now.Add(time.Second)) {
		t.Fatal("second claim should be a duplicate")
	}
	// Same span id in another trace, another span in the same trace, and another site are distinct.
	for _, k := range []SpanKey{recentKey(1, 2, 1), recentKey(1, 1, 2), recentKey(2, 1, 1)} {
		if !r.Claim(k, now) {
			t.Fatalf("%+v should be new", k)
		}
	}
	r.Release(a)
	if !r.Claim(a, now) {
		t.Fatal("a released key can be claimed again")
	}
}

func TestRecentExpires(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	r := NewRecent(100, 10*time.Minute)
	a := recentKey(1, 1, 1)
	r.Claim(a, now)
	if r.Claim(a, now.Add(9*time.Minute)) {
		t.Fatal("still inside the window")
	}
	if !r.Claim(a, now.Add(21*time.Minute)) {
		t.Fatal("past two windows the key is forgotten")
	}
}

func TestRecentCapEvictsOldest(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	r := NewRecent(10, time.Hour) // two generations of 5
	for i := range 10 {
		if !r.Claim(recentKey(1, byte(i), 1), now) {
			t.Fatalf("key %d should be new", i)
		}
	}
	// One more key rotates: keys 0..4 are evicted, 5..9 remain.
	if !r.Claim(recentKey(1, 10, 1), now) {
		t.Fatal("key 10 should be new")
	}
	if r.Len() > 10 {
		t.Fatalf("len %d exceeds the cap", r.Len())
	}
	if !r.Claim(recentKey(1, 0, 1), now) {
		t.Error("oldest key should have been evicted")
	}
	if r.Claim(recentKey(1, 9, 1), now) {
		t.Error("recent key should still be remembered")
	}
}

func TestRecentBytesPerKey(t *testing.T) {
	const n = 200_000
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	r := NewRecent(2*n, time.Hour)
	for i := range n {
		k := SpanKey{Site: 1}
		k.Trace[0], k.Trace[1], k.Trace[2] = byte(i), byte(i>>8), byte(i>>16)
		k.Span[0] = 1
		r.Claim(k, now)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	per := float64(after.HeapAlloc-before.HeapAlloc) / n
	t.Logf("recent set: %.1f bytes per key at %d keys", per, n)
	if per > 80 {
		t.Errorf("%.1f bytes per key, want under 80", per)
	}
	runtime.KeepAlive(r)
}

func TestDedupeBatch(t *testing.T) {
	a, b := mkSpan(1, testNow), mkSpan(2, testNow)
	in := []Span{a, b, a, b, a}
	out, dropped := DedupeBatch(in)
	if len(out) != 2 || dropped != 3 || out[0].SpanID != a.SpanID || out[1].SpanID != b.SpanID {
		t.Fatalf("out %d dropped %d", len(out), dropped)
	}
	// Same span id in another site is not a duplicate.
	c := a
	c.SiteID = 8
	if out, dropped := DedupeBatch([]Span{a, c}); len(out) != 2 || dropped != 0 {
		t.Fatalf("cross-site %d %d", len(out), dropped)
	}
}
