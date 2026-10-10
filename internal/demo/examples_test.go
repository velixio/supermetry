package demo

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"supermetry/internal/alerts"
	"supermetry/internal/api"
	platformgoals "supermetry/internal/platform/goals"
	"supermetry/internal/privacy"
	"supermetry/internal/query"
	"supermetry/internal/replay"
	"supermetry/internal/sites"
	"supermetry/internal/synth"
)

type definitionInvalidations struct{ count int }

func (d *definitionInvalidations) Invalidate() error {
	d.count++
	return nil
}

func TestSeedExamples(t *testing.T) {
	f := newSeedFixture(t)
	ctx := context.Background()
	var ownerID int64
	if err := f.data.Pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('demo-owner@demo.example','Demo owner','hash') returning id`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.Pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, f.orgID, ownerID); err != nil {
		t.Fatal(err)
	}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	index := replay.NewIndexWriter(f.data.Pool, 5*time.Millisecond, nil)
	indexCtx, stopIndex := context.WithCancel(context.Background())
	indexDone := make(chan error, 1)
	go func() { indexDone <- index.Run(indexCtx) }()
	invalidations := &definitionInvalidations{}
	f.service.deps.Blobs = blobs
	f.service.deps.Index = index
	f.service.deps.Alerts = alerts.New(f.data, nil, nil)
	f.service.deps.Definitions = invalidations

	site := f.create("UTC")
	stopIndex()
	if err := <-indexDone; err != nil {
		t.Fatal(err)
	}

	definitions := platformgoals.New(f.data, time.Hour)
	t.Cleanup(definitions.Close)
	engine := query.NewEngine(f.data.Pool, f.dict, f.registry)
	engine.SetTrackingMode(privacy.Identified)
	engine.SetGoalLookup(definitions)
	dateRange := demoRange("2026-10-08", "2026-10-10")
	goals := definitions.Goals(site.ID)
	if len(goals) != 4 {
		t.Fatalf("goals = %d, want 4", len(goals))
	}
	for _, goal := range goals {
		result, queryErr := engine.Query(ctx, api.QueryRequest{
			SiteId: site.ID, DateRange: &dateRange, Metrics: []api.Metric{api.Visits},
			Filters: &[]api.Filter{{Dimension: "goal_id", Operator: api.Is, Values: []string{strconv.FormatInt(goal.ID, 10)}}},
		})
		if queryErr != nil || result.Totals[string(api.Visits)] <= 0 {
			t.Errorf("goal %q conversions = %#v, err=%v", goal.Name, result.Totals, queryErr)
		}
	}

	var rawSteps []byte
	if err := f.data.Pool.QueryRow(ctx, `select steps from funnels where site_id=$1 and name='Signup flow'`, site.ID).Scan(&rawSteps); err != nil {
		t.Fatal(err)
	}
	var steps []api.FunnelStep
	if err := json.Unmarshal(rawSteps, &steps); err != nil {
		t.Fatal(err)
	}
	funnel, err := engine.Funnel(ctx, api.FunnelRequest{SiteId: site.ID, DateRange: dateRange, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	if len(funnel.Steps) != 4 {
		t.Fatalf("funnel steps = %d, want 4", len(funnel.Steps))
	}
	for index, step := range funnel.Steps {
		if step.Visitors <= 0 {
			t.Errorf("funnel step %d visitors = %d", index, step.Visitors)
		}
	}

	assertResourceCounts(t, f, site.ID)
	assertHeatmaps(t, f, site.ID)
	assertReplays(t, f, blobs, site.ID)
	if invalidations.count != 1 {
		t.Fatalf("definition invalidations = %d, want 1", invalidations.count)
	}
	var customID int64
	if err := f.data.Pool.QueryRow(ctx, `insert into annotations(site_id,day,text,created_by) values ($1,'2026-10-10','Keep me',$2) returning id`, site.ID, ownerID).Scan(&customID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SeedNow(ctx, site.ID); err == nil {
		t.Fatal("SeedNow() reseeded an already-ready demo")
	}
	var customExists bool
	if err := f.data.Pool.QueryRow(ctx, `select exists(select 1 from annotations where id=$1)`, customID).Scan(&customExists); err != nil {
		t.Fatal(err)
	}
	if !customExists {
		t.Fatal("SeedNow() deleted a user-created annotation")
	}
}

func TestSeedWithoutOwnerSkipsAuthoredExamples(t *testing.T) {
	f := newSeedFixture(t)
	site := f.create("UTC")
	ctx := context.Background()
	var seeded bool
	if err := f.data.Pool.QueryRow(ctx, `select demo_seeded_at is not null from sites where id=$1`, site.ID).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, table := range []string{"goals", "funnels", "annotations", "segments", "alerts"} {
		var count int
		if err := f.data.Pool.QueryRow(ctx, `select count(*) from `+table+` where site_id=$1`, site.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	if !seeded || counts["goals"] != 4 || counts["funnels"] != 1 || counts["alerts"] != 1 || counts["annotations"] != 0 || counts["segments"] != 0 {
		t.Fatalf("seeded=%t resource counts=%v", seeded, counts)
	}
}

func TestSeedRequiresFeatureDependencies(t *testing.T) {
	f := newSeedFixture(t)
	ctx := context.Background()
	var siteID int32
	if err := f.data.Pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,is_demo)
		values ($1,'missingdeps1','Missing dependencies',array['www.demo.example'],'UTC',90,true)
		returning id`, f.orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	service := New(Deps{
		Pool: f.data.Pool, Registry: f.registry, Dict: f.dict,
		Now: func() time.Time { return seedNow }, Window: 1, Visitors: 1,
	})
	t.Cleanup(service.Close)
	if err := service.SeedNow(ctx, siteID); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("SeedNow() missing dependencies error = %v", err)
	}
}

