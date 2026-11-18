package alerts

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/spans"
)

// fakeMetrics serves scripted values; shifted windows (baselines) ask the
// baseline function with the shift in hours.
type fakeMetrics struct {
	errors   float64
	baseline float64
	ratio    float64
	sample   int64
	perf     float64
	filters  Filters
	baseAsk  bool
	last     struct{ from, to time.Time }
}

func (m *fakeMetrics) ErrorCount(_ context.Context, _ int32, from, to time.Time, f Filters) (float64, error) {
	baseline := m.baseAsk && !m.last.to.IsZero() && to.Before(m.last.to)
	m.filters, m.last.from, m.last.to = f, from, to
	if baseline {
		return m.baseline, nil
	}
	return m.errors, nil
}

func (m *fakeMetrics) CrashFree(_ context.Context, _ int32, from, to time.Time, f Filters, _ bool) (float64, int64, error) {
	m.filters, m.last.from, m.last.to = f, from, to
	return m.ratio, m.sample, nil
}

func (m *fakeMetrics) Perf(_ context.Context, _ int32, _, _ time.Time, _ string, f Filters, _ string) (float64, int64, error) {
	m.filters = f
	return m.perf, m.sample, nil
}

func (f *ruleFixture) metricRule(t *testing.T, m MetricTrigger) Alert {
	t.Helper()
	return f.newRule(t, "Metric", Rule{Trigger: Trigger{Type: TriggerMetric, Metric: &m}, Actions: chanAction(f.channelID)})
}

func (f *ruleFixture) runMetric(t *testing.T, src RuleMetricSource, advance time.Duration) {
	t.Helper()
	f.now = f.now.Add(advance)
	if err := f.evaluator().SetMetricSource(src).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *ruleFixture) lastRule(t *testing.T) RuleNotification {
	t.Helper()
	if len(f.deliverer.notifications) == 0 {
		t.Fatal("no notification delivered")
	}
	return *f.deliverer.notifications[len(f.deliverer.notifications)-1].Rule
}

func (f *ruleFixture) incidents(t *testing.T, alertID int64) (open, total int) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(), `select count(*) filter (where resolved_at is null), count(*) from alert_incidents where alert_id=$1`, alertID).Scan(&open, &total); err != nil {
		t.Fatal(err)
	}
	return open, total
}

func TestThresholdHelpers(t *testing.T) {
	above := MetricTrigger{Op: "above", Critical: 15, Warning: f64(8)}
	for _, tc := range []struct {
		v    float64
		sev  string
		fire bool
	}{{8, "", false}, {8.1, severityWarning, true}, {15, severityWarning, true}, {15.1, severityCritical, true}, {1, "", false}} {
		if sev, fire := evaluateThreshold(above, tc.v); sev != tc.sev || fire != tc.fire {
			t.Errorf("above %v = %q,%v, want %q,%v", tc.v, sev, fire, tc.sev, tc.fire)
		}
	}
	if shouldResolve(above, 8) || !shouldResolve(above, 7.9) {
		t.Error("above resolves strictly below the lowest threshold (8)")
	}
	below := MetricTrigger{Op: "below", Critical: 95, Resolve: f64(97)}
	if sev, fire := evaluateThreshold(below, 95); fire || sev != "" {
		t.Error("a value equal to the threshold must not fire")
	}
	if shouldResolve(below, 97) || shouldResolve(below, 96) || !shouldResolve(below, 97.5) {
		t.Error("below resolves strictly above the resolve level (97)")
	}
}

