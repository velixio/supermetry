package siteexport_test

import (
	"archive/zip"
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/siteexport"
	"supermetry/internal/testpg"
)

const appFixture = `
insert into orgs(id,name,slug) overriding system value values (1,'Org','org'),(2,'Other','other');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
 values (7,1,'appexport001','App','UTC',30,'app'),(8,1,'webexport001','Web','UTC',30,'web'),(9,2,'appexport002','Theirs','UTC',30,'app');
insert into app_issues(id,site_id,fingerprint,kind,error_type,title,first_seen,last_seen,first_release,last_release,status,event_count,level,culprit)
 overriding system value values
 (1,7,'fp1','crash','TypeError','Boom: =cmd','2026-10-01T10:00:00Z','2026-10-02T11:00:00Z','1.0.0','1.1.0','open',3,'error','app.js'),
 (2,7,'fp-old','error','','Old','2026-01-01T00:00:00Z','2026-01-02T00:00:00Z','0.1','0.1','resolved',1,'warning',''),
 (3,9,'fp3','crash','','Other org issue','2026-10-01T10:00:00Z','2026-10-01T10:00:00Z','','','open',1,'error','');
insert into users(id,email,name,password_hash) overriding system value values (50,'exporter@example.test','Exporter','h');
insert into app_issue_activity(site_id,issue_id,at,actor_user_id,kind,data) values
 (7,1,'2026-10-01T10:00:00Z',null,'first_seen','{"release":"1.0.0"}'),
 (7,1,'2026-10-02T12:00:00Z',50,'priority','{"from":"medium","to":"high"}'),
 (7,1,'2026-09-01T12:00:00Z',null,'first_seen','{}'),
 (9,3,'2026-10-02T12:00:00Z',null,'first_seen','{}');
insert into app_issue_comments(site_id,issue_id,author_user_id,body,created_at,deleted_at) values
 (7,1,50,'Looks like the renderer','2026-10-02T13:00:00Z',null),
 (7,1,50,'removed','2026-10-02T13:30:00Z','2026-10-02T14:00:00Z'),
 (9,3,null,'theirs','2026-10-02T13:00:00Z',null);
insert into app_saved_views(site_id,owner_user_id,name,query,shared,created_at) values
 (7,50,'Fatal','level:fatal',true,'2026-10-02T14:00:00Z'),
 (7,50,'My private','assigned:me',false,'2026-10-02T14:00:00Z'),
 (9,null,'Theirs','is:resolved',true,'2026-10-02T14:00:00Z');
insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,level,release,environment,platform,os_version,arch,device_class,runtime,occurrences,
  install_hash,session_hash,user_hash,sdk_name,sdk_version,dist,symbolication)
 values
 (7,'2026-10-02T11:00:00Z','2026-10-02T11:00:01Z','evt-b',1,'crash','error','1.1.0','prod','javascript','14.1','arm64','mobile','chrome',2,
   '\xdeadbeef','\xfeedface','\xcafebabe','sentry.javascript.browser','8.0.0','d1','ok'),
 (7,'2026-10-01T10:00:00Z','2026-10-01T10:00:01Z','evt-a',1,'error','error','1.0.0','prod','javascript','','','','',1,null,null,'\xcafebabe','','','','missing'),
 (7,'2026-09-15T10:00:00Z','2026-09-15T10:00:01Z','evt-out',1,'error','error','1.0.0','prod','','','','','',1,null,null,null,'','','',''),
 (9,'2026-10-02T09:00:00Z','2026-10-02T09:00:01Z','evt-other',3,'crash','error','','','','','','','',1,null,null,null,'','','','');
insert into app_event_bodies(site_id,ts,event_id,body,size) values (7,'2026-10-02T11:00:00Z','evt-b','{"user":{"email":"leak@example.test","ip_address":"203.0.113.9"}}',10);
insert into app_client_reports(site_id,day,reason,category,quantity) values
 (7,'2026-10-02','ratelimit_backoff','error',5),(7,'2026-09-15','queue_overflow','error',1),(9,'2026-10-02','network_error','error',9);
insert into app_session_daily(site_id,day,release,environment,platform,sessions,errored,crashed,abnormal,users,crashed_users,errored_users) values
 (7,'2026-10-02','1.1.0','prod','javascript',32,3,4,1,4,1,2),(7,'2026-09-15','1.0.0','prod','javascript',9,0,0,0,1,0,0),(9,'2026-10-02','x','prod','javascript',5,0,0,0,1,0,0);
insert into app_releases(id,org_id,version,site_ids,url,date_created,date_released) overriding system value values
 (1,1,'1.1.0','{}','https://example.test/r','2026-10-02T08:00:00Z','2026-10-02T09:00:00Z'),
 (2,1,'only-web','{8}','','2026-10-02T08:00:00Z',null),
 (3,2,'other-org','{}','','2026-10-02T08:00:00Z',null);
insert into app_deploys(id,release_id,org_id,site_ids,environment,name,url,date_started,date_finished,date_created) overriding system value values
 (1,1,1,'{}','prod','ship','','2026-10-02T08:30:00Z','2026-10-02T08:40:00Z','2026-10-02T08:31:00Z'),
 (2,2,1,'{}','prod','','','2026-10-02T08:30:00Z',null,'2026-10-02T08:31:00Z'),
 (3,1,1,'{8}','prod','other-project','','2026-10-02T08:30:00Z',null,'2026-10-02T08:31:00Z'),
 (4,1,1,'{7}','prod','this-project','','2026-10-02T08:30:00Z',null,'2026-10-02T08:32:00Z');
insert into app_repositories(id,org_id,name) overriding system value values (1,1,'acme/app'),(2,2,'other/app');
insert into app_commits(id,org_id,repository_id,sha,message,author_name,author_email,committed_at) overriding system value values
 (1,1,1,'abcd0001','Fix =pay','Dev One','dev1@example.com','2026-10-02T07:00:00Z'),
 (2,1,1,'abcd0002','web only','Dev Two','dev2@example.com',null),
 (3,2,2,'abcd0003','theirs','Dev Three','dev3@example.com',null);
insert into app_release_commits(release_id,commit_id,position) values (1,1,0),(2,2,0),(3,3,0);
insert into app_artifact_bundles(id,org_id,site_ids,release,dist,checksum,state,size,created_at) overriding system value values
 (1,1,'{}','1.1.0','d1','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','ok',2048,'2026-10-02T07:00:00Z'),
 (2,2,'{}','x','','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','ok',1,'2026-10-02T07:00:00Z');
insert into app_artifact_files(bundle_id,org_id,site_id,debug_id,release,dist,url,type,blob_key,size) values
 (1,1,null,'11111111-1111-1111-1111-111111111111','1.1.0','d1','~/secret/path.js.map','source_map','bundles/1/secret-key',1024),
 (1,1,null,'22222222-2222-2222-2222-222222222222','1.1.0','d1','~/b.js','minified_source','bundles/1/secret-key2',1024);
insert into app_dif_uploads(id,org_id,site_ids,checksum,name,state,size,blob_key,created_at) overriding system value values
 (1,1,'{}','cccccccccccccccccccccccccccccccccccccccc','libfix.so','ok',4096,'difs/1/secret-raw','2026-10-02T07:30:00Z'),
 (2,2,'{}','dddddddddddddddddddddddddddddddddddddddd','other.so','ok',1,'difs/2/x','2026-10-02T07:30:00Z');
insert into app_debug_files(id,upload_id,org_id,site_id,debug_id,code_id,kind,arch,object_name,features,cache_state,cache_key,created_at) overriding system value values
 (1,1,1,null,'7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01','109e3c7a','elf','arm64','libfix.so','{symtab,debug}','ok','difcache/1/secret-cache','2026-10-02T07:30:00Z'),
 (2,2,2,null,'33333333-3333-3333-3333-333333333333','','elf','arm64','other.so','{}','pending',null,'2026-10-02T07:30:00Z');
`