func TestWriteReplaysRequiresPersistedIndex(t *testing.T) {
	f := newSeedFixture(t)
	ctx := context.Background()
	var siteID int32
	if err := f.data.Pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,is_demo)
		values ($1,'replayack001','Replay acknowledgment',array['www.demo.example'],'UTC',90,true)
		returning id`, f.orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.service.deps.Blobs = blobs
	f.service.deps.Index = replay.NewIndexWriter(f.data.Pool, time.Hour, nil)
	started := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	recording := demoReplay{
		visit:     synth.DemoVisit{Start: started, ScreenW: 1440, Hits: []synth.DemoHit{{Kind: 0, Path: "/"}}},
		sessionID: 41, localDay: started, pathID: 1,
	}
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err = f.service.writeReplays(waitCtx, sites.Site{ID: siteID}, []demoReplay{recording})
	if err == nil {
		t.Fatal("writeReplays() succeeded without a running index writer")
	}
}

func assertResourceCounts(t *testing.T, f *seedFixture, siteID int32) {
	t.Helper()
	ctx := context.Background()
	for table, want := range map[string]int{"annotations": 2, "segments": 2, "alerts": 1} {
		var got int
		if err := f.data.Pool.QueryRow(ctx, `select count(*) from `+table+` where site_id=$1`, siteID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
	}
	var enabled bool
	var channels []int64
	if err := f.data.Pool.QueryRow(ctx, `select enabled,channel_ids from alerts where site_id=$1`, siteID).Scan(&enabled, &channels); err != nil {
		t.Fatal(err)
	}
	if enabled || len(channels) != 0 {
		t.Errorf("demo alert enabled=%v channels=%v", enabled, channels)
	}
}

func assertHeatmaps(t *testing.T, f *seedFixture, siteID int32) {
	t.Helper()
	rows, err := f.data.Pool.Query(context.Background(), `
		select distinct d.value from heatmap_cells h
		join dict d on d.id=h.path_id and d.kind=0
		where h.site_id=$1 and d.value in ('/','/pricing')`, siteID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	paths := map[string]bool{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatal(err)
		}
		paths[path] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !paths["/"] || !paths["/pricing"] {
		t.Fatalf("heatmap paths = %v, want / and /pricing", paths)
	}
}

func assertReplays(t *testing.T, f *seedFixture, blobs replay.BlobStore, siteID int32) {
	t.Helper()
	ctx := context.Background()
	rows, err := f.data.Pool.Query(ctx, `select blob_key from replay_sessions where site_id=$1 order by started_at`, siteID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		reader, err := blobs.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			reader.Close()
			t.Fatal(err)
		}
		fullSnapshot := false
		scanner := bufio.NewScanner(gzipReader)
		for scanner.Scan() {
			var event struct {
				Type int `json:"type"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			fullSnapshot = fullSnapshot || event.Type == 2
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		if err := gzipReader.Close(); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if !fullSnapshot {
			t.Errorf("replay blob %q has no full snapshot", key)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("replay_sessions has no rows")
	}
}
