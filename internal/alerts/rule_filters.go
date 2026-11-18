package alerts

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Filter semantics: a list is OR within its field, fields are AND-ed, and an
// empty list means any. Tags match a key and value, a negated tag matches
// issues or events that do not carry that value. Owner{UserID: 0} is
// "unassigned". Releases may name "latest", resolved to the newest release
// before the filters reach SQL.

// sqlArgs numbers positional parameters while a query is assembled.
type sqlArgs struct{ values []any }

func (a *sqlArgs) add(v any) string {
	a.values = append(a.values, v)
	return "$" + strconv.Itoa(len(a.values))
}

// eventFilterSQL returns " and ..." conditions on app_events e joined to
// app_issues i. Tags match the event's own tags document.
func eventFilterSQL(a *sqlArgs, f Filters) string {
	var b strings.Builder
	list := func(column string, values []string) {
		if len(values) > 0 {
			b.WriteString(" and " + column + " = any(" + a.add(values) + "::text[])")
		}
	}
	list("e.environment", f.Environments)
	list("e.release", f.Releases)
	list("e.platform", f.Platforms)
	list("e.level", f.Levels)
	list("i.kind", f.Kinds)
	for _, t := range f.Tags {
		key, value := a.add(t.Key), a.add(t.Value)
		if t.Negate {
			b.WriteString(" and (e.tags->>" + key + ") is distinct from " + value)
		} else {
			b.WriteString(" and (e.tags->>" + key + ") = " + value)
		}
	}
	b.WriteString(ownerSQL(a, f))
	return b.String()
}

// issueFilterSQL returns " and ..." conditions on app_issues i. Releases,
// environments and platforms match the issue's tag rollup (releases also its
// first and last release); tags match the rollup.
func issueFilterSQL(a *sqlArgs, f Filters) string {
	var b strings.Builder
	tagIn := func(key string, values []string) {
		if len(values) > 0 {
			b.WriteString(" and exists (select 1 from app_issue_tags t where t.issue_id = i.id and t.key = '" + key + "' and t.value = any(" + a.add(values) + "::text[]))")
		}
	}
	tagIn("environment", f.Environments)
	tagIn("platform", f.Platforms)
	if len(f.Releases) > 0 {
		p := a.add(f.Releases)
		b.WriteString(" and (i.last_release = any(" + p + "::text[]) or i.first_release = any(" + p + "::text[]) or exists (select 1 from app_issue_tags t where t.issue_id = i.id and t.key = 'release' and t.value = any(" + p + "::text[])))")
	}
	if len(f.Levels) > 0 {
		b.WriteString(" and i.level = any(" + a.add(f.Levels) + "::text[])")
	}
	if len(f.Kinds) > 0 {
		b.WriteString(" and i.kind = any(" + a.add(f.Kinds) + "::text[])")
	}
	for _, t := range f.Tags {
		key, value := a.add(t.Key), a.add(t.Value)
		exists := "exists (select 1 from app_issue_tags t where t.issue_id = i.id and t.key = " + key + " and t.value = " + value + ")"
		if t.Negate {
			b.WriteString(" and not " + exists)
		} else {
			b.WriteString(" and " + exists)
		}
	}
	b.WriteString(ownerSQL(a, f))
	return b.String()
}

func ownerSQL(a *sqlArgs, f Filters) string {
	switch {
	case f.Owner == nil:
		return ""
	case f.Owner.UserID == 0:
		return " and i.assignee_user_id is null"
	default:
		return " and i.assignee_user_id = " + a.add(f.Owner.UserID)
	}
}

// hasFilters reports whether any filter narrows the candidates.
func (f Filters) hasFilters() bool {
	return len(f.Environments) > 0 || len(f.Releases) > 0 || len(f.Platforms) > 0 || len(f.Levels) > 0 ||
		len(f.Kinds) > 0 || len(f.Tags) > 0 || f.Owner != nil
}

// resolveFilters replaces "latest" in the release filter with the newest
// release. ok is false when the filter names only "latest" and no release
// exists yet, so nothing can match.
func (e *RuleEvaluator) resolveFilters(ctx context.Context, siteID int32, f Filters, now time.Time) (Filters, bool, error) {
	wantsLatest := false
	var releases []string
	for _, r := range f.Releases {
		if r == latestRelease {
			wantsLatest = true
			continue
		}
		releases = append(releases, r)
	}
	if !wantsLatest {
		return f, true, nil
	}
	release, err := e.app.LatestRelease(ctx, siteID, now.AddDate(0, 0, -30), 1)
	if err != nil {
		return f, false, err
	}
	if release != nil {
		releases = append(releases, release.Release)
	}
	if len(releases) == 0 {
		return f, false, nil
	}
	f.Releases = releases
	return f, true, nil
}

// filterIssues keeps the candidates whose issue matches the filters. Without
// filters it returns them untouched and asks the source nothing.
func (e *RuleEvaluator) filterIssues(ctx context.Context, siteID int32, f Filters, candidates []Candidate) ([]Candidate, error) {
	if !f.hasFilters() || len(candidates) == 0 {
		return candidates, nil
	}
	ids := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.IssueID)
	}
	match, err := e.app.MatchIssue(ctx, siteID, ids, f)
	if err != nil {
		return nil, err
	}
	out := candidates[:0:0]
	for _, c := range candidates {
		if match[c.IssueID] {
			out = append(out, c)
		}
	}
	return out, nil
}
