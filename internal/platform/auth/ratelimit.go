package auth

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

const limiterBucketCap = 4096

const accountClientCapacity = 3

// accountCeilingPerMinute bounds login attempts for one account across all clients.
const accountCeilingPerMinute = 30

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
	mu                 sync.Mutex
	account            map[string]bucket
	client             map[string]bucket
	accountClient      map[accountClientKey]bucket
	resetAccount       map[string]bucket
	resetClient        map[string]bucket
	resetAccountClient map[accountClientKey]bucket
	attempt            uint64
}

type accountClientKey struct {
	account string
	client  string
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		account:            make(map[string]bucket),
		client:             make(map[string]bucket),
		accountClient:      make(map[accountClientKey]bucket),
		resetAccount:       make(map[string]bucket),
		resetClient:        make(map[string]bucket),
		resetAccountClient: make(map[accountClientKey]bucket),
	}
}

// allowReset enforces per-target and per-client limits. Buckets are in memory,
// so limits are per process; multi-instance deployments need an upstream or
// shared limiter for a global ceiling.
func (l *loginLimiter) allowReset(account, client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempt++
	if l.attempt%256 == 0 {
		pruneBuckets(l.resetAccount, now)
		pruneBuckets(l.resetClient, now)
		pruneAccountClientBuckets(l.resetAccountClient, now)
	}
	if client != "" && !take(l.resetClient, client, 20, now) {
		return false
	}
	if client != "" && !takeAccountClient(l.resetAccountClient, accountClientKey{account: account, client: client}, accountClientCapacity, now) {
		return false
	}
	return take(l.resetAccount, account, 5, now)
}

func (l *loginLimiter) allow(account, client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempt++
	if l.attempt%256 == 0 {
		pruneBuckets(l.account, now)
		pruneBuckets(l.client, now)
		pruneAccountClientBuckets(l.accountClient, now)
	}

	if client != "" && !take(l.client, client, 20, now) {
		return false
	}
	if client != "" && !takeAccountClient(l.accountClient, accountClientKey{account: account, client: client}, accountClientCapacity, now) {
		return false
	}
	// One client can spend only accountClientCapacity per minute, so a single
	// attacker cannot drain the shared account bucket and lock the user out.
	// The account ceiling stays as a backstop against distributed guessing; it
	// is high enough that it takes many distinct clients to reach it. Callers
	// without a derived client key keep the strict cap.
	if client == "" {
		return take(l.account, account, 5, now)
	}
	return take(l.account, account, accountCeilingPerMinute, now)
}

func pruneAccountClientBuckets(buckets map[accountClientKey]bucket, now time.Time) {
	for key, b := range buckets {
		if now.Sub(b.last) >= time.Minute {
			delete(buckets, key)
		}
	}
	if len(buckets) <= limiterBucketCap {
		return
	}
	type entry struct {
		key  accountClientKey
		last time.Time
	}
	entries := make([]entry, 0, len(buckets))
	for key, b := range buckets {
		entries = append(entries, entry{key: key, last: b.last})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].last.Equal(entries[j].last) {
			if entries[i].key.account == entries[j].key.account {
				return entries[i].key.client < entries[j].key.client
			}
			return entries[i].key.account < entries[j].key.account
		}
		return entries[i].last.Before(entries[j].last)
	})
	for _, entry := range entries[:len(entries)-limiterBucketCap] {
		delete(buckets, entry.key)
	}
}

func (l *loginLimiter) allowClient(client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempt++
	if l.attempt%256 == 0 {
		pruneBuckets(l.account, now)
		pruneBuckets(l.client, now)
	}
	return client == "" || take(l.client, client, 20, now)
}

func pruneBuckets(buckets map[string]bucket, now time.Time) {
	for key, b := range buckets {
		if now.Sub(b.last) >= time.Minute {
			delete(buckets, key)
		}
	}
	if len(buckets) <= limiterBucketCap {
		return
	}
	type entry struct {
		key  string
		last time.Time
	}
	entries := make([]entry, 0, len(buckets))
	for key, b := range buckets {
		entries = append(entries, entry{key: key, last: b.last})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].last.Equal(entries[j].last) {
			return entries[i].key < entries[j].key
		}
		return entries[i].last.Before(entries[j].last)
	})
	for _, entry := range entries[:len(entries)-limiterBucketCap] {
		delete(buckets, entry.key)
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

func takeAccountClient(buckets map[accountClientKey]bucket, key accountClientKey, capacity float64, now time.Time) bool {
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
