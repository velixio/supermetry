// Package profiletombstone prevents erased profile identities from being
// recreated while accepted events are still buffered.
package profiletombstone

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	Window              = 48 * time.Hour
	defaultPollInterval = 2 * time.Second
	defaultPollOverlap  = 2 * time.Minute
)

type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type key struct {
	siteID int32
	hash   [sha256.Size]byte
}

type item struct {
	erasedAt    time.Time
	completedAt time.Time
}

type profileSessions interface {
	TombstoneProfile(siteID int32, hash [sha256.Size]byte)
	ExpireProfileTombstone(siteID int32, hash [sha256.Size]byte)
}

type Store struct {
	pool         *pgxpool.Pool
	clock        Clock
	pollInterval time.Duration

	mu              sync.RWMutex
	items           map[key]item
	lastErasedAt    time.Time
	lastCompletedAt time.Time
	profileSessions profileSessions
	rememberWrites  atomic.Uint64
}

func New(pool *pgxpool.Pool, clock Clock) *Store {
	if clock == nil {
		clock = wallClock{}
	}
	return &Store{
		pool: pool, clock: clock, pollInterval: configuredPollInterval(),
		items: make(map[key]item),
	}
}

func configuredPollInterval() time.Duration {
	if value := os.Getenv("SUPERMETRY_TOMBSTONE_POLL"); value != "" {
		if interval, err := time.ParseDuration(value); err == nil && interval > 0 {
			return interval
		}
	}
	return defaultPollInterval
}

func identityKey(siteID int32, externalID string) key {
	return key{siteID: siteID, hash: sha256.Sum256([]byte(externalID))}
}

// SetSessions connects the hash-only live-session side index. Existing
// tombstones are replayed so startup wiring order does not matter.
func (s *Store) SetSessions(sessions profileSessions) {
	s.mu.Lock()
	s.profileSessions = sessions
	keys := make([]key, 0, len(s.items))
	for tombstone := range s.items {
		keys = append(keys, tombstone)
	}
	s.mu.Unlock()
	if sessions != nil {
		for _, tombstone := range keys {
			sessions.TombstoneProfile(tombstone.siteID, tombstone.hash)
		}
	}
}

