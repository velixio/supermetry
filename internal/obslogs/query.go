package obslogs

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/obslogs/search"
)

// Limits of a search.
const (
	DefaultLimit   = 100
	MaxLimit       = 200
	MaxRange       = 30 * 24 * time.Hour
	dayQueryBudget = 5 * time.Second
	defaultDays    = 7
	traceWindow    = 24 * time.Hour
)

// ErrInvalid marks a request the caller can fix (bad range, limit or cursor).
var ErrInvalid = errors.New("invalid log query")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Cursor is the keyset position of the last row of a page.
type Cursor struct {
	TS  time.Time
	RID int64
}

// Encode is the opaque form of a cursor in URLs.
func (c Cursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.TS.UnixMicro(), 10) + "." + strconv.FormatInt(c.RID, 10)))
}

// ParseCursor reverses Encode.
func ParseCursor(s string) (*Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, invalid("cursor is invalid")
	}
	micro, rid, ok := strings.Cut(string(raw), ".")
	if !ok {
		return nil, invalid("cursor is invalid")
	}
	us, err1 := strconv.ParseInt(micro, 10, 64)
	r, err2 := strconv.ParseInt(rid, 10, 64)
	if err1 != nil || err2 != nil {
		return nil, invalid("cursor is invalid")
	}
	return &Cursor{TS: time.UnixMicro(us).UTC(), RID: r}, nil
}

// LogRow is one log record as the API and the MCP tools show it. It has no
// user hash: identity never leaves the table.
type LogRow struct {
	TS           time.Time      `json:"ts"`
	Severity     int16          `json:"severity"`
	SeverityText string         `json:"severity_text"`
	Service      string         `json:"service"`
	Environment  string         `json:"environment"`
	Release      string         `json:"release"`
	TraceID      string         `json:"trace_id,omitempty"`
	SpanID       string         `json:"span_id,omitempty"`
	Body         string         `json:"body"`
	Truncated    bool           `json:"truncated"`
	Attributes   map[string]any `json:"attributes"`

	rid int64
}

// Page is one page of a search; Next is nil on the last page.
type Page struct {
	Rows []LogRow
	Next *Cursor
}

// SeverityText names an OTLP severity number's band; 0 (unset) has no name.
func SeverityText(n int16) string {
	switch {
	case n >= 21:
		return "fatal"
	case n >= 17:
		return "error"
	case n >= 13:
		return "warn"
	case n >= 9:
		return "info"
	case n >= 5:
		return "debug"
	case n >= 1:
		return "trace"
	}
	return ""
}

