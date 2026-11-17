package sentryingest

import (
	"context"
	"testing"

	"supermetry/internal/testpg"
)

func TestPGArtifactStoreScopesByOrgAndSite(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	const dbg = "efd8cad4-51cc-4e52-b63c-be84e219418a"
	if _, err := pool.Exec(ctx, `
insert into orgs(name,slug) values('A','a'),('B','b');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value values
 (1,1,'appsymb0001a','A1','UTC',30,'app'),(2,1,'appsymb0002a','A2','UTC',30,'app'),(3,2,'appsymb0003b','B1','UTC',30,'app');
insert into app_artifact_bundles(id,org_id,site_ids,release,checksum,state) overriding system value values
 (1,1,'{1}','1.0','`+rep("1")+`','ok'),(2,1,'{}','1.0','`+rep("2")+`','ok'),(3,2,'{}','1.0','`+rep("3")+`','ok');
insert into app_artifact_files(bundle_id,org_id,site_id,debug_id,release,url,type,blob_key,sourcemap_ref) values
 (1,1,1,'`+dbg+`','1.0','~/assets/app.js','minified_source','b1/app.js','app.js.map'),
 (1,1,1,'`+dbg+`','1.0','~/assets/app.js.map','source_map','b1/app.js.map',''),
 (2,1,NULL,NULL,'1.0','~/assets/org.js','minified_source','b2/org.js','org.js.map'),
 (2,1,NULL,NULL,'1.0','~/assets/org.js.map','source_map','b2/org.js.map',''),
 (3,2,NULL,'`+dbg+`','1.0','~/assets/app.js','source_map','b3/app.js.map','')`); err != nil {
		t.Fatal(err)
	}
	st := NewPGArtifactStore(pool)
	got := func(key string, ok bool, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return ""
		}
		return key
	}
	if k := got(st.MapByDebugID(ctx, 1, dbg)); k != "b1/app.js.map" {
		t.Fatalf("site 1 by debug id: %q", k)
	}
	if k := got(st.MapByDebugID(ctx, 2, dbg)); k != "" { // bundle 1 covers site 1 only; bundle 3 is org B
		t.Fatalf("site 2 must not see it: %q", k)
	}
	if k := got(st.MapByDebugID(ctx, 3, dbg)); k != "b3/app.js.map" {
		t.Fatalf("org B by debug id: %q", k)
	}
	if k := got(st.MapByURL(ctx, 1, "1.0", "", "~/assets/app.js")); k != "b1/app.js.map" {
		t.Fatalf("by url via sourcemap ref: %q", k)
	}
	if k := got(st.MapByURL(ctx, 2, "1.0", "", "~/assets/app.js")); k != "" {
		t.Fatalf("site 2 by url: %q", k)
	}
	if k := got(st.MapByURL(ctx, 2, "1.0", "", "~/assets/org.js")); k != "b2/org.js.map" {
		t.Fatalf("org-wide bundle for site 2: %q", k)
	}
	if k := got(st.MapByURL(ctx, 3, "1.0", "", "~/assets/org.js")); k != "" {
		t.Fatalf("org A's org-wide bundle leaked to org B: %q", k)
	}
	if k := got(st.MapByURL(ctx, 1, "2.0", "", "~/assets/app.js")); k != "" {
		t.Fatalf("wrong release: %q", k)
	}
	// A newer upload for the same release and URL wins.
	if _, err := pool.Exec(ctx, `insert into app_artifact_bundles(id,org_id,site_ids,release,checksum,state) overriding system value values (4,1,'{1}','1.0','`+rep("4")+`','ok');
insert into app_artifact_files(bundle_id,org_id,site_id,release,url,type,blob_key,sourcemap_ref) values
 (4,1,1,'1.0','~/assets/app.js','minified_source','b4/app.js',''),(4,1,1,'1.0','~/assets/app.js.map','source_map','b4/app.js.map','')`); err != nil {
		t.Fatal(err)
	}
	if k := got(st.MapByURL(ctx, 1, "1.0", "", "~/assets/app.js")); k != "b4/app.js.map" {
		t.Fatalf("newest bundle must win (map found by url + .map): %q", k)
	}
	// The minified script goes with the same scoping rules.
	if k := got(st.MinifiedByDebugID(ctx, 1, dbg)); k != "b1/app.js" {
		t.Fatalf("minified by debug id: %q", k)
	}
	if k := got(st.MinifiedByDebugID(ctx, 2, dbg)); k != "" {
		t.Fatalf("site 2 must not see the minified script: %q", k)
	}
	if k := got(st.MinifiedByURL(ctx, 2, "1.0", "", "~/assets/org.js")); k != "b2/org.js" {
		t.Fatalf("org-wide minified for site 2: %q", k)
	}
	if k := got(st.MinifiedByURL(ctx, 3, "1.0", "", "~/assets/org.js")); k != "" {
		t.Fatalf("minified leaked to org B: %q", k)
	}
	if k := got(st.MinifiedByURL(ctx, 1, "1.0", "", "~/assets/app.js")); k != "b4/app.js" {
		t.Fatalf("newest minified must win: %q", k)
	}
}

func rep(c string) string {
	out := ""
	for range 40 {
		out += c
	}
	return out
}
