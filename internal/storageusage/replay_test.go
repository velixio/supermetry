package storageusage

import (
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"testing"
	"time"

	"supermetry/internal/replay"
)

type replayBlobStub struct {
	blobs      []replay.BlobInfo
	err        error
	prefixes   []string
	beforeList func(context.Context)
}

type replayWalkingBlobStub struct {
	*replayBlobStub
	walks   int
	walkErr error
}

func (s *replayWalkingBlobStub) Walk(ctx context.Context, prefix string, visit func(replay.BlobInfo) error) error {
	s.walks++
	s.prefixes = append(s.prefixes, prefix)
	for _, blob := range s.blobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(blob); err != nil {
			return err
		}
	}
	return s.walkErr
}

func (*replayBlobStub) Append(context.Context, string, io.Reader) (int64, error) { return 0, nil }
func (*replayBlobStub) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (*replayBlobStub) PutIfAbsent(context.Context, string, io.Reader) (bool, error) {
	return false, nil
}
func (*replayBlobStub) DeletePrefix(context.Context, string) (int64, error) { return 0, nil }
func (*replayBlobStub) Delete(context.Context, string) (int64, error)       { return 0, nil }
func (s *replayBlobStub) List(ctx context.Context, prefix string) ([]replay.BlobInfo, error) {
	if s.beforeList != nil {
		s.beforeList(ctx)
	}
	s.prefixes = append(s.prefixes, prefix)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]replay.BlobInfo(nil), s.blobs...), s.err
}

