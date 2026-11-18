package appdebug

import (
	"context"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/testpg"
)

const (
	idA = "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01"
	idB = "6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b"
)

func rep(c string) string { return strings.Repeat(c, 40) }

func TestMissRecorderAggregatesAndFlushes(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
insert into orgs(name,slug) values('A','a');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values
 (1,1,'appmiss0001a','A1','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	rec := NewMissRecorder(pool, time.Hour, 100)
	t0 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	im := appingest.DebugImage{Type: "elf", DebugID: idA, CodeFile: "/data/app/~~x/com.example-1/lib/arm64/libfixture.so", Arch: "arm64"}
	rec.Missing(1, "elf", im, t0)
	rec.Missing(1, "elf", im, t0.Add(2*time.Minute))
	if err := rec.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	rec.Missing(1, "elf", im, t0.Add(time.Minute))
	rec.Missing(1, "proguard", appingest.DebugImage{Type: "proguard", UUID: "6A1B8C0E-3D5F-3E2A-9B47-0C1D2E3F4A5B"}, t0.Add(time.Minute))
	// System images are never uploaded by users.
	rec.Missing(1, "macho", appingest.DebugImage{Type: "macho", DebugID: idB, CodeFile: "/System/Library/Frameworks/UIKitCore.framework/UIKitCore"}, t0)
	rec.Missing(1, "elf", appingest.DebugImage{Type: "elf", DebugID: idB, CodeFile: "/apex/com.android.runtime/lib64/bionic/libc.so"}, t0)
	if err := rec.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	type row struct {
		kind, id, file string
		events         int64
		first, last    time.Time
	}
	rows, err := pool.Query(ctx, `select kind, debug_id, code_file, events, first_seen, last_seen from app_missing_debug_files where site_id=1 order by kind`)
	if err != nil {
		t.Fatal(err)
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.kind, &r.id, &r.file, &r.events, &r.first, &r.last); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	if len(got) != 2 {
		t.Fatalf("rows = %+v", got)
	}
	if got[0].kind != "elf" || got[0].id != idA || got[0].events != 3 || got[0].file != "libfixture.so" || !got[0].last.Equal(t0.Add(2*time.Minute)) || !got[0].first.Equal(t0) {
		t.Fatalf("elf row = %+v", got[0])
	}
	if got[1].kind != "proguard" || got[1].id != idB || got[1].events != 1 {
		t.Fatalf("proguard row = %+v", got[1])
	}
}

func TestMissRecorderCapCountsDrops(t *testing.T) {
	rec := NewMissRecorder(nil, time.Hour, 2)
	now := time.Now()
	for _, id := range []string{idA, idB, "11111111-1111-1111-1111-111111111111"} {
		rec.Missing(1, "elf", appingest.DebugImage{Type: "elf", DebugID: id}, now)
	}
	rec.Missing(1, "elf", appingest.DebugImage{Type: "elf", DebugID: idA}, now) // a held key still counts
	if rec.Dropped() != 1 {
		t.Fatalf("dropped = %d", rec.Dropped())
	}
}

func TestMissRecorderSkipsAlreadyUploaded(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
insert into orgs(name,slug) values('A','a');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values
 (1,1,'appmiss0001a','A1','UTC',30,'app');
insert into app_dif_uploads(id,org_id,site_ids,checksum,state) overriding system value values (1,1,'{}','`+rep("1")+`','ok');
insert into app_debug_files(upload_id,org_id,site_id,debug_id,kind,cache_state,cache_key) values (1,1,NULL,'`+idA+`','elf','ok','k')`); err != nil {
		t.Fatal(err)
	}
	rec := NewMissRecorder(pool, time.Hour, 10)
	rec.Missing(1, "elf", appingest.DebugImage{Type: "elf", DebugID: idA}, time.Now())
	if err := rec.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from app_missing_debug_files`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
