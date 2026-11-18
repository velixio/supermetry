package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"supermetry/internal/sites"
	"supermetry/internal/spans"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PerfSource reads closed hours of the span rollup (span_hourly).
type PerfSource interface {
	// Hourly sums the transaction (segment) rows whose local hour is in
	// [fromLocalHour, toLocalHour); the times carry the site's wall clock.
	// Transaction "" means every transaction.
	Hourly(ctx context.Context, siteID int32, fromLocalHour, toLocalHour time.Time, transaction string, f Filters) (count, errors int64, hist map[int16]int64, err error)
}

// MetricStore implements RuleMetricSource on Postgres.
type MetricStore struct {
	pool  *pgxpool.Pool
	perf  PerfSource
	sites sites.Registry
}

// NewMetricStore returns the Postgres-backed metric source. perf may be nil,
// which makes the transaction metrics fail rather than guess.
func NewMetricStore(pool *pgxpool.Pool, perf PerfSource, registry sites.Registry) *MetricStore {
	return &MetricStore{pool: pool, perf: perf, sites: registry}
}

var _ RuleMetricSource = (*MetricStore)(nil)

// errorKinds are the event kinds the error_count metric counts.
var errorKinds = []string{"error", "crash", "hang"}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (s *MetricStore) ErrorCount(ctx context.Context, siteID int32, from, to time.Time, f Filters) (float64, error) {
	kinds := errorKinds
	if len(f.Kinds) > 0 {
		kinds = f.Kinds
	}
	var n int64
	err := s.pool.QueryRow(ctx, `select count(*) from app_events
		where site_id=$1 and ts >= $2 and ts < $3 and kind = any($4)
		  and (cardinality($5::text[]) = 0 or environment = any($5))
		  and (cardinality($6::text[]) = 0 or release = any($6))
		  and (cardinality($7::text[]) = 0 or platform = any($7))
		  and (cardinality($8::text[]) = 0 or level = any($8))`,
		siteID, from, to, kinds, orEmpty(f.Environments), orEmpty(f.Releases), orEmpty(f.Platforms), orEmpty(f.Levels)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count error events: %w", err)
	}
	return float64(n), nil
}

func (s *MetricStore) CrashFree(ctx context.Context, siteID int32, from, to time.Time, f Filters, users bool) (float64, int64, error) {
	unit, bad := `*`, `count(*) filter (where status in ('crashed','abnormal'))`
	if users {
		unit = `distinct install_hash`
		bad = `count(distinct install_hash) filter (where status in ('crashed','abnormal'))`
	}
	var total, crashed int64
	err := s.pool.QueryRow(ctx, `select count(`+unit+`), `+bad+` from app_sessions
		where site_id=$1 and started >= $2 and started < $3
		  and (cardinality($4::text[]) = 0 or environment = any($4))
		  and (cardinality($5::text[]) = 0 or release = any($5))
		  and (cardinality($6::text[]) = 0 or platform = any($6))`,
		siteID, from, to, orEmpty(f.Environments), orEmpty(f.Releases), orEmpty(f.Platforms)).Scan(&total, &crashed)
	if err != nil {
		return 0, 0, fmt.Errorf("count sessions: %w", err)
	}
	if total == 0 {
		return 1, 0, nil
	}
	return 1 - float64(crashed)/float64(total), total, nil
}

// wallClock turns an instant into the naive local-hour value of span_hourly.
func wallClock(t time.Time, tz *time.Location) time.Time {
	l := t.In(tz)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), 0, 0, time.UTC)
}

var percentileOf = map[string]float64{
	"transaction_p50": 0.50, "transaction_p75": 0.75, "transaction_p95": 0.95, "transaction_p99": 0.99,
}

func (s *MetricStore) Perf(ctx context.Context, siteID int32, from, to time.Time, transaction string, f Filters, name string) (float64, int64, error) {
	if s.perf == nil {
		return 0, 0, errors.New("performance source is unavailable")
	}
	site, ok := s.sites.Site(siteID)
	if !ok || site.TZ == nil {
		return 0, 0, fmt.Errorf("site %d is unavailable", siteID)
	}
	count, errs, hist, err := s.perf.Hourly(ctx, siteID, wallClock(from, site.TZ), wallClock(to, site.TZ), transaction, f)
	if err != nil {
		return 0, 0, err
	}
	switch name {
	case MetricTransactionCount:
		return float64(count), count, nil
	case MetricTransactionFailRate:
		if count == 0 {
			return 0, 0, nil
		}
		return float64(errs) / float64(count) * 100, count, nil
	}
	p, ok := percentileOf[name]
	if !ok {
		return 0, 0, fmt.Errorf("unknown transaction metric %q", name)
	}
	return spans.Percentiles(hist, p)[0] / 1000, count, nil
}

// PerfStore implements PerfSource over span_hourly.
type PerfStore struct{ pool *pgxpool.Pool }

// NewPerfStore returns the span_hourly reader.
func NewPerfStore(pool *pgxpool.Pool) *PerfStore { return &PerfStore{pool: pool} }

var _ PerfSource = (*PerfStore)(nil)

const perfWhere = ` h.site_id=$1 and h.local_hour >= $2 and h.local_hour < $3 and h.is_segment
	and ($4 = '' or h.name = $4)
	and (cardinality($5::text[]) = 0 or h.environment = any($5))
	and (cardinality($6::text[]) = 0 or h.release = any($6))
	and (cardinality($7::text[]) = 0 or h.platform = any($7))
	and exists (select 1 from span_hour_state st where st.site_id = h.site_id and st.local_hour = h.local_hour and st.status = 'done')`

func (s *PerfStore) Hourly(ctx context.Context, siteID int32, from, to time.Time, transaction string, f Filters) (int64, int64, map[int16]int64, error) {
	args := []any{siteID, from, to, transaction, orEmpty(f.Environments), orEmpty(f.Releases), orEmpty(f.Platforms)}
	var count, errs int64
	if err := s.pool.QueryRow(ctx, `select coalesce(sum(h.count),0)::bigint, coalesce(sum(h.errors),0)::bigint from span_hourly h where`+perfWhere, args...).Scan(&count, &errs); err != nil {
		return 0, 0, nil, fmt.Errorf("sum span hours: %w", err)
	}
	rows, err := s.pool.Query(ctx, `select u.b, sum(u.c)::bigint from span_hourly h, unnest(h.buckets, h.bucket_counts) as u(b, c) where`+perfWhere+` group by u.b`, args...)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("sum span histogram: %w", err)
	}
	defer rows.Close()
	hist := map[int16]int64{}
	for rows.Next() {
		var b int16
		var c int64
		if err := rows.Scan(&b, &c); err != nil {
			return 0, 0, nil, err
		}
		hist[b] = c
	}
	return count, errs, hist, rows.Err()
}
