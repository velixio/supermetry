package appissues

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Activity is one row of an issue's timeline. ActorUserID is nil for the
// system (and for a user that was deleted since).
type Activity struct {
	ID          int64           `json:"id"`
	At          time.Time       `json:"at"`
	Kind        string          `json:"kind"`
	ActorUserID *int64          `json:"actor_user_id"`
	ActorName   string          `json:"actor_name"`
	Data        json.RawMessage `json:"data"`
}

func record(ctx context.Context, tx pgx.Tx, siteID int32, issueID int64, at time.Time, actor any, kind string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode activity: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into app_issue_activity (site_id, issue_id, at, actor_user_id, kind, data) values ($1, $2, $3, $4, $5, $6)`,
		siteID, issueID, at, actor, kind, body); err != nil {
		return fmt.Errorf("record activity: %w", err)
	}
	return nil
}

// RecordSystem adds an activity row by the system (regressions, first sight)
// inside the caller's transaction, so the ingest writer keeps the timeline and
// the status change atomic. It does no read.
func RecordSystem(ctx context.Context, tx pgx.Tx, siteID int32, issueID int64, kind string, data map[string]any) error {
	return record(ctx, tx, siteID, issueID, time.Now(), nil, kind, data)
}

const (
	defaultTimelineLimit = 50
	maxTimelineLimit     = 200
)

// Timeline returns the issue's activity, newest first. A non-zero before
// returns only rows strictly older than it, so a page continues from the last
// row's At. limit defaults to 50 and is capped at 200.
func (s *Service) Timeline(ctx context.Context, siteID int32, issueID int64, limit int, before time.Time) ([]Activity, error) {
	if limit <= 0 {
		limit = defaultTimelineLimit
	}
	if limit > maxTimelineLimit {
		limit = maxTimelineLimit
	}
	if err := s.issueExists(ctx, siteID, issueID); err != nil {
		return nil, err
	}
	var bound any
	if !before.IsZero() {
		bound = before
	}
	rows, err := s.pool.Query(ctx, `select a.id, a.at, a.kind, a.actor_user_id, coalesce(u.name, ''), a.data
from app_issue_activity a left join users u on u.id = a.actor_user_id
where a.issue_id = $1 and a.site_id = $2 and ($3::timestamptz is null or a.at < $3)
order by a.at desc, a.id desc limit $4`, issueID, siteID, bound, limit)
	if err != nil {
		return nil, fmt.Errorf("read timeline: %w", err)
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.At, &a.Kind, &a.ActorUserID, &a.ActorName, &a.Data); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) issueExists(ctx context.Context, siteID int32, issueID int64) error {
	var ok bool
	if err := s.pool.QueryRow(ctx, `select exists (select 1 from app_issues where id = $1 and site_id = $2)`, issueID, siteID).Scan(&ok); err != nil {
		return fmt.Errorf("read issue: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}
