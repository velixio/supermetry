package ingest

import (
	"strings"
	"sync"
	"time"
)

const (
	maxTokenBuckets   = 10_000
	overflowBucketKey = "\x00overflow"
)

// RateLimiter decides whether one request for a key may proceed.
type RateLimiter interface {
	Allow(key string) bool
}

// RateLimitReason identifies the ceiling that rejected an ingest request.
type RateLimitReason uint8

const (
	RateLimitNone RateLimitReason = iota
	RateLimitIP
	RateLimitSite
	RateLimitGlobal
)

type ingestRequestLimiter interface {
	AllowIngest(ipKey, siteKey string) RateLimitReason
}

type refundableRateLimiter interface {
	Refund(key string)
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

type tokenBuckets struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]tokenBucket
	calls   uint64
	now     func() time.Time
}

// NewTokenBuckets returns a concurrency-safe, process-local token-bucket
// limiter. Rate is tokens per second; a zero rate permits only the initial
// burst and is useful for deterministic tests.
func NewTokenBuckets(rate float64, burst int) RateLimiter {
	return newTokenBucketsWithClock(rate, burst, time.Now)
}

func newTokenBucketsWithClock(rate float64, burst int, now func() time.Time) *tokenBuckets {
	if rate < 0 {
		rate = 0
	}
	if burst < 0 {
		burst = 0
	}
	return &tokenBuckets{
		rate:    rate,
		burst:   float64(burst),
		buckets: make(map[string]tokenBucket),
		now:     now,
	}
}

func (l *tokenBuckets) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	key = l.boundedKey(key)
	bucket, ok := l.buckets[key]
	if !ok {
		bucket = tokenBucket{tokens: l.burst, last: now}
	} else if l.rate > 0 {
		bucket.tokens = min(l.burst, bucket.tokens+now.Sub(bucket.last).Seconds()*l.rate)
	}
	bucket.last = now
	allowed := bucket.tokens >= 1
	if allowed {
		bucket.tokens--
	}
	l.buckets[key] = bucket

	l.calls++
	if l.calls%1024 == 0 {
		l.removeIdle(now)
	}
	return allowed
}

// Refund restores one previously spent token without exceeding the burst.
func (l *tokenBuckets) Refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key = l.boundedKey(key)
	if bucket, ok := l.buckets[key]; ok {
		bucket.tokens = min(l.burst, bucket.tokens+1)
		l.buckets[key] = bucket
	}
}

func (l *tokenBuckets) boundedKey(key string) string {
	if _, ok := l.buckets[key]; ok || len(l.buckets) < maxTokenBuckets-1 {
		return key
	}
	return overflowBucketKey
}

func (l *tokenBuckets) removeIdle(now time.Time) {
	if l.rate <= 0 {
		return
	}
	idleFor := max(time.Minute, time.Duration(2*l.burst/l.rate*float64(time.Second)))
	for key, bucket := range l.buckets {
		if now.Sub(bucket.last) >= idleFor {
			delete(l.buckets, key)
		}
	}
}

type ingestRateLimiter struct {
	mu     sync.Mutex
	ip     *tokenBuckets
	site   *tokenBuckets
	global *tokenBuckets
}

// NewIngestRateLimiter creates independent IP, site and process ceilings.
// Churned IPs share a bounded overflow bucket instead of evicting established
// clients and resetting their allowance.
func NewIngestRateLimiter(globalRate float64, globalBurst int) RateLimiter {
	// One address may use at most a tenth of the process ceiling: enough for a
	// reverse proxy that is not in the trusted-proxy list (every visitor then
	// shares one address), far below what a single flooder needs to starve a site.
	ipRate := min(globalRate, 1_000)
	ipBurst := min(globalBurst, 2_000)
	siteRate := min(globalRate, 5_000)
	siteBurst := min(globalBurst, 10_000)
	return newIngestRateLimiter(time.Now, ipRate, ipBurst, siteRate, siteBurst, globalRate, globalBurst)
}

func newIngestRateLimiter(now func() time.Time, ipRate float64, ipBurst int, siteRate float64, siteBurst int, globalRate float64, globalBurst int) *ingestRateLimiter {
	return &ingestRateLimiter{
		ip:     newTokenBucketsWithClock(ipRate, ipBurst, now),
		site:   newTokenBucketsWithClock(siteRate, siteBurst, now),
		global: newTokenBucketsWithClock(globalRate, globalBurst, now),
	}
}

func (l *ingestRateLimiter) AllowIngest(ipKey, siteKey string) RateLimitReason {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.ip.Allow(ipKey) {
		return RateLimitIP
	}
	if !l.site.Allow(siteKey) {
		l.ip.Refund(ipKey)
		return RateLimitSite
	}
	if !l.global.Allow("global") {
		l.ip.Refund(ipKey)
		l.site.Refund(siteKey)
		return RateLimitGlobal
	}
	return RateLimitNone
}

func (l *ingestRateLimiter) Allow(key string) bool {
	switch {
	case strings.HasPrefix(key, "ip:"):
		return l.ip.Allow(key)
	case strings.HasPrefix(key, "site:"):
		return l.site.Allow(key)
	default:
		return l.global.Allow(key)
	}
}

func (l *ingestRateLimiter) Refund(key string) {
	switch {
	case strings.HasPrefix(key, "ip:"):
		l.ip.Refund(key)
	case strings.HasPrefix(key, "site:"):
		l.site.Refund(key)
	default:
		l.global.Refund(key)
	}
}
