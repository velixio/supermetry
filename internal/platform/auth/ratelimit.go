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

const (
	secondFactorMaxFailures = 5
	secondFactorWindow      = 5 * time.Minute
	secondFactorLockout     = 15 * time.Minute
	enrollmentLockout       = 5 * time.Minute
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
	mu                 sync.Mutex
	account            map[string]bucket
	client             map[string]bucket
	accountClient      map[accountClientKey]bucket
	resetAccount       map[string]bucket
	resetClient        map[string]bucket
	resetAccountClient map[accountClientKey]bucket
	attempt            uint64
	loginCapacity      float64
}

type accountClientKey struct {
	account string
	client  string
}

type secondFactorAccount struct {
	failures    []time.Time
	lockedUntil time.Time
	last        time.Time
	pending     int
	generation  uint64
}

type secondFactorAttempt struct {
	accountID  int64
	account    *secondFactorAccount
	generation uint64
}

// secondFactorLimiter tracks failures by account only. It is intentionally
// separate from the password limiter: possession of the first factor must not
// grant a fresh second-factor guessing budget for every client address.
type secondFactorLimiter struct {
	mu          sync.Mutex
	accounts    map[int64]*secondFactorAccount
	maxFailures int
	window      time.Duration
	lockout     time.Duration
}

func newSecondFactorLimiter(maxFailures int, window, lockout time.Duration) *secondFactorLimiter {
	return &secondFactorLimiter{
		accounts:    make(map[int64]*secondFactorAccount),
		maxFailures: maxFailures,
		window:      window,
		lockout:     lockout,
	}
}

func (l *secondFactorLimiter) begin(accountID int64, now time.Time) (*secondFactorAttempt, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	account := l.accounts[accountID]
	if account == nil {
		if len(l.accounts) >= limiterBucketCap && !l.evictOldestSettled() {
			return nil, false
		}
		account = &secondFactorAccount{}
		l.accounts[accountID] = account
	}
	if now.Before(account.lockedUntil) {
		return nil, false
	}
	if !account.lockedUntil.IsZero() {
		account.failures = nil
		account.lockedUntil = time.Time{}
		account.pending = 0
		account.generation++
	}
	cutoff := now.Add(-l.window)
	kept := account.failures[:0]
	for _, failedAt := range account.failures {
		if failedAt.After(cutoff) {
			kept = append(kept, failedAt)
		}
	}
	account.failures = kept
	if len(account.failures)+account.pending >= l.maxFailures {
		return nil, false
	}
	account.pending++
	account.last = now
	return &secondFactorAttempt{accountID: accountID, account: account, generation: account.generation}, true
}

// failed records a rejected code and reports whether this failure newly
// triggered a lockout.
func (l *secondFactorLimiter) failed(attempt *secondFactorAttempt, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	account := l.accounts[attempt.accountID]
	if account != attempt.account || account.generation != attempt.generation {
		return false
	}
	if account.pending > 0 {
		account.pending--
	}
	account.failures = append(account.failures, now)
	account.last = now
	locked := len(account.failures) >= l.maxFailures
	if locked {
		account.failures = nil
		account.lockedUntil = now.Add(l.lockout)
		account.generation++
	}
	l.prune(now)
	return locked
}

func (l *secondFactorLimiter) succeeded(attempt *secondFactorAttempt) {
	l.mu.Lock()
	if account := l.accounts[attempt.accountID]; account == attempt.account && account.generation == attempt.generation {
		account.generation++
		delete(l.accounts, attempt.accountID)
	}
	l.mu.Unlock()
}

func (l *secondFactorLimiter) abandon(attempt *secondFactorAttempt) {
	l.mu.Lock()
	if account := l.accounts[attempt.accountID]; account == attempt.account && account.generation == attempt.generation {
		if account.pending > 0 {
			account.pending--
		}
		if account.pending == 0 && len(account.failures) == 0 {
			delete(l.accounts, attempt.accountID)
		}
	}
	l.mu.Unlock()
}

func (l *secondFactorLimiter) prune(now time.Time) {
	for accountID, account := range l.accounts {
		if account.pending > 0 {
			continue
		}
		if !account.lockedUntil.IsZero() && !now.Before(account.lockedUntil) ||
			account.lockedUntil.IsZero() && now.Sub(account.last) >= l.window {
			delete(l.accounts, accountID)
		}
	}
	for len(l.accounts) > limiterBucketCap {
		if !l.evictOldestSettled() {
			return
		}
	}
}

func (l *secondFactorLimiter) evictOldestSettled() bool {
	var oldestID int64
	var oldest time.Time
	found := false
	for accountID, account := range l.accounts {
		if account.pending > 0 {
			continue
		}
		if !found || account.last.Before(oldest) || account.last.Equal(oldest) && accountID < oldestID {
			oldestID = accountID
			oldest = account.last
			found = true
		}
	}
	if found {
		delete(l.accounts, oldestID)
	}
	return found
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

func (l *loginLimiter) setLoginCapacity(capacity int) {
	if capacity > 0 {
		l.loginCapacity = float64(capacity)
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

	clientCapacity, pairCapacity, accountCapacity := float64(20), float64(accountClientCapacity), float64(accountCeilingPerMinute)
	if l.loginCapacity > 0 {
		clientCapacity, pairCapacity, accountCapacity = l.loginCapacity, l.loginCapacity, l.loginCapacity
	}
	if client != "" && !take(l.client, client, clientCapacity, now) {
		return false
	}
	if client != "" && !takeAccountClient(l.accountClient, accountClientKey{account: account, client: client}, pairCapacity, now) {
		return false
	}
	// One client can spend only accountClientCapacity per minute, so a single
	// attacker cannot drain the shared account bucket and lock the user out.
	// The account ceiling stays as a backstop against distributed guessing; it
	// is high enough that it takes many distinct clients to reach it. Callers
	// without a derived client key keep the strict cap.
	if client == "" {
		if l.loginCapacity > 0 {
			return take(l.account, account, accountCapacity, now)
		}
		return take(l.account, account, 5, now)
	}
	return take(l.account, account, accountCapacity, now)
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
	capacity := float64(20)
	if l.loginCapacity > 0 {
		capacity = l.loginCapacity
	}
	return client == "" || take(l.client, client, capacity, now)
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
