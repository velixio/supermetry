package alerts

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/appmonitors"
)

const (
	monitorCheckin1 = "11111111-1111-4111-8111-111111111111"
	monitorCheckin2 = "22222222-2222-4222-8222-222222222222"
)

func monitorRule(f *ruleFixture, on ...string) Rule {
	return Rule{
		Trigger: Trigger{Type: TriggerMonitor, Monitor: &MonitorTrigger{Slugs: []string{"nightly"}, On: on}},
		Actions: chanAction(f.channelID),
	}
}

// A missed slot fires once (a re-run adds nothing), and the late check-in that
// completes the slot sends one resolve and no second failure.
func TestMonitorRuleMissedThenRecovered(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	alert := f.newRule(t, "Nightly job", monitorRule(f, "missed"))
	store := appmonitors.NewStore(f.pool, func() time.Time { return f.now })
	run := func() {
		t.Helper()
		if err := f.evaluator().WithMonitors(store).Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	start := f.now.Add(time.Minute)
	cfg := &appmonitors.Config{ScheduleType: appmonitors.ScheduleInterval, Schedule: "1 hour", MarginMinutes: 1}
	if err := store.Record(ctx, appmonitors.CheckIn{SiteID: f.siteID, Slug: "nightly", Environment: "production", ID: monitorCheckin1, Status: appmonitors.StatusOK, At: start, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	f.now = start.Add(time.Hour + 3*time.Minute)
	if missed, _, err := store.Sweep(ctx, f.now); err != nil || missed != 1 {
		t.Fatalf("sweep missed=%d err=%v", missed, err)
	}
	run()
	run()
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("deliveries after a miss = %d, want 1", len(f.deliverer.notifications))
	}
	n := f.deliverer.notifications[0]
	if n.Rule == nil || n.Rule.ID != alert.ID || n.Rule.Status != "firing" || n.Rule.TriggerType != TriggerMonitor ||
		n.App == nil || n.App.MonitorSlug != "nightly" || n.App.MonitorStatus != appmonitors.StatusMissed {
		t.Fatalf("notification = %+v / %+v", n, n.App)
	}
	if f.firedKeys(t, alert.ID) != 1 {
		t.Fatalf("keys = %d", f.firedKeys(t, alert.ID))
	}

	f.now = f.now.Add(7 * time.Minute) // the job finally ran, well before the next slot
	if err := store.Record(ctx, appmonitors.CheckIn{SiteID: f.siteID, Slug: "nightly", Environment: "production", ID: monitorCheckin2, Status: appmonitors.StatusOK, At: f.now}); err != nil {
		t.Fatal(err)
	}
	run()
	run()
	if len(f.deliverer.notifications) != 2 {
		t.Fatalf("deliveries after recovery = %d, want 2", len(f.deliverer.notifications))
	}
	if r := f.deliverer.notifications[1].Rule; r == nil || r.Status != "resolved" {
		t.Fatalf("second notification = %+v", f.deliverer.notifications[1].Rule)
	}
}

func TestMonitorRuleHonoursOnAndSlugs(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	f.newRule(t, "Timeouts only", monitorRule(f, "timeout"))
	other := monitorRule(f, "missed")
	other.Trigger.Monitor.Slugs = []string{"someone-else"}
	f.newRule(t, "Other monitor", other)
	store := appmonitors.NewStore(f.pool, func() time.Time { return f.now })
	start := f.now.Add(time.Minute)
	cfg := &appmonitors.Config{ScheduleType: appmonitors.ScheduleInterval, Schedule: "1 hour", MarginMinutes: 1}
	if err := store.Record(ctx, appmonitors.CheckIn{SiteID: f.siteID, Slug: "nightly", Environment: "production", ID: monitorCheckin1, Status: appmonitors.StatusOK, At: start, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	f.now = start.Add(2 * time.Hour)
	if _, _, err := store.Sweep(ctx, f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.evaluator().WithMonitors(store).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.deliverer.notifications) != 0 {
		t.Fatalf("deliveries = %d, want none for a rule that does not match", len(f.deliverer.notifications))
	}
}
