package appquery

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"supermetry/internal/appquery/search"
)

// IssueQuery is the issue list request.
type IssueQuery struct {
	Filter
	Status, Kind, Q, Sort string
	Limit, Offset         int
	// Search is a parsed query-bar search, ANDed with the other fields. Me is
	// the signed-in user's id (0 when there is none, as for API keys) and
	// LookupMember resolves a member email for assigned:<email>.
	Search       *search.Query
	Me           int64
	LookupMember func(email string) (int64, bool)
}

// searchWhere compiles the search into a condition whose placeholders start
// after base.
func (in IssueQuery) searchWhere(now time.Time, base int) (string, []any, error) {
	if in.Search == nil {
		return "true", nil, nil
	}
	where, args, err := search.Compile(*in.Search, now, in.Me, in.LookupMember, base)
	if err != nil {
		return "", nil, invalid("%s", err.Error())
	}
	return where, args, nil
}

// IssueRow is one issue of the list. Events and Users count the range (and the
// release, environment and platform filters); the sparkline is the issue's
// daily events over the range and ignores those filters.
type IssueRow struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	Kind         string    `json:"kind"`
	ErrorType    string    `json:"error_type"`
	Status       string    `json:"status"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	FirstRelease string    `json:"first_release"`
	LastRelease  string    `json:"last_release"`
	Events       int64     `json:"events"`
	Users        int64     `json:"users"`
	Sparkline    []int64   `json:"sparkline"`
	// Workflow state (appissues).
	AssigneeUserID *int64 `json:"assignee_user_id"`
	AssigneeName   string `json:"assignee_name"`
	Priority       string `json:"priority"`
}

// IssueList is one page of issues.
type IssueList struct {
	Issues     []IssueRow `json:"issues"`
	Total      int        `json:"total"`
	NextOffset *int       `json:"next_offset"`
}

const (
	defaultLimit = 25
	maxLimit     = 100
)

var issueStatuses = map[string]bool{"open": true, "resolved": true, "ignored": true, "regressed": true, "archived": true}

// likeEscaper makes a user string a literal LIKE pattern body.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Issues lists the issues that had events in the range. q matches the start of
// the title or of the error type, case-insensitively.
func (q *Querier) Issues(ctx context.Context, scope Scope, in IssueQuery) (IssueList, error) {
	out := IssueList{Issues: []IssueRow{}}
	w, err := q.window(scope, in.Filter)
	if err != nil {
		return out, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 0 || limit > maxLimit {
		return out, invalid("limit must be between 1 and %d", maxLimit)
	}
	if in.Offset < 0 {
		return out, invalid("offset must not be negative")
	}
	if in.Status == "all" {
		in.Status = "" // "all" means no status filter
	}
	if in.Status != "" && !issueStatuses[in.Status] {
		return out, invalid("status must be open, resolved, ignored, regressed or archived")
	}
	var order string
	switch in.Sort {
	case "", "last_seen":
		order = "i.last_seen desc, i.id desc"
	case "events":
		order = "a.events desc, i.last_seen desc, i.id desc"
	case "users":
		order = "a.users desc, i.last_seen desc, i.id desc"
	default:
		return out, invalid("sort must be last_seen, events or users")
	}
	prefix := likeEscaper.Replace(in.Q) + "%"
	args := append(w.evArgs(), in.Status, in.Kind, in.Q, prefix, limit, in.Offset)
	searchSQL, searchArgs, err := in.searchWhere(q.now(), len(args))
	if err != nil {
		return out, err
	}
	args = append(args, searchArgs...)
	rows, err := q.pool.Query(ctx, `
with agg as (
  select issue_id, sum(occurrences) as events, count(distinct install_hash) as users,
         max(exit_reason) filter (where exit_reason in ('oom', 'watchdog_termination')) as term
  from app_events where `+eventWhere+` and issue_id is not null and kind <> 'session'
  group by issue_id
)
select i.id, i.title, `+displayKind+`, i.error_type, i.status, i.first_seen, i.last_seen, i.first_release, i.last_release,
       a.events, a.users, i.assignee_user_id, coalesce(u.name, ''), i.priority, count(*) over ()
