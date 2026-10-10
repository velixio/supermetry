// Package dict stores and caches integer encodings for analytics dimensions.
package dict

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Kind identifies a separate dictionary namespace.
type Kind int16

const (
	Path Kind = iota
	Referrer
	Source
	Medium
	Campaign
	UTMContent
	UTMTerm
	EventName
	PropKey
	Browser
	OS
	Region
	City
	BrowserVersion
	OSVersion
	Language
	Hostname
)

const (
	maxValueBytes = 2000

	// OtherValue is the stable label used when a dictionary kind reaches its
	// configured bound. It matches the daily ingest cardinality bucket.
	OtherValue = "Other"

	// DefaultMaxEntriesPerKind bounds each in-memory and persisted dictionary
	// namespace. One entry is reserved for OtherValue.
	DefaultMaxEntriesPerKind = 100_000

	dictionaryAdvisoryLockNamespace int32 = 1_397_566_537
)

// Option configures a Dict.
type Option func(*Dict)

// WithMaxEntriesPerKind sets the total cache and database admission bound for
// every Kind. The bound includes the reserved OtherValue entry.
func WithMaxEntriesPerKind(limit int) Option {
	return func(d *Dict) {
		if limit > 0 {
			d.maxEntriesPerKind = limit
		}
	}
}

// Dict persists dictionary values in PostgreSQL and caches both directions.
type Dict struct {
	pool *pgxpool.Pool

	mu      sync.RWMutex
	byValue map[Kind]map[string]int32
	byID    map[Kind]map[int32]string

	maxEntriesPerKind int
	kindMu            [Hostname + 1]sync.Mutex
}

// New returns an empty dictionary cache backed by pool.
func New(pool *pgxpool.Pool, options ...Option) *Dict {
	d := &Dict{
		pool:              pool,
		byValue:           make(map[Kind]map[string]int32),
		byID:              make(map[Kind]map[int32]string),
		maxEntriesPerKind: DefaultMaxEntriesPerKind,
	}
	for _, option := range options {
		option(d)
	}
	return d
}

