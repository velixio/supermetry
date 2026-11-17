package appissues

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Webhook kinds a project can subscribe to.
const (
	WebhookCreated      = "created"
	WebhookResolved     = "resolved"
	WebhookRegressed    = "regressed"
	WebhookAssigned     = "assigned"
	WebhookEscalated    = "escalated"
	WebhookGitHubLinked = "github_linked"
)

// WebhookKinds lists every subscribable kind.
var WebhookKinds = []string{WebhookCreated, WebhookResolved, WebhookRegressed, WebhookAssigned, WebhookEscalated, WebhookGitHubLinked}

const (
	webhookBatch       = 20
	webhookMaxAttempts = 6
	webhookLease       = 2 * time.Minute
	webhookBackoff     = 30 * time.Second
	webhookKeepDone    = 7 * 24 * time.Hour
)

// WebhookSettings is a project's issue webhook setting: existing notification
// channels of its organisation and the event kinds sent to them.
type WebhookSettings struct {
	ChannelIDs []int64  `json:"channel_ids"`
	Kinds      []string `json:"kinds"`
}

// WebhookSettings reads a project's setting; an unset project has none.
func (s *Service) WebhookSettings(ctx context.Context, siteID int32) (WebhookSettings, error) {
	out := WebhookSettings{ChannelIDs: []int64{}, Kinds: []string{}}
	err := s.pool.QueryRow(ctx, `select channel_ids, kinds from app_issue_webhooks where site_id = $1`, siteID).Scan(&out.ChannelIDs, &out.Kinds)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, fmt.Errorf("read issue webhooks: %w", err)
	}
	if out.ChannelIDs == nil {
		out.ChannelIDs = []int64{}
	}
	if out.Kinds == nil {
		out.Kinds = []string{}
	}
	return out, nil
}

// SetWebhookSettings replaces a project's setting. Every channel must belong
// to orgID; kinds are de-duplicated and sorted.
func (s *Service) SetWebhookSettings(ctx context.Context, orgID int64, siteID int32, in WebhookSettings) error {
	return s.setWebhookSettings(ctx, s.pool, orgID, siteID, in)
}

// SetWebhookSettingsTx is SetWebhookSettings inside the caller's transaction,
// so the audit row commits with the change.
func (s *Service) SetWebhookSettingsTx(ctx context.Context, tx pgx.Tx, orgID int64, siteID int32, in WebhookSettings) error {
	return s.setWebhookSettings(ctx, tx, orgID, siteID, in)
}

type execQuerier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) setWebhookSettings(ctx context.Context, q execQuerier, orgID int64, siteID int32, in WebhookSettings) error {
	kinds := []string{}
	for _, k := range in.Kinds {
		if !slices.Contains(WebhookKinds, k) {
			return invalid("unknown webhook kind %q", k)
		}
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	slices.Sort(kinds)
	ids := []int64{}
	for _, id := range in.ChannelIDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) > 10 {
		return invalid("at most 10 channels")
	}
	if len(ids) > 0 {
		var n int
		if err := q.QueryRow(ctx, `select count(*) from notification_channels where org_id = $1 and id = any($2)`, orgID, ids).Scan(&n); err != nil {
			return fmt.Errorf("check channels: %w", err)
		}
		if n != len(ids) {
			return invalid("every channel must be a notification channel of this organisation")
		}
	}
	if _, err := q.Exec(ctx, `insert into app_issue_webhooks (site_id, channel_ids, kinds, updated_at) values ($1, $2, $3, $4)
on conflict (site_id) do update set channel_ids = excluded.channel_ids, kinds = excluded.kinds, updated_at = excluded.updated_at`,
		siteID, ids, kinds, s.now()); err != nil {
		return fmt.Errorf("save issue webhooks: %w", err)
	}
	return nil
}

// webhookKind maps an activity row to the webhook kind it triggers, or "".
func webhookKind(kind string, data map[string]any) string {
	switch kind {
	case KindFirstSeen:
		return WebhookCreated
	case KindRegressed:
		return WebhookRegressed
	case KindAssigned:
		return WebhookAssigned
	case KindEscalated:
		return WebhookEscalated
	case KindGitHubLinked:
		if data["url"] != nil {
			return WebhookGitHubLinked
		}
	case KindStatus:
		if data["to"] == "resolved" {
			return WebhookResolved
		}
	}
	return ""
}

// enqueueWebhook adds an outbox row in the caller's transaction when the
// project subscribes to the kind. It is one statement and never delivers.
func enqueueWebhook(ctx context.Context, tx pgx.Tx, siteID int32, issueID int64, at time.Time, actor any, kind string) error {
	if _, err := tx.Exec(ctx, `insert into app_issue_webhook_outbox (site_id, issue_id, kind, actor_user_id, channel_ids, created_at, next_at)
select $1, $2, $3, $4::bigint, w.channel_ids, $5, $5 from app_issue_webhooks w
where w.site_id = $1 and $3 = any(w.kinds) and cardinality(w.channel_ids) > 0`, siteID, issueID, kind, actor, at); err != nil {
		return fmt.Errorf("queue issue webhook: %w", err)
	}
	return nil
}

