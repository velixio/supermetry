// Package appissues is the single writer of app issue workflow state:
// status, assignee, priority, snooze, archive and the GitHub link, plus the
// comments and the activity timeline. Every change records its activity rows
// in the same transaction, so the API, bulk actions, rules and the MCP server
// never write issues directly.
package appissues

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appquery"
)

var (
	// ErrInvalid reports a bad change; the error text says which part.
	ErrInvalid = errors.New("invalid change")
	// ErrInvalidAssignee reports an assignee who is not a member of the
	// site's organisation with access to the site.
	ErrInvalidAssignee = errors.New("assignee must be a member of the organisation with access to this project")
	// ErrInvalidGitHubURL reports a link that is not a github.com issue URL.
	ErrInvalidGitHubURL = fmt.Errorf("%w: github_url must look like https://github.com/<owner>/<repo>/issues/<number>", ErrInvalid)
	// ErrNotFound reports an issue or comment that does not exist for the site.
	ErrNotFound = errors.New("not found")
	// ErrForbidden reports a comment change by someone who is neither its
	// author nor an admin.
	ErrForbidden = errors.New("only the author or an admin can change this comment")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Activity kinds.
const (
	KindStatus       = "status"
	KindAssigned     = "assigned"
	KindUnassigned   = "unassigned"
	KindPriority     = "priority"
	KindSnoozed      = "snoozed"
	KindArchived     = "archived"
	KindEscalated    = "escalated"
	KindCommented    = "commented"
	KindMerged       = "merged"
	KindUnmerged     = "unmerged"
	KindGitHubLinked = "github_linked"
	KindRegressed    = "regressed"
	KindFirstSeen    = "first_seen"
)

// Service writes issue workflow state.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
	q    *appquery.Querier
}

// New returns a Service. now is the clock (time.Now in production).
func New(pool *pgxpool.Pool, now func() time.Time) *Service {
	return &Service{pool: pool, now: now, q: appquery.New(pool, now)}
}

// Actor is who made a change. UserID is an organisation user (0 with System
// for automatic changes). Admin lets the actor edit other people's comments.
type Actor struct {
	UserID int64
	System bool
	Admin  bool
}

func (a Actor) id() any {
	if a.System || a.UserID == 0 {
		return nil
	}
	return a.UserID
}

// Snooze hides an ignored issue until a time passes or it has had that many
// more events or affected users. At least one limit is required.
type Snooze struct {
	Until  *time.Time `json:"until"`
	Events *int64     `json:"events"`
	Users  *int64     `json:"users"`
}

// Change is a partial update; nil fields are left alone. Status is open,
// resolved or ignored (ResolvedInRelease only with resolved; "next" means the
// next release). Assignee 0 unassigns. Archive true archives until the issue
// escalates, false unarchives to open. GitHubURL "" unlinks.
type Change struct {
	Status            *string `json:"status"`
	ResolvedInRelease *string `json:"resolved_in_release"`
	Assignee          *int64  `json:"assignee"`
	Priority          *string `json:"priority"`
	Snooze            *Snooze `json:"snooze"`
	Archive           *bool   `json:"archive"`
	GitHubURL         *string `json:"github_url"`
	// Reason is extra data merged into the activity of an automatic status
	// change (why a snooze ended, the numbers behind an escalation). It is not
	// part of the API body.
	Reason map[string]any `json:"-"`
}

// IssueState is an issue's workflow state after a change.
type IssueState struct {
	ID                int64   `json:"id"`
	Status            string  `json:"status"`
	Previous          string  `json:"-"`
	ResolvedInRelease *string `json:"resolved_in_release"`
	ResolvedNext      bool    `json:"resolved_next"`
}

// Result lists the issues that were changed and the ids that were not found
// on the site (unknown, or belonging to another site).
type Result struct {
	Updated []int64      `json:"updated"`
	Skipped []int64      `json:"skipped"`
	Issues  []IssueState `json:"-"`
	// Fields names what changed on at least one issue (status, assignee, ...).
	Fields []string `json:"-"`
}

