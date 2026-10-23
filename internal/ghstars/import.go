package ghstars

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"supermetry/internal/scheduler"
)

const (
	// defaultLargeThreshold stays well under GitHub's REST depth cap for
	// stargazer listings, which cuts off somewhere past ~30,000 entries.
	defaultLargeThreshold = 30000
	defaultChunkBudget    = 4 * time.Minute
	importEvery           = 10 * time.Minute
	syncSlack             = 5 * time.Minute
	maxIncrementalPages   = 50
)

func (s *Service) largeThreshold() int {
	if s.LargeThreshold > 0 {
		return s.LargeThreshold
	}
	return defaultLargeThreshold
}

func (s *Service) chunkBudget() time.Duration {
	if s.ChunkBudget > 0 {
		return s.ChunkBudget
	}
	return defaultChunkBudget
}

// ImportJob returns the scheduler job that keeps importing the history of large
// repositories chunk by chunk. For everything else it is a cheap no-op.
func (s *Service) ImportJob() scheduler.Job {
	return scheduler.Job{Name: "github-stars-import", Every: importEvery, Run: s.ImportPending}
}

// ImportPending runs one chunk for each enabled repository whose history import
// is unfinished: more stars than the REST threshold, or a saved cursor.
func (s *Service) ImportPending(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `
		select r.id from github_star_repos r left join github_star_state st on st.repo = r.repo
		where r.enabled and not r.backfill_complete and (coalesce(st.stars,0) > $1 or r.backfill_cursor is not null)
		order by r.id`, s.largeThreshold())
	if err != nil {
		return fmt.Errorf("list pending star imports: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.ReconcileRepo(ctx, id); err != nil && !errors.Is(err, ErrBusy) {
			slog.Warn("github star import failed", "repo_id", id, "error", err)
		}
	}
	return nil
}

// reconcileLarge handles repositories the REST listing cannot page through.
// The live counters and the daily snapshot come from the counts call and are
// saved first, so they stay right even when the import cannot run. Then the
// history is imported newest to oldest in resumable chunks and, once complete,
// kept current by reading only the newest pages.
//
// Limit: unstars cannot be detected from a partial walk, so for these
// repositories the webhook is the only source of unstars; a missed unstar is
// not repaired.
func (s *Service) reconcileLarge(ctx context.Context, r repoRow, store *Store, client *Client, counts Counts) error {
	now := s.Now()
	if err := store.SaveState(ctx, counts, now); err != nil {
		return err
	}
	if err := store.SaveSnapshot(ctx, now.In(store.loc), counts); err != nil {
		return err
	}
	if client.Token == "" {
		return ErrTokenRequired
	}
	if r.backfillComplete {
		return s.readNewest(ctx, store, client)
	}
	return s.importChunk(ctx, r, store, client)
}

// importChunk imports pages from the saved cursor until the history ends, the
// time or page budget is spent, or ctx ends. The cursor is saved with each page.
func (s *Service) importChunk(ctx context.Context, r repoRow, store *Store, client *Client) error {
	recorded, err := store.CurrentStargazers(ctx)
	if err != nil {
		return err
	}
	cursor := ""
	if r.backfillCursor != nil {
		cursor = *r.backfillCursor
	}
	deadline := time.Now().Add(s.chunkBudget())
	for pages := 1; ; pages++ {
		page, err := client.StargazerPage(ctx, store.Repo(), cursor)
		if err != nil {
			if ctx.Err() != nil {
				return nil // stopped; everything up to the last saved page is kept
			}
			return err
		}
		if page.HasNext && page.EndCursor == "" {
			return errors.New("github graphql: page without a cursor")
		}
		var events []Event
		for _, g := range page.Stargazers {
			if _, known := recorded[g.UserID]; known {
				continue
			}
			recorded[g.UserID] = g.Login
			events = append(events, Event{UserID: g.UserID, Login: g.Login, Action: ActionStarred, OccurredAt: g.StarredAt, Source: SourceBackfill})
		}
		done := !page.HasNext
		if err := store.ImportPage(ctx, events, page.EndCursor, done); err != nil {
			return err
		}
		if s.pageHook != nil {
			s.pageHook()
		}
		cursor = page.EndCursor
		if done || ctx.Err() != nil {
			return nil
		}
		if s.ChunkPages > 0 && pages >= s.ChunkPages {
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
	}
}

// readNewest reads the newest stargazer pages until one is entirely on record
// and inserts the stars it has not seen, as reconcile events.
func (s *Service) readNewest(ctx context.Context, store *Store, client *Client) error {
	recorded, err := store.CurrentStargazers(ctx)
	if err != nil {
		return err
	}
	erased, err := store.erasedUsers(ctx)
	if err != nil {
		return err
	}
	cursor := ""
	for pages := 0; pages < maxIncrementalPages; pages++ {
		page, err := client.StargazerPage(ctx, store.Repo(), cursor)
		if err != nil {
			return err
		}
		var events []Event
		known := 0
		for _, g := range page.Stargazers {
			if _, ok := recorded[g.UserID]; ok || erased[g.UserID] {
				known++
				continue
			}
			recorded[g.UserID] = g.Login
			events = append(events, Event{UserID: g.UserID, Login: g.Login, Action: ActionStarred, OccurredAt: g.StarredAt, Source: SourceReconcile})
		}
		if err := store.InsertEvents(ctx, events); err != nil {
			return err
		}
		if !page.HasNext || known == len(page.Stargazers) {
			return nil
		}
		cursor = page.EndCursor
	}
	return nil
}
