package links

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrTrackingPixelNotFound = errors.New("tracking pixel not found")
	ErrTagTaken              = errors.New("tracking pixel tag is already in use")
)

type TrackingPixel struct {
	ID        int64     `json:"id"`
	SiteID    int32     `json:"site_id"`
	Name      string    `json:"name"`
	Tag       string    `json:"tag"`
	Total     int64     `json:"total"`
	Views30D  int64     `json:"views_30d"`
	CreatedAt time.Time `json:"created_at"`
}

const trackingPixelColumns = `tp.id, tp.site_id, tp.name, tp.tag, tp.created_at`

func scanTrackingPixel(row pgx.Row) (TrackingPixel, error) {
	var pixel TrackingPixel
	err := row.Scan(&pixel.ID, &pixel.SiteID, &pixel.Name, &pixel.Tag, &pixel.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrackingPixel{}, ErrTrackingPixelNotFound
	}
	return pixel, err
}

func scanTrackingPixelWithTotals(row pgx.Row) (TrackingPixel, error) {
	var pixel TrackingPixel
	err := row.Scan(&pixel.ID, &pixel.SiteID, &pixel.Name, &pixel.Tag, &pixel.CreatedAt, &pixel.Total, &pixel.Views30D)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrackingPixel{}, ErrTrackingPixelNotFound
	}
	return pixel, err
}

func mapTrackingPixelWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "tracking_pixels_site_id_tag_key" {
		return ErrTagTaken
	}
	return err
}

func CreateTrackingPixel(ctx context.Context, db DBTX, pixel TrackingPixel) (TrackingPixel, error) {
	created, err := scanTrackingPixel(db.QueryRow(ctx, `
		insert into tracking_pixels(site_id,name,tag) values ($1,$2,$3)
		returning id, site_id, name, tag, created_at`, pixel.SiteID, pixel.Name, pixel.Tag))
	return created, mapTrackingPixelWriteError(err)
}

func trackingPixelTotalsSQL() string {
	return `, coalesce((select sum(d.impressions) from pixel_impressions_daily d where d.site_id=tp.site_id and d.campaign=tp.tag),0)::bigint,
		coalesce((select sum(d.impressions) from pixel_impressions_daily d where d.site_id=tp.site_id and d.campaign=tp.tag and d.day between $2::date - 29 and $2::date),0)::bigint`
}

func GetTrackingPixel(ctx context.Context, db DBTX, id int64, today string) (TrackingPixel, error) {
	return scanTrackingPixelWithTotals(db.QueryRow(ctx, `select `+trackingPixelColumns+trackingPixelTotalsSQL()+`
		from tracking_pixels tp where tp.id=$1`, id, today))
}

func ListTrackingPixels(ctx context.Context, db DBTX, siteID int32, today string) ([]TrackingPixel, error) {
	rows, err := db.Query(ctx, `select `+trackingPixelColumns+trackingPixelTotalsSQL()+`
		from tracking_pixels tp where tp.site_id=$1 order by tp.id desc`, siteID, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TrackingPixel, 0)
	for rows.Next() {
		pixel, err := scanTrackingPixelWithTotals(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, pixel)
	}
	return result, rows.Err()
}

func RenameTrackingPixel(ctx context.Context, db DBTX, id int64, name string) (TrackingPixel, error) {
	return scanTrackingPixel(db.QueryRow(ctx, `update tracking_pixels set name=$2 where id=$1 returning id, site_id, name, tag, created_at`, id, name))
}

func DeleteTrackingPixel(ctx context.Context, db DBTX, id int64) error {
	var deleted int
	err := db.QueryRow(ctx, `with target as (
		select site_id, tag from tracking_pixels where id=$1
	), impressions as (
		delete from pixel_impressions_daily d using target t where d.site_id=t.site_id and d.campaign=t.tag
	), registration as (
		delete from tracking_pixels tp using target t where tp.id=$1 returning 1
	) select count(*) from registration`, id).Scan(&deleted)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrTrackingPixelNotFound
	}
	return nil
}
