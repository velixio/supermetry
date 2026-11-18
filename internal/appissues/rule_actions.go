package appissues

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
)

// AssignIfUnassigned assigns the issue to the user as the system, unless the
// issue already has an assignee: an alert rule never re-assigns. It reports
// whether the assignment happened. The assignee must belong to the project's
// organisation (ErrInvalidAssignee otherwise).
func (s *Service) AssignIfUnassigned(ctx context.Context, siteID int32, issueID, userID int64) (bool, error) {
	assigned := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var current *int64
		if err := tx.QueryRow(ctx, `select assignee_user_id from app_issues where site_id = $1 and id = $2 for update`, siteID, issueID).Scan(&current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("read assignee: %w", err)
		}
		if current != nil {
			return nil
		}
		res, err := s.UpdateTx(ctx, tx, siteID, []int64{issueID}, Actor{System: true}, Change{Assignee: &userID})
		if err != nil {
			return err
		}
		assigned = len(res.Updated) > 0
		return nil
	})
	return assigned, err
}

// MailRuleOwner mails the issue's owner that an alert rule matched. Only a
// user who can read the project is mailed; a missing or unreadable user is a
// quiet no-op. The mail carries the rule name, the issue title and a plain
// link, no event data.
func (n *Notifier) MailRuleOwner(ctx context.Context, siteID int32, issueID, userID int64, ruleName string) error {
	if n.sender == nil || !n.sender.Enabled() {
		return nil
	}
	var email, title, siteName, orgSlug, siteSlug string
	var canRead bool
	err := n.pool.QueryRow(ctx, `select u.email, `+accessSQL+`, i.title, s.name, o.slug, s.slug
from users u, app_issues i join sites s on s.id = i.site_id join orgs o on o.id = s.org_id
where u.id = $1 and i.id = $3 and i.site_id = $2`, userID, siteID, issueID).Scan(&email, &canRead, &title, &siteName, &orgSlug, &siteSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read owner mail: %w", err)
	}
	if !canRead || email == "" {
		return nil
	}
	p := pending{siteID: siteID, issueID: issueID, title: title, siteName: siteName, orgSlug: orgSlug, siteSlug: siteSlug}
	notice := n.notice(p, "assigned")
	notice.Subject = fmt.Sprintf("[%s] Alert: %s", cleanLine(siteName, 40), cleanLine(title, subjectTitleSz))
	notice.Paragraphs = []string{fmt.Sprintf("The alert rule %q matched an issue assigned to you.", cleanLine(ruleName, 120))}
	notice.ActionURL = fmt.Sprintf("%s/app/%s/%s/issues/%d", n.baseURL, url.PathEscape(orgSlug), url.PathEscape(siteSlug), issueID)
	return n.sender.Send(ctx, notice.Message(email))
}
