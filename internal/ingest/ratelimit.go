package ingest

import (
	"sync"
	"time"
)

// RateLimiter decides whether one request for a key may proceed.
type RateLimiter interface {
	Allow(key string) bool
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
}

// NewTokenBuckets returns a concurrency-safe, process-local token-bucket
// limiter. Rate is tokens per second; a zero rate permits only the initial
// burst and is useful for deterministic tests.
func NewTokenBuckets(rate float64, burst int) RateLimiter {
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
	}
}

func (l *tokenBuckets) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok {
		bucket = tokenBucket{tokens: l.burst, last: now}
	} else if l.rate > 0 {
		bucket.tokens = min(l.burst, bucket.tokens+now.Sub(bucket.last).Seconds()*l.rate)
		bucket.last = now
	}
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