func TestErrorCountStaticWithWarning(t *testing.T) {
	f := newRuleFixture(t)
	src := &fakeMetrics{}
	alert := f.newRule(t, "Metric", Rule{
		Trigger: Trigger{Type: TriggerMetric, Metric: &MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 15, Warning: f64(8), WindowMinutes: 30}},
		Filters: Filters{Environments: []string{"prod"}}, Actions: chanAction(f.channelID)})
	steps := []struct {
		value float64
		want  int
		sev   string
		stat  string
		open  int
		total int
	}{
		{12, 1, severityWarning, "firing", 1, 1},
		{12, 1, severityWarning, "firing", 1, 1}, // unchanged: nothing new
		{16, 2, severityCritical, "firing", 1, 1},
		{7, 3, severityCritical, "resolved", 0, 1},
		{12, 4, severityWarning, "firing", 1, 2},
	}
	for i, s := range steps {
		src.errors = s.value
		f.runMetric(t, src, time.Minute)
		if got := len(f.deliverer.notifications); got != s.want {
			t.Fatalf("step %d: %d notifications, want %d", i, got, s.want)
		}
		if r := f.lastRule(t); r.Severity != s.sev || r.Status != s.stat {
			t.Fatalf("step %d: last = %+v, want %s %s", i, r, s.sev, s.stat)
		}
		if open, total := f.incidents(t, alert.ID); open != s.open || total != s.total {
			t.Fatalf("step %d: incidents open=%d total=%d, want %d, %d", i, open, total, s.open, s.total)
		}
	}
	if len(src.filters.Environments) != 1 || src.filters.Environments[0] != "prod" {
		t.Fatalf("filters = %+v, want environments=[prod]", src.filters)
	}
	if n := f.deliverer.notifications[0]; n.Value != 12 || n.Threshold != 8 || n.Rule.IncidentID == 0 {
		t.Fatalf("open notification = %+v", n)
	}
}

func TestCrashFreeSessionsHysteresis(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.metricRule(t, MetricTrigger{Name: MetricCrashFreeSessions, Op: "below", Critical: 95, Resolve: f64(97), WindowMinutes: 60, MinSample: 20})
	src := &fakeMetrics{ratio: 0.925, sample: 40} // 1 - 3/40
	f.runMetric(t, src, time.Minute)
	if len(f.deliverer.notifications) != 1 || math.Abs(f.deliverer.notifications[0].Value-92.5) > 1e-9 {
		t.Fatalf("notifications = %+v, want one at 92.5", f.deliverer.notifications)
	}
	src.ratio = 0.96 // between 95 and 97: stays open, quiet
	f.runMetric(t, src, time.Hour)
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("96%% sent %d notifications, want the incident to stay open quietly", len(f.deliverer.notifications))
	}
	src.ratio = 0.975 // 39/40 healthy
	f.runMetric(t, src, time.Hour)
	if len(f.deliverer.notifications) != 2 || f.lastRule(t).Status != "resolved" {
		t.Fatalf("notifications = %d, last = %+v, want a resolve", len(f.deliverer.notifications), f.deliverer.notifications)
	}
	if open, total := f.incidents(t, alert.ID); open != 0 || total != 1 {
		t.Fatalf("incidents open=%d total=%d", open, total)
	}
}

func TestCrashFreeWindowIsWholeClosedHours(t *testing.T) {
	f := newRuleFixture(t)
	f.metricRule(t, MetricTrigger{Name: MetricCrashFreeUsers, Op: "below", Critical: 95, WindowMinutes: 120, MinSample: 1})
	f.now = time.Date(2026, 11, 16, 10, 37, 0, 0, time.UTC)
	src := &fakeMetrics{ratio: 0.7, sample: 10}
	f.runMetric(t, src, 0)
	if !src.last.from.Equal(time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)) || !src.last.to.Equal(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %v..%v, want 08:00..10:00", src.last.from, src.last.to)
	}
}

func TestMinSampleSkips(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.metricRule(t, MetricTrigger{Name: MetricCrashFreeSessions, Op: "below", Critical: 95, WindowMinutes: 60, MinSample: 20})
	src := &fakeMetrics{ratio: 0.5, sample: 19}
	f.runMetric(t, src, time.Minute)
	if len(f.deliverer.notifications) != 0 {
		t.Fatal("19 sessions with min sample 20 must not open an incident")
	}
	src.sample = 40
	f.runMetric(t, src, time.Minute)
	if len(f.deliverer.notifications) != 1 {
		t.Fatal("expected the incident to open")
	}
	src.ratio, src.sample = 1, 19 // would resolve, but the sample is too small
	f.runMetric(t, src, time.Minute)
	if open, _ := f.incidents(t, alert.ID); open != 1 || len(f.deliverer.notifications) != 1 {
		t.Fatal("a too-small sample must leave the open incident untouched")
	}
}

