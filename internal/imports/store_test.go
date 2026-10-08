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

func TestStoreCompleteAndDeleteAdvanceCloseVersionAndInvalidate(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	day := date(t, "2026-01-03")
	if _, err := store.pool.Exec(ctx, `insert into day_close(site_id,day,status,version) values ($1,$2,'done',1)`, siteID, day); err != nil {
		t.Fatal(err)
	}
	invalidated := make(chan int32, 2)
	store.invalidateSite = func(got int32) { invalidated <- got }
	created, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "events.csv", CreatedBy: userID})
	if err != nil {
		t.Fatal(err)
	}
	aggregate := Aggregate{Days: map[time.Time]Metrics{day: {Visitors: 2}}, Dimensions: map[DimensionKey]Metrics{}}
	if err := store.Complete(ctx, created.ID, aggregate, ParseStats{Rows: 1}); err != nil {
		t.Fatal(err)
	}
	assertCloseVersionAndInvalidation(t, store, siteID, day, 2, invalidated)
	deleted, err := store.Delete(ctx, created.ID)
	if err != nil || !deleted {
		t.Fatalf("Delete()=(%t,%v), want (true,nil)", deleted, err)
	}
	assertCloseVersionAndInvalidation(t, store, siteID, day, 3, invalidated)
}

func assertCloseVersionAndInvalidation(t *testing.T, store *Store, siteID int32, day time.Time, wantVersion int64, invalidated <-chan int32) {
	t.Helper()
	var version int64
	if err := store.pool.QueryRow(context.Background(), `select version from day_close where site_id=$1 and day=$2`, siteID, day).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != wantVersion {
		t.Fatalf("close version=%d, want %d", version, wantVersion)
	}
	select {
	case got := <-invalidated:
		if got != siteID {
			t.Fatalf("invalidated site=%d, want %d", got, siteID)
		}
	default:
		t.Fatal("site was not invalidated")
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

func TestStoreRejectsDuplicateFilePerSite(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	hash := [32]byte{1, 2, 3}
	first, err := store.Create(context.Background(), CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "events.csv", CreatedBy: userID, FileHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(context.Background(), CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "again.csv", CreatedBy: userID, FileHash: hash})
	var duplicate *AlreadyImportedError
	if !errors.As(err, &duplicate) {
		t.Fatalf("Create duplicate error=%v, want AlreadyImportedError", err)
	}
	if duplicate.ImportID != first.ID || duplicate.ImportedAt.IsZero() {
		t.Fatalf("duplicate=%+v, want import %d with date", duplicate, first.ID)
	}
}

func TestStoreReplaceSwapsRowsAtomically(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	hash := [32]byte{4, 5, 6}
	old, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "events.csv", CreatedBy: userID, FileHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunning(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Complete(ctx, old.ID, Aggregate{Days: map[time.Time]Metrics{day: {Visitors: 3}}, Dimensions: map[DimensionKey]Metrics{}}, ParseStats{Rows: 3}); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "events.csv", CreatedBy: userID, FileHash: hash, Replace: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunning(ctx, replacement.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, replacement.ID, Aggregate{Days: map[time.Time]Metrics{day: {Visitors: 7}}, Dimensions: map[DimensionKey]Metrics{}}, ParseStats{Rows: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old import still exists: %v", err)
	}
	var visitors int64
	if err := store.pool.QueryRow(ctx, `select visitors from imported_day where import_id=$1`, replacement.ID).Scan(&visitors); err != nil {
		t.Fatal(err)
	}
	if visitors != 7 {
		t.Fatalf("replacement visitors=%d, want 7", visitors)
	}
}

func TestStoreFailedReplacePreservesPreviousRows(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	hash := [32]byte{7, 8, 9}
	old, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "events.csv", CreatedBy: userID, FileHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunning(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Complete(ctx, old.ID, Aggregate{Days: map[time.Time]Metrics{day: {Visitors: 3}}, Dimensions: map[DimensionKey]Metrics{}}, ParseStats{Rows: 3}); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "events.csv", CreatedBy: userID, FileHash: hash, Replace: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunning(ctx, replacement.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, replacement.ID, Aggregate{Days: map[time.Time]Metrics{day: {Visitors: -1}}, Dimensions: map[DimensionKey]Metrics{}}, ParseStats{}); err == nil {
		t.Fatal("Complete invalid replacement error=nil")
	}
	var visitors int64
	if err := store.pool.QueryRow(ctx, `select visitors from imported_day where import_id=$1`, old.ID).Scan(&visitors); err != nil {
		t.Fatal(err)
	}
	if visitors != 3 {
		t.Fatalf("old visitors=%d, want 3", visitors)
	}
}

func TestStoreMarksRunningImportsFailedAtStartup(t *testing.T) {
	store, siteID, userID := newImportStore(t)
	ctx := context.Background()
	created, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "events.csv", CreatedBy: userID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunning(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	// A pending import's job lived only in the crashed process's queue, so it
	// can never run and would otherwise block re-uploading the same file.
	pending, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "queued.csv", CreatedBy: userID, FileHash: [32]byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailRunningAtStartup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{created.ID, pending.ID} {
		got, err := store.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusFailed || got.Error == nil || *got.Error != "import interrupted by server restart" {
			t.Fatalf("recovered import=%+v", got)
		}
	}
	if _, err := store.Create(ctx, CreateInput{SiteID: siteID, Source: SourceUmami, FileName: "queued.csv", CreatedBy: userID, FileHash: [32]byte{1}}); err != nil {
		t.Fatalf("re-upload after interrupted import: %v", err)
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
