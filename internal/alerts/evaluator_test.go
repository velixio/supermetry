package alerts

import (
	"context"
	"testing"
	"time"

	"panel/internal/sites"
)

func TestEvaluatorUsesExactTrafficWindowsAndCooldown(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 30, 0, 0, time.UTC)
	lastFired := now.Add(-5 * time.Hour)
	repo := &fakeRepository{alerts: []Alert{
		{ID: 1, SiteID: 10, Kind: AlertTrafficSpike, Threshold: 100, ChannelIDs: []int64{1}, Enabled: true},
		{ID: 2, SiteID: 10, Kind: AlertTrafficDrop, Threshold: 50, ChannelIDs: []int64{1}, Enabled: true},
		{ID: 3, SiteID: 10, Kind: AlertTrafficSpike, Threshold: 1, ChannelIDs: []int64{1}, Enabled: true, LastFiredAt: &lastFired},
	}}
	source := &fakeMetricSource{visitorValues: []float64{40, 100}}
	deliverer := &fakeDeliverer{}
	active := &fakeActiveVisitors{visitors: 120}
	evaluator := NewEvaluator(repo, source, staticRegistry{site: sites.Site{ID: 10, TZ: time.UTC}}, active, deliverer, func() time.Time { return now })

	if err := evaluator.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(source.visitorCalls) != 2 {
		t.Fatalf("visitor calls = %d, want 2", len(source.visitorCalls))
	}
	want := []metricCall{
		{siteID: 10, from: now.Add(-24 * time.Hour), to: now},
		{siteID: 10, from: now.Add(-8 * 24 * time.Hour), to: now.Add(-7 * 24 * time.Hour)},
	}
	if active.siteID != 10 || !active.since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("active visitor window = site %d since %v", active.siteID, active.since)
	}
	for i := range want {
		if source.visitorCalls[i] != want[i] {
			t.Errorf("visitor call %d = %+v, want %+v", i, source.visitorCalls[i], want[i])
		}
	}
	if len(deliverer.notifications) != 2 {
		t.Fatalf("deliveries = %d, want spike and drop", len(deliverer.notifications))
	}
	if len(repo.marked) != 2 {
		t.Fatalf("marked = %v, want alerts 1 and 2", repo.marked)
	}
}

func TestEvaluatorUsesSiteLocalDayForGoalAndCooldown(t *testing.T) {
	kathmandu := mustLocation(t, "Asia/Kathmandu")
	now := time.Date(2026, 10, 8, 0, 15, 0, 0, kathmandu)
	yesterday := now.Add(-time.Hour)
	today := now.Add(-15 * time.Minute)
	goalID := int64(44)
	repo := &fakeRepository{alerts: []Alert{
		{ID: 1, SiteID: 10, Kind: AlertGoalReached, Threshold: 3, GoalID: &goalID, ChannelIDs: []int64{1}, Enabled: true, LastFiredAt: &yesterday},
		{ID: 2, SiteID: 10, Kind: AlertGoalReached, Threshold: 3, GoalID: &goalID, ChannelIDs: []int64{1}, Enabled: true, LastFiredAt: &today},
	}}
	source := &fakeMetricSource{conversionValues: []float64{3}}
	deliverer := &fakeDeliverer{}
	evaluator := NewEvaluator(repo, source, staticRegistry{site: sites.Site{ID: 10, TZ: kathmandu}}, &fakeActiveVisitors{}, deliverer, func() time.Time { return now })

	if err := evaluator.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(source.conversionCalls) != 1 {
		t.Fatalf("conversion calls = %d, want 1", len(source.conversionCalls))
	}
	call := source.conversionCalls[0]
	wantFrom := time.Date(2026, 10, 8, 0, 0, 0, 0, kathmandu)
	if call.siteID != 10 || call.goalID != goalID || !call.from.Equal(wantFrom) || !call.to.Equal(now) {
		t.Fatalf("conversion call = %+v, want site=10 goal=44 from=%v to=%v", call, wantFrom, now)
	}
	if len(deliverer.notifications) != 1 || len(repo.marked) != 1 || repo.marked[0] != 1 {
		t.Fatalf("deliveries=%d marked=%v, want only alert 1", len(deliverer.notifications), repo.marked)
	}
}

func TestEvaluatorMarksFiredAfterAllDeliveriesAreAttempted(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepository{alerts: []Alert{{ID: 7, SiteID: 10, Kind: AlertTrafficSpike, Threshold: 1, ChannelIDs: []int64{1, 2}, Enabled: true}}}
	source := &fakeMetricSource{visitorValues: []float64{2}}
	deliverer := &fakeDeliverer{failAt: 1}
	evaluator := NewEvaluator(repo, source, staticRegistry{site: sites.Site{ID: 10, TZ: time.UTC}}, &fakeActiveVisitors{visitors: 2}, deliverer, func() time.Time { return now })

	if err := evaluator.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want delivery failure")
	}
	if len(repo.marked) != 1 || repo.marked[0] != 7 {
		t.Fatalf("marked = %v, want alert 7 to enter cooldown", repo.marked)
	}
	if deliverer.calls != 2 {
		t.Fatalf("delivery calls = %d, want every channel attempted", deliverer.calls)
	}
}

