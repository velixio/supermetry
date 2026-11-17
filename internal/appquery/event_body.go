package appquery

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EventBody is the scrubbed full body of one stored event.
type EventBody struct {
	EventID string          `json:"event_id"`
	TS      time.Time       `json:"ts"`
	Body    json.RawMessage `json:"body"`
}

// Event returns the scrubbed body of an event of the site, or ErrNotFound when
// the event does not exist for the site or its body has aged out (bodies are
// kept for 30 days).
func (q *Querier) Event(ctx context.Context, scope Scope, eventID string) (EventBody, error) {
	var e EventBody
	err := q.pool.QueryRow(ctx, `select event_id, ts, body from app_event_bodies where site_id = $1 and event_id = $2 order by ts desc limit 1`,
		scope.SiteID, eventID).Scan(&e.EventID, &e.TS, &e.Body)
	if err != nil {
		if isNoRows(err) {
			return EventBody{}, ErrNotFound
		}
		return EventBody{}, fmt.Errorf("read event body: %w", err)
	}
	return e, nil
}
