package storageusage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func insertAppSite(t *testing.T, pool *pgxpool.Pool, orgID int64, publicID string) int32 {
	t.Helper()
	var id int32
	if err := pool.QueryRow(context.Background(),
		`insert into sites(org_id,public_id,name,timezone,retention_class,kind) values($1,$2,$2,'UTC',30,'app') returning id`,
		orgID, publicID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertBundle(t *testing.T, pool *pgxpool.Pool, orgID int64, sites []int32, state string, size int64) {
	t.Helper()
	if sites == nil {
		sites = []int32{}
	}
	var n int
	_ = pool.QueryRow(context.Background(), `select count(*) from app_artifact_bundles`).Scan(&n)
	checksum := fmt.Sprintf("%040x", n+1)
	if _, err := pool.Exec(context.Background(),
		`insert into app_artifact_bundles(org_id,site_ids,checksum,state,size) values($1,$2,$3,$4,$5)`,
		orgID, sites, checksum, state, size); err != nil {
		t.Fatal(err)
	}
}

func artifactBytes(t *testing.T, got []Measurement) map[int32]int64 {
	t.Helper()
	out := map[int32]int64{}
	for _, m := range got {
		if m.Category != CategoryAuxiliary {
			t.Fatalf("category = %q, want auxiliary", m.Category)
		}
		for _, v := range m.Values {
			out[v.SiteID] = v.Bytes
		}
	}
	return out
}

func TestArtifactScannerSplitsBundleBytesAcrossSites(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 0)
	ctx := context.Background()
	var orgA, orgB int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('A','art-a') returning id`).Scan(&orgA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('B','art-b') returning id`).Scan(&orgB); err != nil {
		t.Fatal(err)
	}
	s1 := insertAppSite(t, pool, orgA, "appart0001xx")
	s2 := insertAppSite(t, pool, orgA, "appart0002xx")
	s3 := insertAppSite(t, pool, orgA, "appart0003xx")
	b1 := insertAppSite(t, pool, orgB, "appart0004xx")
	erased := insertAppSite(t, pool, orgA, "appart0005xx")
	if _, err := pool.Exec(ctx, `update sites set erased_at=now() where id=$1`, erased); err != nil {
		t.Fatal(err)
	}
	insertBundle(t, pool, orgA, []int32{s1, s2}, "ok", 101)    // 51 + 50
	insertBundle(t, pool, orgA, nil, "ok", 10)                 // org-wide: 4 + 3 + 3
	insertBundle(t, pool, orgA, []int32{s3}, "error", 999)     // failed assembly: not stored
	insertBundle(t, pool, orgA, []int32{s3}, "assembling", 77) // not stored yet
	insertBundle(t, pool, orgB, nil, "ok", 7)

	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewArtifactScanner(pool)
	units, err := scanner.WorkUnits(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 || units[0] != artifactWorkUnit(orgA) || units[1] != artifactWorkUnit(orgB) {
		t.Fatalf("units = %+v", units)
	}
	scan := func(u WorkUnit) map[int32]int64 {
		t.Helper()
		u.Scanner = scanner.Fingerprint()
		got, err := scanner.Scan(ctx, run.ID, u)
		if err != nil {
			t.Fatal(err)
		}
		return artifactBytes(t, got)
	}
	a := scan(units[0])
	if len(a) != 3 || a[s1] != 55 || a[s2] != 53 || a[s3] != 3 {
		t.Fatalf("org A = %v, want %d:55 %d:53 %d:3", a, s1, s2, s3)
	}
	if sum := a[s1] + a[s2] + a[s3]; sum != 111 {
		t.Fatalf("org A total = %d, want the 101+10 bundle bytes", sum)
	}
	if b := scan(units[1]); len(b) != 1 || b[b1] != 7 {
		t.Fatalf("org B = %v", b)
	}
	// A work unit for another scanner's shape is refused.
	if _, err := scanner.Scan(ctx, run.ID, WorkUnit{Key: "artifacts:x", OrgID: orgA, Scanner: scanner.Fingerprint()}); err == nil {
		t.Fatal("malformed work unit accepted")
	}
}

func TestArtifactScannerSkipsSitesNotCapturedByRun(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 0)
	ctx := context.Background()
	var org int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('C','art-c') returning id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	s1 := insertAppSite(t, pool, org, "appart0011xx")
	insertBundle(t, pool, org, nil, "ok", 10)
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	later := insertAppSite(t, pool, org, "appart0012xx") // created after the run captured its sites
	scanner := NewArtifactScanner(pool)
	u := artifactWorkUnit(org)
	u.Scanner = scanner.Fingerprint()
	got, err := scanner.Scan(ctx, run.ID, u)
	if err != nil {
		t.Fatal(err)
	}
	// The split follows the live sites, but only captured sites get a row.
	m := artifactBytes(t, got)
	if len(m) != 1 || m[s1] != 5 {
		t.Fatalf("contributions = %v (later site %d must not appear)", m, later)
	}
}

func TestArtifactBytesReachPublishedUsage(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 0)
	ctx := context.Background()
	var org int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('D','art-d') returning id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	s1 := insertAppSite(t, pool, org, "appart0021xx")
	insertBundle(t, pool, org, []int32{s1}, "ok", 4096)
	r := NewReconciler(store, MeasurementVersion, NewArtifactScanner(pool))
	now := time.Now()
	for i := 0; i < 5; i++ {
		res, err := r.Step(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if res.Published {
			break
		}
	}
	var aux int64
	if err := pool.QueryRow(ctx, `select auxiliary_bytes from site_storage_usage where site_id=$1`, s1).Scan(&aux); err != nil {
		t.Fatal(err)
	}
	if aux != 4096 {
		t.Fatalf("auxiliary_bytes = %d, want 4096", aux)
	}
}
