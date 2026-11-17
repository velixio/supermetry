package sentryapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxReleaseBody    = 8 << 20 // release PUT only: 5,000 commits at about 1.5 KB
	maxCommitsPerPut  = 5000
	maxPathsPerCommit = 1000
	maxCommitMessage  = 2000
	maxPathLen        = 1024
	maxRepoNameLen    = 200
	maxAuthorNameLen  = 200
	maxAuthorEmailLen = 320
	maxRefsPerPut     = 100
	maxCommitsListed  = 1000
	defaultRepoName   = "unknown"
	defaultChangeType = "M"
)

var shaRE = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

type patchEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

// commitBody is one entry of the release PUT `commits` list, shaped like the
// commits sentry-cli collects from local git.
type commitBody struct {
	ID          string       `json:"id"`
	Repository  string       `json:"repository"`
	Message     string       `json:"message"`
	AuthorName  string       `json:"author_name"`
	AuthorEmail string       `json:"author_email"`
	Timestamp   *time.Time   `json:"timestamp"`
	PatchSet    []patchEntry `json:"patch_set"`
}

type refBody struct {
	Repository     string `json:"repository"`
	Commit         string `json:"commit"`
	PreviousCommit string `json:"previousCommit"`
}

// clip shortens s to at most n runes.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// cleanText drops NUL bytes, which text columns cannot hold.
func cleanText(s string) string { return strings.ReplaceAll(s, "\x00", "") }

