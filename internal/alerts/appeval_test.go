package alerts

import (
	"context"
	"strconv"
	"testing"
	"time"

	"supermetry/internal/sites"
)

type fakeAppSource struct {
	site       AppSite
	newIssues  []AppIssue
	regressed  []AppIssue
	release    *AppRelease
	spikes     []IssueHour
	newSince   time.Time
	newKinds   []string
	hourAsked  time.Time
	releaseMin int64
}

func (f *fakeAppSource) Site(context.Context, int32) (AppSite, error) { return f.site, nil }
func (f *fakeAppSource) NewIssues(_ context.Context, _ int32, since time.Time, kinds []string, _ int) ([]AppIssue, error) {
	f.newSince, f.newKinds = since, kinds
	return f.newIssues, nil
}
func (f *fakeAppSource) RegressedIssues(context.Context, int32, time.Time, int) ([]AppIssue, error) {
	return f.regressed, nil
}
func (f *fakeAppSource) LatestRelease(_ context.Context, _ int32, _ time.Time, minInstalls int64) (*AppRelease, error) {
	f.releaseMin = minInstalls
	if f.release != nil && f.release.Installs < minInstalls {
		return nil, nil
	}
	return f.release, nil
}
func (f *fakeAppSource) IssueHours(_ context.Context, _ int32, hour time.Time, _ int64) ([]IssueHour, error) {
	f.hourAsked = hour
	return f.spikes, nil
}

type fakeAppDedup struct{ fired map[string]time.Time }

func newFakeAppDedup() *fakeAppDedup { return &fakeAppDedup{fired: map[string]time.Time{}} }
func (f *fakeAppDedup) AppFired(_ context.Context, alertID int64, keys []string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	for _, k := range keys {
		if at, ok := f.fired[strconv.FormatInt(alertID, 10)+"/"+k]; ok {
			out[k] = at
		}
	}
	return out, nil
}
func (f *fakeAppDedup) RecordAppFired(_ context.Context, alertID int64, key string, at time.Time) error {
	f.fired[strconv.FormatInt(alertID, 10)+"/"+key] = at
	return nil
}
func (f *fakeAppDedup) ForgetAppFired(_ context.Context, alertID int64, key string) error {
	delete(f.fired, strconv.FormatInt(alertID, 10)+"/"+key)
	return nil
}

type appFixture struct {
	repo      *fakeRepository
	source    *fakeAppSource
	dedup     *fakeAppDedup
	deliverer *fakeDeliverer
	now       time.Time
	eval      *Evaluator
}

func newAppFixture(alert Alert) *appFixture {
	f := &appFixture{
		now:       time.Date(2026, 11, 15, 12, 30, 0, 0, time.UTC),
		source:    &fakeAppSource{site: AppSite{Name: "VoiceStudio", OrgSlug: "acme", SiteSlug: "voicestudio"}},
		dedup:     newFakeAppDedup(),
		deliverer: &fakeDeliverer{},
	}
	alert.SiteID, alert.Enabled, alert.ChannelIDs = 10, true, []int64{1}
	f.repo = &fakeRepository{alerts: []Alert{alert}}
	site := sites.Site{ID: 10, Kind: sites.KindApp, TZ: time.UTC, Slug: "voicestudio"}
	f.eval = NewEvaluator(f.repo, &fakeMetricSource{}, staticRegistry{site: site}, nil, f.deliverer, func() time.Time { return f.now })
	f.eval.WithApp(f.source, f.dedup)
	return f
}

func (f *appFixture) run(t *testing.T) {
	t.Helper()
	if err := f.eval.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAppNewIssueFiresOnceAndHonoursKindFilter(t *testing.T) {
	f := newAppFixture(Alert{ID: 1, Kind: AlertAppNewIssue, Threshold: 1, Config: &AlertConfig{Kinds: []string{"crash"}}})
	f.source.newIssues = []AppIssue{{ID: 5, Title: "EXC_BAD_ACCESS in Render", Kind: "crash", Release: "1.4.0", Events: 3, FirstSeen: f.now.Add(-10 * time.Minute)}}
	f.run(t)
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(f.deliverer.notifications))
	}
	app := f.deliverer.notifications[0].App
	if app == nil || app.IssueID != 5 || app.AppName != "VoiceStudio" || app.IssueURL != "/app/acme/voicestudio/issues/5" || app.Release != "1.4.0" {
		t.Fatalf("notification app = %+v", app)
	}
	if len(f.source.newKinds) != 1 || f.source.newKinds[0] != "crash" || !f.source.newSince.Equal(f.now.Add(-24*time.Hour)) {
		t.Fatalf("source query kinds=%v since=%v", f.source.newKinds, f.source.newSince)
	}
	f.now = f.now.Add(5 * time.Minute)
	f.run(t)
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("second run delivered again: %d", len(f.deliverer.notifications))
	}
}

