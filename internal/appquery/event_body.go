package appquery

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// EventBody is the scrubbed full body of one stored event.
type EventBody struct {
	EventID string    `json:"event_id"`
	TS      time.Time `json:"ts"`
	// TraceID links the event to its trace (32 hex characters), when it has one.
	TraceID string          `json:"trace_id,omitempty"`
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
	var trace []byte
	err = q.pool.QueryRow(ctx, `select trace_id from app_events where site_id = $1 and event_id = $2 and ts = $3 limit 1`,
		scope.SiteID, eventID, e.TS).Scan(&trace)
	if err != nil && !isNoRows(err) {
		return EventBody{}, fmt.Errorf("read event trace: %w", err)
	}
	if len(trace) == 16 {
		e.TraceID = hex.EncodeToString(trace)
	}
	return e, nil
}
