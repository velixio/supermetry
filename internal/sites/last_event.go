package sites

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
)

// LastEventAt reads the compact last-event projection. A missing projection
// row can only predate migration 0049 (or mean the site has never received an
// accepted event), so it is recovered once from data still inside the site's
// retention window. Future accepted events maintain the row in the batch
// writer transaction. A truly empty site remains absent and therefore Waiting.
func LastEventAt(ctx context.Context, pool *pgxpool.Pool, siteID int32, retention db.RetentionClass, now time.Time) (*time.Time, error) {
	var last time.Time
	err := pool.QueryRow(ctx, `select last_event_at from site_last_event where site_id=$1`, siteID).Scan(&last)
	if err == nil {
		return &last, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read site last event: %w", err)
	}

	var recovered *time.Time
	if retention == db.RetForever {
		err = pool.QueryRow(ctx, `
select max(ts) from events
where site_id=$1 and retention_class=$2`, siteID, retention).Scan(&recovered)
	} else {
		cutoff := now.AddDate(0, 0, -int(retention))
		err = pool.QueryRow(ctx, `
select max(ts) from events
where site_id=$1 and retention_class=$2 and ts >= $3`, siteID, retention, cutoff).Scan(&recovered)
	}
	if err != nil {
		return nil, fmt.Errorf("recover site last event: %w", err)
	}
	if recovered == nil {
		return nil, nil
	}
	if err := pool.QueryRow(ctx, `
insert into site_last_event(site_id,last_event_at) values($1,$2)
on conflict (site_id) do update set
last_event_at=greatest(site_last_event.last_event_at,excluded.last_event_at)
returning last_event_at`, siteID, *recovered).Scan(&last); err != nil {
		return nil, fmt.Errorf("store recovered site last event: %w", err)
	}
	return &last, nil
}
