package ingest

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"supermetry/internal/health"
)

func TestIPv6RateLimitKeyUsesNetworkPrefix(t *testing.T) {
	first := netip.MustParseAddr("2001:db8:abcd:12::1")
	rotated := netip.MustParseAddr("2001:db8:abcd:12:ffff::99")
	other := netip.MustParseAddr("2001:db8:abcd:13::1")
	if ipRateKey(first) != ipRateKey(rotated) {
		t.Fatal("addresses in one IPv6 /64 received different rate-limit keys")
	}
	if ipRateKey(first) == ipRateKey(other) {
		t.Fatal("addresses in different IPv6 /64s received the same rate-limit key")
	}
}

func TestTokenBucketsKeepEstablishedKeysAtCapacity(t *testing.T) {
	limiter := NewTokenBuckets(0, 1).(*tokenBuckets)
	for i := 0; i <= maxTokenBuckets; i++ {
		limiter.Allow(fmt.Sprintf("key-%05d", i))
	}

	if got := len(limiter.buckets); got > maxTokenBuckets {
		t.Fatalf("bucket count=%d, want at most %d", got, maxTokenBuckets)
	}
	if _, ok := limiter.buckets["key-00000"]; !ok {
		t.Fatal("established bucket was evicted by key churn")
	}
	if _, ok := limiter.buckets[fmt.Sprintf("key-%05d", maxTokenBuckets)]; ok {
		t.Fatal("overflow key bypassed the hard bucket cap")
	}
}

func TestTokenBucketsRefundRestoresToken(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	limiter := newTokenBucketsWithClock(0, 1, func() time.Time { return now })
	if !limiter.Allow("site:1") || limiter.Allow("site:1") {
		t.Fatal("test setup did not exhaust the bucket")
	}
	limiter.Refund("site:1")
	if !limiter.Allow("site:1") {
		t.Fatal("refunded token was not restored")
	}
}

func TestIngestLimiterFlooderDoesNotSpendAnotherIPsAllowance(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	limiter := newIngestRateLimiter(func() time.Time { return now }, 0, 2, 0, 4, 0, 4)
	for i := 0; i < 2; i++ {
		if reason := limiter.AllowIngest("ip:flooder", "site:1"); reason != RateLimitNone {
			t.Fatalf("flooder request %d rejected for %v", i+1, reason)
		}
	}
	if reason := limiter.AllowIngest("ip:flooder", "site:1"); reason != RateLimitIP {
		t.Fatalf("flooder limit reason = %v, want IP", reason)
	}
	for i := 0; i < 2; i++ {
		if reason := limiter.AllowIngest("ip:visitor", "site:1"); reason != RateLimitNone {
			t.Fatalf("visitor request %d rejected for %v", i+1, reason)
		}
	}
}

func TestIngestLimiterRefundsEarlierCeilings(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	limiter := newIngestRateLimiter(func() time.Time { return now }, 0, 1, 0, 1, 0, 0)
	if reason := limiter.AllowIngest("ip:one", "site:1"); reason != RateLimitGlobal {
		t.Fatalf("limit reason = %v, want global", reason)
	}
	if !limiter.ip.Allow("ip:one") {
		t.Fatal("global rejection did not refund the IP token")
	}
	if !limiter.site.Allow("site:1") {
		t.Fatal("global rejection did not refund the site token")
	}
}

func TestNewIngestRateLimiterUsesConfiguredRatesAndDoubleBursts(t *testing.T) {
	tests := []struct {
		name               string
		ipRate, siteRate   int
		globalRate         int
		requests           [][2]string
		wantFinalRejection RateLimitReason
	}{
		{name: "IP", ipRate: 1, siteRate: 10, globalRate: 100, requests: [][2]string{{"ip:one", "site:one"}, {"ip:one", "site:one"}, {"ip:one", "site:one"}}, wantFinalRejection: RateLimitIP},
		{name: "site", ipRate: 10, siteRate: 1, globalRate: 100, requests: [][2]string{{"ip:one", "site:one"}, {"ip:two", "site:one"}, {"ip:three", "site:one"}}, wantFinalRejection: RateLimitSite},
		{name: "global", ipRate: 10, siteRate: 10, globalRate: 1, requests: [][2]string{{"ip:one", "site:one"}, {"ip:two", "site:two"}, {"ip:three", "site:three"}}, wantFinalRejection: RateLimitGlobal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewIngestRateLimiter(tt.ipRate, tt.siteRate, tt.globalRate).(*ingestRateLimiter)
			for i, keys := range tt.requests {
				reason := limiter.AllowIngest(keys[0], keys[1])
				if i < len(tt.requests)-1 && reason != RateLimitNone {
					t.Fatalf("request %d rejected with %v before double burst exhausted", i+1, reason)
				}
				if i == len(tt.requests)-1 && reason != tt.wantFinalRejection {
					t.Fatalf("final request reason = %v, want %v", reason, tt.wantFinalRejection)
				}
			}
		})
	}
}

func TestRecordIngestRateLimitCountsEachReason(t *testing.T) {
	tests := []struct {
		reason  RateLimitReason
		counter *health.Counter
	}{
		{reason: RateLimitIP, counter: &health.IngestRateLimitedIP},
		{reason: RateLimitSite, counter: &health.IngestRateLimitedSite},
		{reason: RateLimitGlobal, counter: &health.IngestRateLimitedGlobal},
	}
	for _, test := range tests {
		before := test.counter.Load()
		recordIngestRateLimit(test.reason)
		if got := test.counter.Load() - before; got != 1 {
			t.Fatalf("reason %v counter delta = %d, want 1", test.reason, got)
		}
	}
}
