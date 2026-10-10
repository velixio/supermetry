package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestLoginLimiterRejectedClientDoesNotConsumeAccountBudget(t *testing.T) {
	tests := []struct {
		name  string
		allow func(*loginLimiter, string, string, time.Time) bool
	}{
		{name: "login", allow: (*loginLimiter).allow},
		{name: "password reset", allow: (*loginLimiter).allowReset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := newLoginLimiter()
			now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 20; i++ {
				if !tt.allow(limiter, fmt.Sprintf("other-%d", i), "flooder", now) {
					t.Fatalf("flooder warm-up attempt %d rejected", i+1)
				}
			}
			if tt.allow(limiter, "victim", "flooder", now) {
				t.Fatal("client over its own limit was allowed")
			}
			for i := 0; i < 5; i++ {
				if !tt.allow(limiter, "victim", fmt.Sprintf("well-behaved-%d", i), now) {
					t.Fatalf("well-behaved client %d lost shared target budget", i+1)
				}
			}
		})
	}
}

func TestLoginLimiterPairCannotExhaustSharedAccountBudget(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for i := 0; i < accountClientCapacity; i++ {
		if !limiter.allow("victim", "attacker", now) {
			t.Fatalf("paired attempt %d rejected", i+1)
		}
	}
	if limiter.allow("victim", "attacker", now) {
		t.Fatal("attacker exceeded its account-client budget")
	}
	for _, client := range []string{"real-device", "another-device", "new-device"} {
		if !limiter.allow("victim", client, now) {
			t.Fatalf("%s was locked out by a different client", client)
		}
	}
}

func TestPruneBucketsEnforcesHardCap(t *testing.T) {
	const hardCap = 4096
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	buckets := make(map[string]bucket, hardCap+17)
	for i := 0; i < hardCap+17; i++ {
		buckets[fmt.Sprintf("key-%05d", i)] = bucket{tokens: 1, last: now.Add(time.Duration(i) * time.Nanosecond)}
	}

	pruneBuckets(buckets, now.Add(30*time.Second))

	if got := len(buckets); got != hardCap {
		t.Fatalf("bucket count = %d, want %d", got, hardCap)
	}
	if _, ok := buckets["key-00000"]; ok {
		t.Fatal("oldest bucket was not evicted")
	}
	if _, ok := buckets[fmt.Sprintf("key-%05d", hardCap+16)]; !ok {
		t.Fatal("newest bucket was evicted")
	}
}

func TestLoginLimiterAccountCeilingSlowsDistributedGuessing(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for i := 0; i < accountCeilingPerMinute; i++ {
		if !limiter.allow("victim", fmt.Sprintf("client-%d", i), now) {
			t.Fatalf("attempt %d from a distinct client rejected before the ceiling", i+1)
		}
	}
	if limiter.allow("victim", "client-extra", now) {
		t.Fatal("account ceiling did not slow a distributed attack")
	}
	if !limiter.allow("victim", "client-extra", now.Add(2*time.Minute)) {
		t.Fatal("ceiling did not recover after the window")
	}
}

func TestLoginLimiterSingleClientCannotReachAccountCeiling(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 100; i++ {
		limiter.allow("victim", "attacker", now)
	}
	if !limiter.allow("victim", "real-user", now) {
		t.Fatal("one client locked the real user out of the account")
	}
}