func (s *Store) Load(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `
		select t.site_id, t.external_hash, t.erased_at,
			case
				when latest.status = 'done' then latest.completed_at
				when latest.id is null then t.erased_at
			end
		from profile_tombstones t
		left join lateral (
			select id, status, completed_at
			from profile_erasure_requests r
			where r.site_id=t.site_id and r.external_hash=t.external_hash
			order by r.created_at desc, r.id desc limit 1
		) latest on true
		where (latest.id is null and t.erased_at >= $1)
			or (latest.id is not null and (latest.status <> 'done' or latest.completed_at >= $1))`, s.clock.Now().Add(-Window))
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := make(map[key]item)
	var lastErasedAt, lastCompletedAt time.Time
	for rows.Next() {
		tombstone, value, ok, err := scanItem(rows)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		loaded[tombstone] = value
		lastErasedAt = maxTime(lastErasedAt, value.erasedAt)
		lastCompletedAt = maxTime(lastCompletedAt, value.completedAt)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.items = loaded
	s.lastErasedAt = lastErasedAt
	s.lastCompletedAt = lastCompletedAt
	sessions := s.profileSessions
	s.mu.Unlock()
	if sessions != nil {
		for tombstone := range loaded {
			sessions.TombstoneProfile(tombstone.siteID, tombstone.hash)
		}
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanItem(row rowScanner) (key, item, bool, error) {
	var siteID int32
	var hash []byte
	var value item
	var completedAt *time.Time
	if err := row.Scan(&siteID, &hash, &value.erasedAt, &completedAt); err != nil {
		return key{}, item{}, false, err
	}
	if len(hash) != sha256.Size {
		return key{}, item{}, false, nil
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash)
	if completedAt != nil {
		value.completedAt = *completedAt
	}
	return key{siteID: siteID, hash: sum}, value, true, nil
}

func (s *Store) Remember(ctx context.Context, siteID int32, externalID string) error {
	erasedAt := s.clock.Now()
	tombstone := identityKey(siteID, externalID)
	if _, err := s.pool.Exec(ctx, `
		insert into profile_tombstones(site_id,external_hash,erased_at)
		values ($1,$2,$3)
		on conflict (site_id,external_hash) do update set erased_at=excluded.erased_at`,
		siteID, tombstone.hash[:], erasedAt); err != nil {
		return err
	}
	// completedAt is left zero: the underlying erasure request has not
	// completed yet, and a poll will fill it in once it has. Setting it to
	// erasedAt here would start the 48h expiry window at erasure instead of
	// completion.
	s.remember(tombstone, item{erasedAt: erasedAt})
	return nil
}

// RememberTx only persists. Publishing before the caller commits would leave
// a phantom in-memory tombstone if a later request or audit write rolled back.
func (s *Store) RememberTx(ctx context.Context, tx pgx.Tx, siteID int32, externalID string) error {
	tombstone := identityKey(siteID, externalID)
	_, err := tx.Exec(ctx, `
		insert into profile_tombstones(site_id,external_hash,erased_at)
		values ($1,$2,$3)
		on conflict (site_id,external_hash) do update set erased_at=excluded.erased_at`,
		siteID, tombstone.hash[:], s.clock.Now())
	return err
}

func (s *Store) RememberMemory(siteID int32, externalID string) {
	erasedAt := s.clock.Now()
	// See the comment in Remember: completedAt stays zero until a poll (or
	// RememberTx's caller) confirms the request is actually done.
	s.remember(identityKey(siteID, externalID), item{erasedAt: erasedAt})
}

func (s *Store) remember(tombstone key, value item) {
	s.mu.RLock()
	current, known := s.items[tombstone]
	unchanged := known && (current.erasedAt.After(value.erasedAt) ||
		(current.erasedAt.Equal(value.erasedAt) && current.completedAt.Equal(value.completedAt)))
	s.mu.RUnlock()
	if unchanged {
		return
	}

	s.rememberWrites.Add(1)
	s.mu.Lock()
	current, known = s.items[tombstone]
	if known && (current.erasedAt.After(value.erasedAt) ||
		(current.erasedAt.Equal(value.erasedAt) && current.completedAt.Equal(value.completedAt))) {
		s.mu.Unlock()
		return
	}
	s.items[tombstone] = value
	s.lastErasedAt = maxTime(s.lastErasedAt, value.erasedAt)
	sessions := s.profileSessions
	s.mu.Unlock()
	if !known && sessions != nil {
		sessions.TombstoneProfile(tombstone.siteID, tombstone.hash)
	}
}

func (s *Store) rememberWriteLockCount() uint64 { return s.rememberWrites.Load() }

func (s *Store) Contains(siteID int32, externalID string) bool {
	tombstone := identityKey(siteID, externalID)
	s.mu.RLock()
	value, ok := s.items[tombstone]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	if value.completedAt.IsZero() || !s.clock.Now().After(value.completedAt.Add(Window)) {
		return true
	}
	s.expireMemory(tombstone, value.completedAt)
	return false
}

// Poll imports tombstones and completion times committed by other servers,
// then performs database and process-local expiry.
func (s *Store) Poll(ctx context.Context) error {
	if err := s.pollTombstones(ctx); err != nil {
		return err
	}
	if err := s.pollCompletions(ctx); err != nil {
		return err
	}
	return s.DeleteExpired(ctx)
}

func (s *Store) pollTombstones(ctx context.Context) error {
	s.mu.RLock()
	after := s.lastErasedAt.Add(-defaultPollOverlap)
	s.mu.RUnlock()
	rows, err := s.pool.Query(ctx, `
		select t.site_id, t.external_hash, t.erased_at,
			case
				when latest.status = 'done' then latest.completed_at
				when latest.id is null then t.erased_at
			end
		from profile_tombstones t
		left join lateral (
			select id, status, completed_at from profile_erasure_requests r
			where r.site_id=t.site_id and r.external_hash=t.external_hash
			order by r.created_at desc, r.id desc limit 1
		) latest on true
		where t.erased_at >= $1`, after)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		tombstone, value, ok, err := scanItem(rows)
		if err != nil {
			return err
		}
		if ok {
			s.remember(tombstone, value)
		}
	}
	return rows.Err()
}

func (s *Store) pollCompletions(ctx context.Context) error {
	s.mu.RLock()
	after := s.lastCompletedAt.Add(-defaultPollOverlap)
	s.mu.RUnlock()
	rows, err := s.pool.Query(ctx, `
		select r.site_id, r.external_hash, r.completed_at
		from profile_erasure_requests r
		where r.status='done' and r.completed_at is not null and r.completed_at >= $1
			and r.id = (
				select latest.id from profile_erasure_requests latest
				where latest.site_id=r.site_id and latest.external_hash=r.external_hash
				order by latest.created_at desc, latest.id desc limit 1
			)`, after)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var siteID int32
		var hash []byte
		var completedAt time.Time
		if err := rows.Scan(&siteID, &hash, &completedAt); err != nil {
			return err
		}
		if len(hash) != sha256.Size {
			continue
		}
		var sum [sha256.Size]byte
		copy(sum[:], hash)
		tombstone := key{siteID: siteID, hash: sum}
		s.mu.Lock()
		if value, ok := s.items[tombstone]; ok {
			value.completedAt = completedAt
			s.items[tombstone] = value
		}
		s.lastCompletedAt = maxTime(s.lastCompletedAt, completedAt)
		s.mu.Unlock()
	}
	return rows.Err()
}

func (s *Store) DeleteExpired(ctx context.Context) error {
	cutoff := s.clock.Now().Add(-Window)
	if _, err := s.pool.Exec(ctx, `
		delete from profile_tombstones t
		where coalesce((
			select r.status='done' and r.completed_at < $1
			from profile_erasure_requests r
			where r.site_id=t.site_id and r.external_hash=t.external_hash
			order by r.created_at desc, r.id desc limit 1
		), t.erased_at < $1)`, cutoff); err != nil {
		return err
	}
	type expiredItem struct {
		key
		completedAt time.Time
	}
	s.mu.RLock()
	expired := make([]expiredItem, 0)
	for tombstone, value := range s.items {
		if !value.completedAt.IsZero() && value.completedAt.Before(cutoff) {
			expired = append(expired, expiredItem{key: tombstone, completedAt: value.completedAt})
		}
	}
	s.mu.RUnlock()
	for _, candidate := range expired {
		s.expireMemory(candidate.key, candidate.completedAt)
	}
	return nil
}

func (s *Store) expireMemory(tombstone key, completedAt time.Time) {
	s.mu.Lock()
	value, ok := s.items[tombstone]
	removed := ok && value.completedAt.Equal(completedAt)
	if removed {
		delete(s.items, tombstone)
	}
	sessions := s.profileSessions
	s.mu.Unlock()
	if removed && sessions != nil {
		sessions.ExpireProfileTombstone(tombstone.siteID, tombstone.hash)
	}
}

func maxTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func (s *Store) PollInterval() time.Duration { return s.pollInterval }

// Run polls independently in every process. It intentionally does not use
// the globally advisory-locked scheduler, which would let only one server
// update its memory.
func (s *Store) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.Poll(ctx); err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "profile tombstone poll failed", "error", err)
			}
		}
	}
}
