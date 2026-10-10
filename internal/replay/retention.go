package replay

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/config"
	"supermetry/internal/sites"
)

const replayRetentionIdleTimeout = 30 * time.Minute

type retentionSite struct {
	id       int32
	location *time.Location
	settings SiteSettings
}

type retainedReplay struct {
	sessionID int64
	startedAt time.Time
	lastSeen  time.Time
	day       time.Time
	bytes     int64
	blobKey   string
	profileID *int64
}

// RunRetention applies the stored per-site replay retention and quota values.
func RunRetention(ctx context.Context, pool *pgxpool.Pool, store BlobStore, now time.Time) error {
	return RunRetentionConfigured(ctx, pool, store, now, config.ReplayEnv{})
}

// RunRetentionConfigured applies per-site values after environment overrides.
func RunRetentionConfigured(ctx context.Context, pool *pgxpool.Pool, store BlobStore, now time.Time, environment config.ReplayEnv) error {
	if pool == nil {
		return fmt.Errorf("run replay retention: database pool is nil")
	}
	if store == nil {
		return fmt.Errorf("run replay retention: blob store is nil")
	}
	siteRows, err := pool.Query(ctx, `
		select id, timezone, coalesce(settings->'replay', '{}'::jsonb)
		from sites where deleted_at is null order by id`)
	if err != nil {
		return fmt.Errorf("load replay retention sites: %w", err)
	}
	var retentionSites []retentionSite
	for siteRows.Next() {
		var siteID int32
		var timezone string
		var raw json.RawMessage
		if err := siteRows.Scan(&siteID, &timezone, &raw); err != nil {
			siteRows.Close()
			return fmt.Errorf("scan replay retention site: %w", err)
		}
		input, err := sites.ParseReplaySiteInput(raw)
		if err != nil {
			siteRows.Close()
			return fmt.Errorf("parse replay settings for site %d: %w", siteID, err)
		}
		location, err := time.LoadLocation(timezone)
		if err != nil {
			siteRows.Close()
			return fmt.Errorf("load timezone for replay site %d: %w", siteID, err)
		}
		retentionSites = append(retentionSites, retentionSite{
			id: siteID, location: location, settings: ResolveSite(environment, input),
		})
	}
	if err := siteRows.Err(); err != nil {
		siteRows.Close()
		return fmt.Errorf("iterate replay retention sites: %w", err)
	}
	siteRows.Close()

	for _, site := range retentionSites {
		if err := retainSite(ctx, pool, store, site, now); err != nil {
			return err
		}
	}
	return nil
}

func retainSite(ctx context.Context, pool *pgxpool.Pool, store BlobStore, site retentionSite, now time.Time) error {
	rows, err := loadRetainedReplays(ctx, pool, site.id)
	if err != nil {
		return err
	}
	rows, err = deleteOrphanedProfileReplays(ctx, pool, store, site.id, rows)
	if err != nil {
		return err
	}
	localNow := now.In(site.location)
	cutoff := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, site.location).
		AddDate(0, 0, -site.settings.RetentionDays.V)

	firstKept := 0
	for firstKept < len(rows) && dayBefore(rows[firstKept].day, cutoff) {
		day := rows[firstKept].day
		end := dayGroupEnd(rows, firstKept)
		if err := deleteReplayDay(ctx, pool, store, site.id, day); err != nil {
			return err
		}
		firstKept = end
	}
	rows = rows[firstKept:]

	var total int64
	for _, row := range rows {
		if row.bytes < 0 {
			return fmt.Errorf("replay session %d for site %d has negative bytes", row.sessionID, site.id)
		}
		if row.bytes > int64(^uint64(0)>>1)-total {
			return fmt.Errorf("replay byte total overflows for site %d", site.id)
		}
		total += row.bytes
	}
	quota := site.settings.QuotaBytes.V
	for total > quota && len(rows) != 0 {
		end := dayGroupEnd(rows, 0)
		if end < len(rows) {
			dayBytes := replayBytes(rows[:end])
			if err := deleteReplayDay(ctx, pool, store, site.id, rows[0].day); err != nil {
				return err
			}
			total -= dayBytes
			rows = rows[end:]
			continue
		}
		idleBefore := now.Add(-replayRetentionIdleTimeout)
		kept := rows[:0]
		for _, row := range rows {
			if total > quota && !row.lastSeen.After(idleBefore) {
				if err := deleteReplaySession(ctx, pool, store, site.id, row); err != nil {
					return err
				}
				total -= row.bytes
				continue
			}
			kept = append(kept, row)
		}
		rows = kept
		break
	}
	return garbageCollectCSS(ctx, store, site.id, rows, cutoff)
}

