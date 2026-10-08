package imports

import (
	"context"
	"errors"
	"testing"
	"time"

	"panel/internal/db"
	"panel/internal/testpg"
)

func TestStoreLifecyclePersistsAggregate(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	created, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "events.csv", CreatedBy: userID})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != StatusPending || created.Source != SourceUmami || created.FileName != "events.csv" {
		t.Fatalf("created = %+v", created)
	}
	if err := store.SetRunning(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	day := date(t, "2026-01-03")
	aggregate := newAggregate()
	aggregate.Days[day] = Metrics{Visitors: 2, Visits: 3, Pageviews: 4, Bounces: 1, VisitDurationS: 9}
	aggregate.Dimensions[DimensionKey{Day: day, Dim: DimPage, Value: "/"}] = Metrics{Visitors: 2, Visits: 3, Pageviews: 4, Events: 5}
	if err := store.Complete(ctx, created.ID, aggregate, ParseStats{Rows: 8, BadRows: 2}); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted || got.Rows != 8 || got.DateFrom == nil || !got.DateFrom.Equal(day) || got.DateTo == nil || !got.DateTo.Equal(day) || got.CompletedAt == nil {
		t.Fatalf("completed = %+v", got)
	}
	if got.Error == nil || *got.Error != "2 invalid rows skipped" {
		t.Fatalf("completion warning = %v", got.Error)
	}
	var visitors, events int64
	if err := store.pool.QueryRow(ctx, `select visitors from imported_day where import_id=$1`, created.ID).Scan(&visitors); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `select events from imported_day_dim where import_id=$1`, created.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if visitors != 2 || events != 5 {
		t.Fatalf("persisted visitors=%d events=%d", visitors, events)
	}
}

func TestStoreListAndDeleteAreSiteScoped(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	first, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourcePlausible, FileName: "one.zip", CreatedBy: userID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "two.csv", CreatedBy: userID})
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, siteID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("list = %+v", list)
	}
	deleted, err := store.Delete(ctx, first.ID)
	if err != nil || !deleted {
		t.Fatalf("Delete = %t, %v", deleted, err)
	}
	if _, err := store.Get(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted error = %v", err)
	}
	deleted, err = store.Delete(ctx, first.ID)
	if err != nil || deleted {
		t.Fatalf("second Delete = %t, %v", deleted, err)
	}
}

func TestStoreFailureEndsImport(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	created, err := store.Create(context.Background(), CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "bad.csv", CreatedBy: userID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFailed(context.Background(), created.ID, errors.New("invalid archive")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.CompletedAt == nil || got.Error == nil || *got.Error != "invalid archive" {
		t.Fatalf("failed = %+v", got)
	}
}

func newImportStore(t *testing.T) (*Store, int32, int64) {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	var siteID int32
	if err := pool.QueryRow(context.Background(), `insert into users(email,name,password_hash) values ($1,'Importer','hash') returning id`, "importer-"+time.Now().Format("150405.000000000")+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `insert into orgs(name,slug) values ('Imports',$1) returning id`, "imports-"+time.Now().Format("150405.000000000")).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `insert into sites(org_id,public_id,name,timezone,retention_class) values ($1,'importsite01','Imports','UTC',30) returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return NewStore(pool), siteID, userID
}
