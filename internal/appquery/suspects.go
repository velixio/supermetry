package appquery

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const (
	maxSuspects      = 3
	suspectFrames    = 20
	suspectCommitCap = 1000
)

// StackFrame is the part of a stored frame the suspect match reads. It mirrors
// the JSON of appingest.Frame, which this package cannot import (appingest
// depends on appquery through appissues).
type StackFrame struct {
	Filename string `json:"filename"`
	AbsPath  string `json:"abs_path"`
	InApp    bool   `json:"in_app"`
}

// IntroducedIn names the release an issue first appeared in and when that
// release was first deployed to the site (nil when no deploy is recorded).
type IntroducedIn struct {
	Release   string     `json:"release"`
	FirstSeen time.Time  `json:"first_seen"`
	DeployAt  *time.Time `json:"deploy_at"`
}

// SuspectCommit is a commit of the introducing release that touched a file on
// the issue's stack. The author email is never part of it.
type SuspectCommit struct {
	SHA        string     `json:"sha"`
	Repository string     `json:"repository"`
	Message    string     `json:"message"`
	AuthorName string     `json:"author_name"`
	At         *time.Time `json:"at"`
	Path       string     `json:"path"`
	Score      int        `json:"score"`
}

// NormalizeFramePath turns a frame's file name into a repo-relative looking
// path: scheme prefixes, query, fragment, backslashes and leading "./" or "/"
// go away. Case is kept.
func NormalizeFramePath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	switch {
	case strings.HasPrefix(p, "app://"):
		p = p[len("app://"):]
	case strings.HasPrefix(p, "webpack://"):
		p = p[len("webpack://"):]
		if i := strings.Index(p, "/"); i >= 0 {
			p = p[i:]
		} else {
			p = ""
		}
	case strings.HasPrefix(p, "file://"):
		p = p[len("file://"):]
	case strings.HasPrefix(p, "~/"):
		p = p[2:]
	}
	for {
		switch {
		case strings.HasPrefix(p, "./"):
			p = p[2:]
		case strings.HasPrefix(p, "/"):
			p = p[1:]
		default:
			return p
		}
	}
}

func pathMatches(frame, commit string) bool {
	if frame == "" || commit == "" {
		return false
	}
	return frame == commit || strings.HasSuffix(frame, "/"+commit) || strings.HasSuffix(commit, "/"+frame)
}

// MatchSuspects scores commits by the in-app frames (top of stack first, the
// first 20) whose file they changed: a frame at index i adds 20-i. Ties go to
// the newer commit. It returns at most three commits with a positive score.
func MatchSuspects(frames []StackFrame, commits []CommitRef) []SuspectCommit {
	type cand struct {
		SuspectCommit
		best int
		pos  int
	}
	cands := make([]cand, len(commits))
	norm := make([][]string, len(commits))
	for i, c := range commits {
		cands[i] = cand{SuspectCommit: SuspectCommit{SHA: c.SHA, Repository: c.Repository, Message: c.Message, AuthorName: c.AuthorName, At: c.At}, pos: i}
		norm[i] = make([]string, len(c.Paths))
		for j, p := range c.Paths {
			norm[i][j] = NormalizeFramePath(p)
		}
	}
	for idx := 0; idx < len(frames) && idx < suspectFrames; idx++ {
		f := frames[idx]
		if !f.InApp {
			continue
		}
		name := f.Filename
		if name == "" {
			name = f.AbsPath
		}
		fp := NormalizeFramePath(name)
		for i := range cands {
			for j, cp := range norm[i] {
				if !pathMatches(fp, cp) {
					continue
				}
				w := suspectFrames - idx
				cands[i].Score += w
				if w > cands[i].best {
					cands[i].best = w
					cands[i].Path = commits[i].Paths[j]
				}
				break
			}
		}
	}
	var out []cand
	for _, c := range cands {
		if c.Score > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		x, y := out[a].At, out[b].At
		switch {
		case x != nil && y != nil && !x.Equal(*y):
			return x.After(*y)
		case x != nil && y == nil:
			return true
		case x == nil && y != nil:
			return false
		}
		return out[a].pos < out[b].pos
	})
	if len(out) > maxSuspects {
		out = out[:maxSuspects]
	}
	res := make([]SuspectCommit, len(out))
	for i, c := range out {
		res[i] = c.SuspectCommit
	}
	return res
}