func garbageCollectCSS(ctx context.Context, store BlobStore, siteID int32, rows []retainedReplay, cutoff time.Time) error {
	prefix := "replay/" + strconv.FormatInt(int64(siteID), 10) + "/css/"
	blobs, err := store.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list replay CSS for site %d: %w", siteID, err)
	}
	if len(blobs) == 0 {
		return nil
	}
	referenced := make(map[string]struct{})
	for _, row := range rows {
		if err := collectReplayCSSReferences(ctx, store, row.blobKey, referenced); err != nil {
			return fmt.Errorf("collect CSS references from replay session %d for site %d: %w", row.sessionID, siteID, err)
		}
	}
	for _, blob := range blobs {
		hash := strings.TrimSuffix(strings.TrimPrefix(blob.Key, prefix), ".gz")
		if !validCSSHash(hash) || !blob.Modified.Before(cutoff) {
			continue
		}
		if _, ok := referenced[hash]; ok {
			continue
		}
		if _, err := store.Delete(ctx, blob.Key); err != nil {
			return fmt.Errorf("delete unreferenced replay CSS %q: %w", blob.Key, err)
		}
	}
	return nil
}

func collectReplayCSSReferences(ctx context.Context, store BlobStore, key string, references map[string]struct{}) error {
	reader, err := store.Open(ctx, key)
	if err != nil {
		return err
	}
	defer reader.Close()
	compressed, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(compressed)
	for {
		var value any
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			_ = compressed.Close()
			return err
		}
		collectCSSReferences(value, references)
	}
	return compressed.Close()
}

func collectCSSReferences(value any, references map[string]struct{}) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "_cssRef" {
				if hash, ok := child.(string); ok && validCSSHash(hash) {
					references[hash] = struct{}{}
				}
			}
			collectCSSReferences(child, references)
		}
	case []any:
		for _, child := range value {
			collectCSSReferences(child, references)
		}
	}
}

