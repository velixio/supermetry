package appdebug

import (
	"context"
	"errors"
	"testing"

	"supermetry/internal/testpg"
)

// An organization-wide file (no project) serves every project, so a
// project-level delete never removes it; only an explicit org-level grant does.
func TestStoreDeleteOrgWideNeedsGrant(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('A','a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values
 (1,1,'appdbw00001a','A1','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_dif_uploads(id,org_id,site_ids,checksum,state,size) overriding system value values (1,1,'{}','`+rep("1")+`','ok',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into app_debug_files(id,upload_id,org_id,site_id,debug_id,kind,cache_state) overriding system value values
 (1,1,1,NULL,'`+idA+`','elf','ok')`); err != nil {
		t.Fatal(err)
	}
	st := NewStore(pool, nil)
	if err := st.Delete(ctx, 1, 1, 1, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project-level delete of an org-wide file = %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from app_debug_files where id=1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("row gone: %d %v", n, err)
	}
	if err := st.Delete(ctx, 1, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from app_debug_files where id=1`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("row left: %d %v", n, err)
	}
}
