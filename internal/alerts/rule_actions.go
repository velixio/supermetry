package alerts

import (
	"context"
	"log/slog"
)

// IssueAssigner assigns an issue to a user as the system, never replacing an
// existing assignee; it reports whether it assigned. appissues.Service
// implements it.
type IssueAssigner interface {
	AssignIfUnassigned(ctx context.Context, siteID int32, issueID, userID int64) (bool, error)
}

// OwnerMailer mails an issue's owner that a rule matched. appissues.Notifier
// implements it; a user who cannot read the project is not mailed.
type OwnerMailer interface {
	MailRuleOwner(ctx context.Context, siteID int32, issueID, userID int64, ruleName string) error
}

// WithActions enables the assign and notify_owner actions. Without them a rule
// carrying those actions still delivers to its channels.
func (e *RuleEvaluator) WithActions(assigner IssueAssigner, mailer OwnerMailer) *RuleEvaluator {
	e.assigner, e.mailer = assigner, mailer
	return e
}

// runAssignActions runs the rule's assign actions on the candidate's issue
// before its notification, so the delivery sees the new owner. The assignment
// is idempotent (an assigned issue is left alone), so a crash before the key
// commits repeats nothing visible. A failing assignment is logged and never
// blocks the notification.
func (e *RuleEvaluator) runAssignActions(ctx context.Context, alert Alert, c *Candidate) {
	if c.IssueID == 0 {
		return
	}
	for _, action := range alert.Rule.Actions {
		if action.Type != ActionAssign || c.AssigneeUserID != 0 {
			continue
		}
		if e.assigner == nil {
			slog.Warn("alert rule assign action skipped: assigner unavailable", "alert_id", alert.ID)
			return
		}
		assigned, err := e.assigner.AssignIfUnassigned(ctx, alert.SiteID, c.IssueID, action.UserID)
		if err != nil {
			slog.Warn("alert rule assign action failed", "alert_id", alert.ID, "issue_id", c.IssueID, "error", err)
			continue
		}
		if assigned {
			c.AssigneeUserID = action.UserID
		}
	}
}

// ruleChannels returns the channels one candidate is delivered to: the rule's
// channel and webhook targets, plus the fallback channels of notify_owner
// actions when the issue has no owner. They ride the delivery state, so they
// are retried like any other channel.
func ruleChannels(alert Alert, c Candidate) []int64 {
	ids := append([]int64{}, alert.ChannelIDs...)
	if c.IssueID == 0 || c.AssigneeUserID != 0 {
		return ids
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, action := range alert.Rule.Actions {
		if action.Type != ActionNotifyOwner {
			continue
		}
		for _, id := range action.FallbackChannelIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// runOwnerActions mails the owner for notify_owner actions after the key has
// committed. It runs at most once per key: a failure is logged, not retried,
// so a flaky mail server cannot produce a second mail for the same key.
func (e *RuleEvaluator) runOwnerActions(ctx context.Context, alert Alert, c Candidate) {
	if c.IssueID == 0 || c.AssigneeUserID == 0 {
		return
	}
	for _, action := range alert.Rule.Actions {
		if action.Type != ActionNotifyOwner {
			continue
		}
		if e.mailer == nil {
			slog.Warn("alert rule notify_owner action skipped: mailer unavailable", "alert_id", alert.ID)
			return
		}
		if err := e.mailer.MailRuleOwner(ctx, alert.SiteID, c.IssueID, c.AssigneeUserID, alert.Name); err != nil {
			slog.Warn("alert rule notify_owner action failed", "alert_id", alert.ID, "issue_id", c.IssueID, "error", err)
		}
		return
	}
}

// ruleIssueWebhook is the body webhook channels receive for an issue or event
// rule. It carries the issue's public fields only: no tags, user hashes, stack
// frames or end-user email addresses.
type ruleIssueWebhook struct {
	Type string `json:"type"`
	Rule struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"rule"`
	Issue struct {
		ID        int64  `json:"id"`
		Title     string `json:"title"`
		Culprit   string `json:"culprit"`
		Level     string `json:"level"`
		Status    string `json:"status"`
		URL       string `json:"url"`
		FirstSeen string `json:"first_seen"`
		LastSeen  string `json:"last_seen"`
		Events    int64  `json:"events"`
	} `json:"issue"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
}

func ruleIssueWebhookBody(n Notification) (ruleIssueWebhook, bool) {
	var body ruleIssueWebhook
	if n.Rule == nil || n.App == nil || n.App.IssueID == 0 {
		return body, false
	}
	body.Type = "alert.fired"
	if n.Rule.Status == ruleStatusResolve {
		body.Type = "alert.resolved"
	}
	body.Rule.ID, body.Rule.Name = n.Rule.ID, n.Rule.Name
	issue := n.App
	body.Issue.ID, body.Issue.Title, body.Issue.Culprit = issue.IssueID, issue.IssueTitle, issue.Culprit
	body.Issue.Level, body.Issue.Status, body.Issue.URL = issue.IssueLevel, issue.IssueState, issue.IssueURL
	body.Issue.FirstSeen, body.Issue.LastSeen, body.Issue.Events = issue.FirstSeen, issue.LastSeen, issue.Events
	body.Value, body.Threshold = n.Value, n.Threshold
	return body, true
}