func loadRetainedReplays(ctx context.Context, pool *pgxpool.Pool, siteID int32) ([]retainedReplay, error) {
	rows, err := pool.Query(ctx, `
		select session_id, started_at, last_seen_at, day, bytes, blob_key, profile_id
		from replay_sessions where site_id=$1
		order by day, started_at, session_id`, siteID)
	if err != nil {
		return nil, fmt.Errorf("load replay sessions for site %d: %w", siteID, err)
	}
	defer rows.Close()
	var result []retainedReplay
	for rows.Next() {
		var row retainedReplay
		if err := rows.Scan(&row.sessionID, &row.startedAt, &row.lastSeen, &row.day, &row.bytes, &row.blobKey, &row.profileID); err != nil {
			return nil, fmt.Errorf("scan replay session for site %d: %w", siteID, err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate replay sessions for site %d: %w", siteID, err)
	}
	return result, nil
}

func deleteOrphanedProfileReplays(ctx context.Context, pool *pgxpool.Pool, store BlobStore, siteID int32, rows []retainedReplay) ([]retainedReplay, error) {
	profileIDs := make([]int64, 0)
	seen := make(map[int64]struct{})
	for _, row := range rows {
		if row.profileID == nil {
			continue
		}
		if _, exists := seen[*row.profileID]; !exists {
			seen[*row.profileID] = struct{}{}
			profileIDs = append(profileIDs, *row.profileID)
		}
	}
	if len(profileIDs) == 0 {
		return rows, nil
	}
	existingRows, err := pool.Query(ctx, `select id from profiles where site_id=$1 and id=any($2::bigint[])`, siteID, profileIDs)
	if err != nil {
		return nil, fmt.Errorf("load replay profiles for site %d: %w", siteID, err)
	}
	existing := make(map[int64]struct{}, len(profileIDs))
	for existingRows.Next() {
		var profileID int64
		if err := existingRows.Scan(&profileID); err != nil {
			existingRows.Close()
			return nil, fmt.Errorf("scan replay profile for site %d: %w", siteID, err)
		}
		existing[profileID] = struct{}{}
	}
	if err := existingRows.Err(); err != nil {
		existingRows.Close()
		return nil, fmt.Errorf("iterate replay profiles for site %d: %w", siteID, err)
	}
	existingRows.Close()

	kept := rows[:0]
	for _, row := range rows {
		if row.profileID != nil {
			if _, exists := existing[*row.profileID]; !exists {
				if err := deleteReplaySession(ctx, pool, store, siteID, row); err != nil {
					return nil, err
				}
				continue
			}
		}
		kept = append(kept, row)
	}
	return kept, nil
}

func deleteReplayDay(ctx context.Context, pool *pgxpool.Pool, store BlobStore, siteID int32, day time.Time) error {
	prefix := "replay/" + strconv.FormatInt(int64(siteID), 10) + "/" + day.Format(time.DateOnly) + "/"
	if _, err := store.DeletePrefix(ctx, prefix); err != nil {
		return fmt.Errorf("delete replay day %s for site %d: %w", day.Format(time.DateOnly), siteID, err)
	}
	if _, err := pool.Exec(ctx, `delete from replay_sessions where site_id=$1 and day=$2`, siteID, day); err != nil {
		return fmt.Errorf("delete replay index day %s for site %d: %w", day.Format(time.DateOnly), siteID, err)
	}
	return nil
}

func deleteReplaySession(ctx context.Context, pool *pgxpool.Pool, store BlobStore, siteID int32, row retainedReplay) error {
	if _, err := store.Delete(ctx, row.blobKey); err != nil {
		return fmt.Errorf("delete replay session %d for site %d: %w", row.sessionID, siteID, err)
	}
	if _, err := pool.Exec(ctx, `delete from replay_sessions where site_id=$1 and session_id=$2 and started_at=$3`, siteID, row.sessionID, row.startedAt); err != nil {
		return fmt.Errorf("delete replay index session %d for site %d: %w", row.sessionID, siteID, err)
	}
	return nil
}

// EraseSite removes all replay blobs and index rows belonging to a site.
func EraseSite(ctx context.Context, pool *pgxpool.Pool, store BlobStore, siteID int32) error {
	if pool == nil || store == nil {
		return fmt.Errorf("erase replay site %d: database pool and blob store are required", siteID)
	}
	prefix := "replay/" + strconv.FormatInt(int64(siteID), 10) + "/"
	if _, err := store.DeletePrefix(ctx, prefix); err != nil {
		return fmt.Errorf("erase replay blobs for site %d: %w", siteID, err)
	}
	if _, err := pool.Exec(ctx, `delete from replay_sessions where site_id=$1`, siteID); err != nil {
		return fmt.Errorf("erase replay index for site %d: %w", siteID, err)
	}
	return nil
}

// EraseSessions removes selected replay blobs and index rows for a site.
func EraseSessions(ctx context.Context, pool *pgxpool.Pool, store BlobStore, siteID int32, sessionIDs []int64) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	if pool == nil || store == nil {
		return fmt.Errorf("erase replay sessions for site %d: database pool and blob store are required", siteID)
	}
	return eraseSessions(ctx, pool, store, siteID, sessionIDs)
}

type replayEraserDB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func eraseSessions(ctx context.Context, database replayEraserDB, store BlobStore, siteID int32, sessionIDs []int64) error {
	ids := append([]int64(nil), sessionIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	rows, err := database.Query(ctx, `
		select session_id, started_at, last_seen_at, day, bytes, blob_key, profile_id
		from replay_sessions where site_id=$1 and session_id=any($2::bigint[])
		order by started_at, session_id`, siteID, ids)
	if err != nil {
		return fmt.Errorf("load replay sessions for erasure at site %d: %w", siteID, err)
	}
	var selected []retainedReplay
	for rows.Next() {
		var row retainedReplay
		if err := rows.Scan(&row.sessionID, &row.startedAt, &row.lastSeen, &row.day, &row.bytes, &row.blobKey, &row.profileID); err != nil {
			rows.Close()
			return fmt.Errorf("scan replay session for erasure at site %d: %w", siteID, err)
		}
		selected = append(selected, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate replay sessions for erasure at site %d: %w", siteID, err)
	}
	rows.Close()
	for _, row := range selected {
		if _, err := store.Delete(ctx, row.blobKey); err != nil {
			return fmt.Errorf("delete replay session %d for site %d: %w", row.sessionID, siteID, err)
		}
		if _, err := database.Exec(ctx, `delete from replay_sessions where site_id=$1 and session_id=$2 and started_at=$3`, siteID, row.sessionID, row.startedAt); err != nil {
			return fmt.Errorf("delete replay index session %d for site %d: %w", row.sessionID, siteID, err)
		}
	}
	return nil
}

// EraseSessionsTx removes selected replay blobs and index rows as part of a
// caller-owned database transaction. Blob deletion happens before each index
// row is removed so a storage failure leaves the row available for a retry
// when the transaction is rolled back.
func EraseSessionsTx(ctx context.Context, tx pgx.Tx, store BlobStore, siteID int32, sessionIDs []int64) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	if tx == nil || store == nil {
		return fmt.Errorf("erase replay sessions for site %d: database transaction and blob store are required", siteID)
	}
	return eraseSessions(ctx, tx, store, siteID, sessionIDs)
}

func dayBefore(day time.Time, cutoff time.Time) bool {
	year, month, date := day.Date()
	localDay := time.Date(year, month, date, 0, 0, 0, 0, cutoff.Location())
	return localDay.Before(cutoff)
}

func dayGroupEnd(rows []retainedReplay, start int) int {
	day := rows[start].day.Format(time.DateOnly)
	end := start + 1
	for end < len(rows) && rows[end].day.Format(time.DateOnly) == day {
		end++
	}
	return end
}

func replayBytes(rows []retainedReplay) int64 {
	var total int64
	for _, row := range rows {
		total += row.bytes
	}
	return total
}