func TestEvaluatorReportsDeliveryFailureOnceWithoutChannelSecrets(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepository{alerts: []Alert{{ID: 7, SiteID: 10, Kind: AlertTrafficSpike, Threshold: 1, ChannelIDs: []int64{8}, Enabled: true}}}
	source := &fakeMetricSource{visitorValues: []float64{2}}
	deliverer := &fakeDeliverer{failAt: 1}
	var failures []deliveryFailure
	evaluator := NewEvaluator(repo, source, staticRegistry{site: sites.Site{ID: 10, TZ: time.UTC}}, &fakeActiveVisitors{visitors: 2}, deliverer, func() time.Time { return now }, func(alertID, channelID int64, kind ChannelKind, err error) {
		failures = append(failures, deliveryFailure{alertID: alertID, channelID: channelID, kind: kind, err: err})
	})

	if err := evaluator.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want delivery failure")
	}
	if len(failures) != 1 || failures[0].alertID != 7 || failures[0].channelID != 8 || failures[0].kind != ChannelEmail || failures[0].err == nil {
		t.Fatalf("failures = %+v", failures)
	}
}

func TestEvaluatorJobRunsEveryFiveMinutes(t *testing.T) {
	evaluator := NewEvaluator(&fakeRepository{}, &fakeMetricSource{}, staticRegistry{}, &fakeActiveVisitors{}, &fakeDeliverer{}, time.Now)
	job := evaluator.Job()
	if job.Name != "alerts" || job.Every != 5*time.Minute {
		t.Fatalf("Job() = name %q every %v", job.Name, job.Every)
	}
}

type metricCall struct {
	siteID int32
	goalID int64
	from   time.Time
	to     time.Time
}

type fakeMetricSource struct {
	visitorValues    []float64
	conversionValues []float64
	visitorCalls     []metricCall
	conversionCalls  []metricCall
}

type fakeActiveVisitors struct {
	siteID   int32
	since    time.Time
	visitors int
}

func (f *fakeActiveVisitors) ActiveSince(siteID int32, since time.Time) (int, []int32) {
	f.siteID = siteID
	f.since = since
	return f.visitors, nil
}

func (f *fakeMetricSource) Visitors(_ context.Context, site sites.Site, from, to time.Time) (float64, error) {
	f.visitorCalls = append(f.visitorCalls, metricCall{siteID: site.ID, from: from, to: to})
	value := f.visitorValues[0]
	f.visitorValues = f.visitorValues[1:]
	return value, nil
}

func (f *fakeMetricSource) Conversions(_ context.Context, site sites.Site, goalID int64, from, to time.Time) (float64, error) {
	f.conversionCalls = append(f.conversionCalls, metricCall{siteID: site.ID, goalID: goalID, from: from, to: to})
	value := f.conversionValues[0]
	f.conversionValues = f.conversionValues[1:]
	return value, nil
}

type fakeRepository struct {
	alerts []Alert
	marked []int64
}

func (f *fakeRepository) EnabledAlerts(context.Context) ([]Alert, error) {
	return append([]Alert(nil), f.alerts...), nil
}
func (f *fakeRepository) DeliveryChannels(_ context.Context, ids []int64) ([]DeliveryChannel, error) {
	channels := make([]DeliveryChannel, len(ids))
	for i, id := range ids {
		channels[i] = DeliveryChannel{ID: id, Kind: ChannelEmail, Recipients: []string{"owner@example.test"}}
	}
	return channels, nil
}
func (f *fakeRepository) MarkFired(_ context.Context, id int64, _ time.Time) error {
	f.marked = append(f.marked, id)
	return nil
}

type fakeDeliverer struct {
	calls         int
	failAt        int
	notifications []Notification
}

type deliveryFailure struct {
	alertID   int64
	channelID int64
	kind      ChannelKind
	err       error
}

func (f *fakeDeliverer) Deliver(_ context.Context, _ DeliveryChannel, notification Notification) error {
	f.calls++
	if f.failAt == f.calls {
		return context.DeadlineExceeded
	}
	f.notifications = append(f.notifications, notification)
	return nil
}

type staticRegistry struct{ site sites.Site }

func (r staticRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}
func (r staticRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r staticRegistry) All() []sites.Site {
	if r.site.ID == 0 {
		return nil
	}
	return []sites.Site{r.site}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
