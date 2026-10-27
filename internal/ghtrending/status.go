package ghtrending

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Status values of a repository's watcher.
const (
	StatusOff        = "off"        // not opted in
	StatusWaiting    = "waiting"    // opted in, nothing fetched yet
	StatusWatching   = "watching"   // lists read fine
	StatusUnreadable = "unreadable" // at least one relevant list cannot be read right now
)

// ListStatus is the health of one list the repository depends on.
type ListStatus struct {
	Language    string     `json:"list_language"`
	Since       string     `json:"since"`
	LastOKAt    *time.Time `json:"last_ok_at"`
	LastError   string     `json:"last_error"`
	ParseOK     bool       `json:"parse_ok"`
	Entries     int        `json:"entries"`
	NoListUntil *time.Time `json:"no_list_until"`
}

// RepoStatus is the read model of GET .../repos/{id}/trending.
type RepoStatus struct {
	Enabled       bool         `json:"enabled"`
	Language      string       `json:"language"`
	Status        string       `json:"status"`
	LastCheckedAt *time.Time   `json:"last_checked_at"`
	Lists         []ListStatus `json:"lists"`
	// DeferredLists is how many lists the last run postponed because of the
	// per-run request cap; they are fetched on following runs, oldest first.
	DeferredLists int        `json:"deferred_lists"`
	Current       []Sighting `json:"current"`
	// Episodes are the latest episodes, open and closed, newest first.
	Episodes []Sighting `json:"episodes"`
}

// Status derives the watcher state of one repository.
func (s *Service) Status(ctx context.Context, orgID, repoID int64) (RepoStatus, error) {
	var out RepoStatus
	err := s.pool.QueryRow(ctx, `select trending_enabled, language from github_star_repos where id=$1 and org_id=$2`, repoID, orgID).Scan(&out.Enabled, &out.Language)
	if errors.Is(err, pgx.ErrNoRows) {
		return RepoStatus{}, ErrNotFound
	}
	if err != nil {
		return RepoStatus{}, fmt.Errorf("load trending repo: %w", err)
	}
	out.Lists, out.Current, out.Episodes = []ListStatus{}, []Sighting{}, []Sighting{}

	rows, err := s.pool.Query(ctx, `select id, list_language, since, first_rank, best_rank, last_rank, first_seen_at, last_seen_at, left_at
		from github_trending_sightings where repo_id=$1 order by first_seen_at desc, id desc limit $2`, repoID, historyLength)
	if err != nil {
		return RepoStatus{}, fmt.Errorf("query trending episodes: %w", err)
	}
	for rows.Next() {
		var g Sighting
		if err := rows.Scan(&g.ID, &g.ListLanguage, &g.Since, &g.FirstRank, &g.BestRank, &g.LastRank, &g.FirstSeenAt, &g.LastSeenAt, &g.LeftAt); err != nil {
			rows.Close()
			return RepoStatus{}, err
		}
		out.Episodes = append(out.Episodes, g)
		if g.LeftAt == nil {
			out.Current = append(out.Current, g)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return RepoStatus{}, err
	}

	if !out.Enabled {
		out.Status = StatusOff
		return out, nil
	}
	lrows, err := s.pool.Query(ctx, `select list_language, since, last_ok_at, last_error, parse_ok, entries, skip_until
		from github_trending_list_state where list_language='' or list_language=lower($1) order by list_language, since`, out.Language)
	if err != nil {
		return RepoStatus{}, fmt.Errorf("query trending lists: %w", err)
	}
	defer lrows.Close()
	for lrows.Next() {
		var l ListStatus
		if err := lrows.Scan(&l.Language, &l.Since, &l.LastOKAt, &l.LastError, &l.ParseOK, &l.Entries, &l.NoListUntil); err != nil {
			return RepoStatus{}, err
		}
		out.Lists = append(out.Lists, l)
		if l.LastOKAt != nil && (out.LastCheckedAt == nil || l.LastOKAt.After(*out.LastCheckedAt)) {
			t := *l.LastOKAt
			out.LastCheckedAt = &t
		}
	}
	if err := lrows.Err(); err != nil {
		return RepoStatus{}, err
	}
	out.Status = StatusWaiting
	if len(out.Lists) > 0 {
		out.Status = StatusWatching
	}
	for _, l := range out.Lists {
		// A language GitHub has no list for is not a fault of the repository.
		if l.NoListUntil == nil && (!l.ParseOK || l.LastError != "") {
			out.Status = StatusUnreadable
		}
	}
	if err := s.pool.QueryRow(ctx, `select lists_deferred from github_trending_run where id=1`).Scan(&out.DeferredLists); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RepoStatus{}, err
	}
	return out, nil
}
