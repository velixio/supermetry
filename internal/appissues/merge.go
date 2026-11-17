package appissues

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/approllup"
)

// MaxMergeSources caps how many issues one merge takes in.
const MaxMergeSources = 100

// Merge folds the source issues into the target in one transaction: it
// re-points their fingerprint hashes, moves their events, re-sums the daily
// and hourly rollups and recomputes the target's counts from its events. The
// sources stay as hidden rows (merged_into set) so new events with their
// fingerprints land on the target. Targets and sources must belong to the
// site; a source that is itself merged, or a merged target, is rejected.
// Sources that were merge targets keep their own sources, which are re-pointed
// at the new target, so a merge chain is always one level deep.
func (s *Service) Merge(ctx context.Context, siteID int32, targetID int64, sourceIDs []int64, actor Actor) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.MergeTx(ctx, tx, siteID, targetID, sourceIDs, actor)
	})
}

// Unmerge splits the events whose fingerprint hash is in hashes (sha256 of the
// fingerprint, as listed by Merged) back out of the target. Each hash must
// belong to a source merged into the target; that source issue is reinstated
// and receives the events. Both issues' counts and rollups are recomputed from
// events. It returns the first reinstated issue's id.
func (s *Service) Unmerge(ctx context.Context, siteID int32, targetID int64, hashes [][]byte, actor Actor) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		id, err = s.UnmergeTx(ctx, tx, siteID, targetID, hashes, actor)
		return err
	})
	return id, err
}

type mergeRow struct {
	id        int64
	mergedTo  *int64
	count     int64
	firstSeen time.Time
	lastSeen  time.Time
}

// lockIssues locks the rows in id order and returns them by id.
func lockIssues(ctx context.Context, tx pgx.Tx, siteID int32, ids []int64) (map[int64]mergeRow, error) {
	rows, err := tx.Query(ctx, `select id, merged_into, event_count, first_seen, last_seen
from app_issues where site_id = $1 and id = any($2) order by id for update`, siteID, ids)
	if err != nil {
		return nil, fmt.Errorf("lock issues: %w", err)
	}
	defer rows.Close()
	out := map[int64]mergeRow{}
	for rows.Next() {
		var r mergeRow
		if err := rows.Scan(&r.id, &r.mergedTo, &r.count, &r.firstSeen, &r.lastSeen); err != nil {
			return nil, err
		}
		out[r.id] = r
	}
	return out, rows.Err()
}

// historyRemainder is the part of an issue's event_count that its stored
// events no longer account for (events dropped by retention). It is kept on
// the issue that owns it, so a merge or unmerge never loses history.
func historyRemainder(ctx context.Context, tx pgx.Tx, siteID int32, issues map[int64]mergeRow) (map[int64]int64, error) {
	ids := make([]int64, 0, len(issues))
	for id := range issues {
		ids = append(ids, id)
	}
	rows, err := tx.Query(ctx, `select issue_id, sum(occurrences)::bigint from app_events
where site_id = $1 and issue_id = any($2) and kind <> 'session' group by issue_id`, siteID, ids)
	if err != nil {
		return nil, fmt.Errorf("sum issue events: %w", err)
	}
	defer rows.Close()
	out := map[int64]int64{}
	for id, r := range issues {
		out[id] = r.count
	}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = max(0, issues[id].count-n)
	}
	return out, rows.Err()
}

