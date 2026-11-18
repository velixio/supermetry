package appissues

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FeedbackItem is one scrubbed user report. The reporter's email is only ever
// present as a keyed hash, and the name is not kept at all until the opt-in to
// keep names exists. Message is already scrubbed and clipped by the sender.
type FeedbackItem struct {
	SiteID    int32
	At        time.Time
	EventID   string // the event the report is about; "" when unknown
	EmailHash []byte
	Message   string
	URL       string
}

// Feedback is a stored report as the API returns it. It never carries the
// email hash; HasContact only says whether a contact address was given.
type Feedback struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	IssueID    *int64    `json:"issue_id"`
	EventID    *string   `json:"event_id"`
	Name       *string   `json:"name"`
	HasContact bool      `json:"has_contact"`
	Message    string    `json:"message"`
	URL        *string   `json:"url"`
}

const (
	defaultFeedbackQueue = 1000
	feedbackBatch        = 100
	feedbackFlushEvery   = 2 * time.Second
)

// FeedbackWriter stores feedback off the request path: Add never blocks or
// reads the database, a full queue drops the report. Run flushes on a size, an
// interval and shutdown.
type FeedbackWriter struct {
	pool   *pgxpool.Pool
	ch     chan FeedbackItem
	mu     sync.RWMutex
	closed bool
}

// NewFeedbackWriter returns a writer with a bounded queue (1000 when size <= 0).
func NewFeedbackWriter(pool *pgxpool.Pool, size int) *FeedbackWriter {
	if size <= 0 {
		size = defaultFeedbackQueue
	}
	return &FeedbackWriter{pool: pool, ch: make(chan FeedbackItem, size)}
}

// Add queues one report; false means it was dropped.
func (w *FeedbackWriter) Add(it FeedbackItem) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return false
	}
	select {
	case w.ch <- it:
		return true
	default:
		return false
	}
}

// Run flushes until ctx ends, then drains and flushes what is left.
func (w *FeedbackWriter) Run(ctx context.Context) error {
	ticker := time.NewTicker(feedbackFlushEvery)
	defer ticker.Stop()
	batch := make([]FeedbackItem, 0, feedbackBatch)
	flush := func(c context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := w.Write(c, batch); err != nil {
			slog.Error("app feedback write failed", "reports", len(batch), "error", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case it := <-w.ch:
			batch = append(batch, it)
			if len(batch) >= feedbackBatch {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			w.mu.Lock()
			w.closed = true
			w.mu.Unlock()
			final, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				select {
				case it := <-w.ch:
					batch = append(batch, it)
					continue
				default:
				}
				break
			}
			flush(final)
			return nil
		}
	}
}

// Write stores reports of sites that still exist. A report is linked to the
// issue of its event when that event is known; otherwise it stays unlinked
// and is linked on a later write once the event has arrived.
func (w *FeedbackWriter) Write(ctx context.Context, items []FeedbackItem) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin feedback write: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, it := range items {
		var eventID any
		if it.EventID != "" {
			eventID = it.EventID
		}
		var url any
		if it.URL != "" {
			url = it.URL
		}
		if _, err := tx.Exec(ctx, `
insert into app_feedback (site_id, issue_id, event_id, at, name, email_hash, message, url)
select s.id, (select e.issue_id from app_events e where e.site_id = s.id and e.ts >= $3::timestamptz - interval '48 hours' and e.event_id = $2 limit 1),
       $2, $3, null, $4, $5, $6
from sites s where s.id = $1 and s.deleted_at is null`, it.SiteID, eventID, it.At, it.EmailHash, it.Message, url); err != nil {
			return fmt.Errorf("insert feedback: %w", err)
		}
	}
	// Late events: link recent reports whose event was stored after them.
	if _, err := tx.Exec(ctx, `
update app_feedback f set issue_id = e.issue_id from app_events e
where f.issue_id is null and f.event_id is not null and f.at > now() - interval '15 minutes'
  and e.site_id = f.site_id and e.ts between f.at - interval '1 hour' and f.at + interval '5 minutes' and e.event_id = f.event_id`); err != nil {
		return fmt.Errorf("link feedback: %w", err)
	}
	return tx.Commit(ctx)
}

// Feedback lists a project's reports, newest first. issueID 0 lists all of the
// project's, otherwise only that issue's. A non-zero before pages by time.
func (s *Service) Feedback(ctx context.Context, siteID int32, issueID int64, limit int, before time.Time) ([]Feedback, error) {
	if limit <= 0 {
		limit = defaultTimelineLimit
	}
	if limit > maxTimelineLimit {
		limit = maxTimelineLimit
	}
	if issueID != 0 {
		if err := s.issueExists(ctx, siteID, issueID); err != nil {
			return nil, err
		}
	}
	var bound any
	if !before.IsZero() {
		bound = before
	}
	rows, err := s.pool.Query(ctx, `select id, at, issue_id, event_id, name, email_hash is not null, message, url
from app_feedback where site_id = $1 and ($2::bigint = 0 or issue_id = $2) and ($3::timestamptz is null or at < $3)
order by at desc, id desc limit $4`, siteID, issueID, bound, limit)
	if err != nil {
		return nil, fmt.Errorf("read feedback: %w", err)
	}
	defer rows.Close()
	out := []Feedback{}
	for rows.Next() {
		var f Feedback
		if err := rows.Scan(&f.ID, &f.At, &f.IssueID, &f.EventID, &f.Name, &f.HasContact, &f.Message, &f.URL); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
