package sitesvc

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/session"
	"supermetry/internal/testpg"
)

type overviewClock struct{ now time.Time }

func (c overviewClock) Now() time.Time { return c.now }

func TestOverviewListsOnlyMembershipSitesWithTimezoneBucketsAndPagination(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 30, 0, 0, time.UTC)
	var userID, adminID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('member@example.test','Member','x',false) returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('admin@example.test','Admin','x',true) returning id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	var orgA, orgB, foreignOrg int64
	for _, target := range []struct {
		name, slug string
		id         *int64
	}{{"Alpha", "alpha", &orgA}, {"Beta", "beta", &orgB}, {"Foreign", "foreign", &foreignOrg}} {
		if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values($1,$2) returning id`, target.name, target.slug).Scan(target.id); err != nil {
			t.Fatal(err)
		}
	}
	var siteA, siteB, hidden, foreign int32
	for _, target := range []struct {
		orgID      int64
		publicID   string
		name, host string
		timezone   string
		id         *int32
	}{{orgA, "overview0001", "A", "a.example", "Asia/Kathmandu", &siteA}, {orgB, "overview0002", "B", "b.example", "UTC", &siteB}, {orgA, "overview0003", "Hidden", "hidden.example", "UTC", &hidden}, {foreignOrg, "overview0004", "Foreign", "foreign.example", "UTC", &foreign}} {
		if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,created_at) values($1,$2,$3,array[$4],$5,30,$6) returning id`, target.orgID, target.publicID, target.name, target.host, target.timezone, now.Add(time.Duration(*target.id)*time.Second)).Scan(target.id); err != nil {
			// Pointer values are zero before Scan; stable ordering is supplied below.
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `update sites set created_at=$2 where id=$1`, siteA, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update sites set created_at=$2 where id=$1`, siteB, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role,site_ids) values($1,$2,'viewer',array[$3::int]),($4,$2,'editor',null),($5,$6,'owner',null)`, orgA, userID, siteA, orgB, foreignOrg, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into visitors_per_minute(site_id,minute,visitors,pageviews,events) values($1,$2,2,2,0),($1,$3,3,3,0),($4,$2,4,4,0),($5,$2,99,99,0)`, siteA, now.Add(-15*time.Minute), now.Add(-25*time.Hour), siteB, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into site_last_event(site_id,last_event_at) values($1,$2)`, siteA, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	memory := session.NewMemory(1, overviewClock{now})
	memory.RecordActivity(session.Activity{SiteID: siteA, Timestamp: now.Add(-time.Minute), VisitorID: 7, Kind: 0})
	service := NewOverview(&store.Store{Pool: pool}, memory)
	service.SetClock(func() time.Time { return now })

	first, cursor, err := service.List(ctx, userID, Overview24Hours, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].ID != siteA || cursor == "" || first[0].Visitors != 2 || first[0].PreviousVisitors != 3 || first[0].LiveVisitors != 1 {
		t.Fatalf("first page=%+v cursor=%q", first, cursor)
	}
	if first[0].ChangePercent == nil || *first[0].ChangePercent < -33.34 || *first[0].ChangePercent > -33.33 {
		t.Fatalf("change percent=%v, want about -33.33", first[0].ChangePercent)
	}
	if len(first[0].Sparkline) != 24 || first[0].Sparkline[23].Start.Minute() != 0 || first[0].Sparkline[23].Start.Location().String() != "Asia/Kathmandu" {
		t.Fatalf("Kathmandu sparkline=%+v", first[0].Sparkline)
	}
	second, next, err := service.List(ctx, userID, Overview24Hours, cursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID != siteB || next != "" {
		t.Fatalf("second page=%+v next=%q; hidden=%d foreign=%d", second, next, hidden, foreign)
	}
	adminSites, _, err := service.List(ctx, adminID, Overview24Hours, "", OverviewMaxLimit)
	if err != nil || len(adminSites) != 1 || adminSites[0].ID != foreign {
		t.Fatalf("instance admin sites=%+v error=%v", adminSites, err)
	}
}

func TestOverviewRejectsInvalidPeriodLimitAndCursor(t *testing.T) {
	service := &Overview{}
	if _, err := ParseOverviewPeriod("year"); err == nil {
		t.Fatal("invalid period accepted")
	}
	for _, limit := range []int{0, OverviewMaxLimit + 1} {
		if _, _, err := service.List(context.Background(), 1, Overview24Hours, "", limit); err == nil {
			t.Fatalf("limit %d accepted", limit)
		}
	}
}

// A database built without legacy zone aliases rejects names such as
// Asia/Calcutta that Go resolves, which used to fail the whole overview.
func TestOverviewAcceptsTimezoneNamesThePostgresBuildDoesNotKnow(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 30, 0, 0, time.UTC)
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('legacy@example.test','Legacy','x',false) returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('Legacy','legacy') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,created_at) values($1,'legacyzone01','Legacy',array['legacy.example'],'Asia/Calcutta',30,$2) returning id`, orgID, now.Add(-time.Hour)).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role,site_ids) values($1,$2,'owner',null)`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into visitors_per_minute(site_id,minute,visitors,pageviews,events) values($1,$2,5,5,0)`, siteID, now.Add(-15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	service := NewOverview(&store.Store{Pool: pool}, session.NewMemory(1, overviewClock{now}))
	service.SetClock(func() time.Time { return now })

	for _, period := range []OverviewPeriod{Overview24Hours, Overview7Days, Overview30Days} {
		items, _, err := service.List(ctx, userID, period, "", OverviewMaxLimit)
		if err != nil {
			t.Fatalf("%s: %v", period, err)
		}
		if len(items) != 1 || items[0].Visitors != 5 {
			t.Fatalf("%s: items=%+v, want one site with 5 visitors", period, items)
		}
	}
}

func TestOverviewReturnsSiteKind(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 30, 0, 0, time.UTC)
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('kind@example.test','Kind','x') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Kinds','kinds') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,kind,created_at) values($1,'kindweb00001','Web',array['w.example'],'UTC',30,'web',$2),($1,'kindapp00001','Desk',array[]::text[],'UTC',90,'app',$3)`, orgID, now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	service := NewOverview(&store.Store{Pool: pool}, session.NewMemory(1, overviewClock{now}))
	service.SetClock(func() time.Time { return now })
	items, _, err := service.List(ctx, userID, Overview24Hours, "", 10)
	if err != nil || len(items) != 2 || items[0].Kind != "web" || items[1].Kind != "app" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestOverviewHighlightsFromRollupsAndAppTables(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 30, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, now.AddDate(0, 0, -16), now.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('hl@example.test','HL','x',false) returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('HL','hl') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role,site_ids) values($1,$2,'owner',null)`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	var webID, appID int32
	for _, target := range []struct {
		kind, publicID string
		id             *int32
		offset         time.Duration
	}{{"web", "highlight001", &webID, -2 * time.Hour}, {"app", "highlight002", &appID, -time.Hour}} {
		if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,kind,name,allowed_hosts,timezone,retention_class,created_at) values($1,$2,$3,$2,array['h.example'],'UTC',30,$4) returning id`,
			orgID, target.publicID, target.kind, now.Add(target.offset)).Scan(target.id); err != nil {
			t.Fatal(err)
		}
	}
	var sourceID int32
	if err := pool.QueryRow(ctx, `insert into dict(kind,value) values(2,'google.com') returning id`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	// Thirteen closed days before 2026-10-12 at 40 visitors a day; the newest seven
	// carry source rows with 15 visitors from google.com.
	for offset := 1; offset <= 13; offset++ {
		day := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -offset)
		if _, err := pool.Exec(ctx, `insert into rollup_day(site_id,day,visitors,visits,pageviews,bounces,duration_ms,engagement_ms,scroll_sum,scroll_n) values($1,$2,40,40,40,0,0,0,0,0)`, webID, day); err != nil {
			t.Fatal(err)
		}
		if offset > 7 {
			continue
		}
		if _, err := pool.Exec(ctx, `insert into rollup_day_dim(site_id,day,dim,value_id,visitors,visits,pageviews,bounces,duration_ms,engagement_ms,scroll_sum,scroll_n) values($1,$2,2,$3,15,15,15,0,0,0,0,0),($1,$2,2,0,25,25,25,0,0,0,0,0)`, webID, day, sourceID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `insert into app_issues(site_id,fingerprint,kind,first_seen,last_seen) values($1,'a','crash',$2,$2),($1,'b','crash',$2,$2),($1,'old','crash',$3,$3)`, appID, now.Add(-time.Hour), now.AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	// Crash-free users: 100% in the previous seven days, 90% in the last seven.
	if _, err := pool.Exec(ctx, `insert into app_session_daily(site_id,day,release,environment,platform,sessions,users,crashed_users) values($1,'2026-10-10','1','p','ios',50,100,10),($1,'2026-10-03','1','p','ios',50,100,0)`, appID); err != nil {
		t.Fatal(err)
	}
	service := NewOverview(&store.Store{Pool: pool}, session.NewMemory(1, overviewClock{now}))
	service.SetClock(func() time.Time { return now })
	items, _, err := service.List(ctx, userID, Overview7Days, "", OverviewMaxLimit)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	web, app := items[0], items[1]
	// Window is today plus six closed days (240) against seven earlier days (280): -14.3%.
	if len(web.Highlights) != 2 || web.Highlights[0].Kind != HighlightVisitorsChange || *web.Highlights[0].Percent != -14.3 ||
		web.Highlights[1].Kind != HighlightTopSource || web.Highlights[1].Name != "google.com" || *web.Highlights[1].Share != 0.375 {
		t.Fatalf("web highlights=%+v", web.Highlights)
	}
	if got := kinds(app.Highlights); len(got) != 2 || got[0] != HighlightNewIssues || got[1] != HighlightCrashFreeDrop {
		t.Fatalf("app highlights=%+v", app.Highlights)
	}
	if *app.Highlights[0].Count != 2 || *app.Highlights[1].Points != 10 {
		t.Fatalf("app highlight values=%+v", app.Highlights)
	}
}
