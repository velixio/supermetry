package store

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

var rawEventPart = regexp.MustCompile(`^events_(d(\d{8})|p(\d{4})_(\d{2}))$`)

// DropRawEventPartitions drops complete raw-event partitions whose exclusive
// upper bound is at or before cutoff. Closed-tier tables and rollups are not
// touched. Retention is intentionally DROP TABLE (which detaches and removes
// a partition), never row DELETE.
func (s *Store) DropRawEventPartitions(ctx context.Context, cutoff time.Time) ([]string, error) {
	cutoff = cutoff.UTC()
	rows, err := s.Pool.Query(ctx, `SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'events'::regclass`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var dropped []string
	for _, name := range names {
		m := rawEventPart.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		var upper time.Time
		if m[2] != "" {
			from, parseErr := time.Parse("20060102", m[2])
			if parseErr != nil {
				return dropped, parseErr
			}
			upper = from.AddDate(0, 0, 1)
		} else {
			from, parseErr := time.Parse("2006_01", m[3]+"_"+m[4])
			if parseErr != nil {
				return dropped, parseErr
			}
			upper = from.AddDate(0, 1, 0)
		}
		if upper.After(cutoff) {
			continue
		}
		if _, err := s.Pool.Exec(ctx, `DROP TABLE `+pgx.Identifier{name}.Sanitize()); err != nil {
			return dropped, fmt.Errorf("drop raw partition %s: %w", name, err)
		}
		s.mu.Lock()
		delete(s.parts, name)
		s.mu.Unlock()
		dropped = append(dropped, name)
	}
	return dropped, nil
}
