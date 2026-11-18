package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestAppDebugFileOrgWideDeleteNeedsOrgRole(t *testing.T) {
	h := newAppHealthFixture(t)
	ctx := context.Background()
	var orgID, upload int64
	if err := h.pool.QueryRow(ctx, `select org_id from sites where id=$1`, h.siteID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, `insert into app_dif_uploads(org_id,site_ids,checksum,state,size) values($1,'{}','3333333333333333333333333333333333333333','ok',1) returning id`, orgID).Scan(&upload); err != nil {
		t.Fatal(err)
	}
	add := func(site any) int64 {
		var id int64
		if err := h.pool.QueryRow(ctx, `insert into app_debug_files(upload_id,org_id,site_id,debug_id,kind,cache_state)
values($1,$2,$3,gen_random_uuid()::text,'elf','ok') returning id`, upload, orgID, site).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exists := func(id int64) bool {
		var n int
		if err := h.pool.QueryRow(ctx, `select count(*) from app_debug_files where id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	del := func(actor string, id int64) int {
		return h.request(t, actor, http.MethodDelete, fmt.Sprintf("/api/private/sites/%d/app/debug-files/%d", h.siteID, id), ``).Code
	}
	orgWide := add(nil)
	if c := del("editor", orgWide); c != http.StatusNotFound || !exists(orgWide) {
		t.Fatalf("site editor on an org-wide file = %d, exists %v; want 404 and the row kept", c, exists(orgWide))
	}
	own := add(h.siteID)
	if c := del("editor", own); c != http.StatusNoContent || exists(own) {
		t.Fatalf("site editor on the project's own file = %d", c)
	}
	if c := del("admin", orgWide); c != http.StatusNoContent || exists(orgWide) {
		t.Fatalf("org admin on an org-wide file = %d", c)
	}
}