// stackFrames returns the frames of a stored event body, top of stack first:
// the last exception of the chain, its newest frame first.
func stackFrames(body json.RawMessage) []StackFrame {
	var b struct {
		Exceptions []struct {
			Frames []StackFrame `json:"frames"`
		} `json:"exceptions"`
	}
	if json.Unmarshal(body, &b) != nil {
		return nil
	}
	var out []StackFrame
	for e := len(b.Exceptions) - 1; e >= 0; e-- {
		fr := b.Exceptions[e].Frames
		for i := len(fr) - 1; i >= 0; i-- {
			out = append(out, fr[i])
		}
	}
	return out
}

// introducedIn fills the release line and the suspect commits of an issue.
func (q *Querier) introducedIn(ctx context.Context, scope Scope, d *IssueDetail) error {
	d.SuspectCommits = []SuspectCommit{}
	if d.FirstRelease == "" {
		return nil
	}
	intro := &IntroducedIn{Release: d.FirstRelease, FirstSeen: d.FirstSeen}
	var at *time.Time
	err := q.pool.QueryRow(ctx, `
select min(d.at) from app_deploys d join app_releases r on r.id = d.release_id
where d.org_id = $1 and r.version = $2
  and (case when cardinality(d.site_ids) > 0 then $3 = any(d.site_ids)
            when cardinality(r.site_ids) > 0 then $3 = any(r.site_ids)
            else true end)`, scope.OrgID, d.FirstRelease, scope.SiteID).Scan(&at)
	if err != nil {
		return err
	}
	if at != nil {
		u := at.UTC()
		intro.DeployAt = &u
	}
	d.IntroducedIn = intro
	if len(d.LatestEvents) == 0 || d.LatestEvents[0].EventID == "" {
		return nil
	}
	var has bool
	if err := q.pool.QueryRow(ctx, `select exists (select 1 from app_releases r join app_release_commits rc on rc.release_id = r.id
where r.org_id = $1 and r.version = $2)`, scope.OrgID, d.FirstRelease).Scan(&has); err != nil || !has {
		return err
	}
	ev, err := q.Event(ctx, scope, d.LatestEvents[0].EventID)
	if err != nil {
		if err == ErrNotFound {
			return nil
		}
		return err
	}
	commits, err := q.loadCommits(ctx, scope.OrgID, d.FirstRelease, suspectCommitCap)
	if err != nil {
		return err
	}
	d.SuspectCommits = MatchSuspects(stackFrames(ev.Body), commits)
	return nil
}

// CommitRef is one commit of a release with the paths it changed. It matches
// sentryapi.CommitRef, which this package cannot import (import cycle through
// appingest); it carries no author email.
type CommitRef struct {
	SHA, Repository, Message, AuthorName string
	At                                   *time.Time
	Paths                                []string
}

// loadCommits returns up to limit commits of an org's release, newest first
// (payload order), each with at most 1,000 changed paths.
func (q *Querier) loadCommits(ctx context.Context, orgID int64, version string, limit int) ([]CommitRef, error) {
	rows, err := q.pool.Query(ctx, `
select c.id, c.sha, r.name, c.message, c.author_name, c.committed_at
from app_releases rel
join app_release_commits rc on rc.release_id = rel.id
join app_commits c on c.id = rc.commit_id
join app_repositories r on r.id = c.repository_id
where rel.org_id = $1 and rel.version = $2
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
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return out, err
	}
	files, err := q.pool.Query(ctx, `
select commit_id, path from (
  select commit_id, path, row_number() over (partition by commit_id order by path) as n
  from app_commit_files where commit_id = any($1)
) f where n <= 1000 order by commit_id, path`, ids)
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
