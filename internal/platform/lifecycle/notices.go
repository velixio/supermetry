package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/platform/mail"
	"supermetry/internal/scheduler"
)

// DeletionKind identifies the row whose deletion lifecycle generates a notice.
type DeletionKind uint8

const (
	DeletionSite DeletionKind = iota + 1
	DeletionOrg
)

// DeletionStage identifies the delivery state column to mark.
type DeletionStage uint8

const (
	DeletionNotified DeletionStage = iota + 1
	DeletionReminded
)

// DeletionTarget contains the data needed to deliver one deletion notice.
type DeletionTarget struct {
	Kind                 DeletionKind
	ID                   int64
	Name                 string
	OrgSlug              string
	DeletedAt            time.Time
	NotifiedAt           *time.Time
	RemindedAt           *time.Time
	RecipientIDs         []int64
	Recipients           []string
	NotifiedRecipientIDs []int64
	RemindedRecipientIDs []int64
	Items                []string
}

// DeletionNoticeStore persists deletion-notice delivery state.
type DeletionNoticeStore interface {
	PendingDeletionTargets(context.Context, time.Time) ([]DeletionTarget, error)
	RefreshDeletionTarget(context.Context, DeletionTarget, time.Time) (DeletionTarget, bool, error)
	MarkDeletionTarget(context.Context, DeletionTarget, DeletionStage, time.Time) (bool, error)
	RecordDeletionTargetSend(context.Context, DeletionTarget, DeletionStage, int64, time.Time) error
	CompleteDeletionTarget(context.Context, DeletionTarget, DeletionStage, time.Time) (bool, error)
}

var deletionNoticeStores struct {
	sync.RWMutex
	factory func(*pgxpool.Pool) DeletionNoticeStore
}

// RegisterDeletionNoticeStore connects the store implementation without
// reversing the store package's existing dependency on lifecycle grace rules.
func RegisterDeletionNoticeStore(factory func(*pgxpool.Pool) DeletionNoticeStore) {
	deletionNoticeStores.Lock()
	defer deletionNoticeStores.Unlock()
	if deletionNoticeStores.factory != nil {
		panic("lifecycle: deletion notice store already registered")
	}
	deletionNoticeStores.factory = factory
}

// Notices sends deletion notices and reminders.
type Notices struct {
	store   DeletionNoticeStore
	sender  mail.Sender
	baseURL string
	now     func() time.Time
}

// NewNotices constructs the deletion-notice job.
func NewNotices(pool *pgxpool.Pool, sender mail.Sender, baseURL string, now func() time.Time) *Notices {
	deletionNoticeStores.RLock()
	factory := deletionNoticeStores.factory
	deletionNoticeStores.RUnlock()
	var data DeletionNoticeStore
	if factory != nil {
		data = factory(pool)
	}
	if now == nil {
		now = time.Now
	}
	return &Notices{
		store: data, sender: sender,
		baseURL: strings.TrimRight(baseURL, "/"), now: now,
	}
}

// Run delivers one pass of due deletion notices.
func (n *Notices) Run(ctx context.Context) error {
	if n.sender == nil || !n.sender.Enabled() {
		return nil
	}
	if n.store == nil {
		return errors.New("deletion notice store is not registered")
	}
	now := n.now()
	targets, err := n.store.PendingDeletionTargets(ctx, now)
	if err != nil {
		return err
	}
	var result error
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		if err := n.deliver(ctx, target, now); err != nil {
			n.logFailure(ctx, target, err)
			result = errors.Join(result, err)
		}
	}
	return result
}

// Job returns the five-minute scheduler entry for deletion notices.
func (n *Notices) Job() scheduler.Job {
	return scheduler.Job{Name: "deletion-notices", Every: 5 * time.Minute, Run: n.Run}
}

func (n *Notices) deliver(ctx context.Context, target DeletionTarget, now time.Time) error {
	current, eligible, err := n.store.RefreshDeletionTarget(ctx, target, now)
	if err != nil || !eligible {
		return err
	}
	target = current
	if len(target.Recipients) == 0 {
		return nil
	}
	if ReminderDue(target.DeletedAt, now) {
		if target.NotifiedAt == nil {
			marked, err := n.store.MarkDeletionTarget(ctx, target, DeletionNotified, now)
			if err != nil || !marked {
				return err
			}
		}
		if target.RemindedAt != nil {
			return nil
		}
		return n.deliverStage(ctx, target, DeletionReminded, now, true)
	}
	if target.NotifiedAt != nil {
		return nil
	}
	return n.deliverStage(ctx, target, DeletionNotified, now, false)
}

