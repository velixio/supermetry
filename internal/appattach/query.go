package appattach

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means the attachment does not exist for the site, or has aged
// out with its event body.
var ErrNotFound = errors.New("attachment not found")

// Stored is a stored attachment.
type Stored struct {
	Name        string
	ContentType string
	Data        []byte
}

// Get reads one attachment of an event of the site. The site id comes from
// the authorised path, so another site's attachment is never reachable.
func Get(ctx context.Context, pool *pgxpool.Pool, siteID int32, eventID string, idx int) (Stored, error) {
	if idx < 0 || idx >= MaxPerEvent {
		return Stored{}, ErrNotFound
	}
	var s Stored
	err := pool.QueryRow(ctx, `select name, content_type, data from app_event_attachments
where site_id = $1 and event_id = $2 and idx = $3 order by ts desc limit 1`, siteID, eventID, int16(idx)).Scan(&s.Name, &s.ContentType, &s.Data)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stored{}, ErrNotFound
	}
	if err != nil {
		return Stored{}, fmt.Errorf("read attachment: %w", err)
	}
	return s, nil
}