var githubIssueURL = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}/issues/[0-9]{1,10}$`)

const maxReleaseLen = 64

func (c Change) validate(now time.Time) error {
	n := 0
	if c.Status != nil {
		n++
		switch *c.Status {
		case "open", "resolved", "ignored":
		default:
			return invalid("status must be open, resolved or ignored")
		}
	}
	if c.ResolvedInRelease != nil {
		if c.Status == nil || *c.Status != "resolved" {
			return invalid("resolved_in_release is only allowed with status resolved")
		}
		if v := *c.ResolvedInRelease; v == "" || len(v) > maxReleaseLen {
			return invalid("resolved_in_release must be 1 to %d characters", maxReleaseLen)
		}
	}
	if c.Assignee != nil {
		n++
		if *c.Assignee < 0 {
			return invalid("assignee must be a user id, or 0 to unassign")
		}
	}
	if c.Priority != nil {
		n++
		switch *c.Priority {
		case "high", "medium", "low":
		default:
			return invalid("priority must be high, medium or low")
		}
	}
	if c.GitHubURL != nil {
		n++
		if *c.GitHubURL != "" && !githubIssueURL.MatchString(*c.GitHubURL) {
			return ErrInvalidGitHubURL
		}
	}
	if c.Archive != nil {
		n++
		if c.Status != nil || c.Snooze != nil {
			return invalid("archive cannot be combined with status or snooze")
		}
	}
	if sn := c.Snooze; sn != nil {
		n++
		if c.Status != nil {
			return invalid("snooze cannot be combined with status")
		}
		if sn.Until == nil && sn.Events == nil && sn.Users == nil {
			return invalid("snooze needs until, events or users")
		}
		if sn.Until != nil && !sn.Until.After(now) {
			return invalid("snooze until must be in the future")
		}
		if (sn.Events != nil && *sn.Events < 1) || (sn.Users != nil && *sn.Users < 1) {
			return invalid("snooze events and users must be at least 1")
		}
	}
	if n == 0 {
		return invalid("no change given")
	}
	return nil
}

// Update applies ch to the issues of the site in one transaction, writing one
// activity row per issue per changed field. Ids that are not issues of the
// site are reported in Result.Skipped and left alone.
func (s *Service) Update(ctx context.Context, siteID int32, issueIDs []int64, actor Actor, ch Change) (Result, error) {
	var res Result
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		res, err = s.UpdateTx(ctx, tx, siteID, issueIDs, actor, ch)
		return err
	})
	return res, err
}

type issueRow struct {
	id           int64
	status       string
	assignee     *int64
	priority     string
	github       *string
	resolvedIn   *string
	resolvedNext bool
	events       int64
}

// UpdateTx is Update inside the caller's transaction, so the caller can write
// its own audit row atomically.
func (s *Service) UpdateTx(ctx context.Context, tx pgx.Tx, siteID int32, issueIDs []int64, actor Actor, ch Change) (Result, error) {
	res := Result{Updated: []int64{}, Skipped: []int64{}}
	if len(issueIDs) == 0 {
		return res, invalid("no issues given")
	}
	now := s.now()
	if err := ch.validate(now); err != nil {
		return res, err
	}
	if ch.Assignee != nil && *ch.Assignee > 0 {
		var ok bool
		err := tx.QueryRow(ctx, `select exists (select 1 from memberships m join sites s on s.org_id = m.org_id
where s.id = $1 and m.user_id = $2 and (m.site_ids is null or $1 = any(m.site_ids)))`, siteID, *ch.Assignee).Scan(&ok)
		if err != nil {
			return res, fmt.Errorf("check assignee: %w", err)
		}
		if !ok {
			return res, ErrInvalidAssignee
		}
	}
	rows, err := tx.Query(ctx, `select id, status, assignee_user_id, priority, github_url, resolved_in_release, resolved_next, event_count
