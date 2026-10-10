package demo

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/replay"
	"supermetry/internal/retention"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestExpireDueDeletesAndErases(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Expiry','demo-expiry') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,demo_started_at)
		values ($1,'expire-demo1','Demo','UTC',90,true,$2) returning id`, orgID, started).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`insert into profiles(site_id,external_id,first_seen_at,last_seen_at) select $1,'demo-person',$3,$3 where $2::date is not null`,
		`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id) values ($1,90,$2,$3,1,1,0,1)`,
		`insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,pageviews) values ($1,90,1,1,$3,$3,$2,1)`,
		`insert into heatmap_cells(site_id,day,path_id,device,gx,gy,clicks) select $1,$2,1,0,1,1,1 where $3::timestamptz is not null`,
		`insert into replay_sessions(site_id,session_id,started_at,last_seen_at,day,blob_key) values ($1,1,$3,$3,$2,'replay/missing')`,
	} {
		if _, err := pool.Exec(ctx, query, siteID, day, started); err != nil {
			t.Fatal(err)
		}
	}
	service := New(Deps{Pool: pool, Registry: expiryRegistry{}, Now: func() time.Time { return started.Add(DemoLifetime + time.Minute) }})
	t.Cleanup(service.Close)
	service.pending[siteID] = []pendingVisit{{}}
	service.busySeq[siteID] = 41

	if err := service.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	if err := pool.QueryRow(ctx, `select deleted_at is not null from sites where id=$1`, siteID).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("deleted=%t err=%v", deleted, err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where org_id=$1 and target_id=$2 and action='site.deleted'`, orgID, siteID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("delete audits=%d err=%v", audits, err)
	}
	if _, ok := service.pending[siteID]; ok {
		t.Fatal("expired site's pending visits were retained")
	}
	if _, ok := service.busySeq[siteID]; ok {
		t.Fatal("expired site's busy sequence was retained")
	}
	blobs, err := replay.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.EraseSite(ctx, pool, blobs, siteID); err != nil {
		t.Fatal(err)
	}
	if err := retention.Erase(ctx, pool, siteID, 10_000, 0); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"events", "sessions", "replay_sessions", "heatmap_cells", "profiles"} {
		var count int
		if err := pool.QueryRow(ctx, `select count(*) from `+table+` where site_id=$1`, siteID).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s rows=%d err=%v", table, count, err)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDemoSite(ctx, tx, store.Site{OrgID: orgID, Name: "Demo project", Timezone: "UTC", RetentionClass: 90}, started); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestExpireDueKeepsYoungDemo(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Young','young-demo') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,demo_started_at) values ($1,'young-demo02','Demo','UTC',90,true,$2) returning id`, orgID, started).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	service := New(Deps{Pool: pool, Registry: expiryRegistry{}, Now: func() time.Time { return started.Add(23*time.Hour + 59*time.Minute) }})
	t.Cleanup(service.Close)
	if err := service.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err := pool.QueryRow(ctx, `select deleted_at is null from sites where id=$1`, siteID).Scan(&active); err != nil || !active {
		t.Fatalf("active=%t err=%v", active, err)
	}
}

func TestExpireDueSkipsRunningSeed(t *testing.T) {
	f := newSeedFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	f.service.deps.SeedStep = func(ctx context.Context, _ int32) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	site, err := f.service.Create(context.Background(), f.owner, f.orgID, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := f.data.Pool.Exec(context.Background(), `update sites set demo_started_at=$2 where id=$1`, site.ID, seedNow.Add(-DemoLifetime-time.Minute)); err != nil {
		t.Fatal(err)
	}

	if err := f.service.ExpireDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err := f.data.Pool.QueryRow(context.Background(), `select deleted_at is null from sites where id=$1`, site.ID).Scan(&active); err != nil || !active {
		t.Fatalf("running demo active=%t err=%v, want active", active, err)
	}
	close(release)
	f.service.Wait()
}

func TestExpiryJob(t *testing.T) {
	service := New(Deps{})
	t.Cleanup(service.Close)
	job := service.ExpiryJob()
	if job.Name != "demo-expiry" || job.Every != time.Minute || job.Run == nil {
		t.Fatalf("expiry job = %+v", job)
	}
}

type expiryRegistry struct{}

func (expiryRegistry) Invalidate() error                    { return nil }
func (expiryRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (expiryRegistry) Site(int32) (sites.Site, bool)        { return sites.Site{}, false }
func (expiryRegistry) All() []sites.Site                    { return nil }

// Watching a demo never extends its life: heartbeats and views update
// demo_viewed_at only, and the demo still expires 24 h after it started.
func TestExpiryIgnoresWatching(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Watched','demo-watched') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id,public_id,name,timezone,retention_class,is_demo,demo_started_at,demo_seeded_at)
		values ($1,'watched-demo','Demo','UTC',90,true,$2,$2) returning id`, orgID, started).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	now := started
	service := New(Deps{Pool: pool, Registry: expiryRegistry{}, Now: func() time.Time { return now }})
	t.Cleanup(service.Close)

	deleted := func() bool {
		t.Helper()
		var gone bool
		if err := pool.QueryRow(ctx, `select deleted_at is not null from sites where id=$1`, siteID).Scan(&gone); err != nil {
			t.Fatal(err)
		}
		return gone
	}
	// Watched continuously (a heartbeat every 30 s, checked every 10 min) for
	// the whole lifetime, it must survive until the 24 h mark.
	for now = started; now.Before(started.Add(DemoLifetime)); now = now.Add(30 * time.Second) {
		service.Heartbeat(siteID)
		if now.Sub(started)%(10*time.Minute) == 0 {
			if err := service.ExpireDue(ctx); err != nil {
				t.Fatal(err)
			}
			if deleted() {
				t.Fatalf("demo deleted at %s, before its 24 h lifetime", now.Sub(started))
			}
		}
	}
	var startedAt time.Time
	if err := pool.QueryRow(ctx, `select demo_started_at from sites where id=$1`, siteID).Scan(&startedAt); err != nil {
		t.Fatal(err)
	}
	if !startedAt.Equal(started) {
		t.Fatalf("watching moved demo_started_at to %s, want %s", startedAt, started)
	}
	now = started.Add(DemoLifetime + time.Minute)
	service.Heartbeat(siteID)
	if err := service.ExpireDue(ctx); err != nil {
		t.Fatal(err)
	}
	if !deleted() {
		t.Fatal("demo still alive 24 h after start despite being watched")
	}
}
