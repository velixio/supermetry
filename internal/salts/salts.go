// Package salts manages random daily visitor-identification salts.
package salts

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const saltLength = 32

var utcPlus14 = time.FixedZone("UTC+14", 14*60*60)

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

// Store persists salts in PostgreSQL and serves them from an in-memory cache.
type Store struct {
	pool  *pgxpool.Pool
	clock Clock

	mutationMu sync.Mutex
	cacheMu    sync.RWMutex
	cache      map[time.Time][]byte
}

// New returns an empty salt store backed by pool.
func New(pool *pgxpool.Pool, clk Clock) *Store {
	return &Store{
		pool:  pool,
		clock: clk,
		cache: make(map[time.Time][]byte),
	}
}

// Refresh ensures unexpired salts exist for up to two days ago through
// tomorrow in UTC, then replaces the cache with those persisted values.
func (s *Store) Refresh(ctx context.Context) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	now := s.clock.Now()
	today := civilDay(now.UTC())
	cutoff := expiredDayCutoff(now)
	days := [...]time.Time{
		today.AddDate(0, 0, -2),
		today.AddDate(0, 0, -1),
		today,
		today.AddDate(0, 0, 1),
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin salt refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	loaded := make(map[time.Time][]byte, len(days))
	for _, day := range days {
		if day.Before(cutoff) {
			continue
		}
		generated := make([]byte, saltLength)
		if _, err := rand.Read(generated); err != nil {
			return fmt.Errorf("generate salt for %s: %w", day.Format(time.DateOnly), err)
		}
		if _, err := tx.Exec(ctx, `
insert into salts(day, salt)
values ($1, $2)
on conflict (day) do nothing`, day, generated); err != nil {
			return fmt.Errorf("ensure salt for %s: %w", day.Format(time.DateOnly), err)
		}

		var persisted []byte
		if err := tx.QueryRow(ctx, `select salt from salts where day = $1`, day).Scan(&persisted); err != nil {
			return fmt.Errorf("load salt for %s: %w", day.Format(time.DateOnly), err)
		}
		loaded[day] = persisted
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit salt refresh: %w", err)
	}

	s.cacheMu.Lock()
	s.cache = loaded
	s.cacheMu.Unlock()
	return nil
}

// For returns the cached salt for localDay. It never reads PostgreSQL.
func (s *Store) For(localDay time.Time) ([]byte, bool) {
	day := civilDay(localDay)
	s.cacheMu.RLock()
	salt, ok := s.cache[day]
	s.cacheMu.RUnlock()
	return salt, ok
}

// DeleteExpired removes salts whose UTC+14 calendar day ended more than 48
// hours ago and evicts the same days from the cache.
func (s *Store) DeleteExpired(ctx context.Context) (int, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	cutoff := expiredDayCutoff(s.clock.Now())
	rows, err := s.pool.Query(ctx, `delete from salts where day < $1 returning day`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired salts: %w", err)
	}
	defer rows.Close()

	var deleted []time.Time
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			return 0, fmt.Errorf("scan deleted salt day: %w", err)
		}
		deleted = append(deleted, civilDay(day))
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("delete expired salts: %w", err)
	}

	s.cacheMu.Lock()
	for _, day := range deleted {
		delete(s.cache, day)
	}
	s.cacheMu.Unlock()
	return len(deleted), nil
}

// LocalDay returns the local calendar date containing t, represented as UTC
// midnight so it can be used directly as a PostgreSQL date and cache key.
func LocalDay(t time.Time, loc *time.Location) time.Time {
	return civilDay(t.In(loc))
}

func civilDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// expiredDayCutoff returns an exclusive date cutoff. At exactly 48 hours
// after a UTC+14 day ends, that day is retained; one nanosecond later it is
// eligible for deletion.
func expiredDayCutoff(now time.Time) time.Time {
	threshold := now.In(utcPlus14).Add(-48 * time.Hour)
	cutoff := civilDay(threshold)
	if threshold.Hour() == 0 && threshold.Minute() == 0 && threshold.Second() == 0 && threshold.Nanosecond() == 0 {
		cutoff = cutoff.AddDate(0, 0, -1)
	}
	return cutoff
}

var macPool = sync.Pool{New: func() any { return &macState{} }}

type macState struct {
	key []byte
	h   hash.Hash
	buf [sha256.Size]byte
}

// VisitorID returns the first eight bytes of HMAC-SHA256 over the site, IP
// and user agent. Callers must not persist or log the IP or user agent.
func VisitorID(salt []byte, siteID int32, ip, ua string) int64 {
	state := macPool.Get().(*macState)
	if state.h == nil || !hmac.Equal(state.key, salt) {
		state.key = append(state.key[:0], salt...)
		state.h = hmac.New(sha256.New, state.key)
	} else {
		state.h.Reset()
	}

	var site [4]byte
	binary.BigEndian.PutUint32(site[:], uint32(siteID))
	_, _ = state.h.Write(site[:])
	_, _ = state.h.Write([]byte(ip))
	_, _ = state.h.Write([]byte{0})
	_, _ = state.h.Write([]byte(ua))
	sum := state.h.Sum(state.buf[:0])
	id := int64(binary.BigEndian.Uint64(sum[:8]))
	macPool.Put(state)
	return id
}