func newAppExport(t *testing.T) (*siteexport.Exporter, context.Context) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		create table app_events_export_test partition of app_events for values from ('2026-09-01') to ('2026-11-01');
		create table app_event_bodies_export_test partition of app_event_bodies for values from ('2026-09-01') to ('2026-11-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, appFixture); err != nil {
		t.Fatal(err)
	}
	e := siteexport.New(pool)
	e.SetTrackingMode(privacy.Identified)
	return e, ctx
}

func exportFiles(t *testing.T, e *siteexport.Exporter, ctx context.Context, site int32) (map[string][][]string, []string, string) {
	t.Helper()
	var out bytes.Buffer
	if err := e.Write(ctx, &out, site, day("2026-10-01"), day("2026-10-03")); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][][]string{}
	var names []string
	var raw strings.Builder
	for _, f := range zr.File {
		names = append(names, f.Name)
		files[f.Name] = readCSV(t, f)
		for _, row := range files[f.Name] {
			raw.WriteString(strings.Join(row, ",") + "\n")
		}
	}
	return files, names, raw.String()
}

func TestAppSiteExportsExactCSVs(t *testing.T) {
	e, ctx := newAppExport(t)
	files, names, _ := exportFiles(t, e, ctx, 7)
	for _, want := range []string{"app_issues.csv", "app_events.csv", "app_releases.csv", "app_deploys.csv", "app_artifacts.csv", "app_client_reports.csv", "app_issue_activity.csv", "app_issue_comments.csv", "app_sessions_daily.csv", "app_commits.csv", "app_debug_files.csv", "app_saved_views.csv"} {
		if !slices.Contains(names, want) {
			t.Fatalf("missing %s in %v", want, names)
		}
	}
	check := func(name string, want [][]string) {
		t.Helper()
		if !slices.EqualFunc(files[name], want, slices.Equal[[]string]) {
			t.Fatalf("%s = %q\nwant %q", name, files[name], want)
		}
	}
	check("app_issues.csv", [][]string{
		{"issue_id", "title", "culprit", "level", "status", "kind", "error_type", "first_seen", "last_seen", "occurrences", "first_release", "last_release", "resolved_in_release", "priority", "assignee_id", "github_url"},
		{"1", "Boom: =cmd", "app.js", "error", "open", "crash", "TypeError", "2026-10-01T10:00:00.000Z", "2026-10-02T11:00:00.000Z", "3", "1.0.0", "1.1.0", "", "medium", "", ""},
	})
	check("app_issue_activity.csv", [][]string{
		{"activity_id", "issue_id", "at", "kind", "actor_id", "data"},
		{"1", "1", "2026-10-01T10:00:00.000Z", "first_seen", "", `{"release": "1.0.0"}`},
		{"2", "1", "2026-10-02T12:00:00.000Z", "priority", "50", `{"to": "high", "from": "medium"}`},
	})
	check("app_issue_comments.csv", [][]string{
		{"comment_id", "issue_id", "author_id", "created", "edited", "text"},
		{"1", "1", "50", "2026-10-02T13:00:00.000Z", "", "Looks like the renderer"},
	})
	// Shared views only: private ones are the owner's own searches.
	check("app_saved_views.csv", [][]string{
		{"view_id", "name", "query", "owner_id", "created"},
		{"1", "Fatal", "level:fatal", "50", "2026-10-02T14:00:00.000Z"},
	})
	check("app_events.csv", [][]string{
		{"event_id", "timestamp", "issue_id", "kind", "level", "release", "dist", "environment", "platform", "sdk_name", "sdk_version", "os_version", "arch", "device_class", "runtime", "symbolication", "occurrences"},
		{"evt-a", "2026-10-01T10:00:00.000Z", "1", "error", "error", "1.0.0", "", "prod", "javascript", "", "", "", "", "", "", "missing", "1"},
		{"evt-b", "2026-10-02T11:00:00.000Z", "1", "crash", "error", "1.1.0", "d1", "prod", "javascript", "sentry.javascript.browser", "8.0.0", "14.1", "arm64", "mobile", "chrome", "ok", "2"},
	})
	check("app_releases.csv", [][]string{
		{"release_id", "version", "url", "created", "released"},
		{"1", "1.1.0", "https://example.test/r", "2026-10-02T08:00:00.000Z", "2026-10-02T09:00:00.000Z"},
	})
	check("app_deploys.csv", [][]string{
		{"deploy_id", "release", "environment", "name", "url", "started", "finished", "created"},
		{"1", "1.1.0", "prod", "ship", "", "2026-10-02T08:30:00.000Z", "2026-10-02T08:40:00.000Z", "2026-10-02T08:31:00.000Z"},
		{"4", "1.1.0", "prod", "this-project", "", "2026-10-02T08:30:00.000Z", "", "2026-10-02T08:32:00.000Z"},
	})
	check("app_commits.csv", [][]string{
		{"release", "repository", "sha", "message", "author_name", "author_email", "committed_at"},
		{"1.1.0", "acme/app", "abcd0001", "Fix =pay", "Dev One", "dev1@example.com", "2026-10-02T07:00:00.000Z"},
	})
	check("app_client_reports.csv", [][]string{
		{"day", "reason", "category", "quantity"},
		{"2026-10-02", "ratelimit_backoff", "error", "5"},
	})
	check("app_sessions_daily.csv", [][]string{
		{"day", "release", "environment", "platform", "sessions", "errored", "crashed", "abnormal", "users", "crashed_users"},
		{"2026-10-02", "1.1.0", "prod", "javascript", "32", "3", "4", "1", "4", "1"},
	})
	check("app_artifacts.csv", [][]string{
		{"bundle_id", "release", "dist", "state", "debug_ids", "file_count", "size_bytes", "created"},
		{"1", "1.1.0", "d1", "ok", "11111111-1111-1111-1111-111111111111 22222222-2222-2222-2222-222222222222", "2", "2048", "2026-10-02T07:00:00.000Z"},
	})
	check("app_debug_files.csv", [][]string{
		{"file_id", "kind", "debug_id", "code_id", "arch", "object_name", "features", "cache_state", "size_bytes", "created"},
		{"1", "elf", "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01", "109e3c7a", "arm64", "libfix.so", "symtab debug", "ok", "4096", "2026-10-02T07:30:00.000Z"},
	})
}

