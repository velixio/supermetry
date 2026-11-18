package appissues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/mail"
	"supermetry/internal/scheduler"
)

const (
	// notifyMaxAge is how long a notification is retried; older ones are
	// dropped so a mail outage does not send a day of stale news at its end.
	notifyMaxAge   = 24 * time.Hour
	notifyBatch    = 200
	subjectTitleSz = 80
)

// Notifier mails an issue's assignee about workflow events: the issue was
// assigned to them, or an issue assigned to them regressed or was resolved by
// someone else. It reads the activity timeline (so regressions written by the
// ingest writer need no extra hook) and marks each row once handled. It never
// mails the person who made the change, respects user_notification_prefs, and
// only mails users who can read the project. Mail carries plain text and a
// plain link to the issue: no tracking pixels and no tracked links.
type Notifier struct {
	pool    *pgxpool.Pool
	sender  mail.Sender
	baseURL string
	now     func() time.Time
}

// NewNotifier returns the notifier. baseURL is the public base URL used for
// the issue link.
func NewNotifier(pool *pgxpool.Pool, sender mail.Sender, baseURL string, now func() time.Time) *Notifier {
	return &Notifier{pool: pool, sender: sender, baseURL: strings.TrimRight(baseURL, "/"), now: now}
}

// Job returns the every-minute scheduler entry.
func (n *Notifier) Job() scheduler.Job {
	return scheduler.Job{Name: "app-issue-notify", Every: time.Minute, Run: func(ctx context.Context) error {
		_, err := n.Run(ctx)
		return err
	}}
}

type pending struct {
	id       int64
	siteID   int32
	issueID  int64
	kind     string
	actor    *int64
	at       time.Time
	data     map[string]any
	title    string
	assignee *int64
	siteName string
	orgSlug  string
	siteSlug string
}

// Run sends the due notifications and returns how many mails went out. A
// failed delivery is left unmarked and retried on the next run.
func (n *Notifier) Run(ctx context.Context) (int, error) {
	if n.sender == nil || !n.sender.Enabled() {
		return 0, nil
	}
	rows, err := n.pool.Query(ctx, `
select a.id, a.site_id, a.issue_id, a.kind, a.actor_user_id, a.at, a.data,
       i.title, i.assignee_user_id, s.name, o.slug, s.slug
from app_issue_activity a
join app_issues i on i.id = a.issue_id
join sites s on s.id = a.site_id
join orgs o on o.id = s.org_id
where a.notified_at is null and a.kind in ('assigned', 'status', 'regressed')
order by a.id
limit $1`, notifyBatch)
	if err != nil {
		return 0, fmt.Errorf("find notifications: %w", err)
	}
	var todo []pending
	for rows.Next() {
		var p pending
		var raw []byte
		if err := rows.Scan(&p.id, &p.siteID, &p.issueID, &p.kind, &p.actor, &p.at, &raw, &p.title, &p.assignee, &p.siteName, &p.orgSlug, &p.siteSlug); err != nil {
			rows.Close()
			return 0, err
		}
		_ = json.Unmarshal(raw, &p.data)
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	now := n.now()
	sent := 0
	var result error
	for _, p := range todo {
		if err := ctx.Err(); err != nil {
			return sent, errors.Join(result, err)
		}
		delivered, err := n.handle(ctx, p, now)
		if err != nil {
			slog.Error("app issue notification failed", "activity", p.id, "step", mail.ErrorStep(err))
			result = errors.Join(result, err)
			continue
		}
		if delivered {
			sent++
		}
	}
	return sent, result
}

func (n *Notifier) handle(ctx context.Context, p pending, now time.Time) (bool, error) {
	mark := func() error {
		_, err := n.pool.Exec(ctx, `update app_issue_activity set notified_at = $2 where id = $1 and notified_at is null`, p.id, now)
		return err
	}
	if now.Sub(p.at) > notifyMaxAge {
		return false, mark()
	}
	recipient, event := n.recipient(p)
	if recipient == 0 || (p.actor != nil && *p.actor == recipient) {
		return false, mark()
	}
	prefs, err := getPrefs(ctx, n.pool, recipient, p.siteID)
	if err != nil {
		return false, err
	}
	if (event == "assigned" && !prefs.Assigned) || (event == "resolved" && !prefs.Resolved) || (event == "regressed" && !prefs.Regressed) {
		return false, mark()
	}
	var email string
	var canRead bool
	if err := n.pool.QueryRow(ctx, `select u.email, `+accessSQL+` from users u where u.id = $1`, recipient, p.siteID).Scan(&email, &canRead); err != nil {
		// The user is gone: nothing to send.
		return false, mark()
	}
	if !canRead || email == "" {
		return false, mark()
	}
	if err := n.sender.Send(ctx, n.notice(p, event).Message(email)); err != nil {
		return false, err
	}
	return true, mark()
}

// recipient picks who is told about the row, and which kind of event it is.
func (n *Notifier) recipient(p pending) (int64, string) {
	switch p.kind {
	case KindAssigned:
		if v, ok := p.data["assignee"].(float64); ok && v > 0 {
			return int64(v), "assigned"
		}
	case KindRegressed:
		if p.assignee != nil {
			return *p.assignee, "regressed"
		}
	case KindStatus:
		if to, _ := p.data["to"].(string); to == "resolved" && p.assignee != nil {
			return *p.assignee, "resolved"
		}
	}
	return 0, ""
}

// cleanLine makes user-controlled text safe for a mail header and one line.
func cleanLine(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

func (n *Notifier) notice(p pending, event string) mail.Notice {
	title := cleanLine(p.title, subjectTitleSz)
	var subject, line string
	switch event {
	case "assigned":
		subject = fmt.Sprintf("[%s] Issue assigned to you: %s", cleanLine(p.siteName, 40), title)
		line = "An issue was assigned to you."
	case "regressed":
		subject = fmt.Sprintf("[%s] Issue regressed: %s", cleanLine(p.siteName, 40), title)
		line = "An issue assigned to you regressed: it was resolved and has new events."
	default:
		subject = fmt.Sprintf("[%s] Issue resolved: %s", cleanLine(p.siteName, 40), title)
		line = "An issue assigned to you was marked resolved."
	}
	return mail.Notice{
		Subject:     subject,
		Preheader:   title,
		Eyebrow:     "App issues",
		Heading:     title,
		Paragraphs:  []string{line},
		ActionLabel: "Open issue",
		ActionURL:   fmt.Sprintf("%s/app/%s/%s/issues/%d", n.baseURL, url.PathEscape(p.orgSlug), url.PathEscape(p.siteSlug), p.issueID),
		Reason:      "You are receiving this because an issue is assigned to you on Supermetry. Change this under Account, Notifications.",
		BaseURL:     n.baseURL,
	}
}
