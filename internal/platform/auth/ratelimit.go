package auth

import (
	"context"
	"strings"
	"sync"
	"time"
)

type clientKeyContextKey struct{}

// WithClientKey attaches an ephemeral, caller-derived client key to a login
// attempt. The key is retained only in the in-memory limiter.
func WithClientKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, clientKeyContextKey{}, key)
}

func clientKey(ctx context.Context) string {
	key, _ := ctx.Value(clientKeyContextKey{}).(string)
	return strings.TrimSpace(key)
}

type bucket struct {
	tokens float64
	last   time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	account map[string]bucket
	client  map[string]bucket
	attempt uint64
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		account: make(map[string]bucket),
		client:  make(map[string]bucket),
	}
}

func (l *loginLimiter) allow(account, client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempt++
	if l.attempt%256 == 0 {
		pruneBuckets(l.account, now)
		pruneBuckets(l.client, now)
	}

	if !take(l.account, account, 5, now) {
		return false
	}
	if client != "" && !take(l.client, client, 20, now) {
		return false
	}
	return true
}

func pruneBuckets(buckets map[string]bucket, now time.Time) {
	for key, b := range buckets {
		if now.Sub(b.last) >= time.Minute {
			delete(buckets, key)
		}
	}
}

func take(buckets map[string]bucket, key string, capacity float64, now time.Time) bool {
	b := buckets[key]
	if b.last.IsZero() {
		b.tokens = capacity
		b.last = now
	} else if now.After(b.last) {
		b.tokens += now.Sub(b.last).Minutes() * capacity
		if b.tokens > capacity {
			b.tokens = capacity
		}
		b.last = now
	}
	if b.tokens < 1 {
		buckets[key] = b
		return false
	}
	b.tokens--
	buckets[key] = b
	return true
}
