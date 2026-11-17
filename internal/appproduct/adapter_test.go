package appproduct_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/appproduct"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/dropstats"
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
	"supermetry/internal/writer"
)

var (
	secret = []byte("0123456789abcdef0123456789abcdef")
	now    = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
)

const siteID = int32(7)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

type registry struct{ site sites.Site }

func (r registry) Site(id int32) (sites.Site, bool)     { return r.site, id == r.site.ID }
func (r registry) ByPublicID(string) (sites.Site, bool) { return r.site, true }
func (r registry) All() []sites.Site                    { return []sites.Site{r.site} }

type drops struct {
	mu      sync.Mutex
	reasons []dropstats.Reason
}

func (d *drops) Record(_ int32, _ time.Time, reason dropstats.Reason, _ string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reasons = append(d.reasons, reason)
	return true
}

func (d *drops) count(reason dropstats.Reason) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, r := range d.reasons {
		if r == reason {
			n++
		}
	}
	return n
}

func newSite() sites.Site {
	return sites.Site{
		ID: siteID, Kind: sites.KindApp, TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true,
		App: &sites.AppConfig{MaxReportAgeDays: 7, HashSecret: secret},
	}
}

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`insert into orgs(name,slug) values('App Org','app-org')`,
		`insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
		 select 7,id,'appsite00007','App','UTC',30,'app' from orgs`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

// rig is one writer plus adapter; stop drains the writer into the database.
type rig struct {
	adapter *appproduct.Adapter
	writer  *writer.Writer
	sess    session.Store
	drops   *drops
	site    sites.Site
	stop    func()
}

func newRig(t *testing.T, pool *pgxpool.Pool, mode privacy.Mode, queue int) *rig {
	t.Helper()
	site := newSite()
	sess := session.NewMemory(1, fixedClock{})
	d := &drops{}
	w2 := writer.New(pool, sess, queue, time.Hour, time.Hour)
	w2.SetClock(fixedClock{})
	w2.SetTrackingPolicy(mode, registry{site: site})
	r := &rig{adapter: appproduct.New(w2, sess, dict.New(pool), d, func() time.Time { return now }), writer: w2, sess: sess, drops: d, site: site}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w2.Run(ctx) }()
	var once sync.Once
	r.stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("writer: %v", err)
			}
		})
	}
	t.Cleanup(r.stop)
	return r
}

func at(day, clock string) time.Time {
	ts, err := time.Parse("2006-01-02 15:04", "2026-"+day+" "+clock)
	if err != nil {
		panic(err)
	}
	return ts
}

type hit struct {
	install, day, clock, screen, name string
}

func (r *rig) send(t *testing.T, h hit) appproduct.Outcome {
	t.Helper()
	release, platform, country := "1.0.0", "ios", "DE"
	switch h.install {
	case "C", "D":
		platform, country = "android", "US"
	}
	if h.install == "A" && (h.day == "10-08" || h.day == "10-09") {
		release = "1.1.0"
	}
	return r.adapter.Accept(context.Background(), r.site, appproduct.Hit{
		SiteID: siteID, TS: at(h.day, h.clock), InstallID: "install-" + h.install,
		Screen: h.screen, Name: h.name, Release: release, Platform: platform, OSName: "OS", Country: country,
	})
}

func mustAccept(t *testing.T, r *rig, hits ...hit) {
	t.Helper()
	for _, h := range hits {
		if out := r.send(t, h); !out.Accepted() {
			t.Fatalf("hit %+v: %+v", h, out)
		}
	}
}

func datasetWithoutD() []hit {
	return []hit{
		{"A", "10-06", "10:00", "home", ""}, {"A", "10-06", "10:01", "paywall", ""}, {"B", "10-06", "11:00", "home", ""},
		{"A", "10-07", "10:00", "home", ""}, {"A", "10-07", "10:02", "", "purchase"},
		{"C", "10-07", "12:00", "home", ""}, {"C", "10-07", "12:01", "paywall", ""}, {"C", "10-07", "12:02", "", "purchase"},
		{"A", "10-08", "10:00", "home", ""}, {"B", "10-08", "11:00", "home", ""},
		{"A", "10-09", "09:00", "home", ""},
	}
}