from app_issues i join agg a on a.issue_id = i.id left join users u on u.id = i.assignee_user_id
where i.site_id = $1 and i.merged_into is null and ($7 = '' or i.status = $7) and ($8 = '' or i.kind = $8 or `+termKind+` = $8)
  and ($9 = '' or i.title ilike $10 escape '\' or i.error_type ilike $10 escape '\')
  and (`+searchSQL+`)
order by `+order+`
limit $11 offset $12`, args...)
	if err != nil {
		return out, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var r IssueRow
		var total int
		if err := rows.Scan(&r.ID, &r.Title, &r.Kind, &r.ErrorType, &r.Status, &r.FirstSeen, &r.LastSeen, &r.FirstRelease, &r.LastRelease, &r.Events, &r.Users, &r.AssigneeUserID, &r.AssigneeName, &r.Priority, &total); err != nil {
			return out, err
		}
		out.Total = total
		out.Issues = append(out.Issues, r)
		ids = append(ids, r.ID)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if next := in.Offset + len(out.Issues); len(out.Issues) == limit && next < out.Total {
		out.NextOffset = &next
	}
	if len(ids) == 0 {
		return out, nil
	}
	spark, err := q.sparklines(ctx, w, ids)
	if err != nil {
		return out, err
	}
	for i := range out.Issues {
		out.Issues[i].Sparkline = spark[out.Issues[i].ID]
	}
	return out, nil
}

// sparklines reads the daily events of the given issues from app_issue_daily,
// zero-filled to the range's days.
func (q *Querier) sparklines(ctx context.Context, w window, ids []int64) (map[int64][]int64, error) {
	rows, err := q.pool.Query(ctx, `
select issue_id, day, events from app_issue_daily
where site_id = $1 and issue_id = any($2) and day >= $3 and day <= $4 and day >= $5`,
		w.scope.SiteID, ids, w.fromDay, w.toDay, w.cutoffDay)
	if err != nil {
		return nil, fmt.Errorf("read sparklines: %w", err)
	}
	defer rows.Close()
	index := make(map[string]int, len(w.days))
	for i, d := range w.days {
		index[d] = i
	}
	out := make(map[int64][]int64, len(ids))
	for _, id := range ids {
		out[id] = make([]int64, len(w.days))
	}
	for rows.Next() {
		var id, events int64
		var day time.Time
		if err := rows.Scan(&id, &day, &events); err != nil {
			return nil, err
		}
		if i, ok := index[day.Format(time.DateOnly)]; ok {
			out[id][i] = events
		}
	}
	return out, rows.Err()
}

// IssueDay is one day of an issue's series (from app_issue_daily).
type IssueDay struct {
	Day      string `json:"day"`
	Events   int64  `json:"events"`
	Installs int64  `json:"installs"`
}

// Breakdown is one value of a dimension with its events and distinct users.
type Breakdown struct {
	Value  string `json:"value"`
	Events int64  `json:"events"`
	Users  int64  `json:"users"`
}

// EventSample is a recent event of an issue. It carries no install or session
// identifier, hashed or not.
type EventSample struct {
	EventID     string         `json:"event_id,omitempty"`
	TS          time.Time      `json:"ts"`
	Release     string         `json:"release"`
	Platform    string         `json:"platform"`
	OSVersion   string         `json:"os_version"`
	Arch        string         `json:"arch"`
	DeviceClass string         `json:"device_class"`
	Runtime     string         `json:"runtime"`
	ExitReason  string         `json:"exit_reason"`
	Signal      string         `json:"signal"`
	ExitCode    *int32         `json:"exit_code"`
	Frames      string         `json:"frames"`
	Tags        map[string]any `json:"tags"`
}

// IssueDetail is an issue with its series, breakdowns and latest events.
// Breakdowns, latest events and the per-value counts follow the range and the
// filters; the series ignores the release, environment and platform filters.
type IssueDetail struct {
	ID                int64     `json:"id"`
	Title             string    `json:"title"`
	Kind              string    `json:"kind"`
	ErrorType         string    `json:"error_type"`
	Status            string    `json:"status"`
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
	FirstRelease      string    `json:"first_release"`
	LastRelease       string    `json:"last_release"`
	ResolvedInRelease *string   `json:"resolved_in_release"`
	ResolvedNext      bool      `json:"resolved_next"`
	EventCount        int64     `json:"event_count"`
	// Workflow state (appissues).
	AssigneeUserID          *int64                 `json:"assignee_user_id"`
	AssigneeName            string                 `json:"assignee_name"`
	Priority                string                 `json:"priority"`
	SnoozeUntil             *time.Time             `json:"snooze_until"`
	SnoozeEvents            *int64                 `json:"snooze_events"`
	SnoozeUsers             *int64                 `json:"snooze_users"`
	ArchivedUntilEscalating bool                   `json:"archived_until_escalating"`
	GitHubURL               *string                `json:"github_url"`
	MergedInto              *int64                 `json:"merged_into"`
	Series                  []IssueDay             `json:"series"`
	Breakdowns              map[string][]Breakdown `json:"breakdowns"`
	LatestEvents            []EventSample          `json:"latest_events"`
	IntroducedIn            *IntroducedIn          `json:"introduced_in"`
	SuspectCommits          []SuspectCommit        `json:"suspect_commits"`
}

// breakdownDims maps a breakdown name to its fixed column.
var breakdownDims = []string{"release", "platform", "os_version", "arch", "device_class", "environment"}

const (
	breakdownTop = 10
	maxTagKeys   = 50
	latestEvents = 5
)

// Issue returns one issue of the site, or ErrNotFound.
func (q *Querier) Issue(ctx context.Context, scope Scope, id int64, f Filter) (IssueDetail, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return IssueDetail{}, err
	}
	var d IssueDetail
	err = q.pool.QueryRow(ctx, `
select i.id, i.title, i.kind, i.error_type, i.status, i.first_seen, i.last_seen, i.first_release, i.last_release, i.resolved_in_release, i.resolved_next, i.event_count,
       i.assignee_user_id, coalesce(u.name, ''), i.priority, i.snooze_until, i.snooze_events, i.snooze_users, i.archived_until_escalating, i.github_url, i.merged_into
from app_issues i left join users u on u.id = i.assignee_user_id where i.id = $1 and i.site_id = $2`, id, scope.SiteID).
		Scan(&d.ID, &d.Title, &d.Kind, &d.ErrorType, &d.Status, &d.FirstSeen, &d.LastSeen, &d.FirstRelease, &d.LastRelease, &d.ResolvedInRelease, &d.ResolvedNext, &d.EventCount,
			&d.AssigneeUserID, &d.AssigneeName, &d.Priority, &d.SnoozeUntil, &d.SnoozeEvents, &d.SnoozeUsers, &d.ArchivedUntilEscalating, &d.GitHubURL, &d.MergedInto)
	if err != nil {
		if isNoRows(err) {
			return IssueDetail{}, ErrNotFound
		}
		return IssueDetail{}, fmt.Errorf("read issue: %w", err)
	}
	if d.Series, err = q.issueSeries(ctx, w, id); err != nil {
		return d, err
	}
	d.Breakdowns = map[string][]Breakdown{}
	args := append(w.evArgs(), id)
	for _, dim := range breakdownDims {
		rows, err := q.pool.Query(ctx, `select `+dim+`, sum(occurrences), count(distinct install_hash) from app_events
where `+eventWhere+` and issue_id = $7 and `+dim+` <> '' group by 1 order by 2 desc, 1 limit `+fmt.Sprint(breakdownTop), args...)
		if err != nil {
			return d, fmt.Errorf("read %s breakdown: %w", dim, err)
		}
		list := []Breakdown{}
		for rows.Next() {
			var b Breakdown
			if err := rows.Scan(&b.Value, &b.Events, &b.Users); err != nil {
				rows.Close()
				return d, err
			}
			list = append(list, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return d, err
		}
		d.Breakdowns[dim] = list
	}
	if err := q.tagBreakdowns(ctx, args, d.Breakdowns); err != nil {
		return d, err
	}
	if d.LatestEvents, err = q.latest(ctx, args); err != nil {
		return d, err
	}
	return d, q.introducedIn(ctx, scope, &d)
}

func (q *Querier) issueSeries(ctx context.Context, w window, id int64) ([]IssueDay, error) {
	rows, err := q.pool.Query(ctx, `select day, events, installs from app_issue_daily
where site_id = $1 and issue_id = $2 and day >= $3 and day <= $4 and day >= $5`, w.scope.SiteID, id, w.fromDay, w.toDay, w.cutoffDay)
	if err != nil {
		return nil, fmt.Errorf("read issue series: %w", err)
	}
	defer rows.Close()
	by := map[string]IssueDay{}
	for rows.Next() {
		var day time.Time
		var p IssueDay
		if err := rows.Scan(&day, &p.Events, &p.Installs); err != nil {
			return nil, err
		}
		p.Day = day.Format(time.DateOnly)
		by[p.Day] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	series := make([]IssueDay, 0, len(w.days))
	for _, day := range w.days {
		p, ok := by[day]
		if !ok {
			p = IssueDay{Day: day}
		}
		series = append(series, p)
	}
	return series, nil
}

// tagBreakdowns adds a "tag:<key>" entry per tag key, top values each.
func (q *Querier) tagBreakdowns(ctx context.Context, args []any, into map[string][]Breakdown) error {
	rows, err := q.pool.Query(ctx, `
select k, v, events, users from (
  select t.key as k, t.value as v, sum(e.occurrences) as events, count(distinct e.install_hash) as users,
         row_number() over (partition by t.key order by sum(e.occurrences) desc, t.value) as rn
  from app_events e, jsonb_each_text(e.tags) t
  where e.site_id = $1 and e.ts >= $2 and e.ts < $3 and ($4 = '' or e.release = $4) and ($5 = '' or e.environment = $5)
    and ($6 = '' or e.platform = $6) and e.issue_id = $7
  group by t.key, t.value
) ranked where rn <= `+fmt.Sprint(breakdownTop)+` order by k, events desc, v`, args...)
	if err != nil {
		return fmt.Errorf("read tag breakdowns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var b Breakdown
		if err := rows.Scan(&k, &b.Value, &b.Events, &b.Users); err != nil {
			return err
		}
		name := "tag:" + k
		if _, ok := into[name]; !ok && countTags(into) >= maxTagKeys {
			continue
		}
		into[name] = append(into[name], b)
	}
	return rows.Err()
}

func countTags(m map[string][]Breakdown) int {
	n := 0
	for k := range m {
		if strings.HasPrefix(k, "tag:") {
			n++
		}
	}
	return n
}

func (q *Querier) latest(ctx context.Context, args []any) ([]EventSample, error) {
	rows, err := q.pool.Query(ctx, `
select event_id, ts, release, platform, os_version, arch, device_class, runtime, exit_reason, signal, exit_code, frames, tags
from app_events where `+eventWhere+` and issue_id = $7 order by ts desc, event_id limit `+fmt.Sprint(latestEvents), args...)
	if err != nil {
		return nil, fmt.Errorf("read latest events: %w", err)
	}
	defer rows.Close()
	out := []EventSample{}
	for rows.Next() {
		var e EventSample
		if err := rows.Scan(&e.EventID, &e.TS, &e.Release, &e.Platform, &e.OSVersion, &e.Arch, &e.DeviceClass, &e.Runtime, &e.ExitReason, &e.Signal, &e.ExitCode, &e.Frames, &e.Tags); err != nil {
			return nil, err
		}
		if e.Tags == nil {
			e.Tags = map[string]any{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TopIssue is an issue ranked within a compatibility row.
type TopIssue struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Events int64  `json:"events"`
}

// CompatRow is one platform, OS, architecture and device class combination.
type CompatRow struct {
	Platform       string     `json:"platform"`
	OSVersion      string     `json:"os_version"`
	Arch           string     `json:"arch"`
	DeviceClass    string     `json:"device_class"`
	Events         int64      `json:"events"`
	Crashes        int64      `json:"crashes"`
	Installs       int64      `json:"installs"`
	CrashFreeUsers *float64   `json:"crash_free_users"`
	TopIssues      []TopIssue `json:"top_issues"`
}

const maxCompatRows = 200

// Compatibility returns the combinations seen in the range, most crashes
// first, each with its three biggest issues.
func (q *Querier) Compatibility(ctx context.Context, scope Scope, f Filter) ([]CompatRow, error) {
	w, err := q.window(scope, f)
	if err != nil {
		return nil, err
	}
	groups, err := q.healthBy(ctx, w, "", "platform", "os_version", "arch", "device_class")
	if err != nil {
		return nil, err
	}
	rows := make([]CompatRow, 0, len(groups))
	index := map[[4]string]int{}
	for _, g := range groups {
		key := [4]string{g.keys[0], g.keys[1], g.keys[2], g.keys[3]}
		index[key] = len(rows)
		rows = append(rows, CompatRow{
			Platform: g.keys[0], OSVersion: g.keys[1], Arch: g.keys[2], DeviceClass: g.keys[3],
			Events: g.Events, Crashes: g.Crashes, Installs: g.Installs, CrashFreeUsers: g.crashFreeUsers(), TopIssues: []TopIssue{},
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Crashes != b.Crashes {
			return a.Crashes > b.Crashes
		}
		if a.Events != b.Events {
			return a.Events > b.Events
		}
		return a.Platform+"\x00"+a.OSVersion+"\x00"+a.Arch+"\x00"+a.DeviceClass < b.Platform+"\x00"+b.OSVersion+"\x00"+b.Arch+"\x00"+b.DeviceClass
	})
	if len(rows) > maxCompatRows {
		rows = rows[:maxCompatRows]
	}
	for i := range rows {
		index[[4]string{rows[i].Platform, rows[i].OSVersion, rows[i].Arch, rows[i].DeviceClass}] = i
	}
	top, err := q.pool.Query(ctx, `
select platform, os_version, arch, device_class, issue_id, title, events from (
  select e.platform, e.os_version, e.arch, e.device_class, e.issue_id, sum(e.occurrences) as events,
         row_number() over (partition by e.platform, e.os_version, e.arch, e.device_class order by sum(e.occurrences) desc, e.issue_id) as rn
  from app_events e
  where e.site_id = $1 and e.ts >= $2 and e.ts < $3 and ($4 = '' or e.release = $4) and ($5 = '' or e.environment = $5)
    and ($6 = '' or e.platform = $6) and e.issue_id is not null and e.kind <> 'session'
  group by e.platform, e.os_version, e.arch, e.device_class, e.issue_id
) t join app_issues i on i.id = t.issue_id
where rn <= 3 order by platform, os_version, arch, device_class, rn`, w.evArgs()...)
	if err != nil {
		return nil, fmt.Errorf("read compatibility issues: %w", err)
	}
	defer top.Close()
	for top.Next() {
		var key [4]string
		var ti TopIssue
		if err := top.Scan(&key[0], &key[1], &key[2], &key[3], &ti.ID, &ti.Title, &ti.Events); err != nil {
			return nil, err
		}
		if i, ok := index[key]; ok {
			rows[i].TopIssues = append(rows[i].TopIssues, ti)
		}
	}
	return rows, top.Err()
}

// MatchIssues returns the ids of the issues the request matches, up to max+1
// of them (so a caller can tell the match is larger than max). It applies the
// same filters as Issues but not paging or sort.
func (q *Querier) MatchIssues(ctx context.Context, scope Scope, in IssueQuery, max int) ([]int64, error) {
	w, err := q.window(scope, in.Filter)
	if err != nil {
		return nil, err
	}
	if in.Status == "all" {
		in.Status = ""
	}
	if in.Status != "" && !issueStatuses[in.Status] {
		return nil, invalid("status must be open, resolved, ignored, regressed or archived")
	}
	prefix := likeEscaper.Replace(in.Q) + "%"
	args := append(w.evArgs(), in.Status, in.Kind, in.Q, prefix, max+1)
	searchSQL, searchArgs, err := in.searchWhere(q.now(), len(args))
	if err != nil {
		return nil, err
	}
	args = append(args, searchArgs...)
	rows, err := q.pool.Query(ctx, `
with agg as (
  select issue_id, sum(occurrences) as events, count(distinct install_hash) as users,
         max(exit_reason) filter (where exit_reason in ('oom', 'watchdog_termination')) as term
  from app_events where `+eventWhere+` and issue_id is not null and kind <> 'session'
  group by issue_id
)
select i.id from app_issues i join agg a on a.issue_id = i.id
where i.site_id = $1 and ($7 = '' or i.status = $7) and ($8 = '' or i.kind = $8 or `+termKind+` = $8)
  and ($9 = '' or i.title ilike $10 escape '\' or i.error_type ilike $10 escape '\')
  and (`+searchSQL+`)
order by i.id
limit $11`, args...)
	if err != nil {
		return nil, fmt.Errorf("match issues: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EventHit is one event found by SearchEvents: identifiers and dimensions
// only, no event body, install, session or user identifier.
type EventHit struct {
	EventID  string    `json:"event_id"`
	TS       time.Time `json:"ts"`
	IssueID  int64     `json:"issue_id"`
	Release  string    `json:"release"`
	Platform string    `json:"platform"`
}

// SearchEvents returns the newest events of the issues the search matches.
func (q *Querier) SearchEvents(ctx context.Context, scope Scope, in IssueQuery, limit int) ([]EventHit, error) {
	w, err := q.window(scope, in.Filter)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 50 {
		return nil, invalid("limit must be between 1 and 50")
	}
	args := append(w.evArgs(), limit)
	searchSQL, searchArgs, err := in.searchWhere(q.now(), len(args))
	if err != nil {
		return nil, err
	}
	args = append(args, searchArgs...)
	rows, err := q.pool.Query(ctx, `
with agg as (
  select issue_id, sum(occurrences) as events, count(distinct install_hash) as users,
         max(exit_reason) filter (where exit_reason in ('oom', 'watchdog_termination')) as term
  from app_events where `+eventWhere+` and issue_id is not null and kind <> 'session'
  group by issue_id
), hit as (
  select i.id from app_issues i join agg a on a.issue_id = i.id where i.site_id = $1 and (`+searchSQL+`)
)
select event_id, ts, issue_id, release, platform from app_events
where `+eventWhere+` and kind <> 'session' and issue_id in (select id from hit) and event_id <> ''
order by ts desc, event_id limit $7`, args...)
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}
	defer rows.Close()
	out := []EventHit{}
	for rows.Next() {
		var h EventHit
		if err := rows.Scan(&h.EventID, &h.TS, &h.IssueID, &h.Release, &h.Platform); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// termKind is the issue kind a termination is exposed as: an out-of-memory or
// watchdog kill is a crash issue whose events carry an exit reason (2c-D1).
// It reads the agg CTE of the issue queries.
const termKind = `(case a.term when 'oom' then 'oom' when 'watchdog_termination' then 'watchdog' end)`

// displayKind is the kind column of the issue list: the termination kind when
// there is one, else the issue's own kind. A crash filter still matches
// terminations, since they are crashes.
const displayKind = `coalesce(` + termKind + `, i.kind)`