func (n *Notices) deliverStage(ctx context.Context, target DeletionTarget, stage DeletionStage, now time.Time, reminder bool) error {
	if len(target.RecipientIDs) != len(target.Recipients) {
		return errors.New("deletion notice recipients are misaligned")
	}
	sentIDs := target.NotifiedRecipientIDs
	if stage == DeletionReminded {
		sentIDs = target.RemindedRecipientIDs
	}
	sent := make(map[int64]struct{}, len(sentIDs))
	for _, userID := range sentIDs {
		sent[userID] = struct{}{}
	}
	userIDs := make([]int64, 0, len(target.RecipientIDs))
	recipients := make([]string, 0, len(target.Recipients))
	for index, userID := range target.RecipientIDs {
		if _, ok := sent[userID]; ok {
			continue
		}
		userIDs = append(userIDs, userID)
		recipients = append(recipients, target.Recipients[index])
	}
	if len(recipients) == 0 {
		if len(target.Recipients) == 0 {
			return nil
		}
		_, err := n.store.CompleteDeletionTarget(ctx, target, stage, now)
		return err
	}

	results := mail.SendEach(ctx, n.sender, recipients, n.notice(target, reminder))
	var deliveryErr error
	failed := 0
	for index, sendErr := range results {
		if sendErr != nil {
			failed++
			deliveryErr = errors.Join(deliveryErr, sendErr)
			continue
		}
		if err := n.store.RecordDeletionTargetSend(ctx, target, stage, userIDs[index], now); err != nil {
			return err
		}
	}
	if _, err := n.store.CompleteDeletionTarget(ctx, target, stage, now); err != nil {
		return err
	}
	if failed > 0 {
		return deletionDeliveryError{err: deliveryErr, failedCount: failed}
	}
	return nil
}

func (n *Notices) notice(target DeletionTarget, reminder bool) mail.Notice {
	purge := PurgeAt(target.DeletedAt).UTC()
	purgeDate := purge.Format("2006-01-02")
	purgeTime := purge.Format("2006-01-02 15:04 UTC")
	if target.Kind == DeletionOrg {
		subject := fmt.Sprintf("%s is scheduled for deletion on %s", target.Name, purgeDate)
		paragraph := fmt.Sprintf("The organization and the sites listed below will be permanently deleted on %s.", purgeTime)
		if reminder {
			subject = fmt.Sprintf("Reminder: %s will be deleted in 3 days", target.Name)
			paragraph = fmt.Sprintf("The organization and the sites listed below will be permanently deleted on %s.", purgeTime)
		}
		return mail.Notice{
			Subject: subject, Heading: subject,
			Paragraphs:  []string{paragraph, "Restore the organization before then to keep its data."},
			Items:       target.Items,
			ActionLabel: "Restore organization",
			ActionURL:   n.baseURL + "/app/account",
		}
	}
	subject := fmt.Sprintf("%s is scheduled for deletion on %s", target.Name, purgeDate)
	paragraph := fmt.Sprintf("The site will be permanently deleted on %s.", purgeTime)
	if reminder {
		subject = fmt.Sprintf("Reminder: %s will be deleted in 3 days", target.Name)
		paragraph = fmt.Sprintf("The site will be permanently deleted on %s.", purgeTime)
	}
	return mail.Notice{
		Subject: subject, Heading: subject,
		Paragraphs:  []string{paragraph, "Restore the site before then to keep its data."},
		ActionLabel: "Restore site",
		ActionURL:   n.baseURL + "/app/" + url.PathEscape(target.OrgSlug) + "/sites",
	}
}

func (n *Notices) logFailure(ctx context.Context, target DeletionTarget, err error) {
	targetName := "site"
	if target.Kind == DeletionOrg {
		targetName = "org"
	}
	failed := 0
	var deliveryErr deletionDeliveryError
	if errors.As(err, &deliveryErr) {
		failed = deliveryErr.failedCount
	}
	slog.ErrorContext(ctx, "send deletion notice", "target", targetName, "id", target.ID, "failed_count", failed)
}

type deletionDeliveryError struct {
	err         error
	failedCount int
}

func (deletionDeliveryError) Error() string { return "deletion notice delivery failed" }

func (e deletionDeliveryError) Unwrap() error { return e.err }
