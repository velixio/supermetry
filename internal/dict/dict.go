// Package dict stores and caches integer encodings for analytics dimensions.
package dict

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/health"
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
	rollupRefreshAdvisoryLockID     int64 = 7_469_002
	minimumSiteShare                      = 2_000
	garbageCollectBatchSize               = 1_000
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

// WithLogger replaces the logger used for capacity warnings.
func WithLogger(logger *slog.Logger) Option {
	return func(d *Dict) {
		if logger != nil {
			d.logger = logger
		}
	}
}

// Dict persists dictionary values in PostgreSQL and caches both directions.
type Dict struct {
	pool *pgxpool.Pool

	mu            sync.RWMutex
	byValue       map[Kind]map[string]int32
	byID          map[Kind]map[int32]string
	firstSiteByID map[Kind]map[int32]int32
	siteEntries   map[Kind]map[int32]int

	maxEntriesPerKind int
	kindMu            [Hostname + 1]sync.Mutex
	logger            *slog.Logger
	warnMu            sync.Mutex
	warnings          [Hostname + 1]capacityWarning
}

type capacityWarning struct {
	day  string
	mask uint8
}

// New returns an empty dictionary cache backed by pool.
func New(pool *pgxpool.Pool, options ...Option) *Dict {
	d := &Dict{
		pool:              pool,
		byValue:           make(map[Kind]map[string]int32),
		byID:              make(map[Kind]map[int32]string),
		firstSiteByID:     make(map[Kind]map[int32]int32),
		siteEntries:       make(map[Kind]map[int32]int),
		maxEntriesPerKind: DefaultMaxEntriesPerKind,
		logger:            slog.Default(),
	}
	for _, option := range options {
		option(d)
	}
	return d
}

// IDs returns stable IDs for values, inserting cache misses in one batch.
func (d *Dict) IDs(ctx context.Context, kind Kind, values []string) ([]int32, error) {
	return d.IDsForSite(ctx, 0, kind, values)
}

