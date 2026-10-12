package imports

import (
	"context"
	"crypto/sha256"
	"testing"

	"supermetry/internal/db"
)

func completeGA4Fixture(t *testing.T, store *Store, siteID int32, userID int64, fixture string) Import {
	t.Helper()
	ctx := context.Background()
	aggregate, stats := parseGA4Fixture(t, fixture)
	created, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceGA4, FileName: fixture, CreatedBy: userID, FileHash: sha256.Sum256([]byte(fixture))})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunning(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, created.ID, aggregate, stats); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGA4ImportPersistsRangeWarningAndUndoRemovesRows(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	done := completeGA4Fixture(t, store, siteID, userID, "ga4_pages_sources.csv")
	if done.Source != SourceGA4 || done.Status != StatusCompleted || done.Rows != 3 ||
		done.DateFrom == nil || done.DateFrom.Format("2006-01-02") != "2024-03-01" || done.DateTo.Format("2006-01-02") != "2024-03-02" {
		t.Fatalf("import = %+v", done)
	}
	if done.Error == nil || *done.Error != "2 invalid rows skipped (first: row 6: invalid date \"2024-0303\")" {
		t.Fatalf("warning = %v", done.Error)
	}
	var pageviews, dims int64
	if err := store.pool.QueryRow(ctx, `select coalesce(sum(pageviews),0) from imported_day where import_id=$1`, done.ID).Scan(&pageviews); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `select count(*) from imported_day_dim where import_id=$1`, done.ID).Scan(&dims); err != nil {
		t.Fatal(err)
	}
	// 3 page, 3 source, 2 country (one "(not set)" dropped), 3 device rows.
	if pageviews != 34 || dims != 3+3+2+3 {
		t.Fatalf("pageviews=%d dims=%d", pageviews, dims)
	}
	deleted, err := store.Delete(ctx, done.ID)
	if err != nil || !deleted {
		t.Fatalf("Delete = (%t,%v)", deleted, err)
	}
	var left int64
	if err := store.pool.QueryRow(ctx, `select (select count(*) from imported_day)+(select count(*) from imported_day_dim)`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d imported rows left after undo", left)
	}
}

func TestGA4PreviewReportsOverlapLiveDaysAndSameFile(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	first := completeGA4Fixture(t, store, siteID, userID, "ga4_daily.csv")
	aggregate, _ := parseGA4Fixture(t, "ga4_daily.csv")
	if err := db.EnsurePartitions(ctx, store.pool, date(t, "2024-03-01"), date(t, "2024-03-05")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `insert into rollup_day(site_id,day,visitors,visits,pageviews,bounces,duration_ms,engagement_ms,scroll_sum,scroll_n)
		values ($1,'2024-03-03',1,1,1,0,0,0,0,0)`, siteID); err != nil {
		t.Fatal(err)
	}
	overlap, err := store.Preview(ctx, siteID, sha256.Sum256([]byte("ga4_daily.csv")), aggregate)
	if err != nil {
		t.Fatal(err)
	}
	if overlap.ImportedDays != 3 || overlap.LiveDays != 1 || overlap.SameFile == nil || overlap.SameFile.ImportID != first.ID {
		t.Fatalf("overlap = %+v", overlap)
	}
	other, err := store.Preview(ctx, siteID, sha256.Sum256([]byte("other")), aggregate)
	if err != nil || other.SameFile != nil {
		t.Fatalf("other = %+v err=%v", other, err)
	}
	fresh, _ := parseGA4Fixture(t, "ga4_export_style.csv")
	none, err := store.Preview(ctx, siteID, [32]byte{}, fresh)
	if err != nil || none.ImportedDays != 1 || none.LiveDays != 0 {
		// 2024-03-01 is covered by the daily import; 2024-02-29 is new.
		t.Fatalf("none = %+v err=%v", none, err)
	}
}
