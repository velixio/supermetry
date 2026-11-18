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
	// A stored debug file counts like a bundle; a failed one does not.
	if _, err := pool.Exec(ctx, `insert into app_dif_uploads(org_id,site_ids,checksum,state,size) values($1,$2,$3,'ok',20),($1,$2,$4,'error',500)`,
		orgA, []int32{s3}, fmt.Sprintf("%040x", 901), fmt.Sprintf("%040x", 902)); err != nil {
		t.Fatal(err)
	}

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
	if len(a) != 3 || a[s1] != 55 || a[s2] != 53 || a[s3] != 23 {
		t.Fatalf("org A = %v, want %d:55 %d:53 %d:23", a, s1, s2, s3)
	}
	if sum := a[s1] + a[s2] + a[s3]; sum != 131 {
		t.Fatalf("org A total = %d, want the 101+10 bundle and 20 debug file bytes", sum)
	}
	if b := scan(units[1]); len(b) != 1 || b[b1] != 7 {
		t.Fatalf("org B = %v", b)
	}
	// A work unit for another scanner's shape is refused (see also the commit-bytes test below).
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

func TestArtifactScannerCountsReleaseCommits(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 0)
	ctx := context.Background()
	var org, relID, repoID, commitID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('C','art-c') returning id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	s1 := insertAppSite(t, pool, org, "appcom0001xx")
	s2 := insertAppSite(t, pool, org, "appcom0002xx")
	if err := pool.QueryRow(ctx, `insert into app_releases(org_id,version,site_ids) values($1,'1.0.0','{}') returning id`, org).Scan(&relID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into app_repositories(org_id,name) values($1,'acme/app') returning id`, org).Scan(&repoID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into app_commits(org_id,repository_id,sha,message,author_name,author_email) values($1,$2,'abcd0001','msg','Dev','dev@example.com') returning id`, org, repoID).Scan(&commitID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_commit_files(commit_id,path) values($1,'src/a.ts')`, commitID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_release_commits(release_id,commit_id,position) values($1,$2,0)`, relID, commitID); err != nil {
		t.Fatal(err)
	}
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewArtifactScanner(pool)
	units, err := scanner.WorkUnits(ctx, run.ID)
	if err != nil || len(units) != 1 || units[0] != artifactWorkUnit(org) {
		t.Fatalf("units = %+v %v", units, err)
	}
	u := units[0]
	u.Scanner = scanner.Fingerprint()
	got, err := scanner.Scan(ctx, run.ID, u)
	if err != nil {
		t.Fatal(err)
	}
	// commit 8+3+3+15+32 = 61, file 8+9 = 17, repository 8+16 = 24, link 16: 118 bytes, 59 per site.
	b := artifactBytes(t, got)
	if len(b) != 2 || b[s1] != 59 || b[s2] != 59 {
		t.Fatalf("commit bytes = %v, want 59 per site", b)
	}
}
