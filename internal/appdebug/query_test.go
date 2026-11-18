package appdebug

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"supermetry/internal/replay"
	"supermetry/internal/testpg"
)

func TestStoreListAndDelete(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('A','a'),('B','b')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values
 (1,1,'appdbq00001a','A1','UTC',30,'app'),(2,1,'appdbq00002a','A2','UTC',30,'app'),(3,2,'appdbq00003b','B1','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_dif_uploads(id,org_id,site_ids,checksum,state,size) overriding system value values
 (1,1,'{1,2}','`+rep("1")+`','ok',100),(2,2,'{}','`+rep("2")+`','ok',50)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_debug_files(id,upload_id,org_id,site_id,debug_id,kind,arch,object_name,features,cache_state,cache_key) overriding system value values
 (1,1,1,1,'`+idA+`','elf','arm64','libfixture.so','{symtab,debug}','ok','difcache/1/a'),
 (2,1,1,2,'`+idA+`','elf','arm64','libfixture.so','{symtab,debug}','ok','difcache/1/a'),
 (3,2,2,NULL,'`+idB+`','elf','','other.so','{}','ok','difcache/2/b')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_missing_debug_files(site_id,kind,debug_id,code_file,arch,first_seen,last_seen,events) values
 (1,'macho','`+idB+`','Fix','arm64',now(),now(),4),(2,'macho','`+idB+`','Fix','arm64',now(),now(),9)`); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"difcache/1/a", "difs/1/" + rep("1"), "difcache/2/b", "difs/2/" + rep("2")} {
		if _, err := blobs.PutIfAbsent(ctx, k, bytes.NewReader([]byte("x"))); err != nil {
			t.Fatal(err)
		}
	}
	st := NewStore(pool, blobs)

	got, err := st.List(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].ID != 1 || got.Files[0].Size != 100 || got.Files[0].Features[1] != "debug" {
		t.Fatalf("files = %+v", got.Files)
	}
	if len(got.Missing) != 1 || got.Missing[0].Events != 4 {
		t.Fatalf("missing = %+v (another project's rows must not show)", got.Missing)
	}

	// Another organization's file is invisible, so it cannot be deleted.
	if err := st.Delete(ctx, 1, 1, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org delete err = %v", err)
	}
	// Project 1's row goes; project 2's row still uses the upload and the cache.
	if err := st.Delete(ctx, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := blobs.Open(ctx, "difcache/1/a"); err != nil {
		t.Fatalf("shared cache blob deleted early: %v", err)
	}
	if err := st.Delete(ctx, 1, 1, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a row of another project err = %v", err)
	}
	if err := st.Delete(ctx, 1, 2, 2); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"difcache/1/a", "difs/1/" + rep("1")} {
		if r, err := blobs.Open(ctx, k); err == nil {
			r.Close()
			t.Fatalf("%s still stored after the last row went", k)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from app_dif_uploads where id=1`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("upload left: %d %v", n, err)
	}
}
