package dict_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/testpg"
)

func TestIDsForSiteCapsAttackerWithoutBlockingAnotherSite(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	const limit = 4002
	d := dict.New(pool, dict.WithMaxEntriesPerKind(limit))
	values := make([]string, limit/2+1)
	for i := range values {
		values[i] = fmt.Sprintf("/junk/%d", i)
	}
	ids, err := d.IDsForSite(ctx, 1, dict.Path, values)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := d.Value(dict.Path, ids[len(ids)-1]); !ok || got != dict.OtherValue {
		t.Fatalf("attacker overflow = %q, %v, want %q", got, ok, dict.OtherValue)
	}

	legitimate, err := d.IDsForSite(ctx, 2, dict.Path, []string{"/pricing"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := d.Value(dict.Path, legitimate[0]); !ok || got != "/pricing" {
		t.Fatalf("second site value = %q, %v, want /pricing", got, ok)
	}

	reloaded := dict.New(pool, dict.WithMaxEntriesPerKind(limit))
	if err := reloaded.Load(ctx); err != nil {
		t.Fatal(err)
	}
	afterLoad, err := reloaded.IDsForSite(ctx, 1, dict.Path, []string{"/more-junk"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reloaded.Value(dict.Path, afterLoad[0]); !ok || got != dict.OtherValue {
		t.Fatalf("reloaded attacker overflow = %q, %v, want %q", got, ok, dict.OtherValue)
	}
}

func TestIDsForSiteEnforcesShareAcrossInstances(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	const limit = 4002
	first := dict.New(pool, dict.WithMaxEntriesPerKind(limit))
	second := dict.New(pool, dict.WithMaxEntriesPerKind(limit))
	if err := second.Load(ctx); err != nil {
		t.Fatal(err)
	}
	values := make([]string, limit/2+1)
	for i := range values {
		values[i] = fmt.Sprintf("/distributed-junk/%d", i)
	}
	if _, err := first.IDsForSite(ctx, 1, dict.Path, values); err != nil {
		t.Fatal(err)
	}

	ids, err := second.IDsForSite(ctx, 1, dict.Path, []string{"/more-distributed-junk"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := second.Value(dict.Path, ids[0]); !ok || got != dict.OtherValue {
		t.Fatalf("second instance overflow = %q, %v, want %q", got, ok, dict.OtherValue)
	}
}

func TestGarbageCollectDeletesOnlyUnreferencedValuesAndEvictsCache(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, now, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool)
	kinds := []dict.Kind{dict.Path, dict.Referrer, dict.Campaign, dict.UTMContent, dict.UTMTerm, dict.EventName}
	ids := make(map[dict.Kind][]int32, len(kinds))
	for _, kind := range kinds {
		var err error
		ids[kind], err = d.IDsForSite(ctx, 1, kind, []string{fmt.Sprintf("kept-%d", kind), fmt.Sprintf("garbage-%d", kind)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,name_id,path_id,referrer_id,campaign_id,utm_content_id,utm_term_id) values (1,7,$1,$2,1,1,1,$3,$4,$5,$6,$7,$8)`,
		now, now, ids[dict.EventName][0], ids[dict.Path][0], ids[dict.Referrer][0], ids[dict.Campaign][0], ids[dict.UTMContent][0], ids[dict.UTMTerm][0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update dict set created_at=now()-interval '2 days'`); err != nil {
		t.Fatal(err)
	}

	deleted, err := d.GarbageCollect(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("first GarbageCollect deleted %d rows, want quarantine only", deleted)
	}
	for _, kind := range kinds {
		if _, ok := d.Value(kind, ids[kind][1]); ok {
			t.Errorf("quarantined kind %d value remained cached", kind)
		}
	}
	if _, err := pool.Exec(ctx, `update dict set gc_marked_at=now()-interval '2 days' where gc_marked_at is not null`); err != nil {
		t.Fatal(err)
	}

	deleted, err = d.GarbageCollect(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != int64(len(kinds)) {
		t.Fatalf("GarbageCollect deleted %d rows, want %d", deleted, len(kinds))
	}
	for _, kind := range kinds {
		if got, ok := d.Value(kind, ids[kind][0]); !ok || got != fmt.Sprintf("kept-%d", kind) {
			t.Errorf("referenced kind %d value = %q, %v", kind, got, ok)
		}
		if _, ok := d.Value(kind, ids[kind][1]); ok {
			t.Errorf("deleted kind %d value remained cached", kind)
		}
	}

	replacement, err := d.IDsForSite(ctx, 1, dict.Path, []string{"/replacement"})
	if err != nil {
		t.Fatal(err)
	}
	if replacement[0] <= ids[dict.Path][1] {
		t.Fatalf("replacement ID %d reused deleted identity %d", replacement[0], ids[dict.Path][1])
	}
}

func TestIDsForSiteRevivesQuarantinedValue(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool)
	ids, err := d.IDsForSite(ctx, 1, dict.Path, []string{"/revived"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update dict set created_at=now()-interval '2 days' where kind=$1 and id=$2`, dict.Path, ids[0]); err != nil {
		t.Fatal(err)
	}
	if deleted, err := d.GarbageCollect(ctx, 1); err != nil || deleted != 0 {
		t.Fatalf("quarantine deleted=%d err=%v, want 0, nil", deleted, err)
	}

	revived, err := d.IDsForSite(ctx, 1, dict.Path, []string{"/revived"})
	if err != nil {
		t.Fatal(err)
	}
	if revived[0] != ids[0] {
		t.Fatalf("revived ID = %d, want original %d", revived[0], ids[0])
	}
	var markedAt *time.Time
	if err := pool.QueryRow(ctx, `select gc_marked_at from dict where kind=$1 and id=$2`, dict.Path, ids[0]).Scan(&markedAt); err != nil {
		t.Fatal(err)
	}
	if markedAt != nil {
		t.Fatalf("revived value remains quarantined at %v", *markedAt)
	}
}

func TestGarbageCollectRevivesQuarantinedValueThatGainsReference(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, now, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool)
	ids, err := d.IDsForSite(ctx, 1, dict.Path, []string{"/late-reference"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update dict set created_at=now()-interval '2 days' where kind=$1 and id=$2`, dict.Path, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GarbageCollect(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id) values (1,7,$1,$2,1,1,0,$3)`, now, now, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update dict set gc_marked_at=now()-interval '2 days' where kind=$1 and id=$2`, dict.Path, ids[0]); err != nil {
		t.Fatal(err)
	}

	if deleted, err := d.GarbageCollect(ctx, 1); err != nil || deleted != 0 {
		t.Fatalf("referenced quarantine deleted=%d err=%v, want 0, nil", deleted, err)
	}
	if err := d.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got, ok := d.Value(dict.Path, ids[0]); !ok || got != "/late-reference" {
		t.Fatalf("revived referenced value = %q, %v, want /late-reference", got, ok)
	}
}

func TestDictionaryWarnsOncePerThresholdPerKind(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	d := dict.New(pool, dict.WithMaxEntriesPerKind(10), dict.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	values := make([]string, 11)
	for i := range values {
		values[i] = fmt.Sprintf("/warning/%d", i)
	}
	if _, err := d.IDsForSite(ctx, 1, dict.Path, values); err != nil {
		t.Fatal(err)
	}
	if _, err := d.IDsForSite(ctx, 2, dict.Path, []string{"/another-overflow"}); err != nil {
		t.Fatal(err)
	}
	output := logs.String()
	if got := strings.Count(output, "dictionary capacity warning"); got != 2 {
		t.Fatalf("warning count = %d, want 2:\n%s", got, output)
	}
	for _, threshold := range []string{"threshold=80", "threshold=95"} {
		if !strings.Contains(output, threshold) {
			t.Errorf("missing %s warning:\n%s", threshold, output)
		}
	}
}

func TestIDsStableConcurrentAndCached(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool)
	var wg sync.WaitGroup
	res := make([][]int32, 8)
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], _ = d.IDs(ctx, dict.Path, []string{"/", "/pricing", "/"})
		}(i)
	}
	wg.Wait()

	for _, r := range res {
		if r[0] != res[0][0] || r[1] != res[0][1] || r[0] != r[2] {
			t.Fatalf("unstable ids %v", res)
		}
	}

	d2 := dict.New(pool)
	if err := d2.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if v, ok := d2.Value(dict.Path, res[0][1]); !ok || v != "/pricing" {
		t.Fatalf("Load: %q %v", v, ok)
	}
}

func TestRegionAndCityUseSeparateNamespaces(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool)
	regionIDs, err := d.IDs(ctx, dict.Region, []string{"Singapore"})
	if err != nil {
		t.Fatal(err)
	}
	cityIDs, err := d.IDs(ctx, dict.City, []string{"Singapore"})
	if err != nil {
		t.Fatal(err)
	}
	if regionIDs[0] == cityIDs[0] {
		t.Fatalf("region and city IDs share a namespace: %d", regionIDs[0])
	}
	if got, ok := d.Value(dict.Region, regionIDs[0]); !ok || got != "Singapore" {
		t.Fatalf("region lookup = %q, %v", got, ok)
	}
	if got, ok := d.Value(dict.City, cityIDs[0]); !ok || got != "Singapore" {
		t.Fatalf("city lookup = %q, %v", got, ok)
	}
}

func TestIDsFoldAtPerKindEntryLimit(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	const limit = 8
	d := dict.New(pool, dict.WithMaxEntriesPerKind(limit))
	values := make([]string, 32)
	for i := range values {
		values[i] = fmt.Sprintf("/%03d-%s", i, strings.Repeat("x", 300))
	}
	ids, err := d.IDs(ctx, dict.Path, values)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Len(dict.Path); got > limit {
		t.Fatalf("cached path count = %d, want at most %d", got, limit)
	}
	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from dict where kind=$1`, dict.Path).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows > limit {
		t.Fatalf("persisted path count = %d, want at most %d", rows, limit)
	}
	overflowID := ids[limit-1]
	if overflowID == 0 {
		t.Fatal("overflow ID is zero")
	}
	for i := limit - 1; i < len(ids); i++ {
		if ids[i] != overflowID {
			t.Fatalf("ID %d = %d, want overflow ID %d", i, ids[i], overflowID)
		}
	}
	if got, ok := d.Value(dict.Path, overflowID); !ok || got != dict.OtherValue {
		t.Fatalf("overflow lookup = %q, %v, want %q", got, ok, dict.OtherValue)
	}
}

func TestIDsBelowPerKindEntryLimitAreUnchanged(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	d := dict.New(pool, dict.WithMaxEntriesPerKind(4))
	values := []string{"/", "/pricing", "/docs"}
	ids, err := d.IDs(ctx, dict.Path, values)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if id == 0 {
			t.Fatalf("ID %d is zero", i)
		}
		if got, ok := d.Value(dict.Path, id); !ok || got != values[i] {
			t.Fatalf("Value(%d) = %q, %v, want %q", id, got, ok, values[i])
		}
	}
}

func TestLoadBoundsCacheAndResolvesOverflowedLegacyIDs(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `insert into dict(kind,value) select $1, '/legacy/' || n::text from generate_series(1, 8) as n returning id`, dict.Path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()

	d := dict.New(pool, dict.WithMaxEntriesPerKind(4))
	if err := d.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := d.Len(dict.Path); got > 4 {
		t.Fatalf("loaded path count = %d, want at most 4", got)
	}
	for _, id := range ids {
		if value, ok := d.Value(dict.Path, id); !ok || value == "" {
			t.Fatalf("legacy ID %d did not resolve: %q, %v", id, value, ok)
		}
	}
}

func TestConcurrentIDsRespectLimitAndDoNotBurnIdentityValues(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	const limit = 16
	dicts := []*dict.Dict{
		dict.New(pool, dict.WithMaxEntriesPerKind(limit)),
		dict.New(pool, dict.WithMaxEntriesPerKind(limit)),
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	results := make(chan int32, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids, err := dicts[i%len(dicts)].IDs(ctx, dict.EventName, []string{"shared"})
			if err != nil {
				errs <- err
				return
			}
			results <- ids[0]
		}(i)
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatal(err)
	}
	var sharedID int32
	for id := range results {
		if sharedID == 0 {
			sharedID = id
		}
		if id != sharedID {
			t.Fatalf("concurrent shared IDs differ: got %d and %d", sharedID, id)
		}
	}

	next, err := dictID(dicts[0], ctx, dict.EventName, "next")
	if err != nil {
		t.Fatal(err)
	}
	if next != sharedID+1 {
		t.Fatalf("next ID = %d, want %d; conflicts burned identity values", next, sharedID+1)
	}
}

func dictID(d *dict.Dict, ctx context.Context, kind dict.Kind, value string) (int32, error) {
	ids, err := d.IDs(ctx, kind, []string{value})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

func TestKindFormatsAsAnIntegerForSQLBuilders(t *testing.T) {
	if got := fmt.Sprint(dict.Path); got != "0" {
		t.Fatalf("fmt.Sprint(dict.Path) = %q, want 0: SQL builders format kinds as integers", got)
	}
	if got := fmt.Sprintf("%v", dict.EventName); got != "7" {
		t.Fatalf("%%v of EventName = %q, want 7", got)
	}
	if dict.Hostname.Label() != "hostname" || dict.Path.Label() != "path" {
		t.Fatalf("labels = %q, %q", dict.Path.Label(), dict.Hostname.Label())
	}
}
