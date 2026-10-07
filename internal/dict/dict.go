// Package dict stores and caches integer encodings for analytics dimensions.
package dict

import (
	"context"
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
)

const maxValueBytes = 2000

// Dict persists dictionary values in PostgreSQL and caches both directions.
type Dict struct {
	pool *pgxpool.Pool

	mu      sync.RWMutex
	byValue map[Kind]map[string]int32
	byID    map[Kind]map[int32]string
}

// New returns an empty dictionary cache backed by pool.
func New(pool *pgxpool.Pool) *Dict {
	return &Dict{
		pool:    pool,
		byValue: make(map[Kind]map[string]int32),
		byID:    make(map[Kind]map[int32]string),
	}
}

// IDs returns stable IDs for values, inserting cache misses in one batch.
func (d *Dict) IDs(ctx context.Context, kind Kind, values []string) ([]int32, error) {
	normalized := make([]string, len(values))
	ids := make([]int32, len(values))
	missing := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))

	d.mu.RLock()
	cache := d.byValue[kind]
	for i, value := range values {
		value = normalize(value)
		normalized[i] = value
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

	rows, err := d.pool.Query(ctx, `
		INSERT INTO dict (kind, value)
		SELECT $1, value FROM unnest($2::text[]) AS values(value)
		ON CONFLICT (kind, value) DO NOTHING
		RETURNING id, value`, kind, missing)
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

	rows, err = d.pool.Query(ctx, `
		SELECT id, value
		FROM dict
		WHERE kind = $1 AND value = ANY($2::text[])`, kind, missing)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int32
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			return nil, err
		}
		d.store(kind, id, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d.mu.RLock()
	cache = d.byValue[kind]
	for i, value := range normalized {
		ids[i] = cache[value]
	}
	d.mu.RUnlock()
	return ids, nil
}

// Value returns a cached value without accessing PostgreSQL.
func (d *Dict) Value(kind Kind, id int32) (string, bool) {
	d.mu.RLock()
	value, ok := d.byID[kind][id]
	d.mu.RUnlock()
	return value, ok
}

// Load adds every persisted dictionary entry to the cache.
func (d *Dict) Load(ctx context.Context) error {
	rows, err := d.pool.Query(ctx, `SELECT kind, id, value FROM dict`)
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

func (d *Dict) store(kind Kind, id int32, value string) {
	d.mu.Lock()
	if d.byValue[kind] == nil {
		d.byValue[kind] = make(map[string]int32)
	}
	if d.byID[kind] == nil {
		d.byID[kind] = make(map[int32]string)
	}
	d.byValue[kind][value] = id
	d.byID[kind][id] = value
	d.mu.Unlock()
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