// IDsForSite returns stable IDs and attributes newly admitted values to siteID.
// Once a kind is at least half full, a site is limited to the larger of 5% of
// capacity or 2,000 entries; the early headroom keeps small installations useful.
func (d *Dict) IDsForSite(ctx context.Context, siteID int32, kind Kind, values []string) ([]int32, error) {
	if kind < Path || kind > Hostname {
		return nil, fmt.Errorf("dict: invalid kind %d", kind)
	}
	if ids, ok := d.cachedIDs(kind, values); ok {
		return ids, nil
	}
	d.kindMu[kind].Lock()
	defer d.kindMu[kind].Unlock()
	if ids, ok := d.cachedIDs(kind, values); ok {
		return ids, nil
	}

	normalized := make([]string, len(values))
	uncached := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	d.mu.RLock()
	cache := d.byValue[kind]
	for i, value := range values {
		value = normalize(value)
		normalized[i] = value
		if _, ok := cache[value]; ok {
			continue
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			uncached = append(uncached, value)
		}
	}
	d.mu.RUnlock()

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, dictionaryAdvisoryLockNamespace, int32(kind)); err != nil {
		return nil, err
	}

	existing := make(map[string]struct{}, len(uncached)+1)
	rows, err := tx.Query(ctx, `SELECT value FROM dict WHERE kind=$1 AND (value=ANY($2::text[]) OR value=$3)`, kind, uncached, OtherValue)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return nil, err
		}
		existing[value] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var regularEntries, siteEntries int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE value<>$3),
		       count(*) FILTER (WHERE value<>$3 AND first_site_id=NULLIF($2, 0))
		FROM dict WHERE kind=$1`, kind, siteID, OtherValue).Scan(&regularEntries, &siteEntries); err != nil {
		return nil, err
	}
	regularRemaining := max(d.maxEntriesPerKind-1-regularEntries, 0)
	siteShare := max((d.maxEntriesPerKind+19)/20, minimumSiteShare)
	resolved := make(map[string]string, len(uncached))
	for _, value := range uncached {
		if _, ok := existing[value]; ok {
			resolved[value] = value
			continue
		}
		admitted := value
		if value != OtherValue {
			atSiteShare := siteID > 0 && regularEntries*2 >= d.maxEntriesPerKind && siteEntries >= siteShare
			if regularRemaining == 0 || atSiteShare {
				admitted = OtherValue
			} else {
				regularEntries++
				regularRemaining--
				if siteID > 0 {
					siteEntries++
				}
			}
		}
		resolved[value] = admitted
	}

	toInsert := make([]string, 0, len(resolved))
	toRevive := make([]string, 0, len(resolved))
	seen = make(map[string]struct{}, len(resolved))
	for _, value := range resolved {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		if _, ok := existing[value]; ok {
			toRevive = append(toRevive, value)
		} else {
			toInsert = append(toInsert, value)
		}
	}
	entries := make([]deletedEntry, 0, len(toInsert)+len(toRevive))
	if len(toRevive) > 0 {
		rows, err = tx.Query(ctx, `
			UPDATE dict SET gc_marked_at=NULL
			WHERE kind=$1 AND value=ANY($2::text[])
			RETURNING id, value, first_site_id`, kind, toRevive)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var entry deletedEntry
			entry.kind = kind
			if err := rows.Scan(&entry.id, &entry.value, &entry.firstSite); err != nil {
				rows.Close()
				return nil, err
			}
			entries = append(entries, entry)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if len(toInsert) > 0 {
		rows, err = tx.Query(ctx, `
			INSERT INTO dict (kind, value, first_site_id)
			SELECT $1, value, CASE WHEN value=$4 THEN NULL ELSE NULLIF($3, 0) END
			FROM unnest($2::text[]) AS input(value)
			RETURNING id, value, first_site_id`, kind, toInsert, siteID, OtherValue)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var entry deletedEntry
			entry.kind = kind
			if err := rows.Scan(&entry.id, &entry.value, &entry.firstSite); err != nil {
				rows.Close()
				return nil, err
			}
			entries = append(entries, entry)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		d.store(entry.kind, entry.id, entry.value, entry.firstSite)
	}
	d.updateFill(kind)

	ids := make([]int32, len(values))
	d.mu.RLock()
	cache = d.byValue[kind]
	for i, value := range normalized {
		if admitted, ok := resolved[value]; ok {
			value = admitted
		}
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
		SELECT kind, id, value, first_site_id FROM dict WHERE value = $1 AND gc_marked_at IS NULL
		UNION ALL
		SELECT entries.kind, entries.id, entries.value, entries.first_site_id
		FROM generate_series($2::integer, $3::integer) AS kinds(kind)
		CROSS JOIN LATERAL (
			SELECT dict.kind, dict.id, dict.value, dict.first_site_id
			FROM dict
			WHERE dict.kind = kinds.kind AND dict.value <> $1 AND dict.gc_marked_at IS NULL
			ORDER BY dict.id
			LIMIT $4
		) AS entries`, OtherValue, Path, Hostname, max(d.maxEntriesPerKind-1, 0))
	if err != nil {
		return err
	}
	defer rows.Close()
	byValue := make(map[Kind]map[string]int32)
	byID := make(map[Kind]map[int32]string)
	firstSiteByID := make(map[Kind]map[int32]int32)
	siteEntries := make(map[Kind]map[int32]int)

	for rows.Next() {
		var kind Kind
		var id int32
		var value string
		var firstSite *int32
		if err := rows.Scan(&kind, &id, &value, &firstSite); err != nil {
			return err
		}
		if byValue[kind] == nil {
			byValue[kind] = make(map[string]int32)
			byID[kind] = make(map[int32]string)
			firstSiteByID[kind] = make(map[int32]int32)
			siteEntries[kind] = make(map[int32]int)
		}
		byValue[kind][value] = id
		byID[kind][id] = value
		if firstSite != nil && value != OtherValue {
			firstSiteByID[kind][id] = *firstSite
			siteEntries[kind][*firstSite]++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	d.byValue, d.byID = byValue, byID
	d.firstSiteByID, d.siteEntries = firstSiteByID, siteEntries
	d.mu.Unlock()
	for kind := Path; kind <= Hostname; kind++ {
		d.updateFill(kind)
	}
	return nil
}

// Len returns the number of cached entries for kind.
func (d *Dict) Len(kind Kind) int {
	d.mu.RLock()
	n := len(d.byID[kind])
	d.mu.RUnlock()
	return n
}

func (d *Dict) store(kind Kind, id int32, value string, firstSite *int32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byValue[kind] == nil {
		d.byValue[kind] = make(map[string]int32)
	}
	if d.byID[kind] == nil {
		d.byID[kind] = make(map[int32]string)
		d.firstSiteByID[kind] = make(map[int32]int32)
		d.siteEntries[kind] = make(map[int32]int)
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
	if firstSite != nil && value != OtherValue {
		d.firstSiteByID[kind][id] = *firstSite
		d.siteEntries[kind][*firstSite]++
	}
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

func (d *Dict) updateFill(kind Kind) {
	d.mu.RLock()
	entries := len(d.byID[kind])
	d.mu.RUnlock()
	ratio := float64(entries) / float64(d.maxEntriesPerKind)
	health.SetDimensionDictionaryFill(int(kind), ratio)

	day := time.Now().Format(time.DateOnly)
	d.warnMu.Lock()
	state := &d.warnings[kind]
	if state.day != day {
		state.day, state.mask = day, 0
	}
	warn := make([]int, 0, 2)
	for _, threshold := range []struct {
		percent int
		ratio   float64
		bit     uint8
	}{{80, 0.80, 1}, {95, 0.95, 2}} {
		if ratio >= threshold.ratio && state.mask&threshold.bit == 0 {
			state.mask |= threshold.bit
			warn = append(warn, threshold.percent)
		}
	}
	d.warnMu.Unlock()
	for _, threshold := range warn {
		d.logger.Warn("dictionary capacity warning", "kind", kind.Label(), "fill_percent", ratio*100, "threshold", threshold)
	}
}

// Label returns the stable health label for a dictionary kind. It is not a
// String method on purpose: SQL builders and drivers format kinds as integers.
func (k Kind) Label() string {
	names := [...]string{
		"path", "referrer", "source", "medium", "campaign", "utm_content",
		"utm_term", "event_name", "prop_key", "browser", "os", "region", "city",
		"browser_version", "os_version", "language", "hostname",
	}
	if k < Path || k > Hostname {
		return fmt.Sprintf("kind_%d", k)
	}
	return names[k]
}

var garbageCollectedKinds = [...]Kind{Path, Referrer, Campaign, UTMContent, UTMTerm, EventName}

// GarbageCollect quarantines unreferenced rows, then deletes them only after a
// full day. The grace period lets every process refresh its cache and lets
// already queued writes land before deletion. It covers only kinds whose
// references can be exhaustively enumerated and serializes with day closing.
func (d *Dict) GarbageCollect(ctx context.Context, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = garbageCollectBatchSize
	}
	var total int64
	for {
		batch, err := d.garbageCollectBatch(ctx, batchSize)
		if err != nil {
			return total, err
		}
		total += int64(batch.deleted)
		if batch.marked < batchSize && batch.deleted < batchSize && batch.revived < batchSize {
			return total, nil
		}
	}
}

type deletedEntry struct {
	kind      Kind
	id        int32
	value     string
	firstSite *int32
}

type garbageCollectBatchResult struct {
	marked  int
	deleted int
	revived int
}

func (d *Dict) garbageCollectBatch(ctx context.Context, batchSize int) (garbageCollectBatchResult, error) {
	for _, kind := range garbageCollectedKinds {
		d.kindMu[kind].Lock()
	}
	defer func() {
		for i := len(garbageCollectedKinds) - 1; i >= 0; i-- {
			d.kindMu[garbageCollectedKinds[i]].Unlock()
		}
	}()

	evicted := make([]deletedEntry, 0, batchSize*2)
	revived := make([]deletedEntry, 0, batchSize)
	result := garbageCollectBatchResult{}
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, rollupRefreshAdvisoryLockID); err != nil {
			return fmt.Errorf("dict: lock rollup refresh: %w", err)
		}
		for _, kind := range garbageCollectedKinds {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, dictionaryAdvisoryLockNamespace, int32(kind)); err != nil {
				return fmt.Errorf("dict: lock kind %d: %w", kind, err)
			}
		}
		rows, err := tx.Query(ctx, garbageCollectSQL, batchSize)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var entry deletedEntry
			var action string
			if err := rows.Scan(&action, &entry.kind, &entry.id, &entry.value, &entry.firstSite); err != nil {
				return err
			}
			switch action {
			case "marked":
				evicted = append(evicted, entry)
				result.marked++
			case "deleted":
				evicted = append(evicted, entry)
				result.deleted++
			case "revived":
				revived = append(revived, entry)
				result.revived++
			}
		}
		return rows.Err()
	})
	if err != nil {
		return garbageCollectBatchResult{}, err
	}

	d.mu.Lock()
	for _, entry := range evicted {
		delete(d.byValue[entry.kind], entry.value)
		delete(d.byID[entry.kind], entry.id)
		if firstSite, ok := d.firstSiteByID[entry.kind][entry.id]; ok {
			delete(d.firstSiteByID[entry.kind], entry.id)
			if d.siteEntries[entry.kind][firstSite] > 0 {
				d.siteEntries[entry.kind][firstSite]--
			}
		}
	}
	d.mu.Unlock()
	for _, entry := range revived {
		d.store(entry.kind, entry.id, entry.value, entry.firstSite)
	}
	for _, kind := range garbageCollectedKinds {
		d.updateFill(kind)
	}
	return result, nil
}