// IDs returns stable IDs for values, inserting cache misses in one batch.
func (d *Dict) IDs(ctx context.Context, kind Kind, values []string) ([]int32, error) {
	if kind < Path || kind > Hostname {
		return nil, fmt.Errorf("dict: invalid kind %d", kind)
	}
	if ids, ok := d.cachedIDs(kind, values); ok {
		return ids, nil
	}
	d.kindMu[kind].Lock()
	defer d.kindMu[kind].Unlock()

	normalized := make([]string, len(values))
	ids := make([]int32, len(values))
	missing := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	resolved := make(map[string]string, len(values))

	d.mu.RLock()
	cache := d.byValue[kind]
	regularEntries := len(cache)
	if _, ok := cache[OtherValue]; ok {
		regularEntries--
	}
	regularRemaining := max(d.maxEntriesPerKind-1-regularEntries, 0)
	for i, value := range values {
		value = normalize(value)
		normalized[i] = value
		if id, ok := cache[value]; ok {
			ids[i] = id
			continue
		}
		if admitted, ok := resolved[value]; ok {
			normalized[i] = admitted
			if id, cached := cache[admitted]; cached {
				ids[i] = id
			}
			continue
		}
		original := value
		if value != OtherValue {
			if regularRemaining == 0 {
				normalized[i] = OtherValue
				value = OtherValue
			} else {
				regularRemaining--
			}
		}
		resolved[original] = value
		if id, ok := cache[value]; ok {
			ids[i] = id
			continue
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			missing = append(missing, value)
		}
	}
	d.mu.RUnlock()

	if len(missing) == 0 {
		return ids, nil
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, dictionaryAdvisoryLockNamespace, int32(kind)); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		WITH input(value) AS MATERIALIZED (
			SELECT DISTINCT value FROM unnest($2::text[]) AS values(value)
		), inserted AS (
			INSERT INTO dict (kind, value)
			SELECT $1, input.value
			FROM input
			WHERE NOT EXISTS (
				SELECT 1 FROM dict WHERE kind = $1 AND value = input.value
			)
			RETURNING id, value
		)
		SELECT id, value FROM inserted
		UNION ALL
		SELECT dict.id, dict.value
		FROM dict
		JOIN input ON input.value = dict.value
		WHERE dict.kind = $1
		  AND NOT EXISTS (SELECT 1 FROM inserted WHERE inserted.value = dict.value)`,
		kind, missing)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int32
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			rows.Close()
			return nil, err
		}
		d.store(kind, id, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	d.mu.RLock()
	cache = d.byValue[kind]
	for i, value := range normalized {
		id, ok := cache[value]
		if !ok {
			id, ok = cache[OtherValue]
		}
		if !ok {
			d.mu.RUnlock()
			return nil, fmt.Errorf("dict: value %q was not resolved", value)
		}
		ids[i] = id
	}
	d.mu.RUnlock()
	return ids, nil
}

// cachedIDs resolves values without taking the per-kind admission lock when
// every value is already cached, keeping steady-state ingest lookups parallel.
func (d *Dict) cachedIDs(kind Kind, values []string) ([]int32, bool) {
	ids := make([]int32, len(values))
	d.mu.RLock()
	defer d.mu.RUnlock()
	cache := d.byValue[kind]
	for i, value := range values {
		id, ok := cache[normalize(value)]
		if !ok {
			return nil, false
		}
		ids[i] = id
	}
	return ids, true
}

// Value returns a cached value without accessing PostgreSQL.
func (d *Dict) Value(kind Kind, id int32) (string, bool) {
	d.mu.RLock()
	value, ok := d.byID[kind][id]
	if !ok && id != 0 && d.kindAtLimitLocked(kind) {
		value, ok = OtherValue, true
	}
	d.mu.RUnlock()
	return value, ok
}

// Load adds a bounded set of persisted dictionary entries to the cache. Older
// values outside the bound resolve to OtherValue, preserving dashboard labels
// without reloading an attacker-sized table into memory.
func (d *Dict) Load(ctx context.Context) error {
	for kind := Path; kind <= Hostname; kind++ {
		d.kindMu[kind].Lock()
	}
	defer func() {
		for kind := Hostname; kind >= Path; kind-- {
			d.kindMu[kind].Unlock()
		}
	}()

	rows, err := d.pool.Query(ctx, `
		SELECT kind, id, value FROM dict WHERE value = $1
		UNION ALL
		SELECT entries.kind, entries.id, entries.value
		FROM generate_series($2::integer, $3::integer) AS kinds(kind)
		CROSS JOIN LATERAL (
			SELECT dict.kind, dict.id, dict.value
			FROM dict
			WHERE dict.kind = kinds.kind AND dict.value <> $1
			ORDER BY dict.id
			LIMIT $4
		) AS entries`, OtherValue, Path, Hostname, max(d.maxEntriesPerKind-1, 0))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var kind Kind
		var id int32
		var value string
		if err := rows.Scan(&kind, &id, &value); err != nil {
			return err
		}
		d.store(kind, id, value)
	}
	return rows.Err()
}

// Len returns the number of cached entries for kind.
func (d *Dict) Len(kind Kind) int {
	d.mu.RLock()
	n := len(d.byID[kind])
	d.mu.RUnlock()
	return n
}

func (d *Dict) store(kind Kind, id int32, value string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byValue[kind] == nil {
		d.byValue[kind] = make(map[string]int32)
	}
	if d.byID[kind] == nil {
		d.byID[kind] = make(map[int32]string)
	}
	if _, exists := d.byValue[kind][value]; exists {
		return
	}
	if value != OtherValue && d.regularEntriesLocked(kind) >= max(d.maxEntriesPerKind-1, 0) {
		return
	}
	if value == OtherValue && len(d.byID[kind]) >= d.maxEntriesPerKind {
		return
	}
	d.byValue[kind][value] = id
	d.byID[kind][id] = value
}

func (d *Dict) kindAtLimitLocked(kind Kind) bool {
	return d.regularEntriesLocked(kind) >= max(d.maxEntriesPerKind-1, 0)
}

func (d *Dict) regularEntriesLocked(kind Kind) int {
	entries := len(d.byValue[kind])
	if _, ok := d.byValue[kind][OtherValue]; ok {
		entries--
	}
	return entries
}

func normalize(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= maxValueBytes {
		return value
	}
	value = value[:maxValueBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
