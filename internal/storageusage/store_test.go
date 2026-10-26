package storageusage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestStoreRunLifecycleAndIdempotentStageReplacement(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 2)
	ctx := context.Background()
	started := time.Date(2026, 10, 26, 1, 2, 3, 0, time.UTC)

	run, err := store.BeginOrResume(ctx, MeasurementVersion, started)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := store.BeginOrResume(ctx, MeasurementVersion, started.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != run.ID {
		t.Fatalf("resumed run = %d, want %d", resumed.ID, run.ID)
	}
	if len(run.SiteIDs) != 2 || run.SiteIDs[0] != sites[0] || run.SiteIDs[1] != sites[1] {
		t.Fatalf("captured sites = %v, want %v", run.SiteIDs, sites)
	}

	if err := store.ReplaceSource(ctx, run.ID, "postgres:events:p1", CategoryHot, []Contribution{
		{SiteID: sites[0], Bytes: 10}, {SiteID: sites[1], Bytes: math.MaxInt32 + 50},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSource(ctx, run.ID, "postgres:events:p1", CategoryHot, []Contribution{
		{SiteID: sites[0], Bytes: 25},
	}); err != nil {
		t.Fatal(err)
	}
	var rows int
	var bytes int64
	if err := pool.QueryRow(ctx, `select count(*),coalesce(sum(bytes),0) from storage_usage_stage where run_id=$1`, run.ID).Scan(&rows, &bytes); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || bytes != 25 {
		t.Fatalf("staged rows/bytes = %d/%d, want 1/25", rows, bytes)
	}

	advanced := started.Add(2 * time.Hour)
	if err := store.AdvanceCursor(ctx, run.ID, "postgres:events:p1", advanced); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Cursor != "postgres:events:p1" || !loaded.UpdatedAt.Equal(advanced) {
		t.Fatalf("loaded run = %+v", loaded)
	}

	next, err := store.BeginOrResume(ctx, MeasurementVersion+1, advanced.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == run.ID {
		t.Fatal("measurement-version change reused the old run")
	}
	loaded, err = store.LoadRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != RunAbandoned {
		t.Fatalf("old run status = %q, want %q", loaded.Status, RunAbandoned)
	}
}

func TestStoreRejectsNegativeContributions(t *testing.T) {
	store, _, sites := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	err = store.ReplaceSource(context.Background(), run.ID, "negative", CategoryHot, []Contribution{{SiteID: sites[0], Bytes: -1}})
	if err == nil {
		t.Fatal("negative contribution succeeded")
	}
}

func TestStoreManifestIsImmutableAndResumable(t *testing.T) {
	store, _, _ := newStoreFixture(t, 1)
	ctx := context.Background()
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	units := []WorkUnit{{Key: "a", Scanner: "test", Category: CategoryHot}, {Key: "b", Scanner: "test", Category: CategoryAuxiliary}}
	if err := store.SaveManifest(ctx, run.ID, units); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveManifest(ctx, run.ID, units); err != nil {
		t.Fatalf("idempotent save: %v", err)
	}
	if err := store.SaveManifest(ctx, run.ID, units[:1]); !errors.Is(err, ErrManifestImmutable) {
		t.Fatalf("changed manifest error = %v", err)
	}
	got, ready, err := store.LoadManifest(ctx, run.ID)
	if err != nil || !ready || len(got) != 2 || got[0] != units[0] || got[1] != units[1] {
		t.Fatalf("manifest = %+v ready=%v err=%v", got, ready, err)
	}
}

func TestPublishUsesCapturedLiveSitesAndOneTimestamp(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 3)
	ctx := context.Background()
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	createdLater := insertSite(t, pool, "later-site01")
	if _, err := pool.Exec(ctx, `update sites set erased_at=now() where id=$1`, sites[2]); err != nil {
		t.Fatal(err)
	}
	for _, staged := range []struct {
		key      string
		category Category
		values   []Contribution
	}{
		{"hot", CategoryHot, []Contribution{{sites[0], 10}, {sites[2], 999}}},
		{"retained", CategoryRetained, []Contribution{{sites[0], 20}}},
		{"aux", CategoryAuxiliary, []Contribution{{sites[0], 30}}},
		{"recording", CategoryReplayRecording, []Contribution{{sites[0], 40}}},
		{"css", CategoryReplayCSS, []Contribution{{sites[0], 50}}},
	} {
		if err := store.ReplaceSource(ctx, run.ID, staged.key, staged.category, staged.values); err != nil {
			t.Fatal(err)
		}
	}
	measuredAt := time.Date(2026, 10, 26, 6, 7, 8, 0, time.UTC)
	if err := store.Publish(ctx, run.ID, measuredAt); err != nil {
		t.Fatal(err)
	}

	type snapshot struct {
		siteID                                                            int32
		hot, retained, auxiliary, recording, css, database, replay, total int64
		measuredAt                                                        time.Time
	}
	rows, err := pool.Query(ctx, `select site_id,hot_bytes,retained_bytes,auxiliary_bytes,replay_recording_bytes,replay_css_bytes,database_bytes,replay_bytes,estimated_storage_bytes,measured_at from site_storage_usage order by site_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []snapshot
	for rows.Next() {
		var s snapshot
		if err := rows.Scan(&s.siteID, &s.hot, &s.retained, &s.auxiliary, &s.recording, &s.css, &s.database, &s.replay, &s.total, &s.measuredAt); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("snapshots = %+v, want measured and zero captured sites", got)
	}
	if got[0].siteID != sites[0] || got[0].hot != 10 || got[0].retained != 20 || got[0].auxiliary != 30 || got[0].recording != 40 || got[0].css != 50 || got[0].database != 60 || got[0].replay != 90 || got[0].total != 150 {
		t.Fatalf("measured snapshot = %+v", got[0])
	}
	if got[1].siteID != sites[1] || got[1].total != 0 {
		t.Fatalf("zero snapshot = %+v", got[1])
	}
	if !got[0].measuredAt.Equal(measuredAt) || !got[1].measuredAt.Equal(measuredAt) {
		t.Fatalf("measured timestamps = %v/%v", got[0].measuredAt, got[1].measuredAt)
	}
	for _, absent := range []int32{sites[2], createdLater} {
		var exists bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from site_storage_usage where site_id=$1)`, absent).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("site %d received a snapshot", absent)
		}
	}
	loaded, err := store.LoadRun(ctx, run.ID)
	if err != nil || loaded.Status != RunComplete || loaded.CompletedAt == nil || !loaded.CompletedAt.Equal(measuredAt) {
		t.Fatalf("published run = %+v, %v", loaded, err)
	}
}