func TestFailureRateAndPerfRule(t *testing.T) {
	f := newRuleFixture(t)
	f.metricRule(t, MetricTrigger{Name: MetricTransactionFailRate, Op: "above", Critical: 2, WindowMinutes: 60})
	src := &fakeMetrics{perf: 3.7, sample: 1000} // 37 errors in 1,000 transactions
	f.runMetric(t, src, time.Minute)
	if len(f.deliverer.notifications) != 1 || f.deliverer.notifications[0].Value != 3.7 {
		t.Fatalf("notifications = %+v, want one at 3.7", f.deliverer.notifications)
	}

	g := newRuleFixture(t)
	g.metricRule(t, MetricTrigger{Name: "transaction_p95", Transaction: "GET /items", Op: "above", Critical: 800, WindowMinutes: 60})
	p95 := spans.Estimate(143) / 1000 // about 868 ms
	perf := &fakeMetrics{perf: p95, sample: 500}
	g.runMetric(t, perf, time.Minute)
	if len(g.deliverer.notifications) != 1 || g.deliverer.notifications[0].Value != p95 {
		t.Fatalf("notifications = %+v, want one at %v", g.deliverer.notifications, p95)
	}
}

func TestChangeComparison(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.metricRule(t, MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 40, Comparison: "change", ChangeWindow: "1w", WindowMinutes: 60})
	src := &fakeMetrics{errors: 30, baseline: 20, baseAsk: true}
	f.runMetric(t, src, time.Minute)
	if len(f.deliverer.notifications) != 1 || math.Abs(f.deliverer.notifications[0].Value-50) > 1e-9 {
		t.Fatalf("notifications = %+v, want +50%%", f.deliverer.notifications)
	}

	// Review focus 2: a zero baseline neither fires nor touches state.
	g := newRuleFixture(t)
	zero := g.metricRule(t, MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 40, Comparison: "change", ChangeWindow: "1w", WindowMinutes: 60})
	g.runMetric(t, &fakeMetrics{errors: 30, baseline: 0, baseAsk: true}, time.Minute)
	if open, total := g.incidents(t, zero.ID); len(g.deliverer.notifications) != 0 || open != 0 || total != 0 {
		t.Fatalf("zero baseline: %d notifications, %d incidents", len(g.deliverer.notifications), total)
	}
	_ = alert
}

func TestRenotify(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.metricRule(t, MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 10, WindowMinutes: 30, RenotifyMinutes: 1440})
	src := &fakeMetrics{errors: 12}
	f.runMetric(t, src, 0)
	for _, step := range []struct {
		after time.Duration
		want  int
	}{{23 * time.Hour, 1}, {time.Hour, 2}, {23 * time.Hour, 2}, {time.Hour, 3}} {
		f.runMetric(t, src, step.after)
		if len(f.deliverer.notifications) != step.want {
			t.Fatalf("after +%v: %d notifications, want %d", step.after, len(f.deliverer.notifications), step.want)
		}
	}
	if open, total := f.incidents(t, alert.ID); open != 1 || total != 1 {
		t.Fatalf("incidents open=%d total=%d, want one still open", open, total)
	}
}

func TestIncidentRestart(t *testing.T) {
	f := newRuleFixture(t)
	f.metricRule(t, MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 10, WindowMinutes: 30})
	src := &fakeMetrics{errors: 12}
	for i := 0; i < 3; i++ { // a new evaluator each run, as after a restart
		f.runMetric(t, src, time.Minute)
	}
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("notifications = %d, want exactly 1 across restarts", len(f.deliverer.notifications))
	}
}

// A delivery that fails after the key committed is finished by the next run
// once, and the incident bookkeeping catches up without a second notice.
func TestIncidentOpenSurvivesDeliveryFailure(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.metricRule(t, MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 10, WindowMinutes: 30})
	f.deliverer.failAt = 1
	src := &fakeMetrics{errors: 12}
	f.now = f.now.Add(time.Minute)
	if err := f.evaluator().SetMetricSource(src).Run(context.Background()); err == nil {
		t.Fatal("first run: want the delivery error")
	}
	for i := 0; i < 3; i++ {
		f.runMetric(t, src, time.Minute)
	}
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("notifications = %d, want exactly 1", len(f.deliverer.notifications))
	}
	var notified bool
	if err := f.pool.QueryRow(context.Background(), `select notified_at is not null from alert_incidents where alert_id=$1`, alert.ID).Scan(&notified); err != nil || !notified {
		t.Fatalf("notified_at set = %v, %v", notified, err)
	}
}

