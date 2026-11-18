package platformhttp_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAppDebugFilesListDeleteAndPlatforms(t *testing.T) {
	h := newAppHealthFixture(t)
	ctx := context.Background()
	var orgID, otherOrg, file, otherFile int64
	if err := h.pool.QueryRow(ctx, `select org_id from sites where id=$1`, h.siteID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, `insert into orgs(name,slug) values('Other','other-dbg') returning id`).Scan(&otherOrg); err != nil {
		t.Fatal(err)
	}
	const sum1, sum2 = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	var up1, up2 int64
	if err := h.pool.QueryRow(ctx, `insert into app_dif_uploads(org_id,site_ids,checksum,state,size) values($1,$2,$3,'ok',123) returning id`, orgID, []int32{h.siteID}, sum1).Scan(&up1); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, `insert into app_dif_uploads(org_id,site_ids,checksum,state,size) values($1,'{}',$2,'ok',5) returning id`, otherOrg, sum2).Scan(&up2); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, `insert into app_debug_files(upload_id,org_id,site_id,debug_id,kind,arch,object_name,features,cache_state)
values($1,$2,$3,'7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01','elf','arm64','libfixture.so','{symtab}','ok') returning id`, up1, orgID, h.siteID).Scan(&file); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, `insert into app_debug_files(upload_id,org_id,site_id,debug_id,kind,cache_state)
values($1,$2,NULL,'6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b','proguard','ok') returning id`, up2, otherOrg).Scan(&otherFile); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `insert into app_missing_debug_files(site_id,kind,debug_id,code_file,arch,first_seen,last_seen,events)
values($1,'macho','de7fada3-56f7-3ce7-b81e-0b6e964459c1','libfixture.dylib','arm64',now(),now(),7)`, h.siteID); err != nil {
		t.Fatal(err)
	}

	for _, actor := range []string{"owner", "editor", "viewer"} {
		code, body, raw := h.get(t, actor, "debug-files")
		files, _ := body["files"].([]any)
		missing, _ := body["missing"].([]any)
		if code != http.StatusOK || len(files) != 1 || len(missing) != 1 {
			t.Fatalf("%s list = %d %s", actor, code, raw)
		}
		f := files[0].(map[string]any)
		if f["debug_id"] != "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01" || f["size"] != 123.0 || f["cache_state"] != "ok" {
			t.Fatalf("file = %v", f)
		}
		if m := missing[0].(map[string]any); m["code_file"] != "libfixture.dylib" || m["events"] != 7.0 {
			t.Fatalf("missing = %v", m)
		}
	}
	code, body, raw := h.get(t, "viewer", "platforms")
	plats, _ := body["platforms"].([]any)
	if code != http.StatusOK || len(plats) != 1 || plats[0].(map[string]any)["platform"] != "linux" {
		t.Fatalf("platforms = %d %s", code, raw)
	}

	del := func(actor string, id int64) int {
		return h.request(t, actor, http.MethodDelete, fmt.Sprintf("/api/private/sites/%d/app/debug-files/%d", h.siteID, id), ``).Code
	}
	if c := del("viewer", file); c != http.StatusForbidden {
		t.Fatalf("viewer delete = %d, want 403", c)
	}
	if c := del("owner", otherFile); c != http.StatusNotFound {
		t.Fatalf("another organization's file = %d, want 404", c)
	}
	if c := del("editor", file); c != http.StatusNoContent {
		t.Fatalf("editor delete = %d", c)
	}
	if c := del("owner", file); c != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", c)
	}
	_, after, raw := h.get(t, "owner", "debug-files")
	if files, _ := after["files"].([]any); len(files) != 0 || !strings.Contains(raw, `"missing"`) {
		t.Fatalf("after delete = %s", raw)
	}
}