func TestPublishRollsBackEntirelyOnFailure(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	ctx := context.Background()
	previous := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into site_storage_usage(site_id,hot_bytes,retained_bytes,auxiliary_bytes,replay_recording_bytes,replay_css_bytes,measured_at,measurement_version) values($1,7,0,0,0,0,$2,1)`, sites[0], previous); err != nil {
		t.Fatal(err)
	}
	run, err := store.BeginOrResume(ctx, MeasurementVersion, previous.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSource(ctx, run.ID, "bad", CategoryHot, []Contribution{{sites[0], math.MaxInt64}, {sites[0], 1}}); err == nil {
		t.Fatal("duplicate contribution unexpectedly succeeded")
	}
	// Overflow is induced with two distinct source keys so aggregation fails in Publish.
	if err := store.ReplaceSource(ctx, run.ID, "huge", CategoryHot, []Contribution{{sites[0], math.MaxInt64}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSource(ctx, run.ID, "one", CategoryHot, []Contribution{{sites[0], 1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, run.ID, previous.Add(24*time.Hour)); err == nil {
		t.Fatal("overflowing publish succeeded")
	}
	var hot int64
	var measured time.Time
	if err := pool.QueryRow(ctx, `select hot_bytes,measured_at from site_storage_usage where site_id=$1`, sites[0]).Scan(&hot, &measured); err != nil {
		t.Fatal(err)
	}
	if hot != 7 || !measured.Equal(previous) {
		t.Fatalf("snapshot changed to %d at %v", hot, measured)
	}
	loaded, err := store.LoadRun(ctx, run.ID)
	if err != nil || loaded.Status != RunActive {
		t.Fatalf("run after failed publish = %+v, %v", loaded, err)
	}
}

func TestPublishRemovesOlderReconciliationMetadata(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 1)
	ctx := context.Background()
	firstAt := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	first, err := store.BeginOrResume(ctx, MeasurementVersion, firstAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, first.ID, firstAt); err != nil {
		t.Fatal(err)
	}
	second, err := store.BeginOrResume(ctx, MeasurementVersion, firstAt.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(ctx, second.ID, firstAt.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	var runs, captured int
	if err := pool.QueryRow(ctx, `select count(*),(select count(*) from storage_usage_run_sites) from storage_usage_runs`).Scan(&runs, &captured); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || captured != 1 {
		t.Fatalf("reconciliation metadata runs/captured = %d/%d, want 1/1", runs, captured)
	}
}

func TestStoreEraseSiteRemovesSnapshotAndStaging(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	ctx := context.Background()
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSource(ctx, run.ID, "source", CategoryAuxiliary, []Contribution{{sites[0], 3}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into site_storage_usage(site_id,hot_bytes,retained_bytes,auxiliary_bytes,replay_recording_bytes,replay_css_bytes,measured_at,measurement_version) values($1,0,0,3,0,0,now(),1)`, sites[0]); err != nil {
		t.Fatal(err)
	}
	if err := store.EraseSite(ctx, sites[0]); err != nil {
		t.Fatal(err)
	}
	var snapshots, stage, captured int
	if err := pool.QueryRow(ctx, `select (select count(*) from site_storage_usage where site_id=$1),(select count(*) from storage_usage_stage where site_id=$1),(select count(*) from storage_usage_run_sites where site_id=$1)`, sites[0]).Scan(&snapshots, &stage, &captured); err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 || stage != 0 || captured != 0 {
		t.Fatalf("remaining snapshot/stage/captured = %d/%d/%d", snapshots, stage, captured)
	}
}