// MergeTx is Merge inside the caller's transaction.
func (s *Service) MergeTx(ctx context.Context, tx pgx.Tx, siteID int32, targetID int64, sourceIDs []int64, actor Actor) error {
	srcs := slices.Clone(sourceIDs)
	slices.Sort(srcs)
	srcs = slices.Compact(srcs)
	if len(srcs) == 0 {
		return invalid("no source issues given")
	}
	if len(srcs) > MaxMergeSources {
		return invalid("at most %d issues can be merged at once", MaxMergeSources)
	}
	if slices.Contains(srcs, targetID) {
		return invalid("an issue cannot be merged into itself")
	}
	if err := approllup.LockSite(ctx, tx, siteID); err != nil {
		return err
	}
	all := append([]int64{targetID}, srcs...)
	issues, err := lockIssues(ctx, tx, siteID, all)
	if err != nil {
		return err
	}
	if len(issues) != len(all) {
		return ErrNotFound // unknown, or another site's issue
	}
	target := issues[targetID]
	if target.mergedTo != nil {
		return invalid("the target issue is itself merged into another issue")
	}
	for _, id := range srcs {
		if issues[id].mergedTo != nil {
			return invalid("issue %d is already merged into another issue", id)
		}
	}
	remainder, err := historyRemainder(ctx, tx, siteID, issues)
	if err != nil {
		return err
	}
	var history int64
	first, last := target.firstSeen, target.lastSeen
	for _, id := range all {
		history += remainder[id]
		if f := issues[id].firstSeen; f.Before(first) {
			first = f
		}
		if l := issues[id].lastSeen; l.After(last) {
			last = l
		}
	}
	// Chains: the sources' own sources now point at the new target.
	if _, err := tx.Exec(ctx, `update app_issues set merged_into = $2 where site_id = $1 and merged_into = any($3)`, siteID, targetID, srcs); err != nil {
		return fmt.Errorf("re-point merged issues: %w", err)
	}
	if _, err := tx.Exec(ctx, `update app_issues set merged_into = $2 where site_id = $1 and id = any($3)`, siteID, targetID, srcs); err != nil {
		return fmt.Errorf("mark merged issues: %w", err)
	}
	if _, err := tx.Exec(ctx, `update app_issue_hashes set issue_id = $2 where site_id = $1 and issue_id = any($3)`, siteID, targetID, srcs); err != nil {
		return fmt.Errorf("re-point issue hashes: %w", err)
	}
	if _, err := tx.Exec(ctx, `update app_events set issue_id = $2 where site_id = $1 and issue_id = any($3)`, siteID, targetID, srcs); err != nil {
		return fmt.Errorf("move events: %w", err)
	}
	if err := s.foldRollups(ctx, tx, siteID, targetID, srcs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update app_issues set first_seen = $3, last_seen = $4,
  event_count = coalesce((select sum(occurrences) from app_events where site_id = $1 and issue_id = $2 and kind <> 'session'), 0) + $5
where id = $2`, siteID, targetID, first, last, history); err != nil {
		return fmt.Errorf("recount target: %w", err)
	}
	now := s.now()
	if err := record(ctx, tx, siteID, targetID, now, actor.id(), KindMerged, map[string]any{"sources": srcs}); err != nil {
		return err
	}
	for i, id := range srcs {
		if err := record(ctx, tx, siteID, id, now.Add(time.Duration(i+1)*time.Microsecond), actor.id(), KindMerged, map[string]any{"into": targetID}); err != nil {
			return err
		}
	}
	return nil
}

// UnmergeTx is Unmerge inside the caller's transaction.
func (s *Service) UnmergeTx(ctx context.Context, tx pgx.Tx, siteID int32, targetID int64, hashes [][]byte, actor Actor) (int64, error) {
	if len(hashes) == 0 {
		return 0, invalid("no hashes given")
	}
	if len(hashes) > MaxMergeSources {
		return 0, invalid("at most %d hashes can be unmerged at once", MaxMergeSources)
	}
	for _, h := range hashes {
		if len(h) != 32 {
			return 0, invalid("a hash must be 32 bytes (64 hex characters)")
		}
	}
	if err := approllup.LockSite(ctx, tx, siteID); err != nil {
		return 0, err
	}
	target, err := lockIssues(ctx, tx, siteID, []int64{targetID})
	if err != nil {
		return 0, err
	}
	if _, ok := target[targetID]; !ok {
		return 0, ErrNotFound
	}
	if target[targetID].mergedTo != nil {
		return 0, invalid("the issue is itself merged into another issue")
	}
	type source struct {
		id          int64
		fingerprint string
		hash        []byte
	}
	var sources []source
	seen := map[int64]bool{}
	for _, h := range hashes {
		var sc source
		sc.hash = h
		err := tx.QueryRow(ctx, `select id, fingerprint from app_issues
where site_id = $1 and merged_into = $2 and sha256(convert_to(fingerprint, 'UTF8')) = $3 for update`, siteID, targetID, h).Scan(&sc.id, &sc.fingerprint)
		if err != nil {
			if err == pgx.ErrNoRows {
				return 0, invalid("hash %s is not merged into this issue", hex.EncodeToString(h))
			}
			return 0, fmt.Errorf("find merged issue: %w", err)
		}
		if !seen[sc.id] {
			seen[sc.id] = true
			sources = append(sources, sc)
		}
	}
	remainder, err := historyRemainder(ctx, tx, siteID, target)
	if err != nil {
		return 0, err
	}
	ids := []int64{targetID}
	for _, sc := range sources {
		if _, err := tx.Exec(ctx, `update app_events set issue_id = $3 where site_id = $1 and issue_id = $2 and fingerprint = $4`, siteID, targetID, sc.id, sc.fingerprint); err != nil {
			return 0, fmt.Errorf("move events back: %w", err)
		}
		if _, err := tx.Exec(ctx, `update app_issue_hashes set issue_id = $3 where site_id = $1 and issue_id = $2 and hash = $4`, siteID, targetID, sc.id, sc.hash); err != nil {
			return 0, fmt.Errorf("re-point issue hash: %w", err)
		}
		if _, err := tx.Exec(ctx, `update app_issues set merged_into = null,
  event_count = coalesce((select sum(occurrences) from app_events where site_id = $1 and issue_id = $2 and kind <> 'session'), 0),
  first_seen = least(first_seen, coalesce((select min(ts) from app_events where site_id = $1 and issue_id = $2 and kind <> 'session'), first_seen)),
  last_seen = greatest(last_seen, coalesce((select max(ts) from app_events where site_id = $1 and issue_id = $2 and kind <> 'session'), last_seen))
where id = $2`, siteID, sc.id); err != nil {
			return 0, fmt.Errorf("reinstate issue: %w", err)
		}
		ids = append(ids, sc.id)
	}
	// The target's history older than its stored events stays with it.
	if _, err := tx.Exec(ctx, `update app_issues set
  event_count = coalesce((select sum(occurrences) from app_events where site_id = $1 and issue_id = $2 and kind <> 'session'), 0) + $3,
  first_seen = case when $3 > 0 then first_seen else coalesce((select min(ts) from app_events where site_id = $1 and issue_id = $2 and kind <> 'session'), first_seen) end,
  last_seen = coalesce((select max(ts) from app_events where site_id = $1 and issue_id = $2 and kind <> 'session'), last_seen)
where id = $2`, siteID, targetID, remainder[targetID]); err != nil {
		return 0, fmt.Errorf("recount target: %w", err)
	}
	if err := s.rebuildRollups(ctx, tx, siteID, ids); err != nil {
		return 0, err
	}
	now := s.now()
	back := make([]int64, 0, len(sources))
	for _, sc := range sources {
		back = append(back, sc.id)
	}
	if err := record(ctx, tx, siteID, targetID, now, actor.id(), KindUnmerged, map[string]any{"sources": back}); err != nil {
		return 0, err
	}
	for i, sc := range sources {
		if err := record(ctx, tx, siteID, sc.id, now.Add(time.Duration(i+1)*time.Microsecond), actor.id(), KindUnmerged, map[string]any{"from": targetID}); err != nil {
			return 0, err
		}
	}
	return sources[0].id, nil
}

// MergedSource is one issue merged into a target, identified by the hash of
// its fingerprint, which is what Unmerge takes.
type MergedSource struct {
	IssueID  int64     `json:"issue_id"`
	Hash     string    `json:"hash"`
	Title    string    `json:"title"`
	Kind     string    `json:"kind"`
	Events   int64     `json:"events"`
	LastSeen time.Time `json:"last_seen"`
}

// Merged lists the issues merged into the target, newest first. Events counts
// the target's stored events with that fingerprint.
func (s *Service) Merged(ctx context.Context, siteID int32, targetID int64) ([]MergedSource, error) {
	if err := s.issueExists(ctx, siteID, targetID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `select i.id, encode(sha256(convert_to(i.fingerprint, 'UTF8')), 'hex'), i.title, i.kind, i.last_seen,
  coalesce((select sum(e.occurrences) from app_events e where e.site_id = $1 and e.issue_id = $2 and e.fingerprint = i.fingerprint and e.kind <> 'session'), 0)::bigint
from app_issues i where i.site_id = $1 and i.merged_into = $2 order by i.last_seen desc, i.id desc limit 500`, siteID, targetID)
	if err != nil {
		return nil, fmt.Errorf("list merged issues: %w", err)
	}
	defer rows.Close()
	out := []MergedSource{}
	for rows.Next() {
		var m MergedSource
		if err := rows.Scan(&m.IssueID, &m.Hash, &m.Title, &m.Kind, &m.LastSeen, &m.Events); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
