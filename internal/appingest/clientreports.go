package appingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultReportFlush = 30 * time.Second
	maxReportKeys      = 20_000
)

type reportKey struct {
	siteID           int32
	day              time.Time
	reason, category string
}

// ClientReports aggregates the SDKs' discarded-event counts in memory and
// upserts them into app_client_reports on an interval: no database write per
// request. A flush that fails keeps the counts for the next one.
type ClientReports struct {
	pool       *pgxpool.Pool
	flushEvery time.Duration

	mu      sync.Mutex
	pending map[reportKey]int64
}

// NewClientReports builds an aggregator; flushEvery 0 uses 30 seconds.
func NewClientReports(pool *pgxpool.Pool, flushEvery time.Duration) *ClientReports {
	if flushEvery <= 0 {
		flushEvery = defaultReportFlush
	}
	return &ClientReports{pool: pool, flushEvery: flushEvery, pending: map[reportKey]int64{}}
}

// Add counts quantity discarded events for the site's local day. The number of
// buffered keys is bounded; counts for new keys beyond it are dropped.
func (c *ClientReports) Add(siteID int32, day time.Time, reason, category string, quantity int64) {
	if c == nil || quantity < 1 || siteID <= 0 {
		return
	}
	k := reportKey{siteID, time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), reason, category}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pending[k]; !ok && len(c.pending) >= maxReportKeys {
		return
	}
	c.pending[k] = min(c.pending[k]+quantity, 1_000_000_000_000)
}

// Flush upserts and adds the buffered counts in one transaction.
func (c *ClientReports) Flush(ctx context.Context) error {
	c.mu.Lock()
	taken := c.pending
	c.pending = map[reportKey]int64{}
	c.mu.Unlock()
	if len(taken) == 0 {
		return nil
	}
	err := pgx.BeginFunc(ctx, c.pool, func(tx pgx.Tx) error {
		ids := map[int32]struct{}{}
		for k := range taken {
			ids[k.siteID] = struct{}{}
		}
		list := make([]int32, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		rows, err := tx.Query(ctx, `select id from sites where id = any($1)`, list)
		if err != nil {
			return err
		}
		live := map[int32]struct{}{}
		for rows.Next() {
			var id int32
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			live[id] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for k, q := range taken {
			if _, ok := live[k.siteID]; !ok {
				continue
			}
			batch.Queue(`insert into app_client_reports(site_id, day, reason, category, quantity) values ($1,$2,$3,$4,$5)
on conflict (site_id, day, reason, category) do update set quantity = app_client_reports.quantity + excluded.quantity`,
				k.siteID, k.day, k.reason, k.category, q)
		}
		return tx.SendBatch(ctx, batch).Close()
	})
	if err != nil {
		c.mu.Lock()
		for k, q := range taken {
			c.pending[k] += q
		}
		c.mu.Unlock()
		return fmt.Errorf("flush client reports: %w", err)
	}
	return nil
}

// Run flushes periodically and once more when ctx ends.
func (c *ClientReports) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := c.Flush(final); err != nil {
				slog.Error("client report final flush failed", "error", err)
			}
			return nil
		case <-ticker.C:
			if err := c.Flush(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("client report flush failed, keeping the counts", "error", err)
			}
		}
	}
}
