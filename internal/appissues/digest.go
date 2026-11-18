package appissues

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/mail"
	"supermetry/internal/scheduler"
)

const (
	digestTopIssues  = 5
	digestTitleSize  = 80
	digestLocalDay   = time.Monday
	digestLocalHour  = 9
	digestSubjectMax = 60
)

// emailPattern finds addresses an error title may have picked up from user
// input; the digest replaces them so it never repeats an end user's address.
var emailPattern = regexp.MustCompile(`[[:alnum:]._%+-]+@[[:alnum:].-]+`)

// DigestIssue is one of the week's busiest issues.
type DigestIssue struct {
	ID     int64
	Title  string
	Events int64
}

// Digest is one project's week in numbers. It holds counts, issue titles and
// a crash-free figure, and no user identifier of any kind.
type Digest struct {
	SiteID              int32
	SiteName            string
	OrgSlug, SiteSlug   string
	WeekStart, WeekEnd  time.Time
	NewIssues           int64
	RegressedIssues     int64
	Top                 []DigestIssue
	CrashFreeUsers      float64 // 0..1, crashed installs against all installs of the week
	PriorCrashFreeUsers float64
	HasCrashFree        bool
	HasPriorCrashFree   bool
}

// Digester mails the weekly digest. On Monday between 09:00 and 09:59 in a
// project's time zone, each member who switched the digest on for that project
// and can still read it gets one mail covering the week that just closed
// (Monday to Sunday, in the project's time zone) with the week before for
// comparison. The ISO week is claimed on the preference row before sending, so
// a second run, a second instance or a retry never mails the same week twice;
// a failed send releases the claim.
type Digester struct {
	pool    *pgxpool.Pool
	sender  mail.Sender
	baseURL string
	now     func() time.Time
}

// NewDigester returns the digest job; baseURL is the public base URL used for
// the project links.
func NewDigester(pool *pgxpool.Pool, sender mail.Sender, baseURL string, now func() time.Time) *Digester {
	return &Digester{pool: pool, sender: sender, baseURL: strings.TrimRight(baseURL, "/"), now: now}
}

// Job returns the hourly scheduler entry.
func (d *Digester) Job() scheduler.Job {
	return scheduler.Job{Name: "app-weekly-digest", Every: time.Hour, Run: func(ctx context.Context) error {
		_, err := d.Run(ctx)
		return err
	}}
}

type digestTarget struct {
	userID   int64
	email    string
	siteID   int32
	site     string
	orgSlug  string
	siteSlug string
	loc      *time.Location
	last     *string
	week     string // ISO week being mailed, such as 2026-W47
	start    time.Time
}

// isoWeek renders the ISO week of t as 2026-W47.
func isoWeek(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}

