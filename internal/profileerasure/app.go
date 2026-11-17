package profileerasure

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EraseAppUser removes what an app project stores about one end user. The user
// is known only by the HMAC of their id under the app's hash secret, so the id
// is hashed here and matched against app_events.user_hash. Stored event bodies
// of that user's events are deleted (their detail may mention the user), then
// the hash is cleared from the event rows, which stay as anonymous counts.
// It runs in batches and is safe to repeat. It reports the bodies deleted and
// the event rows whose user_hash was cleared.
func EraseAppUser(ctx context.Context, pool *pgxpool.Pool, siteID int32, userID string, batch int) (bodies, events int64, err error) {
	if userID == "" {
		return 0, 0, errors.New("empty user id")
	}
	if batch <= 0 {
		batch = 1000
	}
	var secret []byte
	err = pool.QueryRow(ctx, `select secret from app_hash_secrets where site_id = $1`, siteID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil // no event was ever hashed for this app
	}
	if err != nil {
		return 0, 0, fmt.Errorf("read app hash secret: %w", err)
	}
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(userID))
	hash := h.Sum(nil)

	for {
		tag, err := pool.Exec(ctx, `
with target as (
  select b.tableoid, b.ctid from app_event_bodies b
  join app_events e on e.site_id = b.site_id and e.event_id = b.event_id and e.ts = b.ts
  where e.site_id = $1 and e.user_hash = $2
  limit $3
) delete from app_event_bodies b using target t where b.tableoid = t.tableoid and b.ctid = t.ctid`, siteID, hash, batch)
		if err != nil {
			return bodies, events, fmt.Errorf("delete app event bodies: %w", err)
		}
		bodies += tag.RowsAffected()
		if tag.RowsAffected() < int64(batch) {
			break
		}
	}
	for {
		tag, err := pool.Exec(ctx, `
with target as (
  select tableoid, ctid from app_events where site_id = $1 and user_hash = $2 limit $3
) update app_events e set user_hash = null from target t where e.tableoid = t.tableoid and e.ctid = t.ctid`, siteID, hash, batch)
		if err != nil {
			return bodies, events, fmt.Errorf("clear app event user hash: %w", err)
		}
		events += tag.RowsAffected()
		if tag.RowsAffected() < int64(batch) {
			break
		}
	}
	return bodies, events, nil
}