from app_issues where site_id = $1 and id = any($2) order by id for update`, siteID, issueIDs)
	if err != nil {
		return res, fmt.Errorf("lock issues: %w", err)
	}
	found := map[int64]issueRow{}
	var order []int64
	for rows.Next() {
		var r issueRow
		if err := rows.Scan(&r.id, &r.status, &r.assignee, &r.priority, &r.github, &r.resolvedIn, &r.resolvedNext, &r.events); err != nil {
			rows.Close()
			return res, err
		}
		found[r.id] = r
		order = append(order, r.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	seen := map[int64]bool{}
	for _, id := range issueIDs {
		if _, ok := found[id]; !ok && !seen[id] {
			res.Skipped = append(res.Skipped, id)
		}
		seen[id] = true
	}
	var retention int
	if ch.Status != nil && *ch.Status == "resolved" && ch.ResolvedInRelease != nil && *ch.ResolvedInRelease == appquery.NextRelease {
		if err := tx.QueryRow(ctx, `select retention_class from sites where id = $1`, siteID).Scan(&retention); err != nil {
			return res, fmt.Errorf("read retention: %w", err)
		}
	}
	fields := map[string]bool{}
	for _, id := range order {
		changed, state, err := s.applyOne(ctx, tx, siteID, found[id], actor, ch, now, retention)
		if err != nil {
			return res, err
		}
		res.Updated = append(res.Updated, id)
		res.Issues = append(res.Issues, state)
		for _, f := range changed {
			fields[f] = true
		}
	}
	for _, f := range []string{"status", "assignee", "priority", "snooze", "archive", "github_url"} {
		if fields[f] {
			res.Fields = append(res.Fields, f)
		}
	}
	return res, nil
}

func (s *Service) applyOne(ctx context.Context, tx pgx.Tx, siteID int32, cur issueRow, actor Actor, ch Change, now time.Time, retention int) ([]string, IssueState, error) {
	state := IssueState{ID: cur.id, Status: cur.status, Previous: cur.status, ResolvedInRelease: cur.resolvedIn, ResolvedNext: cur.resolvedNext}
	var changed []string
	var sets []string
	var args []any
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)+1)) // $1 is the issue id
	}
	clearSnooze := func() {
		sets = append(sets, "snooze_until = null, snooze_events = null, snooze_users = null, snooze_base_events = null, snooze_base_users = null")
	}
	type act struct {
		kind string
		data map[string]any
	}
	var acts []act

	if ch.Status != nil {
		in := appquery.StatusInput{Status: *ch.Status, ResolvedInRelease: ch.ResolvedInRelease}
		r, err := s.q.SetStatus(ctx, tx, appquery.Scope{SiteID: siteID, RetentionDays: retention}, cur.id, in)
		if err != nil {
			if errors.Is(err, appquery.ErrInvalid) {
				return nil, state, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
			return nil, state, err
		}
		state.Status, state.ResolvedInRelease, state.ResolvedNext = r.Status, r.ResolvedInRelease, r.ResolvedNext
		if r.Status != cur.status || !sameString(r.ResolvedInRelease, cur.resolvedIn) || r.ResolvedNext != cur.resolvedNext {
			data := map[string]any{"from": cur.status, "to": r.Status}
			if r.ResolvedInRelease != nil {
				data["resolved_in_release"] = *r.ResolvedInRelease
			}
			if r.ResolvedNext {
				data["resolved_next"] = true
			}
			for k, v := range ch.Reason {
				data[k] = v
			}
			acts = append(acts, act{KindStatus, data})
			changed = append(changed, "status")
		}
		if r.Status != cur.status {
			// Leaving ignored or archived ends the snooze and the archive.
			clearSnooze()
			sets = append(sets, "archived_until_escalating = false")
		}
	}
	if ch.Archive != nil {
		switch {
		case *ch.Archive && (cur.status != "archived"):
			set("status", "archived")
			sets = append(sets, "archived_until_escalating = true")
			clearSnooze()
			state.Status = "archived"
			acts = append(acts, act{KindArchived, map[string]any{"archived": true, "from": cur.status}})
			changed = append(changed, "archive")
		case !*ch.Archive && cur.status == "archived":
			set("status", "open")
			sets = append(sets, "archived_until_escalating = false")
			state.Status = "open"
			if actor.System {
				// Unarchiving by the system is an escalation.
				data := map[string]any{"from": "archived"}
				for k, v := range ch.Reason {
					data[k] = v
				}
				acts = append(acts, act{KindEscalated, data})
			} else {
				acts = append(acts, act{KindArchived, map[string]any{"archived": false}})
			}
			changed = append(changed, "archive")
		}
	}
	if sn := ch.Snooze; sn != nil {
		var baseUsers any
		if sn.Users != nil {
			var n int64
			if err := tx.QueryRow(ctx, `select count(distinct user_hash) from app_events where site_id = $1 and issue_id = $2 and user_hash is not null`, siteID, cur.id).Scan(&n); err != nil {
				return nil, state, fmt.Errorf("count issue users: %w", err)
			}
			baseUsers = n
		}
		set("status", "ignored")
		set("snooze_until", sn.Until)
		set("snooze_events", sn.Events)
		set("snooze_users", sn.Users)
		set("snooze_base_events", cur.events)
		set("snooze_base_users", baseUsers)
		sets = append(sets, "archived_until_escalating = false")
		state.Status = "ignored"
		data := map[string]any{}
		if sn.Until != nil {
			data["until"] = sn.Until.UTC().Format(time.RFC3339)
		}
		if sn.Events != nil {
			data["events"] = *sn.Events
		}
		if sn.Users != nil {
			data["users"] = *sn.Users
		}
		acts = append(acts, act{KindSnoozed, data})
		changed = append(changed, "snooze")
	}
	if ch.Assignee != nil {
		switch id := *ch.Assignee; {
		case id == 0 && cur.assignee != nil:
			set("assignee_user_id", nil)
			acts = append(acts, act{KindUnassigned, map[string]any{"from": *cur.assignee}})
			changed = append(changed, "assignee")
		case id > 0 && (cur.assignee == nil || *cur.assignee != id):
			set("assignee_user_id", id)
			data := map[string]any{"assignee": id}
			if cur.assignee != nil {
				data["from"] = *cur.assignee
			}
			acts = append(acts, act{KindAssigned, data})
			changed = append(changed, "assignee")
		}
	}
	if ch.Priority != nil && *ch.Priority != cur.priority {
		set("priority", *ch.Priority)
		acts = append(acts, act{KindPriority, map[string]any{"from": cur.priority, "to": *ch.Priority}})
		changed = append(changed, "priority")
	}
	if ch.GitHubURL != nil {
		want := *ch.GitHubURL
		have := ""
		if cur.github != nil {
			have = *cur.github
		}
		if want != have {
			if want == "" {
				set("github_url", nil)
			} else {
				set("github_url", want)
			}
			data := map[string]any{"url": nilIfEmpty(want)}
			if have != "" {
				data["from"] = have
			}
			acts = append(acts, act{KindGitHubLinked, data})
			changed = append(changed, "github_url")
		}
	}
	if len(sets) > 0 {
		if _, err := tx.Exec(ctx, `update app_issues set `+strings.Join(sets, ", ")+` where id = $1`, append([]any{cur.id}, args...)...); err != nil {
			return nil, state, fmt.Errorf("update issue: %w", err)
		}
	}
	// Rows of one change get distinct, increasing timestamps so the timeline
	// pages by time without dropping ties.
	for i, a := range acts {
		if err := record(ctx, tx, siteID, cur.id, now.Add(time.Duration(i)*time.Microsecond), actor.id(), a.kind, a.data); err != nil {
			return nil, state, err
		}
	}
	return changed, state, nil
}

func sameString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