func TestReplayWorkUnitsIncludeUnErasedSites(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 3)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `update sites set deleted_at=now() where id=$1`, sites[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update sites set erased_at=now() where id=$1`, sites[2]); err != nil {
		t.Fatal(err)
	}
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewReplayScanner(pool, &replayBlobStub{})
	units, err := scanner.WorkUnits(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("work units = %+v, want one per active/restorable site", units)
	}
	for _, siteID := range sites[:2] {
		if !hasReplayUnit(units, siteID) {
			t.Errorf("site %d work units missing: %+v", siteID, units)
		}
	}
	if hasReplayUnit(units, sites[2]) {
		t.Errorf("erased site %d has work units", sites[2])
	}
}

func TestReplayScanSeparatesCategoriesAndDeduplicatesKeys(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	prefix := replayPrefix(sites[0])
	large := int64(math.MaxInt32) + 50
	blobs := &replayBlobStub{blobs: []replay.BlobInfo{
		{Key: prefix + "/2026-10-26/1.gz", Size: large},
		{Key: prefix + "/2026-10-26/2.gz", Size: 7},
		{Key: prefix + "/css/abc.gz", Size: 11},
		{Key: prefix + "/css/abc.gz", Size: 11},
		{Key: "replay/999/2026-10-26/other.gz", Size: 999},
	}}
	scanner := NewReplayScanner(pool, blobs)
	units, err := scanner.WorkUnits(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	unit := findReplayUnit(t, units, sites[0])
	got, err := scanner.Scan(context.Background(), run.ID, unit)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		category Category
		want     int64
	}{
		{name: "recordings", category: CategoryReplayRecording, want: large + 7},
		{name: "css", category: CategoryReplayCSS, want: 11},
	} {
		t.Run(test.name, func(t *testing.T) {
			measurement := findMeasurement(t, got, test.category)
			if len(measurement.Values) != 1 || measurement.Values[0].SiteID != sites[0] || measurement.Values[0].Bytes != test.want {
				t.Fatalf("contributions = %+v, want site %d bytes %d", got, sites[0], test.want)
			}
		})
	}
	if len(blobs.prefixes) != 1 || blobs.prefixes[0] != prefix {
		t.Fatalf("list prefixes = %v", blobs.prefixes)
	}
}

func TestReplayScanPrefersStreamingWalk(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	prefix := replayPrefix(sites[0])
	legacy := &replayBlobStub{
		blobs: []replay.BlobInfo{
			{Key: prefix + "/2026-10-26/one.gz", Size: 9},
			{Key: prefix + "/css/a.gz", Size: 4},
		},
		err: errors.New("List must not be called"),
	}
	blobs := &replayWalkingBlobStub{replayBlobStub: legacy}
	scanner := NewReplayScanner(pool, blobs)
	unit := findReplayUnit(t, mustReplayUnits(t, scanner, run.ID), sites[0])
	got, err := scanner.Scan(context.Background(), run.ID, unit)
	if err != nil {
		t.Fatal(err)
	}
	if blobs.walks != 1 || len(blobs.prefixes) != 1 || blobs.prefixes[0] != prefix {
		t.Fatalf("walks = %d, prefixes = %v, want one walk for %q", blobs.walks, blobs.prefixes, prefix)
	}
	if findMeasurement(t, got, CategoryReplayRecording).Values[0].Bytes != 9 || findMeasurement(t, got, CategoryReplayCSS).Values[0].Bytes != 4 {
		t.Fatalf("streamed contributions = %+v", got)
	}
}

func TestReplayScanSkipsSiteNotCapturedByRun(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	createdLater := insertSite(t, pool, "replaylater1")
	blobs := &replayBlobStub{blobs: []replay.BlobInfo{{Key: replayPrefix(createdLater) + "/x.gz", Size: 20}}}
	scanner := NewReplayScanner(pool, blobs)
	unit := replayWorkUnit(createdLater)
	got, err := scanner.Scan(context.Background(), run.ID, unit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(blobs.prefixes) != 0 {
		t.Fatalf("uncaptured scan = %+v, list calls %v", got, blobs.prefixes)
	}
}

func TestReplayScanReturnsZeroForMissingObjects(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewReplayScanner(pool, &replayBlobStub{})
	unit := findReplayUnit(t, mustReplayUnits(t, scanner, run.ID), sites[0])
	got, err := scanner.Scan(context.Background(), run.ID, unit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || findMeasurement(t, got, CategoryReplayRecording).Values[0] != (Contribution{SiteID: sites[0], Bytes: 0}) || findMeasurement(t, got, CategoryReplayCSS).Values[0] != (Contribution{SiteID: sites[0], Bytes: 0}) {
		t.Fatalf("missing-object contribution = %+v", got)
	}
}

func TestReplayScanDropsContributionWhenSiteErasedDuringListing(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	blobs := &replayBlobStub{blobs: []replay.BlobInfo{{Key: replayPrefix(sites[0]) + "/one.gz", Size: 10}}}
	blobs.beforeList = func(ctx context.Context) {
		if err := store.EraseSite(ctx, sites[0]); err != nil {
			t.Fatal(err)
		}
	}
	scanner := NewReplayScanner(pool, blobs)
	unit := replayWorkUnit(sites[0])
	got, err := scanner.Scan(context.Background(), run.ID, unit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("erased-site contribution = %+v", got)
	}
}

func TestReplayScanRejectsNegativeAndOverflowSizes(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	prefix := replayPrefix(sites[0])
	for _, test := range []struct {
		name  string
		blobs []replay.BlobInfo
	}{
		{name: "negative", blobs: []replay.BlobInfo{{Key: prefix + "/one.gz", Size: -1}}},
		{name: "overflow", blobs: []replay.BlobInfo{{Key: prefix + "/one.gz", Size: math.MaxInt64}, {Key: prefix + "/two.gz", Size: 1}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			scanner := NewReplayScanner(pool, &replayBlobStub{blobs: test.blobs})
			unit := findReplayUnit(t, mustReplayUnits(t, scanner, run.ID), sites[0])
			if _, err := scanner.Scan(context.Background(), run.ID, unit); err == nil {
				t.Fatal("invalid blob sizes were accepted")
			}
		})
	}
}

func TestReplayScanHonorsCancellation(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewReplayScanner(pool, &replayBlobStub{})
	unit := findReplayUnit(t, mustReplayUnits(t, scanner, run.ID), sites[0])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanner.Scan(ctx, run.ID, unit); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan error = %v", err)
	}
}

func TestReplayListingCompletesBeforeReconcilerStages(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	blobs := &replayBlobStub{}
	blobs.blobs = []replay.BlobInfo{{Key: replayPrefix(sites[0]) + "/css/a.gz", Size: 5}}
	blobs.beforeList = func(ctx context.Context) {
		var staged int
		if err := pool.QueryRow(ctx, `select count(*) from storage_usage_stage`).Scan(&staged); err != nil {
			t.Fatal(err)
		}
		if staged != 0 {
			t.Fatalf("stage rows during external listing = %d", staged)
		}
	}
	reconciler := NewReconciler(store, MeasurementVersion, NewReplayScanner(pool, blobs))
	if _, err := reconciler.Step(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var staged int
	if err := pool.QueryRow(context.Background(), `select count(*) from storage_usage_stage`).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if staged != 2 {
		t.Fatalf("stage rows after listing = %d, want 2", staged)
	}
}

func TestReplayMeasurementsReplaceBothCategoriesAtomically(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 1)
	ctx := context.Background()
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key := "replay:atomic"
	first := []Measurement{
		{Category: CategoryReplayRecording, Values: []Contribution{{SiteID: sites[0], Bytes: 10}}},
		{Category: CategoryReplayCSS, Values: []Contribution{{SiteID: sites[0], Bytes: 20}}},
	}
	if err := store.ReplaceMeasurements(ctx, run.ID, key, first); err != nil {
		t.Fatal(err)
	}
	second := []Measurement{
		{Category: CategoryReplayRecording, Values: []Contribution{{SiteID: sites[0], Bytes: 30}}},
		{Category: CategoryReplayCSS, Values: []Contribution{{SiteID: sites[0], Bytes: 40}}},
	}
	if err := store.ReplaceMeasurements(ctx, run.ID, key, second); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceMeasurements(ctx, run.ID, key, []Measurement{
		{Category: CategoryReplayRecording, Values: []Contribution{{SiteID: sites[0], Bytes: 99}}},
		{Category: CategoryReplayCSS, Values: []Contribution{{SiteID: sites[0], Bytes: -1}}},
	}); err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	rows, err := pool.Query(ctx, `select category,bytes from storage_usage_stage where run_id=$1 and source_key=$2 order by category`, run.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[Category]int64{}
	for rows.Next() {
		var category Category
		var bytes int64
		if err := rows.Scan(&category, &bytes); err != nil {
			t.Fatal(err)
		}
		got[category] = bytes
	}
	if got[CategoryReplayRecording] != 30 || got[CategoryReplayCSS] != 40 || len(got) != 2 {
		t.Fatalf("staged replay measurements = %+v", got)
	}
}

func hasReplayUnit(units []WorkUnit, siteID int32) bool {
	for _, unit := range units {
		if unit.SiteID == siteID {
			return true
		}
	}
	return false
}

func mustReplayUnits(t *testing.T, scanner *ReplayScanner, runID int64) []WorkUnit {
	t.Helper()
	units, err := scanner.WorkUnits(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	return units
}

func findReplayUnit(t *testing.T, units []WorkUnit, siteID int32) WorkUnit {
	t.Helper()
	for _, unit := range units {
		if unit.SiteID == siteID {
			return unit
		}
	}
	t.Fatalf("replay unit site=%d not found in %+v", siteID, units)
	return WorkUnit{}
}

func findMeasurement(t *testing.T, measurements []Measurement, category Category) Measurement {
	t.Helper()
	for _, measurement := range measurements {
		if measurement.Category == category {
			return measurement
		}
	}
	t.Fatalf("measurement category %s not found in %+v", category, measurements)
	return Measurement{}
}

func replayPrefix(siteID int32) string {
	return "replay/" + strconv.FormatInt(int64(siteID), 10)
}

var _ replay.BlobStore = (*replayBlobStub)(nil)
