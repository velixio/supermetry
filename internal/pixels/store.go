// Package pixels serves view-counting images for pixel analytics projects.
package pixels

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const MaxPerSite = 100

var (
	ErrNotFound = errors.New("pixel not found")
	ErrLimit    = errors.New("pixel limit reached")
)

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type Pixel struct {
	ID         int64     `json:"id"`
	SiteID     int32     `json:"-"`
	PublicID   string    `json:"public_id"`
	Name       string    `json:"name"`
	BadgeLabel string    `json:"badge_label"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func NewPublicID() (string, error) {
	raw := make([]byte, 11)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, 12)
	out[0] = 'p'
	for i, b := range raw {
		out[i+1] = idAlphabet[int(b)%len(idAlphabet)]
	}
	return string(out), nil
}

const columns = `id, site_id, public_id, name, badge_label, enabled, created_at`

func scan(row pgx.Row) (Pixel, error) {
	var p Pixel
	err := row.Scan(&p.ID, &p.SiteID, &p.PublicID, &p.Name, &p.BadgeLabel, &p.Enabled, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pixel{}, ErrNotFound
	}
	return p, err
}

func Create(ctx context.Context, db DBTX, p Pixel) (Pixel, error) {
	if p.BadgeLabel == "" {
		p.BadgeLabel = "views"
	}
	if _, err := db.Exec(ctx, `select 1 from sites where id=$1 for update`, p.SiteID); err != nil {
		return Pixel{}, err
	}
	for range 3 {
		id, err := NewPublicID()
		if err != nil {
			return Pixel{}, err
		}
		out, err := scan(db.QueryRow(ctx, `
			insert into pixels(site_id, public_id, name, badge_label, enabled)
			select $1, $2, $3, $4, $5
			where (select count(*) from pixels where site_id=$1) < $6
			on conflict (public_id) do nothing
			returning `+columns, p.SiteID, id, p.Name, p.BadgeLabel, p.Enabled, MaxPerSite))
		if errors.Is(err, ErrNotFound) {
			var n int
			if err := db.QueryRow(ctx, `select count(*) from pixels where site_id=$1`, p.SiteID).Scan(&n); err != nil {
				return Pixel{}, err
			}
			if n >= MaxPerSite {
				return Pixel{}, ErrLimit
			}
			continue
		}
		return out, err
	}
	return Pixel{}, fmt.Errorf("allocate pixel public id: too many collisions")
}

func Update(ctx context.Context, db DBTX, p Pixel) (Pixel, error) {
	return scan(db.QueryRow(ctx, `
		update pixels set name=$3, badge_label=$4, enabled=$5
		where id=$1 and site_id=$2 returning `+columns,
		p.ID, p.SiteID, p.Name, p.BadgeLabel, p.Enabled))
}

func Delete(ctx context.Context, db DBTX, siteID int32, id int64) error {
	tag, err := db.Exec(ctx, `delete from pixels where id=$1 and site_id=$2`, id, siteID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func list(ctx context.Context, db DBTX, query string, args ...any) ([]Pixel, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Pixel, error) { return scan(row) })
}

func List(ctx context.Context, db DBTX, siteID int32) ([]Pixel, error) {
	return list(ctx, db, `select `+columns+` from pixels where site_id=$1 order by id`, siteID)
}

func All(ctx context.Context, db DBTX) ([]Pixel, error) {
	return list(ctx, db, `
		select p.id, p.site_id, p.public_id, p.name, p.badge_label, p.enabled, p.created_at
		from pixels p join sites s on s.id = p.site_id
		where s.deleted_at is null and s.kind = 'pixel' order by p.id`)
}

func Totals(ctx context.Context, db DBTX) (map[int64]int64, error) {
	rows, err := db.Query(ctx, `select id, total_views from pixels`)
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

// RecentDayViews returns per-pixel view counts by day since the given date.
func RecentDayViews(ctx context.Context, db DBTX, since string) (map[int64]map[string]int64, error) {
	rows, err := db.Query(ctx, `select pixel_id, day::text, sum(views)::bigint from pixel_views_daily
		where day >= $1::date group by pixel_id, day`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]int64{}
	for rows.Next() {
		var id, n int64
		var day string
		if err := rows.Scan(&id, &day, &n); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]int64{}
		}
		out[id][day] = n
	}
	return out, rows.Err()
}