const garbageCollectSQL = `
WITH checked AS MATERIALIZED (
    SELECT d.kind, d.id, d.gc_marked_at,
      CASE d.kind
        WHEN 0 THEN
          NOT EXISTS (SELECT 1 FROM events e WHERE e.path_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.entry_path=d.id OR s.exit_path=d.id)
          AND NOT EXISTS (SELECT 1 FROM sessions_closed s WHERE s.entry_path=d.id OR s.exit_path=d.id OR EXISTS (SELECT 1 FROM unnest(s.seq) code WHERE code > 0 AND code >> 2=d.id))
          AND NOT EXISTS (SELECT 1 FROM custom_closed c WHERE c.path_id=d.id OR c.entry_path=d.id OR c.exit_path=d.id)
          AND NOT EXISTS (SELECT 1 FROM rollup_day_dim r WHERE r.dim IN (1,7,8) AND r.value_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM rollup_month_dim r WHERE r.dim IN (1,7,8) AND r.value_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM rollup_block_dim r WHERE r.dim IN (1,7,8) AND r.value_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM heatmap_cells h WHERE h.path_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM verified_page_sessions v WHERE v.path_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM verified_page_day v WHERE v.path_id=d.id)
        WHEN 1 THEN NOT EXISTS (SELECT 1 FROM events e WHERE e.referrer_id=d.id)
        WHEN 4 THEN
          NOT EXISTS (SELECT 1 FROM events e WHERE e.campaign_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.campaign_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM sessions_closed s WHERE s.campaign_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM custom_closed c WHERE c.campaign_id=d.id OR c.event_utm_campaign_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM rollup_day_dim r WHERE r.dim=18 AND r.value_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM rollup_month_dim r WHERE r.dim=18 AND r.value_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM rollup_block_dim r WHERE r.dim=18 AND r.value_id=d.id)
        WHEN 5 THEN
          NOT EXISTS (SELECT 1 FROM events e WHERE e.utm_content_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM sessions_closed s WHERE s.utm_content_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM custom_closed c WHERE c.utm_content_id=d.id OR c.event_utm_content_id=d.id)
        WHEN 6 THEN
          NOT EXISTS (SELECT 1 FROM events e WHERE e.utm_term_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM sessions_closed s WHERE s.utm_term_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM custom_closed c WHERE c.utm_term_id=d.id OR c.event_utm_term_id=d.id)
        WHEN 7 THEN
          NOT EXISTS (SELECT 1 FROM events e WHERE e.name_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM custom_closed c WHERE c.name_id=d.id)
          AND NOT EXISTS (SELECT 1 FROM sessions_closed s WHERE EXISTS (SELECT 1 FROM unnest(s.seq) code WHERE code < 0 AND -code=d.id))
      END AS unreferenced
    FROM dict d
    WHERE d.value <> 'Other'
      -- Leave enough time for admitted IDs queued in the ingest writer to land.
      AND d.created_at < now() - interval '1 day'
      AND d.kind IN (0, 1, 4, 5, 6, 7)
), eligible AS MATERIALIZED (
    SELECT kind, id, gc_marked_at FROM checked WHERE unreferenced
), delete_candidates AS (
    SELECT kind, id FROM eligible
    WHERE gc_marked_at < now() - interval '1 day'
    ORDER BY kind, id
    LIMIT $1
), deleted AS (
    DELETE FROM dict d
    USING delete_candidates c
    WHERE d.kind=c.kind AND d.id=c.id
    RETURNING d.kind, d.id, d.value, d.first_site_id
), mark_candidates AS (
    SELECT kind, id FROM eligible
    WHERE gc_marked_at IS NULL
    ORDER BY kind, id
    LIMIT $1
), marked AS (
    UPDATE dict d SET gc_marked_at=now()
    FROM mark_candidates c
    WHERE d.kind=c.kind AND d.id=c.id
    RETURNING d.kind, d.id, d.value, d.first_site_id
), revive_candidates AS (
    SELECT kind, id FROM checked
    WHERE gc_marked_at IS NOT NULL AND NOT unreferenced
    ORDER BY kind, id
    LIMIT $1
), revived AS (
    UPDATE dict d SET gc_marked_at=NULL
    FROM revive_candidates c
    WHERE d.kind=c.kind AND d.id=c.id
    RETURNING d.kind, d.id, d.value, d.first_site_id
)
SELECT 'deleted', kind, id, value, first_site_id FROM deleted
UNION ALL
SELECT 'marked', kind, id, value, first_site_id FROM marked
UNION ALL
SELECT 'revived', kind, id, value, first_site_id FROM revived`

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
