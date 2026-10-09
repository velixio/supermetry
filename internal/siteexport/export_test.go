package siteexport_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"slices"
	"supermetry/internal/privacy"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/siteexport"
	"supermetry/internal/testpg"
)

func TestExporterWritesCompleteZIP(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		create table rollup_day_export_test partition of rollup_day for values from ('2026-10-01') to ('2026-10-04');
		create table rollup_day_dim_export_test partition of rollup_day_dim for values from ('2026-10-01') to ('2026-10-04');
		create table custom_closed_export_test partition of custom_closed for values from ('2026-10-01') to ('2026-10-04')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into orgs(id,name,slug) overriding system value values (1,'Org','org');
		insert into users(id,email,name,password_hash) overriding system value values (1,'owner@example.test','Owner','hash');
		insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value
		values (7,1,'siteexport01','Site','UTC',30);
		insert into imports(id,site_id,source,status,file_name,created_by) overriding system value
		values (1,7,'plausible','completed','plausible.zip',1);
		insert into imported_day(site_id,import_id,day,visitors,visits,pageviews,bounces,visit_duration_s)
		values (7,1,'2026-10-01',2,2,5,1,9);
		insert into rollup_day(site_id,day,visitors,visits,pageviews,bounces,duration_ms,engagement_ms,scroll_sum,scroll_n)
		values (7,'2026-10-02',3,4,8,1,12000,0,0,0);
		insert into dict(kind,id,value) overriding system value values
		  (0,101,'/pricing'),(2,201,'google'),(7,301,'signup');
		insert into rollup_day_dim(site_id,day,dim,value_id,visitors,visits,pageviews,bounces,duration_ms,engagement_ms,scroll_sum,scroll_n)
		values (7,'2026-10-02',1,101,2,2,5,1,9000,0,0,0);
		insert into custom_closed(site_id,day,visitor_id,name_id,path_id,source_id,medium_id,campaign_id,utm_content_id,utm_term_id,country,browser_id,os_id,device,entry_path,exit_path,props,revenue_micros,revenue_currency)
		values (7,'2026-10-02',99,301,101,201,0,0,0,0,'US',0,0,1,101,101,'{"plan":"pro"}',2500000,'USD')
	`); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	exporter := siteexport.New(pool)
	exporter.SetTrackingMode(privacy.Identified)
	if err := exporter.Write(ctx, &output, 7, day("2026-10-01"), day("2026-10-03")); err != nil {
		t.Fatal(err)
	}

	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{
		"daily_totals.csv", "pages.csv", "entry_pages.csv", "exit_pages.csv", "sources.csv",
		"utm_sources.csv", "utm_mediums.csv", "utm_campaigns.csv", "utm_contents.csv", "utm_terms.csv",
		"countries.csv", "regions.csv", "cities.csv", "browsers.csv", "operating_systems.csv", "devices.csv",
		"custom_events.csv",
	}
	gotNames := make([]string, 0, len(archive.File))
	for _, file := range archive.File {
		gotNames = append(gotNames, file.Name)
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("ZIP entries = %v, want %v", gotNames, wantNames)
	}

	daily := readCSV(t, archive.File[0])
	if got, want := daily, [][]string{{"day", "visitors", "visits", "pageviews", "bounces", "visit_duration_s"}, {"2026-10-01", "2", "2", "5", "1", "9"}, {"2026-10-02", "3", "4", "8", "1", "12"}}; !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("daily_totals.csv = %v, want %v", got, want)
	}
	custom := readCSV(t, archive.File[len(archive.File)-1])
	if len(custom) != 2 || custom[1][1] != "signup" || custom[1][3] != `{"plan": "pro"}` {
		t.Fatalf("custom_events.csv = %v", custom)
	}
}

func readCSV(t *testing.T, file *zip.File) [][]string {
	t.Helper()
	r, err := file.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rows, err := csv.NewReader(io.LimitReader(r, 1<<20)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func day(value string) time.Time {
	parsed, _ := time.Parse(time.DateOnly, value)
	return parsed
}