func TestAdapterDatasetPerDay(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	mustAccept(t, r, datasetWithoutD()...)
	r.stop()
	ctx := context.Background()
	for _, day := range []string{"2026-10-06", "2026-10-07", "2026-10-08"} {
		d, _ := time.Parse(time.DateOnly, day)
		if _, err := closer.CloseDay(ctx, pool, []int32{siteID}, d); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string][2]int{"2026-10-06": {2, 3}, "2026-10-07": {2, 3}, "2026-10-08": {2, 2}}
	for day, w := range want {
		var visitors, pageviews int
		if err := pool.QueryRow(ctx, `select visitors, pageviews from rollup_day where site_id=$1 and day=$2`, siteID, day).Scan(&visitors, &pageviews); err != nil {
			t.Fatalf("%s: %v", day, err)
		}
		if visitors != w[0] || pageviews != w[1] {
			t.Errorf("%s: visitors=%d pageviews=%d, want %v", day, visitors, pageviews, w)
		}
	}
	// The open day is read live from the raw events.
	var visitors, pageviews int
	if err := pool.QueryRow(ctx, `select count(distinct visitor_id), count(*) filter (where kind=0) from events where site_id=$1 and local_day='2026-10-09'`, siteID).Scan(&visitors, &pageviews); err != nil {
		t.Fatal(err)
	}
	if visitors != 1 || pageviews != 1 {
		t.Errorf("open day: visitors=%d pageviews=%d", visitors, pageviews)
	}
	// Dimensions: platform in hostname, screen as path, release as a prop.
	var hostname, path, release string
	if err := pool.QueryRow(ctx, `select h.value, p.value, e.props->>'$release' from events e
		join dict h on h.kind=16 and h.id=e.hostname_id join dict p on p.kind=0 and p.id=e.path_id
		where e.site_id=$1 and e.ts=$2`, siteID, at("10-08", "10:00")).Scan(&hostname, &path, &release); err != nil {
		t.Fatal(err)
	}
	if hostname != "ios" || path != "/home" || release != "1.1.0" {
		t.Errorf("dimensions: hostname=%q path=%q release=%q", hostname, path, release)
	}
}

func TestAdapterLateHitMarksDirty(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	mustAccept(t, r, datasetWithoutD()...)
	r.stop()
	ctx := context.Background()
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if _, err := closer.CloseDay(ctx, pool, []int32{siteID}, day); err != nil {
		t.Fatal(err)
	}

	late := newRig(t, pool, privacy.Identified, 100)
	mustAccept(t, late,
		hit{"D", "10-06", "18:00", "home", ""}, hit{"D", "10-06", "18:01", "paywall", ""}, hit{"D", "10-06", "18:02", "", "purchase"})
	late.stop()

	var n int
	var reason string
	if err := pool.QueryRow(ctx, `select count(*), coalesce(min(reason),'') from dirty_day where site_id=$1 and day=$2`, siteID, day).Scan(&n, &reason); err != nil {
		t.Fatal(err)
	}
	if n != 1 || reason != "late_event" {
		t.Fatalf("dirty_day rows=%d reason=%q", n, reason)
	}
	var total int
	if err := pool.QueryRow(ctx, `select count(*) from dirty_day`).Scan(&total); err != nil || total != 1 {
		t.Fatalf("dirty_day total=%d err=%v", total, err)
	}
}