func TestLatestReleaseGroupAndStaleIncident(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.metricRule(t, MetricTrigger{Name: MetricCrashFreeSessions, Op: "below", Critical: 95, WindowMinutes: 60, MinSample: 10, Release: "latest"})
	f.source.release = &AppRelease{Release: "2.0.0", Installs: 50}
	src := &fakeMetrics{ratio: 0.9, sample: 40}
	f.runMetric(t, src, time.Minute)
	if len(src.filters.Releases) != 1 || src.filters.Releases[0] != "2.0.0" {
		t.Fatalf("filters = %+v, want release 2.0.0", src.filters)
	}
	var group string
	if err := f.pool.QueryRow(context.Background(), `select group_key from alert_incidents where alert_id=$1`, alert.ID).Scan(&group); err != nil || group != "release:2.0.0" {
		t.Fatalf("group = %q, %v", group, err)
	}
	f.source.release = &AppRelease{Release: "2.1.0", Installs: 50}
	src.ratio = 0.99
	f.runMetric(t, src, time.Minute)
	if open, total := f.incidents(t, alert.ID); open != 0 || total != 1 || len(f.deliverer.notifications) != 1 {
		t.Fatalf("after the release moved on: open=%d total=%d notifications=%d, want the old incident closed quietly", open, total, len(f.deliverer.notifications))
	}
}

// Review focus 4: a converted v1 crash-free rule whose release is in cooldown
// stays quiet, then re-sends once its 24 h are up.
func TestConvertedCrashFreeStaysQuietInCooldown(t *testing.T) {
	f := newRuleFixture(t)
	notified := f.now.Add(-3 * time.Hour)
	alert := f.newRule(t, "Crash-free rate below threshold", Rule{
		Trigger: Trigger{Type: TriggerMetric, Metric: &MetricTrigger{Name: MetricCrashFreeSessions, Op: "below", Critical: 98.5, WindowMinutes: 1440, MinSample: 100, Release: "latest", RenotifyMinutes: 1440}},
		Actions: chanAction(f.channelID), NotifyOnResolve: new(bool),
	})
	if _, err := f.pool.Exec(context.Background(), `insert into alert_incidents(alert_id,site_id,group_key,severity,opened_at,last_value,peak_value,threshold,notified_at)
		values($1,$2,'release:2.1.0','critical',$3,98.5,98.5,98.5,$3)`, alert.ID, f.siteID, notified); err != nil {
		t.Fatal(err)
	}
	f.source.release = &AppRelease{Release: "2.1.0", Installs: 200}
	src := &fakeMetrics{ratio: 0.9, sample: 200}
	f.runMetric(t, src, 0)
	f.runMetric(t, src, 20*time.Hour) // 23 h after the notice
	if f.deliverer.calls != 0 {
		t.Fatalf("deliveries = %d, want none inside the cooldown", f.deliverer.calls)
	}
	f.runMetric(t, src, 2*time.Hour) // 25 h
	if f.deliverer.calls != 1 {
		t.Fatalf("deliveries = %d, want one renotify after 24 h", f.deliverer.calls)
	}
}

// Incidents list newest first, page by id, and stay inside the rule's site.
func TestListIncidentsPagesNewestFirst(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	alert := f.metricRule(t, MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 10, WindowMinutes: 60})
	for i := 0; i < 3; i++ {
		resolved := f.now.Add(time.Duration(i+1) * time.Minute)
		if _, err := f.service.OpenIncident(ctx, Incident{AlertID: alert.ID, SiteID: f.siteID, GroupKey: "g" + string(rune('a'+i)), Severity: "critical", OpenedAt: f.now, ResolvedAt: &resolved, PeakValue: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.service.ListIncidents(ctx, f.principal, f.siteID, alert.ID, 2, 0)
	if err != nil || len(page) != 2 || page[0].GroupKey != "gc" || page[1].GroupKey != "gb" {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	rest, err := f.service.ListIncidents(ctx, f.principal, f.siteID, alert.ID, 2, page[1].ID)
	if err != nil || len(rest) != 1 || rest[0].GroupKey != "ga" {
		t.Fatalf("second page = %+v, %v", rest, err)
	}
	if _, err := f.service.ListIncidents(ctx, f.principal, f.siteID, alert.ID+999, 10, 0); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown alert err = %v, want not found", err)
	}
	other := f.principal
	other.OrgID++
	if _, err := f.service.ListIncidents(ctx, other, f.siteID, alert.ID, 10, 0); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("other org err = %v, want not found", err)
	}
}