// WebhookIssue is the issue part of a webhook payload.
type WebhookIssue struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Culprit   string    `json:"culprit"`
	Level     string    `json:"level"`
	Status    string    `json:"status"`
	URL       string    `json:"url"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Events    int64     `json:"events"`
}

// WebhookActor says who caused the event: a user (name only when it is a
// plain display name) or the system.
type WebhookActor struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// WebhookPayload is the body sent to webhook channels. It carries no end-user
// data and no emails.
type WebhookPayload struct {
	Type  string       `json:"type"`
	Issue WebhookIssue `json:"issue"`
	Actor WebhookActor `json:"actor"`
}

// WebhookMessage is a payload with the plain text for chat and email channels.
type WebhookMessage struct {
	Type    string
	Subject string
	Text    string
	Payload WebhookPayload
}

// WebhookSender delivers a message to one notification channel. The adapter
// signs webhook channels like alert webhooks.
type WebhookSender interface {
	Send(ctx context.Context, channelID int64, m WebhookMessage) error
}

type outboxRow struct {
	id, siteID, issueID int64
	kind                string
	actor               *int64
	channels            []int64
	attempts            int
}

// DeliverWebhooks sends the due outbox rows once. Failures back off
// exponentially (30 s, 1 min, 2 min, ...) and a row is dropped after six
// attempts; a row is done only when every channel accepted it. baseURL
// prefixes the issue link in payloads.
func (s *Service) DeliverWebhooks(ctx context.Context, sender WebhookSender, baseURL string) error {
	now := s.now()
	rows, err := s.pool.Query(ctx, `update app_issue_webhook_outbox set next_at = $2
where id in (select id from app_issue_webhook_outbox where done_at is null and next_at <= $1 order by next_at, id limit $3 for update skip locked)
returning id, site_id, issue_id, kind, actor_user_id, channel_ids, attempts`, now, now.Add(webhookLease), webhookBatch)
	if err != nil {
		return fmt.Errorf("claim webhooks: %w", err)
	}
	var due []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.id, &r.siteID, &r.issueID, &r.kind, &r.actor, &r.channels, &r.attempts); err != nil {
			rows.Close()
			return err
		}
		due = append(due, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var errs []error
	for _, r := range due {
		msg, ok, err := s.webhookMessage(ctx, r, baseURL)
		if err == nil && ok {
			for _, ch := range r.channels {
				if serr := sender.Send(ctx, ch, msg); serr != nil {
					err = errors.Join(err, fmt.Errorf("channel %d: %w", ch, serr))
				}
			}
		}
		switch {
		case err == nil:
			_, err = s.pool.Exec(ctx, `update app_issue_webhook_outbox set done_at = $2, attempts = attempts + 1 where id = $1`, r.id, now)
		case r.attempts+1 >= webhookMaxAttempts:
			slog.Warn("issue webhook dropped", "outbox_id", r.id, "kind", r.kind, "error", err)
			_, err = s.pool.Exec(ctx, `update app_issue_webhook_outbox set done_at = $2, attempts = attempts + 1 where id = $1`, r.id, now)
		default:
			delay := webhookBackoff << r.attempts
			_, uerr := s.pool.Exec(ctx, `update app_issue_webhook_outbox set attempts = attempts + 1, next_at = $2 where id = $1`, r.id, now.Add(delay))
			if uerr != nil {
				err = errors.Join(err, uerr)
			} else {
				err = nil
			}
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := s.pool.Exec(ctx, `delete from app_issue_webhook_outbox where done_at < $1`, now.Add(-webhookKeepDone)); err != nil {
		errs = append(errs, fmt.Errorf("purge webhook outbox: %w", err))
	}
	return errors.Join(errs...)
}

// webhookMessage builds the payload from the issue as it is now. ok is false
// when the issue is gone (merged away or deleted): nothing to send.
func (s *Service) webhookMessage(ctx context.Context, r outboxRow, baseURL string) (WebhookMessage, bool, error) {
	var (
		p                  WebhookPayload
		orgSlug, siteSlug  string
		merged             *int64
		actorName          *string
		siteName, issueRef string
	)
	err := s.pool.QueryRow(ctx, `select i.title, i.culprit, i.level, i.status, i.first_seen, i.last_seen, i.event_count, i.merged_into,
  o.slug, s.slug, s.name, u.name
from app_issues i join sites s on s.id = i.site_id join orgs o on o.id = s.org_id left join users u on u.id = $2
where i.id = $1`, r.issueID, r.actor).Scan(&p.Issue.Title, &p.Issue.Culprit, &p.Issue.Level, &p.Issue.Status, &p.Issue.FirstSeen, &p.Issue.LastSeen, &p.Issue.Events, &merged,
		&orgSlug, &siteSlug, &siteName, &actorName)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookMessage{}, false, nil
	}
	if err != nil {
		return WebhookMessage{}, false, fmt.Errorf("read issue for webhook: %w", err)
	}
	p.Type = r.kind
	p.Issue.ID = r.issueID
	issueRef = fmt.Sprintf("/app/%s/%s/issues/%d", orgSlug, siteSlug, r.issueID)
	p.Issue.URL = strings.TrimRight(baseURL, "/") + issueRef
	p.Actor = WebhookActor{Kind: "system"}
	if r.actor != nil {
		p.Actor.Kind = "user"
		// A display name that looks like an address is withheld.
		if actorName != nil && !strings.ContainsAny(*actorName, "@<>") {
			p.Actor.Name = *actorName
		}
	}
	title := p.Issue.Title
	if len(title) > 200 {
		title = title[:200]
	}
	text := fmt.Sprintf("Issue %s in %s: %s\n%s", r.kind, siteName, title, p.Issue.URL)
	return WebhookMessage{Type: r.kind, Subject: fmt.Sprintf("Issue %s: %s", r.kind, title), Text: text, Payload: p}, true, nil
}