func TestAdapterTooOld(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	out := r.send(t, hit{"A", "10-01", "10:00", "home", ""})
	if !out.Dropped || out.Reason != dropstats.TooOld {
		t.Fatalf("outcome %+v", out)
	}
	// Now is 10-09: 7 days late (10-02) is accepted, 8 days late (10-01) is not.
	if out := r.send(t, hit{"A", "10-02", "10:00", "home", ""}); !out.Accepted() {
		t.Fatalf("7 days late must be accepted: %+v", out)
	}
	r.stop()
	if r.drops.count(dropstats.TooOld) != 1 {
		t.Fatalf("too_old drops=%d", r.drops.count(dropstats.TooOld))
	}
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from events where site_id=$1`, siteID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events=%d err=%v", n, err)
	}
}

func TestAdapterNoSaltNeeded(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	mustAccept(t, r, datasetWithoutD()...)
	r.stop()
	var salts, events int
	if err := pool.QueryRow(context.Background(), `select (select count(*) from salts), (select count(*) from events)`).Scan(&salts, &events); err != nil {
		t.Fatal(err)
	}
	if salts != 0 || events != len(datasetWithoutD()) {
		t.Fatalf("salts=%d events=%d", salts, events)
	}
}

func TestAdapterBotUAKept(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	out := r.adapter.Accept(context.Background(), r.site, appproduct.Hit{
		SiteID: siteID, TS: at("10-09", "09:00"), InstallID: "install-A", Screen: "home",
		OSName: "Googlebot/2.1 (+http://www.google.com/bot.html)", Platform: "web",
	})
	if !out.Accepted() {
		t.Fatalf("outcome %+v", out)
	}
	r.stop()
	var n int
	if err := pool.QueryRow(context.Background(), `select count(*) from events where site_id=$1`, siteID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events=%d err=%v", n, err)
	}
}

func TestAdapterRejectsDollarProps(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	base := appproduct.Hit{SiteID: siteID, TS: at("10-09", "09:00"), InstallID: "install-A", Name: "purchase", Platform: "ios"}
	bad := base
	bad.Props = map[string]string{"$release": "9.9.9"}
	if out := r.adapter.Accept(context.Background(), r.site, bad); !out.Dropped || out.Reason != dropstats.AppInvalid {
		t.Fatalf("dollar prop: %+v", out)
	}
	tooMany := base
	tooMany.Props = map[string]string{}
	for i := 0; i < 21; i++ {
		tooMany.Props[strings.Repeat("k", i+1)] = "v"
	}
	if out := r.adapter.Accept(context.Background(), r.site, tooMany); !out.Dropped || out.Reason != dropstats.AppInvalid {
		t.Fatalf("21 props: %+v", out)
	}
	long := base
	long.Props = map[string]string{"k": strings.Repeat("v", 201)}
	if out := r.adapter.Accept(context.Background(), r.site, long); !out.Dropped || out.Reason != dropstats.AppInvalid {
		t.Fatalf("long value: %+v", out)
	}
	ok := base
	ok.Props = map[string]string{"plan": "pro"}
	ok.Release = "2.0.0"
	if out := r.adapter.Accept(context.Background(), r.site, ok); !out.Accepted() {
		t.Fatalf("valid props: %+v", out)
	}
	r.stop()
	var plan, release string
	if err := pool.QueryRow(context.Background(), `select props->>'plan', props->>'$release' from events where site_id=$1`, siteID).Scan(&plan, &release); err != nil {
		t.Fatal(err)
	}
	if plan != "pro" || release != "2.0.0" {
		t.Fatalf("props plan=%q release=%q", plan, release)
	}
	if got := r.drops.count(dropstats.AppInvalid); got != 3 {
		t.Fatalf("app_invalid drops=%d", got)
	}
}

func TestAdapterQueueFull(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 0)
	out := r.send(t, hit{"A", "10-09", "09:00", "home", ""})
	if !out.QueueFull || out.Dropped {
		t.Fatalf("outcome %+v", out)
	}
	if visitors, _ := r.sess.ActiveSince(siteID, at("10-09", "00:00")); visitors != 0 {
		t.Fatalf("session store was touched: %d visitors", visitors)
	}
}

func TestAdapterSecretMissing(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Identified, 100)
	site := r.site
	site.App = &sites.AppConfig{MaxReportAgeDays: 7}
	out := r.adapter.Accept(context.Background(), site, appproduct.Hit{SiteID: siteID, TS: at("10-09", "09:00"), InstallID: "install-A", Screen: "home"})
	if !out.QueueFull {
		t.Fatalf("outcome %+v", out)
	}
}

func TestAdapterAnonymousModeKeepsNoProfile(t *testing.T) {
	pool := setupDB(t)
	r := newRig(t, pool, privacy.Anonymous, 100)
	mustAccept(t, r, hit{"A", "10-09", "09:00", "home", ""})
	r.stop()
	var profiles, events int
	if err := pool.QueryRow(context.Background(), `select (select count(*) from profiles), (select count(*) from events)`).Scan(&profiles, &events); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || events != 1 {
		t.Fatalf("profiles=%d events=%d", profiles, events)
	}
}