// Retention is how many days of logs the site keeps visible (its
// log_retention_days setting, default 7). Partitions are shared by all sites,
// so rows older than this can still be stored; they are hidden here.
func Retention(ctx context.Context, pool *pgxpool.Pool, siteID int32) (time.Duration, error) {
	var days int
	err := pool.QueryRow(ctx, `select coalesce(nullif((settings->'app'->>'log_retention_days')::int, 0), $2) from sites where id = $1`, siteID, defaultDays).Scan(&days)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read log retention: %w", err)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

const rowColumns = `l.ts, l.rid, l.severity, l.service, l.environment, l.release, l.trace_id, l.span_id, l.body, l.truncated, l.attributes`

func scanRows(rows pgx.Rows) ([]LogRow, error) {
	defer rows.Close()
	var out []LogRow
	for rows.Next() {
		var r LogRow
		var trace, span []byte
		if err := rows.Scan(&r.TS, &r.rid, &r.Severity, &r.Service, &r.Environment, &r.Release, &trace, &span, &r.Body, &r.Truncated, &r.Attributes); err != nil {
			return nil, err
		}
		r.TS = r.TS.UTC()
		r.SeverityText = SeverityText(r.Severity)
		if trace != nil {
			r.TraceID = hex.EncodeToString(trace)
		}
		if span != nil {
			r.SpanID = hex.EncodeToString(span)
		}
		if r.Attributes == nil {
			r.Attributes = map[string]any{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Search returns the site's logs matching q between from (inclusive) and to
// (exclusive), newest first, one UTC day at a time: each day is a separate
// query over one partition that stops at the page size, so a common word is
// answered from the newest day and a rare one scans only as far as needed. from
// is clamped to now minus the site's retention; the window is at most 30 days
// and the limit at most 200 (0 means 100).
func Search(ctx context.Context, pool *pgxpool.Pool, siteID int32, now time.Time, q search.Query, from, to time.Time, limit int, cursor *Cursor) (Page, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 0 || limit > MaxLimit {
		return Page{}, invalid("limit must be between 1 and %d", MaxLimit)
	}
	if !from.Before(to) {
		return Page{}, invalid("from must be before to")
	}
	if to.Sub(from) > MaxRange {
		return Page{}, invalid("the time range is at most 30 days")
	}
	keep, err := Retention(ctx, pool, siteID)
	if err != nil {
		return Page{}, err
	}
	if floor := now.Add(-keep); from.Before(floor) {
		from = floor
	}
	if !from.Before(to) {
		return Page{}, nil
	}
	offset := 3
	if cursor != nil {
		offset = 5
	}
	where, args, err := search.Compile(q, offset)
	if err != nil {
		return Page{}, err
	}
	var rows []LogRow
	want := limit + 1 // one extra row says whether a next page exists
	for dayEnd := to; dayEnd.After(from) && len(rows) < want; {
		dayStart := dayEnd.UTC().Truncate(24 * time.Hour)
		if dayStart.Equal(dayEnd.UTC()) {
			dayStart = dayStart.Add(-24 * time.Hour)
		}
		lo := dayStart
		if lo.Before(from) {
			lo = from
		}
		sql := `select ` + rowColumns + ` from logs l where l.site_id = $1 and l.ts >= $2 and l.ts < $3 `
		params := []any{siteID, lo, dayEnd}
		if cursor != nil {
			sql += `and (l.ts, l.rid) < ($4, $5) `
			params = append(params, cursor.TS, cursor.RID)
		}
		sql += `and ` + where + ` order by l.ts desc, l.rid desc limit ` + strconv.Itoa(want-len(rows))
		params = append(params, args...)
		got, err := dayQuery(ctx, pool, sql, params)
		if err != nil {
			return Page{}, err
		}
		rows = append(rows, got...)
		dayEnd = dayStart
	}
	page := Page{Rows: rows}
	if len(rows) > limit {
		page.Rows = rows[:limit]
		last := page.Rows[limit-1]
		page.Next = &Cursor{TS: last.TS, RID: last.rid}
	}
	return page, nil
}

func dayQuery(ctx context.Context, pool *pgxpool.Pool, sql string, params []any) ([]LogRow, error) {
	ctx, cancel := context.WithTimeout(ctx, dayQueryBudget)
	defer cancel()
	rs, err := pool.Query(ctx, sql, params...)
	if err != nil {
		return nil, fmt.Errorf("search logs: %w", err)
	}
	out, err := scanRows(rs)
	if err != nil {
		return nil, fmt.Errorf("search logs: %w", err)
	}
	return out, nil
}

// TraceLogs returns the site's logs of one trace within a day of around, oldest
// first, hidden beyond the site's retention. It uses the partial trace index
// and prunes partitions by the time window.
func TraceLogs(ctx context.Context, pool *pgxpool.Pool, siteID int32, now time.Time, traceID []byte, around time.Time, limit int) ([]LogRow, error) {
	if len(traceID) != 16 {
		return nil, invalid("trace id must be 32 hex characters")
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 0 || limit > MaxLimit {
		return nil, invalid("limit must be between 1 and %d", MaxLimit)
	}
	keep, err := Retention(ctx, pool, siteID)
	if err != nil {
		return nil, err
	}
	lo, hi := around.Add(-traceWindow), around.Add(traceWindow)
	if floor := now.Add(-keep); lo.Before(floor) {
		lo = floor
	}
	if !lo.Before(hi) {
		return nil, nil
	}
	return dayQuery(ctx, pool, `select `+rowColumns+` from logs l where l.site_id = $1 and l.trace_id = $2 and l.ts >= $3 and l.ts <= $4 order by l.ts, l.rid limit `+strconv.Itoa(limit),
		[]any{siteID, traceID, lo, hi})
}

// Reader binds a pool and a clock; the API and MCP layers use it.
type Reader struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
}

func (r *Reader) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Search is the package function with the reader's pool and clock.
func (r *Reader) Search(ctx context.Context, siteID int32, q search.Query, from, to time.Time, limit int, cursor *Cursor) (Page, error) {
	return Search(ctx, r.Pool, siteID, r.now(), q, from, to, limit, cursor)
}

// TraceLogs is the package function with the reader's pool and clock.
func (r *Reader) TraceLogs(ctx context.Context, siteID int32, traceID []byte, around time.Time, limit int) ([]LogRow, error) {
	return TraceLogs(ctx, r.Pool, siteID, r.now(), traceID, around, limit)
}
