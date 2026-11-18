package siteexport_test

import (
	"archive/zip"
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"supermetry/internal/db"
)

func checkCSV(t *testing.T, files map[string][][]string, name string, want [][]string) {
	t.Helper()
	if !slices.EqualFunc(files[name], want, slices.Equal[[]string]) {
		t.Fatalf("%s = %q\nwant %q", name, files[name], want)
	}
}

func TestAppExportIssueMerges(t *testing.T) {
	e, ctx, pool := newAppExportPool(t)
	if _, err := pool.Exec(ctx, `
insert into app_issues(id,site_id,fingerprint,kind,error_type,title,first_seen,last_seen,first_release,last_release,status,event_count,level,culprit,merged_into)
 overriding system value values
 (4,7,'fp4','error','','Merged away','2026-10-01T10:00:00Z','2026-10-02T10:00:00Z','1.0.0','1.0.0','open',1,'error','',1)`); err != nil {
		t.Fatal(err)
	}
	files, _, _ := exportFiles(t, e, ctx, 7)
	rows := files["app_issues.csv"]
	if len(rows) != 3 || rows[0][len(rows[0])-1] != "merged_into" {
		t.Fatalf("app_issues.csv = %q", rows)
	}
	if rows[1][0] != "1" || rows[1][16] != "" || rows[2][0] != "4" || rows[2][16] != "1" {
		t.Fatalf("merged_into column wrong: %q", rows)
	}
}

func TestAppExportAttachments(t *testing.T) {
	e, ctx, pool := newAppExportPool(t)
	if _, err := pool.Exec(ctx, `
create table app_event_attachments_export_test partition of app_event_attachments for values from ('2026-09-01') to ('2026-11-01');
insert into app_event_attachments(site_id,ts,event_id,idx,name,content_type,size,data) values
 (7,'2026-10-02T11:00:00Z','evt-b',0,'screenshot.png','image/png',4,'\x89504e47'),
 (7,'2026-10-02T11:00:00Z','evt-b',1,'../etc/pass wd.json','application/json',2,'{}'),
 (7,'2026-09-15T10:00:00Z','evt-out',0,'old.txt','text/plain',3,'old'),
 (9,'2026-10-02T09:00:00Z','evt-other',0,'theirs.txt','text/plain',6,'theirs')`); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := e.Write(ctx, &out, 7, day("2026-10-01"), day("2026-10-03")); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string]string{}
	files := map[string][][]string{}
	for _, f := range zr.File {
		if f.Name == "app_event_attachments.csv" {
			files[f.Name] = readCSV(t, f)
			continue
		}
		if strings.HasPrefix(f.Name, "app_attachments/") {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			blobs[f.Name] = string(b)
		}
	}
	checkCSV(t, files, "app_event_attachments.csv", [][]string{
		{"event_id", "index", "name", "content_type", "size_bytes", "created", "file"},
		{"evt-b", "0", "screenshot.png", "image/png", "4", "2026-10-02T11:00:00.000Z", "app_attachments/evt-b/0-screenshot.png"},
		{"evt-b", "1", "../etc/pass wd.json", "application/json", "2", "2026-10-02T11:00:00.000Z", "app_attachments/evt-b/1-.._etc_pass_wd.json"},
	})
	want := map[string]string{
		"app_attachments/evt-b/0-screenshot.png":      "\x89PNG",
		"app_attachments/evt-b/1-.._etc_pass_wd.json": "{}",
	}
	if len(blobs) != len(want) {
		t.Fatalf("blobs = %q", blobs)
	}
	for k, v := range want {
		if blobs[k] != v {
			t.Fatalf("%s = %q, want %q", k, blobs[k], v)
		}
	}
}

func TestAppExportMetricRollups(t *testing.T) {
	e, ctx, pool := newAppExportPool(t)
	if err := db.EnsureMonthlyPartitions(ctx, pool, "metric_hourly", day("2026-09-01"), day("2026-11-01")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
insert into metric_series(id,site_id,name,kind,unit,bounds,attrs,attrs_hash,first_seen,last_seen) overriding system value values
 (1,7,'queue.depth','gauge','1',null,'{}','\x01','2026-10-02T09:00:00Z','2026-10-02T10:00:00Z'),
 (2,7,'http.latency','histogram','ms','{10,100}','{}','\x02','2026-10-02T09:00:00Z','2026-10-02T10:00:00Z'),
 (3,9,'theirs','gauge','','{}','{}','\x03','2026-10-02T09:00:00Z','2026-10-02T10:00:00Z');
insert into metric_hourly(site_id,series_id,hour,samples,value_sum,value_min,value_max,value_last,count,sum,bucket_counts) values
 (7,1,'2026-10-02 09:00',4,10,1,5,2.5,null,null,null),
 (7,2,'2026-10-02 10:00',6,null,3,200,null,6,420.5,'{1,3,2}'),
 (7,1,'2026-09-01 09:00',1,1,1,1,1,null,null,null),
 (9,3,'2026-10-02 09:00',1,1,1,1,1,null,null,null)`); err != nil {
		t.Fatal(err)
	}
	files, _, _ := exportFiles(t, e, ctx, 7)
	checkCSV(t, files, "metric_hourly.csv", [][]string{
		{"series_id", "local_hour", "samples", "value_sum", "value_min", "value_max", "value_last", "count", "sum", "bucket_counts"},
		{"1", "2026-10-02T09:00", "4", "10", "1", "5", "2.5", "", "", ""},
		{"2", "2026-10-02T10:00", "6", "", "3", "200", "", "6", "420.5", "1 3 2"},
	})
	checkCSV(t, files, "metric_series.csv", [][]string{
		{"series_id", "name", "kind", "unit", "bounds", "attributes", "first_seen", "last_seen"},
		{"1", "queue.depth", "gauge", "1", "", "{}", "2026-10-02T09:00:00.000Z", "2026-10-02T10:00:00.000Z"},
		{"2", "http.latency", "histogram", "ms", "10 100", "{}", "2026-10-02T09:00:00.000Z", "2026-10-02T10:00:00.000Z"},
	})
}
