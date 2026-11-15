package ingest_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/dropstats"
	"supermetry/internal/sites"
)

// linkedRegistry adds an app project and links web site 1 to it.
type linkedRegistry struct {
	sites.Registry
	app  sites.Site
	link int32
}

func (r linkedRegistry) patch(s sites.Site, ok bool) (sites.Site, bool) {
	if ok && s.ID == 1 {
		s.LinkedAppID = r.link
	}
	return s, ok
}

func (r linkedRegistry) ByPublicID(id string) (sites.Site, bool) {
	if id == r.app.PublicID {
		return r.app, true
	}
	s, ok := r.Registry.ByPublicID(id)
	return r.patch(s, ok)
}

func (r linkedRegistry) Site(id int32) (sites.Site, bool) {
	if id == r.app.ID {
		return r.app, true
	}
	s, ok := r.Registry.Site(id)
	return r.patch(s, ok)
}

type linkFixture struct {
	*handlerFixture
	stopApp func()
}

// linkedFixture links web site 1 to app site 2 (or to link, when non-zero).
func linkedFixture(t *testing.T, identified, profiles bool, link int32) *linkFixture {
	t.Helper()
	f := diagFixture(t, identified, profiles)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `insert into sites(id, org_id, public_id, name, timezone, retention_class, kind)
		overriding system value values (2, (select org_id from sites where id=1), 'appsite00002', 'App', 'UTC', 30, 'app')`); err != nil {
		t.Fatal(err)
	}
	app := sites.Site{
		ID: 2, PublicID: "appsite00002", Kind: sites.KindApp, TZ: time.UTC, Retention: 30,
		App: &sites.AppConfig{MaxReportAgeDays: 7},
	}
	f.h.Sites = linkedRegistry{Registry: f.h.Sites, app: app, link: link}
	f.h.DropStats = dropstats.New(f.pool, dropstats.Options{MaxSites: 10})
	intake := appingest.NewIntake(appingest.Config{DropStats: f.h.DropStats})
	f.h.AppIntake = intake
	w := appingest.NewWriter(f.pool, intake, appingest.WriterConfig{FlushEvery: 20 * time.Millisecond})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	lf := &linkFixture{handlerFixture: f}
	stopped := false
	lf.stopApp = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(lf.stopApp)
	return lf
}