func TestAppNewIssueIgnoresIssuesOlderThanTheAlert(t *testing.T) {
	created := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	f := newAppFixture(Alert{ID: 1, Kind: AlertAppNewIssue, Threshold: 1, CreatedAt: created})
	f.run(t)
	if !f.source.newSince.Equal(created) {
		t.Fatalf("since = %v, want alert creation %v", f.source.newSince, created)
	}
}

func TestAppIssueRegressedFiresOncePerRegression(t *testing.T) {
	f := newAppFixture(Alert{ID: 2, Kind: AlertAppIssueRegressed, Threshold: 1})
	first := f.now.Add(-20 * time.Minute)
	f.source.regressed = []AppIssue{{ID: 7, Title: "Crash in Export", Kind: "crash", Release: "1.5.0", RegressedAt: first}}
	f.run(t)
	f.now = f.now.Add(5 * time.Minute)
	f.run(t)
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("deliveries = %d, want 1 for one regression", len(f.deliverer.notifications))
	}
	// Resolved and regressed again later: a new regression time is a new key.
	f.source.regressed[0].RegressedAt = f.now.Add(-time.Minute)
	f.run(t)
	if len(f.deliverer.notifications) != 2 {
		t.Fatalf("deliveries = %d, want 2 after a second regression", len(f.deliverer.notifications))
	}
}

func pct(v float64) *float64 { return &v }

func TestAppCrashFreeBelowThresholdAndInstallFloor(t *testing.T) {
	f := newAppFixture(Alert{ID: 3, Kind: AlertAppCrashFreeBelow, Threshold: 99, Config: &AlertConfig{Metric: MetricUsers}})
	f.source.release = &AppRelease{Release: "2.0.0", Installs: 99, CrashFreeUsers: pct(0.90)}
	f.run(t)
	if len(f.deliverer.notifications) != 0 || f.source.releaseMin != 100 {
		t.Fatalf("under 100 installs fired: %d (floor %d)", len(f.deliverer.notifications), f.source.releaseMin)
	}
	f.source.release = &AppRelease{Release: "2.0.0", Installs: 250, CrashFreeUsers: pct(0.985), CrashFreeSessions: pct(0.5)}
	f.run(t)
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(f.deliverer.notifications))
	}
	n := f.deliverer.notifications[0]
	if n.Value < 98.49 || n.Value > 98.51 || n.Threshold != 99 || n.App.Release != "2.0.0" || n.App.Metric != MetricUsers || n.App.Installs != 250 {
		t.Fatalf("notification = %+v app=%+v", n, n.App)
	}
}

func TestAppCrashFreeAtThresholdDoesNotFire(t *testing.T) {
	f := newAppFixture(Alert{ID: 3, Kind: AlertAppCrashFreeBelow, Threshold: 99, Config: &AlertConfig{Metric: MetricSessions}})
	f.source.release = &AppRelease{Release: "2.0.0", Installs: 500, CrashFreeUsers: pct(0.1), CrashFreeSessions: pct(0.99)}
	f.run(t)
	if len(f.deliverer.notifications) != 0 {
		t.Fatal("fired at exactly the threshold on the sessions metric")
	}
	f.source.release.CrashFreeSessions = nil // no session pings: nothing to compare
	f.run(t)
	if len(f.deliverer.notifications) != 0 {
		t.Fatal("fired without a sessions value")
	}
}