func TestMigrationStorageUsageConstraintsAndIndexes(t *testing.T) {
	_, pool, sites := newStoreFixture(t, 1)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `insert into site_storage_usage(site_id,hot_bytes,retained_bytes,auxiliary_bytes,replay_recording_bytes,replay_css_bytes,measured_at,measurement_version) values($1,-1,0,0,0,0,now(),1)`, sites[0])
	if err == nil || !isCheckViolation(err) {
		t.Fatalf("negative snapshot error = %v, want check violation", err)
	}
	for _, index := range []string{"storage_usage_run_sites_site_id_idx", "storage_usage_stage_site_id_idx"} {
		var exists bool
		if err := pool.QueryRow(ctx, `select to_regclass($1) is not null`, index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("index %s is missing", index)
		}
	}
}

func newStoreFixture(t *testing.T, siteCount int) (*Store, *pgxpool.Pool, []int32) {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	sites := make([]int32, 0, siteCount)
	for i := 0; i < siteCount; i++ {
		sites = append(sites, insertSite(t, pool, fmt.Sprintf("usage%07d", i)))
	}
	return NewStore(pool), pool, sites
}

func insertSite(t *testing.T, pool *pgxpool.Pool, publicID string) int32 {
	t.Helper()
	ctx := context.Background()
	var orgID int64
	slug := "org-" + publicID
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values($1,$2) returning id`, slug, slug).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class) values($1,$2,$2,'UTC',30) returning id`, orgID, publicID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return siteID
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
