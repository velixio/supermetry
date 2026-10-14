package links

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestTrackingPixelStoreCRUDTotalsAndSelectiveDelete(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()

	created, err := CreateTrackingPixel(ctx, pool, TrackingPixel{SiteID: siteID, Name: "Newsletter", Tag: "news.v1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateTrackingPixel(ctx, pool, TrackingPixel{SiteID: siteID, Name: "Receipt", Tag: "receipt"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.Name != "Newsletter" || created.Tag != "news.v1" {
		t.Fatalf("created = %+v", created)
	}
	if _, err := CreateTrackingPixel(ctx, pool, TrackingPixel{SiteID: siteID, Name: "Duplicate", Tag: "news.v1"}); !errors.Is(err, ErrTagTaken) {
		t.Fatalf("duplicate tag error = %v", err)
	}

	if _, err := pool.Exec(ctx, `insert into pixel_impressions_daily(site_id,day,campaign,country,impressions) values
		($1,'2026-09-01','news.v1','US',11),
		($1,'2026-09-13','news.v1','US',3),
		($1,'2026-10-11','news.v1','US',4),
		($1,'2026-10-12','news.v1','NP',2),
		($1,'2026-10-12','receipt','NP',7)`, siteID); err != nil {
		t.Fatal(err)
	}

	got, err := GetTrackingPixel(ctx, pool, created.ID, "2026-10-12")
	if err != nil || got.Total != 20 || got.Views30D != 9 {
		t.Fatalf("get = %+v, %v; want total=20 views_30d=9", got, err)
	}
	listed, err := ListTrackingPixels(ctx, pool, siteID, "2026-10-12")
	if err != nil || len(listed) != 2 || listed[0].ID != second.ID || listed[1].Total != 20 || listed[1].Views30D != 9 {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	updated, err := RenameTrackingPixel(ctx, pool, created.ID, "Weekly news")
	if err != nil || updated.Name != "Weekly news" || updated.Tag != "news.v1" {
		t.Fatalf("rename = %+v, %v", updated, err)
	}

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return DeleteTrackingPixel(ctx, tx, created.ID) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetTrackingPixel(ctx, pool, created.ID, "2026-10-12"); !errors.Is(err, ErrTrackingPixelNotFound) {
		t.Fatalf("get deleted error = %v", err)
	}
	var newsRows, receiptRows int
	if err := pool.QueryRow(ctx, `select count(*) filter (where campaign='news.v1'), count(*) filter (where campaign='receipt') from pixel_impressions_daily where site_id=$1`, siteID).Scan(&newsRows, &receiptRows); err != nil {
		t.Fatal(err)
	}
	if newsRows != 0 || receiptRows != 1 {
		t.Fatalf("impression rows after delete: news=%d receipt=%d", newsRows, receiptRows)
	}
}
