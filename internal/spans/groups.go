package spans

import (
	"container/list"
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

const seenGroupsMax = 100_000

// groupKey identifies a span group of a site.
type groupKey struct {
	site int32
	hash int64
}

// seenGroups is a small LRU of the groups already written to span_groups, so
// the writer inserts each group once instead of once per batch. Only the
// writer goroutine uses it.
type seenGroups struct {
	max   int
	order *list.List
	items map[groupKey]*list.Element
}

func newSeenGroups(max int) *seenGroups {
	return &seenGroups{max: max, order: list.New(), items: map[groupKey]*list.Element{}}
}

func (s *seenGroups) has(k groupKey) bool {
	e, ok := s.items[k]
	if ok {
		s.order.MoveToFront(e)
	}
	return ok
}

func (s *seenGroups) add(k groupKey) {
	if e, ok := s.items[k]; ok {
		s.order.MoveToFront(e)
		return
	}
	s.items[k] = s.order.PushFront(k)
	if s.order.Len() > s.max {
		oldest := s.order.Back()
		s.order.Remove(oldest)
		delete(s.items, oldest.Value.(groupKey))
	}
}

// upsertGroups records the (op, description) of every new child-span group in
// span_groups, once per group (the database ignores repeats). A failure is
// logged and retried with the next batch: the groups stay unseen.
func (w *Writer) upsertGroups(ctx context.Context, batch []Span) {
	if w.pool == nil {
		return
	}
	if w.seen == nil {
		w.seen = newSeenGroups(seenGroupsMax)
	}
	type group struct{ op, description string }
	fresh := map[groupKey]group{}
	for i := range batch {
		s := &batch[i]
		if s.IsSegment {
			continue
		}
		k := groupKey{s.SiteID, GroupHash(s.Op, s.Description)}
		if _, dup := fresh[k]; dup || w.seen.has(k) {
			continue
		}
		fresh[k] = group{s.Op, s.Description}
	}
	if len(fresh) == 0 {
		return
	}
	b := &pgx.Batch{}
	keys := make([]groupKey, 0, len(fresh))
	for k, g := range fresh {
		b.Queue(`insert into span_groups (site_id, group_hash, op, description) values ($1, $2, $3, $4)
on conflict (site_id, group_hash) do nothing`, k.site, k.hash, g.op, g.description)
		keys = append(keys, k)
	}
	res := w.pool.SendBatch(ctx, b)
	defer res.Close()
	for _, k := range keys {
		if _, err := res.Exec(); err != nil {
			slog.Warn("span group upsert failed", "site", k.site, "error", err)
			return
		}
		w.seen.add(k)
	}
}
