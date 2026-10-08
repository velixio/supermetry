// Package profiletombstone prevents erased profile identities from being
// recreated while events accepted before the erasure are still buffered.
//
// Only SHA-256(external id) is kept, never the raw id. Lookups on the ingest
// and flush paths are in-memory; the table is read once at startup. With
// several instances, an erasure on one becomes visible to the others only
// after they restart.
package profiletombstone

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/scheduler"
)

const Window = 48 * time.Hour

type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type key struct {
	siteID int32
	hash   [sha256.Size]byte
}

type Store struct {
	pool  *pgxpool.Pool
	clock Clock
	mu    sync.RWMutex
	items map[key]time.Time
}

func New(pool *pgxpool.Pool, clock Clock) *Store {
	if clock == nil {
		clock = wallClock{}
	}
	return &Store{pool: pool, clock: clock, items: make(map[key]time.Time)}
}

func identityKey(siteID int32, externalID string) key {
	return key{siteID: siteID, hash: sha256.Sum256([]byte(externalID))}
}

func (s *Store) Load(ctx context.Context) error {
	now := s.clock.Now()
	rows, err := s.pool.Query(ctx, `
		select site_id, external_hash, erased_at
		from profile_tombstones where erased_at > $1`, now.Add(-Window))
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := make(map[key]time.Time)
	for rows.Next() {
		var siteID int32
		var hash []byte
		var erasedAt time.Time
		if err := rows.Scan(&siteID, &hash, &erasedAt); err != nil {
			return err
		}
		if len(hash) != sha256.Size {
			continue
		}
		var sum [sha256.Size]byte
		copy(sum[:], hash)
		loaded[key{siteID: siteID, hash: sum}] = erasedAt
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.items = loaded
	s.mu.Unlock()
	return nil
}

func (s *Store) Remember(ctx context.Context, siteID int32, externalID string) error {
	erasedAt := s.clock.Now()
	k := identityKey(siteID, externalID)
	if _, err := s.pool.Exec(ctx, `
		insert into profile_tombstones(site_id,external_hash,erased_at)
		values ($1,$2,$3)
		on conflict (site_id,external_hash) do update set erased_at=excluded.erased_at`,
		siteID, k.hash[:], erasedAt); err != nil {
		return err
	}
	s.remember(k, erasedAt)
	return nil
}

// RememberTx persists a tombstone inside the caller's erasure transaction and
// immediately exposes it to concurrent intake and writer flushes.
func (s *Store) RememberTx(ctx context.Context, tx pgx.Tx, siteID int32, externalID string) error {
	erasedAt := s.clock.Now()
	k := identityKey(siteID, externalID)
	s.remember(k, erasedAt)
	if _, err := tx.Exec(ctx, `
		insert into profile_tombstones(site_id,external_hash,erased_at)
		values ($1,$2,$3)
		on conflict (site_id,external_hash) do update set erased_at=excluded.erased_at`,
		siteID, k.hash[:], erasedAt); err != nil {
		s.forget(k, erasedAt)
		return err
	}
	return nil
}

func (s *Store) RememberMemory(siteID int32, externalID string) {
	s.remember(identityKey(siteID, externalID), s.clock.Now())
}

func (s *Store) ForgetMemory(siteID int32, externalID string) {
	k := identityKey(siteID, externalID)
	s.mu.Lock()
	delete(s.items, k)
	s.mu.Unlock()
}

func (s *Store) remember(k key, erasedAt time.Time) {
	s.mu.Lock()
	s.items[k] = erasedAt
	s.mu.Unlock()
}

func (s *Store) forget(k key, erasedAt time.Time) {
	s.mu.Lock()
	if s.items[k].Equal(erasedAt) {
		delete(s.items, k)
	}
	s.mu.Unlock()
}

func (s *Store) Contains(siteID int32, externalID string) bool {
	k := identityKey(siteID, externalID)
	now := s.clock.Now()
	s.mu.RLock()
	erasedAt, ok := s.items[k]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	if now.Sub(erasedAt) <= Window {
		return true
	}
	s.forget(k, erasedAt)
	return false
}

func (s *Store) DeleteExpired(ctx context.Context) error {
	cutoff := s.clock.Now().Add(-Window)
	if _, err := s.pool.Exec(ctx, `delete from profile_tombstones where erased_at <= $1`, cutoff); err != nil {
		return err
	}
	s.mu.Lock()
	for key, erasedAt := range s.items {
		if !erasedAt.After(cutoff) {
			delete(s.items, key)
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Store) Job() scheduler.Job {
	return scheduler.Job{Name: "profile-tombstones", Every: time.Hour, Run: s.DeleteExpired}
}
