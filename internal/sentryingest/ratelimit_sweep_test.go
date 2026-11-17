package sentryingest

import (
	"strconv"
	"testing"
	"time"
)

func fillLimiter(l *ipLimiter, now time.Time) {
	for i := 0; i < maxTrackedIPs; i++ {
		l.buckets["k"+strconv.Itoa(i)] = &ipBucket{tokens: ipBurst, last: now}
	}
}

// A full table of active addresses must not be rescanned for every new address.
func TestIPLimiterSweepsAtMostOncePerInterval(t *testing.T) {
	l := newIPLimiter(600)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	fillLimiter(l, now)
	for i := 0; i < 1000; i++ {
		if !l.allow("new"+strconv.Itoa(i), now) {
			t.Fatal("a new address over the tracking cap must fail open")
		}
	}
	if l.sweeps != 1 {
		t.Fatalf("sweeps = %d, want 1 for 1000 new addresses within one interval", l.sweeps)
	}
	l.allow("later", now.Add(ipSweepEvery))
	if l.sweeps != 2 {
		t.Fatalf("sweeps = %d after the interval, want 2", l.sweeps)
	}
}

// A bucket idle long enough to refill completely is indistinguishable from a
// new one, so the sweep may drop it and track the new address instead.
func TestIPLimiterEvictsFullyRefilledBuckets(t *testing.T) {
	l := newIPLimiter(600) // 10/s, burst 120: full again after 12 s
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	fillLimiter(l, now)
	later := now.Add(13 * time.Second)
	l.allow("fresh", later)
	if _, ok := l.buckets["fresh"]; !ok {
		t.Fatal("new address not tracked after idle buckets were evictable")
	}
	if len(l.buckets) != 1 {
		t.Fatalf("tracked = %d, want only the new address", len(l.buckets))
	}
}

// Eviction must not reset an address that is still being limited.
func TestIPLimiterKeepsPartiallyRefilledBuckets(t *testing.T) {
	l := newIPLimiter(600)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for i := 0; i < ipBurst; i++ {
		l.allow("busy", now)
	}
	if l.allow("busy", now) {
		t.Fatal("burst exhausted but request allowed")
	}
	fillLimiter(l, now)
	l.buckets["busy"] = &ipBucket{tokens: 0, last: now}
	l.allow("other", now.Add(2*time.Second))
	if b := l.buckets["busy"]; b == nil {
		t.Fatal("a still-limited address was evicted")
	}
}