// normalizeCommits validates a commit list and returns it deduplicated by
// (repository, sha) in payload order, with over-long text clipped. The string
// result is a 400 detail, empty when the list is acceptable.
func normalizeCommits(in []commitBody) ([]commitBody, string) {
	if len(in) > maxCommitsPerPut {
		return nil, "too many commits"
	}
	type key struct{ repo, sha string }
	seen := make(map[key]bool, len(in))
	out := make([]commitBody, 0, len(in))
	for _, c := range in {
		c.ID = strings.ToLower(strings.TrimSpace(c.ID))
		if !shaRE.MatchString(c.ID) {
			return nil, "invalid commit id"
		}
		c.Repository = cleanText(strings.TrimSpace(c.Repository))
		if c.Repository == "" {
			c.Repository = defaultRepoName
		}
		if utf8.RuneCountInString(c.Repository) > maxRepoNameLen {
			return nil, "repository name is too long"
		}
		if len(c.PatchSet) > maxPathsPerCommit {
			return nil, "too many files in a commit"
		}
		c.Message = clip(cleanText(c.Message), maxCommitMessage)
		c.AuthorName = clip(cleanText(c.AuthorName), maxAuthorNameLen)
		c.AuthorEmail = clip(cleanText(c.AuthorEmail), maxAuthorEmailLen)
		paths := make(map[string]bool, len(c.PatchSet))
		files := make([]patchEntry, 0, len(c.PatchSet))
		for _, p := range c.PatchSet {
			p.Path = cleanText(p.Path)
			if p.Path == "" || utf8.RuneCountInString(p.Path) > maxPathLen {
				return nil, "invalid file path in a commit"
			}
			if p.Type != "A" && p.Type != "D" {
				p.Type = defaultChangeType
			}
			if !paths[p.Path] {
				paths[p.Path] = true
				files = append(files, p)
			}
		}
		c.PatchSet = files
		k := key{c.Repository, c.ID}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out, ""
}

func normalizeRefs(in []refBody) ([]refBody, string) {
	if len(in) > maxRefsPerPut {
		return nil, "too many refs"
	}
	seen := map[string]bool{}
	out := make([]refBody, 0, len(in))
	for _, r := range in {
		r.Repository = cleanText(strings.TrimSpace(r.Repository))
		r.Commit = strings.ToLower(strings.TrimSpace(r.Commit))
		r.PreviousCommit = strings.ToLower(strings.TrimSpace(r.PreviousCommit))
		if r.Repository == "" || utf8.RuneCountInString(r.Repository) > maxRepoNameLen {
			return nil, "invalid ref repository"
		}
		if !shaRE.MatchString(r.Commit) || (r.PreviousCommit != "" && !shaRE.MatchString(r.PreviousCommit)) {
			return nil, "invalid ref commit"
		}
		if !seen[r.Repository] {
			seen[r.Repository] = true
			out = append(out, r)
		}
	}
	return out, ""
}

// replaceReleaseCommits makes commits the release's commit list in one
// transaction. Commits are upserted by (repository, sha) so releases share
// them; links, not commit rows, are replaced. A nil commits slice leaves the
// links alone, as does a nil refs slice for the refs.
func replaceReleaseCommits(ctx context.Context, pool *pgxpool.Pool, orgID, releaseID int64, commits []commitBody, setCommits bool, refs []refBody, setRefs bool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if setCommits {
		if err := writeCommits(ctx, tx, orgID, releaseID, commits); err != nil {
			return err
		}
	}
	if setRefs {
		if _, err := tx.Exec(ctx, `delete from app_release_refs where release_id=$1`, releaseID); err != nil {
			return err
		}
		for _, r := range refs {
			if _, err := tx.Exec(ctx, `insert into app_release_refs(release_id, repository, commit_sha, previous_sha) values ($1,$2,$3,nullif($4,''))`,
				releaseID, r.Repository, r.Commit, r.PreviousCommit); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func writeCommits(ctx context.Context, tx pgx.Tx, orgID, releaseID int64, commits []commitBody) error {
	if _, err := tx.Exec(ctx, `delete from app_release_commits where release_id=$1`, releaseID); err != nil {
		return err
	}
	if len(commits) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		create temporary table tmp_release_commits (repo text, sha text, message text, author_name text, author_email text, committed_at timestamptz, position int) on commit drop;
		create temporary table tmp_release_files (repo text, sha text, path text, change text) on commit drop`); err != nil {
		return err
	}
	var commitRows, fileRows [][]any
	for i, c := range commits {
		var at any
		if c.Timestamp != nil {
			at = *c.Timestamp
		}
		commitRows = append(commitRows, []any{c.Repository, c.ID, c.Message, c.AuthorName, c.AuthorEmail, at, i})
		for _, p := range c.PatchSet {
			fileRows = append(fileRows, []any{c.Repository, c.ID, p.Path, p.Type})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"tmp_release_commits"}, []string{"repo", "sha", "message", "author_name", "author_email", "committed_at", "position"}, pgx.CopyFromRows(commitRows)); err != nil {
		return err
	}
	if len(fileRows) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"tmp_release_files"}, []string{"repo", "sha", "path", "change"}, pgx.CopyFromRows(fileRows)); err != nil {
			return err
		}
	}
	steps := []string{
		`insert into app_repositories(org_id, name) select distinct $1::bigint, repo from tmp_release_commits on conflict (org_id, name) do nothing`,
		`insert into app_commits(org_id, repository_id, sha, message, author_name, author_email, committed_at)
		 select $1, r.id, t.sha, t.message, t.author_name, t.author_email, t.committed_at
		 from tmp_release_commits t join app_repositories r on r.org_id=$1 and r.name=t.repo
		 on conflict (repository_id, sha) do update set message=excluded.message, author_name=excluded.author_name,
		   author_email=excluded.author_email, committed_at=coalesce(excluded.committed_at, app_commits.committed_at)`,
		`insert into app_commit_files(commit_id, path, change)
		 select c.id, f.path, f.change
		 from tmp_release_files f join app_repositories r on r.org_id=$1 and r.name=f.repo
		 join app_commits c on c.repository_id=r.id and c.sha=f.sha
		 on conflict (commit_id, path) do update set change=excluded.change`,
		`insert into app_release_commits(release_id, commit_id, position)
		 select $2, c.id, t.position
		 from tmp_release_commits t join app_repositories r on r.org_id=$1 and r.name=t.repo
		 join app_commits c on c.repository_id=r.id and c.sha=t.sha`,
	}
	for i, q := range steps {
		args := []any{orgID}
		if i == 3 {
			args = append(args, releaseID)
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return fmt.Errorf("write commits step %d: %w", i, err)
		}
	}
	return nil
}

// commitJSON is one commit in a release summary.
type commitJSON struct {
	ID          string         `json:"id"`
	Message     string         `json:"message"`
	DateCreated any            `json:"dateCreated"`
	Repository  map[string]any `json:"repository"`
}

// releaseCommitSummary returns the commit count and newest commit (position 0).
func (h *Handler) releaseCommitSummary(ctx context.Context, releaseID int64) (count int, last *commitJSON, err error) {
	var sha, message, repo string
	var at *time.Time
	err = h.deps.Pool.QueryRow(ctx, `
		select (select count(*) from app_release_commits where release_id=$1),
		       c.sha, c.message, c.committed_at, r.name
		from app_release_commits rc
		join app_commits c on c.id=rc.commit_id
		join app_repositories r on r.id=c.repository_id
		where rc.release_id=$1
		order by rc.position limit 1`, releaseID).Scan(&count, &sha, &message, &at, &repo)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	return count, &commitJSON{ID: sha, Message: message, DateCreated: formatTime(at), Repository: map[string]any{"name": repo}}, nil
}

// listCommits serves GET /releases/{version}/commits/.
func (h *Handler) listCommits(w http.ResponseWriter, r *http.Request, c *call) {
	rel, err := h.loadRelease(r.Context(), c.orgID, r.PathValue("version"))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !h.canUseRelease(c, rel)) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	rows, err := h.deps.Pool.Query(r.Context(), `
		select c.sha, c.message, c.committed_at, c.author_name, c.author_email, r.name
		from app_release_commits rc
		join app_commits c on c.id=rc.commit_id
		join app_repositories r on r.id=c.repository_id
		where rc.release_id=$1
		order by rc.position limit $2`, rel.id, maxCommitsListed)
	if err != nil {
		serverError(w)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var sha, message, name, email, repo string
		var at *time.Time
		if err := rows.Scan(&sha, &message, &at, &name, &email, &repo); err != nil {
			serverError(w)
			return
		}
		out = append(out, map[string]any{
			"id": sha, "message": message, "dateCreated": formatTime(at),
			"author": map[string]any{"name": name, "email": email}, "repository": map[string]any{"name": repo},
		})
	}
	if rows.Err() != nil {
		serverError(w)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// previousWithCommits serves GET /releases/{version}/previous-with-commits/:
// the newest older release of the org that shares a site with this one (or is
// org-wide) and has commits. sentry-cli reads 404 as "no previous release".
func (h *Handler) previousWithCommits(w http.ResponseWriter, r *http.Request, c *call) {
	rel, err := h.loadRelease(r.Context(), c.orgID, r.PathValue("version"))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !h.canUseRelease(c, rel)) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	var restrict any // nil: no key restriction
	if c.principal.SiteIDs != nil {
		restrict = c.principal.SiteIDs
	}
	var version string
	var created time.Time
	var id int64
	err = h.deps.Pool.QueryRow(r.Context(), `
		select p.id, p.version, p.date_created
		from app_releases p
		where p.org_id=$1 and (p.date_created, p.id) < ($2, $3)
		  and (cardinality(p.site_ids)=0 or coalesce(cardinality($4::int[]),0)=0 or p.site_ids && $4::int[])
		  and ($5::int[] is null or (cardinality(p.site_ids)>0 and p.site_ids <@ $5::int[]))
		  and exists (select 1 from app_release_commits rc where rc.release_id=p.id)
		order by p.date_created desc, p.id desc limit 1`,
		c.orgID, rel.dateCreated, rel.id, rel.siteIDs, restrict).Scan(&id, &version, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w)
		return
	}
	_, last, err := h.releaseCommitSummary(r.Context(), id)
	if err != nil {
		serverError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "dateCreated": created.UTC().Format(time.RFC3339Nano), "lastCommit": last})
}

// CommitRef is one commit of a release with the paths it changed.
type CommitRef struct {
	SHA, Repository, Message, AuthorName string
	At                                   *time.Time
	Paths                                []string
}

// LoadReleaseCommits returns up to limit commits of an org's release, newest
// first, each with at most 1,000 changed paths. The author email is not part
// of the result: callers feed it to read models that may reach MCP.
func LoadReleaseCommits(ctx context.Context, pool *pgxpool.Pool, orgID int64, version string, limit int) ([]CommitRef, error) {
	if limit < 1 || limit > maxCommitsListed {
		limit = maxCommitsListed
	}
	rows, err := pool.Query(ctx, `
		select c.id, c.sha, r.name, c.message, c.author_name, c.committed_at
		from app_releases rel
		join app_release_commits rc on rc.release_id=rel.id
		join app_commits c on c.id=rc.commit_id
		join app_repositories r on r.id=c.repository_id
		where rel.org_id=$1 and rel.version=$2
		order by rc.position limit $3`, orgID, version, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CommitRef
	var ids []int64
	index := map[int64]int{}
	for rows.Next() {
		var id int64
		var c CommitRef
		if err := rows.Scan(&id, &c.SHA, &c.Repository, &c.Message, &c.AuthorName, &c.At); err != nil {
			return nil, err
		}
		index[id] = len(out)
		ids = append(ids, id)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	files, err := pool.Query(ctx, `
		select commit_id, path from (
			select commit_id, path, row_number() over (partition by commit_id order by path) as n
			from app_commit_files where commit_id = any($1)
		) f where n <= $2 order by commit_id, path`, ids, maxPathsPerCommit)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	for files.Next() {
		var id int64
		var path string
		if err := files.Scan(&id, &path); err != nil {
			return nil, err
		}
		out[index[id]].Paths = append(out[index[id]].Paths, path)
	}
	return out, files.Err()
}
