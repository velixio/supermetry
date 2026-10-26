package storageusage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeScanner struct {
	fingerprint string
	units       []WorkUnit
	values      map[string][]Contribution
	fail        map[string]int
	scanCalls   []string
}

func (f *fakeScanner) Fingerprint() string {
	if f.fingerprint == "" {
		return "fake-v1"
	}
	return f.fingerprint
}

func (f *fakeScanner) WorkUnits(context.Context, int64) ([]WorkUnit, error) {
	return append([]WorkUnit(nil), f.units...), nil
}

func (f *fakeScanner) Scan(ctx context.Context, _ int64, unit WorkUnit) ([]Measurement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.scanCalls = append(f.scanCalls, unit.Key)
	if f.fail[unit.Key] > 0 {
		f.fail[unit.Key]--
		return nil, errors.New("temporary scan failure")
	}
	return []Measurement{{Category: unit.Category, Values: append([]Contribution(nil), f.values[unit.Key]...)}}, nil
}

func TestReconcilerProcessesOneWorkUnitAndPublishesCompleteGeneration(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 2)
	ctx := context.Background()
	oldAt := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `insert into site_storage_usage(site_id,hot_bytes,retained_bytes,auxiliary_bytes,replay_recording_bytes,replay_css_bytes,measured_at,measurement_version) values($1,5,0,0,0,0,$2,1)`, sites[0], oldAt); err != nil {
		t.Fatal(err)
	}
	scanner := &fakeScanner{
		units: []WorkUnit{
			{Key: "a-hot", Category: CategoryHot},
			{Key: "b-replay", Category: CategoryReplayRecording},
		},
		values: map[string][]Contribution{
			"a-hot":    {{SiteID: sites[0], Bytes: 10}},
			"b-replay": {{SiteID: sites[0], Bytes: 20}},
		},
	}
	reconciler := NewReconciler(store, MeasurementVersion, scanner)
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)

	first, err := reconciler.Step(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Started || first.ProcessedKey != "a-hot" || first.Published || first.Pending != 1 {
		t.Fatalf("first step = %+v", first)
	}
	assertSnapshotTotal(t, pool, sites[0], 5, oldAt)

	second, err := reconciler.Step(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Started || second.ProcessedKey != "b-replay" || second.Published || second.Pending != 0 {
		t.Fatalf("second step = %+v", second)
	}
	assertSnapshotTotal(t, pool, sites[0], 5, oldAt)

	third, err := reconciler.Step(ctx, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !third.Published || third.ProcessedKey != "" {
		t.Fatalf("publish step = %+v", third)
	}
	assertSnapshotTotal(t, pool, sites[0], 30, now.Add(2*time.Minute))
	assertSnapshotTotal(t, pool, sites[1], 0, now.Add(2*time.Minute))
	status, err := reconciler.Status(ctx)
	if err != nil || status.LastCompletedAt == nil || !status.LastCompletedAt.Equal(now.Add(2*time.Minute)) || status.ActiveStartedAt != nil || status.Pending != 0 {
		t.Fatalf("completed status = %+v, %v", status, err)
	}

	idle, err := reconciler.Step(ctx, now.Add(time.Hour))
	if err != nil || !idle.Idle || idle.Started {
		t.Fatalf("fresh generation step = %+v, %v", idle, err)
	}
	due, err := reconciler.Step(ctx, now.Add(24*time.Hour+2*time.Minute))
	if err != nil || !due.Started || due.ProcessedKey != "a-hot" {
		t.Fatalf("due generation step = %+v, %v", due, err)
	}
}

func TestReconcilerRetriesWithoutDoubleCountingAndResumes(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	scanner := &fakeScanner{
		units:  []WorkUnit{{Key: "only", Category: CategoryAuxiliary}},
		values: map[string][]Contribution{"only": {{SiteID: sites[0], Bytes: 17}}},
		fail:   map[string]int{"only": 1},
	}
	reconciler := NewReconciler(store, MeasurementVersion, scanner)
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if _, err := reconciler.Step(context.Background(), now); err == nil {
		t.Fatal("failing scan succeeded")
	}
	active, err := store.ActiveRun(context.Background())
	if err != nil || active == nil || active.Cursor != "" {
		t.Fatalf("active run after failure = %+v, %v", active, err)
	}
	if _, err := reconciler.Step(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Step(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertSnapshotTotal(t, pool, sites[0], 17, now.Add(2*time.Minute))
	if len(scanner.scanCalls) != 2 || scanner.scanCalls[0] != "only" || scanner.scanCalls[1] != "only" {
		t.Fatalf("scan calls = %v", scanner.scanCalls)
	}
}

func TestReconcilerManifestIgnoresNewLowerUnitAndKeepsRemovedUnit(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	scanner := &fakeScanner{
		units: []WorkUnit{{Key: "a", Category: CategoryHot}, {Key: "b", Category: CategoryHot}},
		values: map[string][]Contribution{
			"a": {{SiteID: sites[0], Bytes: 10}},
			"b": {{SiteID: sites[0], Bytes: 20}},
		},
	}
	reconciler := NewReconciler(store, MeasurementVersion, scanner)
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if _, err := reconciler.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	createdLater := insertSite(t, pool, "later-site02")
	// Discovery changes after the run starts: a lower key appears and the
	// already-manifested first key disappears. Neither mutates the run manifest.
	scanner.units = []WorkUnit{{Key: "0-new", Category: CategoryHot}, {Key: "b", Category: CategoryHot}}
	resumed := NewReconciler(store, MeasurementVersion, scanner)
	if _, err := resumed.Step(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Step(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertSnapshotTotal(t, pool, sites[0], 30, now.Add(2*time.Minute))
	if len(scanner.scanCalls) != 2 || scanner.scanCalls[0] != "a" || scanner.scanCalls[1] != "b" {
		t.Fatalf("manifest scan calls = %v", scanner.scanCalls)
	}
	var exists bool
	if err := pool.QueryRow(context.Background(), `select exists(select 1 from site_storage_usage where site_id=$1)`, createdLater).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("site created mid-run received a snapshot")
	}
}

func TestReconcilerFingerprintChangeAbandonsAndRestarts(t *testing.T) {
	store, _, _ := newStoreFixture(t, 1)
	scanner := &fakeScanner{fingerprint: "one", units: []WorkUnit{{Key: "a", Category: CategoryHot}}, values: map[string][]Contribution{}}
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	first, err := NewReconciler(store, MeasurementVersion, scanner).Step(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	scanner.fingerprint = "two"
	second, err := NewReconciler(store, MeasurementVersion, scanner).Step(context.Background(), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.RunID == first.RunID || !second.Started {
		t.Fatalf("fingerprint restart = first %+v second %+v", first, second)
	}
	old, err := store.LoadRun(context.Background(), first.RunID)
	if err != nil || old.Status != RunAbandoned {
		t.Fatalf("old run = %+v, %v", old, err)
	}
}

func TestStoreStatusReportsLastSuccessActiveStartAndPending(t *testing.T) {
	store, _, _ := newStoreFixture(t, 1)
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	reconciler := NewReconciler(store, MeasurementVersion, &fakeScanner{units: []WorkUnit{{Key: "a", Category: CategoryHot}, {Key: "b", Category: CategoryHot}}, values: map[string][]Contribution{}})
	if _, err := reconciler.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	status, err := reconciler.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ActiveStartedAt == nil || !status.ActiveStartedAt.Equal(now) || status.Pending != 1 || status.LastCompletedAt != nil {
		t.Fatalf("active status = %+v", status)
	}
}

func TestReconcilerCancellationLeavesCursorResumable(t *testing.T) {
	store, _, _ := newStoreFixture(t, 1)
	scanner := &fakeScanner{units: []WorkUnit{{Key: "only", Category: CategoryHot}}, values: map[string][]Contribution{}}
	reconciler := NewReconciler(store, MeasurementVersion, scanner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reconciler.Step(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled step error = %v", err)
	}
}

func assertSnapshotTotal(t *testing.T, storePool *pgxpool.Pool, siteID int32, want int64, wantAt time.Time) {
	t.Helper()
	var total int64
	var measuredAt time.Time
	if err := storePool.QueryRow(context.Background(), `select estimated_storage_bytes,measured_at from site_storage_usage where site_id=$1`, siteID).Scan(&total, &measuredAt); err != nil {
		t.Fatal(err)
	}
	if total != want || !measuredAt.Equal(wantAt) {
		t.Fatalf("site %d snapshot = %d at %v, want %d at %v", siteID, total, measuredAt, want, wantAt)
	}
}
