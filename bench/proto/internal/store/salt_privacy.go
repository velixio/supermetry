package store

import (
	"context"
	"time"
)

// PurgeSaltsBefore deletes persisted salts strictly older than cutoff.
// Taking the cutoff as data keeps retention behavior independent of the
// database server's wall clock and makes the 48-hour boundary testable.
func (s *Store) PurgeSaltsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM salts WHERE created_at < $1`, cutoff)
	return tag.RowsAffected(), err
}