func TestAppExportLeaksNoPersonalData(t *testing.T) {
	e, ctx := newAppExport(t)
	files, _, raw := exportFiles(t, e, ctx, 7)
	for name, rows := range files {
		if !strings.HasPrefix(name, "app_") {
			continue
		}
		for _, col := range rows[0] {
			lc := strings.ToLower(col)
			if lc == "users" || lc == "crashed_users" { // distinct-user counts, not identities
				continue
			}
			for _, bad := range []string{"user", "email", "ip", "hash", "body", "blob", "url_path"} {
				if name == "app_commits.csv" && lc == "author_email" {
					continue // developer metadata the customer sent; exported by design
				}
				if lc == bad || strings.Contains(lc, bad) && lc != "url" {
					t.Fatalf("%s has sensitive column %q", name, col)
				}
			}
		}
	}
	for _, bad := range []string{"cafebabe", "deadbeef", "feedface", "leak@", "203.0.113.9", "secret", "difcache/", "difs/1", "bundles/1", "ip_address"} {
		if strings.Contains(strings.ToLower(raw), bad) {
			t.Fatalf("export contains %q", bad)
		}
	}
}

func TestWebSiteExportHasNoAppFiles(t *testing.T) {
	e, ctx := newAppExport(t)
	_, names, _ := exportFiles(t, e, ctx, 8)
	for _, n := range names {
		if strings.HasPrefix(n, "app_") {
			t.Fatalf("web site export contains %s", n)
		}
	}
	if len(names) != 17 {
		t.Fatalf("web export has %d files, want 17: %v", len(names), names)
	}
}

func TestAppExportIsScopedToItsSite(t *testing.T) {
	e, ctx := newAppExport(t)
	files, _, raw := exportFiles(t, e, ctx, 9)
	if strings.Contains(raw, "evt-a") || strings.Contains(raw, "1.1.0") || strings.Contains(raw, "Boom") {
		t.Fatalf("other organisation's export contains org 1 data: %s", raw)
	}
	if got := len(files["app_events.csv"]); got != 2 {
		t.Fatalf("site 9 events rows = %d", got)
	}
}