func TestAppCrashFreeDedupsFor24HoursAndRearmsAfterRecovery(t *testing.T) {
	f := newAppFixture(Alert{ID: 3, Kind: AlertAppCrashFreeBelow, Threshold: 99, Config: &AlertConfig{Metric: MetricUsers}})
	f.source.release = &AppRelease{Release: "2.0.0", Installs: 300, CrashFreeUsers: pct(0.95)}
	f.run(t)
	f.now = f.now.Add(23 * time.Hour)
	f.run(t)
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("deliveries = %d, want 1 inside 24h", len(f.deliverer.notifications))
	}
	f.now = f.now.Add(2 * time.Hour) // 25h after the first
	f.run(t)
	if len(f.deliverer.notifications) != 2 {
		t.Fatalf("deliveries = %d, want a repeat after 24h", len(f.deliverer.notifications))
	}
	// Recover, then drop again within 24h: re-armed, fires at once.
	f.source.release.CrashFreeUsers = pct(0.995)
	f.now = f.now.Add(time.Hour)
	f.run(t)
	f.source.release.CrashFreeUsers = pct(0.95)
	f.now = f.now.Add(time.Hour)
	f.run(t)
	if len(f.deliverer.notifications) != 3 {
		t.Fatalf("deliveries = %d, want fire after recovery", len(f.deliverer.notifications))
	}
}

func TestAppIssueSpike(t *testing.T) {
	cases := []struct {
		name   string
		events int64
		sum7   int64 // events over the same hour on the previous 7 days
		fire   bool
	}{
		{"30 against an average of 5", 30, 35, true},
		{"9 against an average of 0 is under the floor", 9, 0, false},
		{"20 against an average of 8 is under 3x", 20, 56, false},
		{"exactly 3x and the floor", 12, 28, true},
		{"floor with no history", 10, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAppFixture(Alert{ID: 4, Kind: AlertAppIssueSpike, Threshold: 1})
			f.source.spikes = []IssueHour{{AppIssue: AppIssue{ID: 9, Title: "Timeout", Kind: "error", Release: "1.0.0"}, Events: c.events, BaselineSum: c.sum7}}
			f.run(t)
			if got := len(f.deliverer.notifications) == 1; got != c.fire {
				t.Fatalf("fired = %v, want %v", got, c.fire)
			}
			wantHour := time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC)
			if !f.source.hourAsked.Equal(wantHour) {
				t.Fatalf("hour = %v, want last full hour %v", f.source.hourAsked, wantHour)
			}
			if c.fire {
				f.now = f.now.Add(5 * time.Minute)
				f.run(t)
				if len(f.deliverer.notifications) != 1 {
					t.Fatal("fired twice for the same issue and hour")
				}
				app := f.deliverer.notifications[0].App
				if app.Events != c.events || app.Baseline != float64(c.sum7)/7 {
					t.Fatalf("app = %+v", app)
				}
				f.now = f.now.Add(time.Hour) // next hour: a new key
				f.run(t)
				if len(f.deliverer.notifications) != 2 {
					t.Fatal("did not fire for the next hour")
				}
			}
		})
	}
}

func TestAppAlertKeysAreNotRecordedUntilDeliveryIsPersisted(t *testing.T) {
	f := newAppFixture(Alert{ID: 1, Kind: AlertAppNewIssue, Threshold: 1})
	f.source.newIssues = []AppIssue{{ID: 1, Title: "a", Kind: "crash"}, {ID: 2, Title: "b", Kind: "crash"}}
	f.deliverer.failAll = true
	if err := f.eval.Run(context.Background()); err == nil {
		t.Fatal("expected the delivery failure to surface")
	}
	// The first candidate is persisted (retried through the delivery state); the second waits.
	if len(f.dedup.fired) != 1 {
		t.Fatalf("recorded keys = %v, want only the in-flight issue", f.dedup.fired)
	}
}

func TestAppAlertOnNonAppSiteIsSkipped(t *testing.T) {
	f := newAppFixture(Alert{ID: 1, Kind: AlertAppNewIssue, Threshold: 1})
	f.eval = NewEvaluator(f.repo, &fakeMetricSource{}, staticRegistry{site: sites.Site{ID: 10, Kind: sites.KindWeb, TZ: time.UTC}}, nil, f.deliverer, func() time.Time { return f.now })
	f.eval.WithApp(f.source, f.dedup)
	f.source.newIssues = []AppIssue{{ID: 1, Kind: "crash"}}
	f.run(t)
	if len(f.deliverer.notifications) != 0 {
		t.Fatal("app alert fired for a web site")
	}
}