func (f *linkFixture) appEvents(t *testing.T) int {
	t.Helper()
	f.stopApp()
	var n int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from app_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *linkFixture) dropped(t *testing.T, siteID int32, reason string) int64 {
	t.Helper()
	if err := f.h.DropStats.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := f.pool.QueryRow(context.Background(), `select coalesce(sum(count),0) from site_drop_daily where site_id=$1 and reason=$2`, siteID, reason).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLinkedSiteStoresAThreeDayOldDiagnosticReportAsAnAppEvent(t *testing.T) {
	f := linkedFixture(t, true, true, 2)
	ts := fakeNow().Add(-3 * 24 * time.Hour)
	rr := diagPost(t, f.h, diagReport("a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", ts, diagFrames()), diagOrigin, diagUA)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d", rr.Code)
	}
	if n := f.appEvents(t); n != 1 {
		t.Fatalf("app_events = %d, want 1", n)
	}
	var (
		siteID, occurrences, exitCode            int32
		kind, level, release, build, platform    string
		arch, deviceClass, errorType, fp, tagCat string
		eventTS                                  time.Time
		hashed                                   bool
	)
	err := f.pool.QueryRow(context.Background(), `
		select site_id, kind, level, release, build, platform, arch, device_class, error_type, fingerprint, occurrences, exit_code, ts,
		       install_hash is not null, tags->>'category'
		from app_events`).Scan(&siteID, &kind, &level, &release, &build, &platform, &arch, &deviceClass, &errorType, &fp, &occurrences, &exitCode, &eventTS, &hashed, &tagCat)
	if err != nil {
		t.Fatal(err)
	}
	if siteID != 2 || kind != "crash" || level != "fatal" || release != "1.2.3" || build != "abcdef1" || platform != "linux" || arch != "x64" ||
		deviceClass != "cuda" || errorType != "RuntimeError" || occurrences != 3 || exitCode != 137 || tagCat != "backend_crash" || !hashed {
		t.Fatalf("stored fields wrong: %d %s %s %s %s %s %s %s %s %d %d %s %v", siteID, kind, level, release, build, platform, arch, deviceClass, errorType, occurrences, exitCode, tagCat, hashed)
	}
	if !eventTS.Equal(ts) || len(fp) != 64 {
		t.Fatalf("ts=%v fingerprint=%q", eventTS, fp)
	}
	f.stop(t)
	var custom int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events where kind <> 0`).Scan(&custom); err != nil || custom != 0 {
		t.Fatalf("a mapped report must not also be a custom event: %v %d", err, custom)
	}
	if got := f.dropped(t, 1, "too_old"); got != 0 {
		t.Fatalf("web site too_old drops = %d", got)
	}
}

func TestLinkedSiteDropsReportsOlderThanTheAppsMaxAge(t *testing.T) {
	f := linkedFixture(t, true, true, 2)
	rr := diagPost(t, f.h, diagReport("b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", fakeNow().Add(-8*24*time.Hour), diagFrames()), diagOrigin, diagUA)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d", rr.Code)
	}
	if n := f.appEvents(t); n != 0 {
		t.Fatalf("app_events = %d", n)
	}
	if got := f.dropped(t, 2, "too_old"); got != 1 {
		t.Fatalf("app too_old drops = %d, want 1", got)
	}
}

func TestLinkedSiteDedupesAndStillFiltersBotsAndHosts(t *testing.T) {
	f := linkedFixture(t, true, true, 2)
	body := diagReport("c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", fakeNow(), diagFrames())
	diagPost(t, f.h, body, diagOrigin, diagUA)
	diagPost(t, f.h, body, diagOrigin, diagUA)
	diagPost(t, f.h, diagReport("d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4", fakeNow(), diagFrames()), diagOrigin, "curl/8.5.0")
	badHost := diagReport("e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5e5", fakeNow(), diagFrames())
	badHost["url"] = "https://not-allowed.example/diagnostics"
	if rr := diagPost(t, f.h, badHost, diagOrigin, diagUA); rr.Code != http.StatusBadRequest {
		t.Fatalf("disallowed host status %d", rr.Code)
	}
	if n := f.appEvents(t); n != 1 {
		t.Fatalf("app_events = %d, want 1 (duplicate, bot and bad host dropped)", n)
	}
}

func TestUnlinkedSiteKeepsStoringDiagnosticReportsAsCustomEvents(t *testing.T) {
	f := linkedFixture(t, true, true, 0)
	if rr := diagPost(t, f.h, diagReport("f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6f6", fakeNow(), diagFrames()), diagOrigin, diagUA); rr.Code != http.StatusAccepted {
		t.Fatalf("status %d", rr.Code)
	}
	// The 24-hour rule still applies to an unlinked site.
	diagPost(t, f.h, diagReport("a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7", fakeNow().Add(-3*24*time.Hour), diagFrames()), diagOrigin, diagUA)
	if n := f.appEvents(t); n != 0 {
		t.Fatalf("app_events = %d", n)
	}
	f.stop(t)
	var custom int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events where kind <> 0`).Scan(&custom); err != nil || custom != 1 {
		t.Fatalf("custom events: %v %d", err, custom)
	}
	if got := f.dropped(t, 1, "too_old"); got != 1 {
		t.Fatalf("too_old = %d", got)
	}
}

func TestLinkedSiteWithoutIdentityBehavesAsToday(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		identified, profiles bool
	}{{"anonymous instance", false, true}, {"profiles off", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := linkedFixture(t, tc.identified, tc.profiles, 2)
			if rr := diagPost(t, f.h, diagReport("b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8b8", fakeNow(), diagFrames()), diagOrigin, diagUA); rr.Code != http.StatusAccepted {
				t.Fatalf("status %d", rr.Code)
			}
			if n := f.appEvents(t); n != 0 {
				t.Fatalf("app_events = %d, want 0 without identity", n)
			}
		})
	}
}

func TestLinkToANonAppSiteIsIgnored(t *testing.T) {
	f := linkedFixture(t, true, true, 1) // links to itself, a web site
	if rr := diagPost(t, f.h, diagReport("c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9c9", fakeNow(), diagFrames()), diagOrigin, diagUA); rr.Code != http.StatusAccepted {
		t.Fatalf("status %d", rr.Code)
	}
	if n := f.appEvents(t); n != 0 {
		t.Fatalf("app_events = %d", n)
	}
	f.stop(t)
	var custom int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from events where kind <> 0`).Scan(&custom); err != nil || custom != 1 {
		t.Fatalf("custom events: %v %d", err, custom)
	}
}