// Run sends the digests that are due and returns how many mails went out.
func (d *Digester) Run(ctx context.Context) (int, error) {
	if d.sender == nil || !d.sender.Enabled() {
		return 0, nil
	}
	rows, err := d.pool.Query(ctx, `
select p.user_id, u.email, s.id, s.name, o.slug, s.slug, s.timezone, p.last_digest_week
from user_notification_prefs p
join users u on u.id = p.user_id
join sites s on s.id = p.site_id and s.kind = 'app' and s.deleted_at is null
join orgs o on o.id = s.org_id and o.deleted_at is null
where p.weekly_digest
  and exists (select 1 from memberships m where m.org_id = s.org_id and m.user_id = p.user_id
              and (m.site_ids is null or s.id = any(m.site_ids)))
order by p.user_id, s.id`)
	if err != nil {
		return 0, fmt.Errorf("find digest recipients: %w", err)
	}
	now := d.now()
	var due []digestTarget
	for rows.Next() {
		var t digestTarget
		var tz string
		if err := rows.Scan(&t.userID, &t.email, &t.siteID, &t.site, &t.orgSlug, &t.siteSlug, &tz, &t.last); err != nil {
			rows.Close()
			return 0, err
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			loc = time.UTC
		}
		local := now.In(loc)
		if local.Weekday() != digestLocalDay || local.Hour() != digestLocalHour {
			continue
		}
		t.loc, t.week = loc, isoWeek(local)
		if t.last != nil && *t.last == t.week {
			continue
		}
		monday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		t.start = monday.AddDate(0, 0, -7)
		due = append(due, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	sent := 0
	var result error
	for i := 0; i < len(due); {
		j := i
		for j < len(due) && due[j].userID == due[i].userID {
			j++
		}
		delivered, err := d.mailUser(ctx, due[i:j])
		if err != nil {
			slog.Error("weekly digest failed", "user", due[i].userID, "step", mail.ErrorStep(err))
			result = errors.Join(result, err)
		}
		if delivered {
			sent++
		}
		if ctx.Err() != nil {
			return sent, errors.Join(result, ctx.Err())
		}
		i = j
	}
	return sent, result
}

// mailUser claims, builds and sends one user's digest for the given projects.
func (d *Digester) mailUser(ctx context.Context, targets []digestTarget) (bool, error) {
	var claimed []digestTarget
	release := func() {
		for _, t := range claimed {
			// Best effort: a failed release only delays the retry to next week.
			_, _ = d.pool.Exec(context.WithoutCancel(ctx), `update user_notification_prefs set last_digest_week = $4
where user_id = $1 and site_id = $2 and last_digest_week = $3`, t.userID, t.siteID, t.week, t.last)
		}
	}
	for _, t := range targets {
		tag, err := d.pool.Exec(ctx, `update user_notification_prefs set last_digest_week = $3
where user_id = $1 and site_id = $2 and weekly_digest and last_digest_week is distinct from $3`, t.userID, t.siteID, t.week)
		if err != nil {
			release()
			return false, err
		}
		if tag.RowsAffected() == 1 {
			claimed = append(claimed, t)
		}
	}
	if len(claimed) == 0 {
		return false, nil
	}
	digests := make([]Digest, 0, len(claimed))
	for _, t := range claimed {
		digest, err := d.BuildDigest(ctx, t.siteID, t.start)
		if err != nil {
			release()
			return false, err
		}
		digests = append(digests, digest)
	}
	if err := d.sender.Send(ctx, d.notice(digests).Message(claimed[0].email)); err != nil {
		release()
		return false, err
	}
	return true, nil
}

// BuildDigest collects the project's week starting at weekStart (a local
// midnight in the project's time zone) and the seven days before it.
func (d *Digester) BuildDigest(ctx context.Context, siteID int32, weekStart time.Time) (Digest, error) {
	dg := Digest{SiteID: siteID, WeekStart: weekStart, WeekEnd: weekStart.AddDate(0, 0, 7)}
	prior := weekStart.AddDate(0, 0, -7)
	err := d.pool.QueryRow(ctx, `
select s.name, o.slug, s.slug,
  (select count(*) from app_issues where site_id = s.id and merged_into is null and first_seen >= $2 and first_seen < $3),
  (select count(*) from app_issues where site_id = s.id and merged_into is null and regressed_at >= $2 and regressed_at < $3)
from sites s join orgs o on o.id = s.org_id where s.id = $1`, siteID, weekStart, dg.WeekEnd).
		Scan(&dg.SiteName, &dg.OrgSlug, &dg.SiteSlug, &dg.NewIssues, &dg.RegressedIssues)
	if err != nil {
		return Digest{}, fmt.Errorf("digest counts: %w", err)
	}
	top, err := d.pool.Query(ctx, `
select i.id, i.title, e.events from (
  select issue_id, sum(occurrences)::bigint as events from app_events
  where site_id = $1 and ts >= $2 and ts < $3 and issue_id is not null and kind <> 'session'
  group by issue_id order by 2 desc, issue_id limit $4) e
join app_issues i on i.id = e.issue_id
order by e.events desc, i.id`, siteID, weekStart, dg.WeekEnd, digestTopIssues)
	if err != nil {
		return Digest{}, fmt.Errorf("digest top issues: %w", err)
	}
	for top.Next() {
		var issue DigestIssue
		if err := top.Scan(&issue.ID, &issue.Title, &issue.Events); err != nil {
			top.Close()
			return Digest{}, err
		}
		issue.Title = digestTitle(issue.Title)
		dg.Top = append(dg.Top, issue)
	}
	top.Close()
	if err := top.Err(); err != nil {
		return Digest{}, err
	}
	if dg.CrashFreeUsers, dg.HasCrashFree, err = d.crashFreeUsers(ctx, siteID, weekStart, dg.WeekEnd); err != nil {
		return Digest{}, err
	}
	if dg.PriorCrashFreeUsers, dg.HasPriorCrashFree, err = d.crashFreeUsers(ctx, siteID, prior, weekStart); err != nil {
		return Digest{}, err
	}
	return dg, nil
}

// crashFreeUsers is 1 - installs with a crashed session / installs with any
// session in [from, to), the same definition the release health pages use
// (abnormal sessions are not crashes). Sessions without an install do not
// count. The bool is false when no install reported a session.
func (d *Digester) crashFreeUsers(ctx context.Context, siteID int32, from, to time.Time) (float64, bool, error) {
	var total, crashed int64
	if err := d.pool.QueryRow(ctx, `
select count(distinct install_hash), count(distinct install_hash) filter (where status = 'crashed')
from app_sessions where site_id = $1 and started >= $2 and started < $3 and install_hash is not null`, siteID, from, to).Scan(&total, &crashed); err != nil {
		return 0, false, fmt.Errorf("digest crash-free users: %w", err)
	}
	if total == 0 {
		return 0, false, nil
	}
	return 1 - float64(crashed)/float64(total), true, nil
}

// digestTitle makes an issue title safe for a mail line: one line, bounded,
// and without any email address the error text may have carried.
func digestTitle(title string) string {
	return cleanLine(emailPattern.ReplaceAllString(title, "[email]"), digestTitleSize)
}

func (d *Digester) notice(digests []Digest) mail.Notice {
	sections := make([]mail.Section, 0, len(digests))
	links := make([]mail.Link, 0, len(digests))
	for _, dg := range digests {
		last := dg.WeekEnd.AddDate(0, 0, -1)
		rows := []mail.Row{
			{Label: "New issues", Value: fmt.Sprintf("%d", dg.NewIssues)},
			{Label: "Regressed issues", Value: fmt.Sprintf("%d", dg.RegressedIssues)},
		}
		for i, issue := range dg.Top {
			rows = append(rows, mail.Row{Label: fmt.Sprintf("Top issue %d", i+1), Value: fmt.Sprintf("%s (%d events)", issue.Title, issue.Events)})
		}
		switch {
		case dg.HasCrashFree && dg.HasPriorCrashFree:
			rows = append(rows, mail.Row{Label: "Crash-free users", Value: fmt.Sprintf("%.1f%% (%.1f%% the week before)", dg.CrashFreeUsers*100, dg.PriorCrashFreeUsers*100)})
		case dg.HasCrashFree:
			rows = append(rows, mail.Row{Label: "Crash-free users", Value: fmt.Sprintf("%.1f%%", dg.CrashFreeUsers*100)})
		}
		name := cleanLine(dg.SiteName, digestSubjectMax)
		sections = append(sections, mail.Section{
			Heading: fmt.Sprintf("%s, %s to %s", name, dg.WeekStart.Format("2 Jan"), last.Format("2 Jan")),
			Rows:    rows,
		})
		links = append(links, mail.Link{Label: "Open " + name, URL: fmt.Sprintf("%s/app/%s/%s", d.baseURL, url.PathEscape(dg.OrgSlug), url.PathEscape(dg.SiteSlug))})
	}
	subject := "Your weekly digest"
	if len(digests) == 1 {
		subject = fmt.Sprintf("[%s] Your weekly digest", cleanLine(digests[0].SiteName, digestSubjectMax))
	}
	return mail.Notice{
		Subject:    subject,
		Preheader:  "Last week's issues and crash-free users",
		Eyebrow:    "Weekly digest",
		Heading:    "Your week in app monitoring",
		Paragraphs: []string{"New and regressed issues, the busiest issues and crash-free users for the week that just ended."},
		Sections:   sections,
		Links:      links,
		Reason:     "You are receiving this because you turned on the weekly digest on Supermetry. Change this under Account, Notifications.",
		BaseURL:    d.baseURL,
	}
}
