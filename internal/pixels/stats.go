package pixels

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type DayViews struct {
	Day    string `json:"day"`
	Proxy  int64  `json:"proxy"`
	Direct int64  `json:"direct"`
}

type PixelViews struct {
	PixelID int64 `json:"pixel_id"`
	Views   int64 `json:"views"`
}

type CountryViews struct {
	Country string `json:"country"`
	Views   int64  `json:"views"`
}

// Summary counts views for today, the last 7 and 30 days (including today) and all time.
type Summary struct {
	Today  int64 `json:"today"`
	Last7  int64 `json:"last_7"`
	Last30 int64 `json:"last_30"`
	Total  int64 `json:"total"`
}

type Stats struct {
	Daily     []DayViews     `json:"daily"`
	Pixels    []PixelViews   `json:"pixels"`
	Countries []CountryViews `json:"countries"`
}

const scope = `from pixel_views_daily v join pixels p on p.id = v.pixel_id
	where p.site_id = $1 and v.day between $2::date and $3::date and ($4 = 0 or v.pixel_id = $4)`

func StatsFor(ctx context.Context, db DBTX, siteID int32, from, to string, pixelID int64) (Stats, error) {
	var s Stats
	var err error
	args := []any{siteID, from, to, pixelID}
	if s.Daily, err = collect(ctx, db, `select v.day::text,
		coalesce(sum(v.views) filter (where v.via='proxy'),0)::bigint,
		coalesce(sum(v.views) filter (where v.via='direct'),0)::bigint `+scope+` group by v.day order by v.day`,
		args, func(r pgx.CollectableRow) (DayViews, error) {
			var d DayViews
			return d, r.Scan(&d.Day, &d.Proxy, &d.Direct)
		}); err != nil {
		return Stats{}, err
	}
	if s.Pixels, err = collect(ctx, db, `select v.pixel_id, sum(v.views)::bigint `+scope+` group by v.pixel_id order by v.pixel_id`,
		args, func(r pgx.CollectableRow) (PixelViews, error) {
			var p PixelViews
			return p, r.Scan(&p.PixelID, &p.Views)
		}); err != nil {
		return Stats{}, err
	}
	if s.Countries, err = collect(ctx, db, `select v.country, sum(v.views)::bigint `+scope+` and v.via='direct' and v.country <> ''
		group by v.country order by 2 desc, 1 limit 250`,
		args, func(r pgx.CollectableRow) (CountryViews, error) {
			var c CountryViews
			return c, r.Scan(&c.Country, &c.Views)
		}); err != nil {
		return Stats{}, err
	}
	return s, nil
}

func collect[T any](ctx context.Context, db DBTX, q string, args []any, fn pgx.RowToFunc[T]) ([]T, error) {
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, fn)
	if out == nil {
		out = []T{}
	}
	return out, err
}

func Views30(ctx context.Context, db DBTX, siteID int32, today string) (map[int64]int64, error) {
	rows, err := db.Query(ctx, `select v.pixel_id, sum(v.views)::bigint from pixel_views_daily v join pixels p on p.id=v.pixel_id
		where p.site_id=$1 and v.day > $2::date - 30 and v.day <= $2::date group by v.pixel_id`, siteID, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// SummaryFor totals views for a project, or for one pixel when pixelID is not 0,
// counting days up to and including today (a site-local YYYY-MM-DD).
func SummaryFor(ctx context.Context, db DBTX, siteID int32, today string, pixelID int64) (Summary, error) {
	var s Summary
	err := db.QueryRow(ctx, `select
		coalesce(sum(v.views) filter (where v.day = $2::date),0)::bigint,
		coalesce(sum(v.views) filter (where v.day > $2::date - 7),0)::bigint,
		coalesce(sum(v.views) filter (where v.day > $2::date - 30),0)::bigint,
		coalesce(sum(v.views),0)::bigint
		from pixel_views_daily v join pixels p on p.id = v.pixel_id
		where p.site_id = $1 and v.day <= $2::date and ($3 = 0 or v.pixel_id = $3)`, siteID, today, pixelID).
		Scan(&s.Today, &s.Last7, &s.Last30, &s.Total)
	return s, err
}
