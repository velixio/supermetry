package alerts

import (
	"context"
	"testing"
	"time"

	"supermetry/internal/platform/authz"
	"supermetry/internal/sites"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ruleFixture struct {
	service   *Service
	pool      *pgxpool.Pool
	principal authz.Principal
	siteID    int32
	webSiteID int32
	channelID int64
	source    *fakeAppSource
	deliverer *fakeDeliverer
	now       time.Time
}

func newRuleFixture(t *testing.T) *ruleFixture {
	t.Helper()
	deliverer := &fakeDeliverer{}
	service, pool, principal, webSiteID, _ := newServiceTestWithDeliverer(t, deliverer)
	ctx := context.Background()
	f := &ruleFixture{
		service: service, pool: pool, principal: principal, webSiteID: webSiteID, deliverer: deliverer,
		source: &fakeAppSource{site: AppSite{Name: "VoiceStudio", OrgSlug: "acme", SiteSlug: "voicestudio"}},
		now:    time.Now().UTC().Truncate(time.Second),
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,kind) values ($1,'appabcdefghi','App','UTC',30,'app') returning id`, principal.OrgID).Scan(&f.siteID); err != nil {
		t.Fatal(err)
	}
	channel, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	f.channelID = channel.ID
	return f
}

// evaluator returns a fresh RuleEvaluator on the same database, as a restarted
// process would have.
func (f *ruleFixture) evaluator() *RuleEvaluator {
	registry := staticRegistry{site: sites.Site{ID: f.siteID, Kind: sites.KindApp, TZ: time.UTC, Slug: "voicestudio"}}
	store := NewAppStore(f.pool)
	return NewRuleEvaluator(f.service, f.service, f.source, store, f.deliverer, registry, func() time.Time { return f.now })
}

func (f *ruleFixture) newRule(t *testing.T, name string, rule Rule) Alert {
	t.Helper()
	alert, err := f.service.CreateAlert(context.Background(), f.principal, f.siteID, AlertInput{Kind: AlertRule, Name: name, Rule: &rule})
	if err != nil {
		t.Fatal(err)
	}
	return alert
}

func (f *ruleFixture) newIssueRule(t *testing.T) Alert {
	return f.newRule(t, "New issue", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: chanAction(f.channelID)})
}

func (f *ruleFixture) firedKeys(t *testing.T, alertID int64) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from app_alert_fired where alert_id=$1`, alertID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRuleFireOnce(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.newIssueRule(t)
	f.source.newIssues = []AppIssue{{ID: 7, Title: "Crash in render", Kind: "crash", Release: "1.0.0", Events: 3, FirstSeen: f.now.Add(-time.Minute)}}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		f.now = f.now.Add(time.Minute)
		if err := f.evaluator().Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if f.deliverer.calls != 1 || len(f.deliverer.notifications) != 1 {
		t.Fatalf("deliveries = %d, want 1", f.deliverer.calls)
	}
	n := f.deliverer.notifications[0]
	if n.Kind != AlertRule || n.Rule == nil || n.Rule.ID != alert.ID || n.Rule.Name != "New issue" || n.Rule.Status != "firing" || n.App.IssueID != 7 {
		t.Fatalf("notification = %+v", n)
	}
	if f.firedKeys(t, alert.ID) != 1 {
		t.Fatalf("keys = %d, want 1", f.firedKeys(t, alert.ID))
	}
	var state, last any
	if err := f.pool.QueryRow(ctx, `select delivery_state, last_fired_at from alerts where id=$1`, alert.ID).Scan(&state, &last); err != nil {
		t.Fatal(err)
	}
	if state != nil || last == nil {
		t.Fatalf("delivery_state=%v last_fired_at=%v, want cleared state and a fire time", state, last)
	}
}

// Review focus 1: the key and the delivery state commit together, so a delivery
// that fails (or a process that dies) after the commit is finished by the next
// evaluator exactly once, and the key is recorded exactly once.
func TestRuleFireAtomicAcrossRestart(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.newIssueRule(t)
	f.deliverer.failAt = 1
	f.source.newIssues = []AppIssue{{ID: 7, Title: "Crash in render", Kind: "crash", Events: 3, FirstSeen: f.now.Add(-time.Minute)}}
	ctx := context.Background()

	if err := f.evaluator().Run(ctx); err == nil {
		t.Fatal("first run: want the delivery error")
	}
	var saved bool
	if err := f.pool.QueryRow(ctx, `select delivery_state is not null from alerts where id=$1`, alert.ID).Scan(&saved); err != nil || !saved || f.firedKeys(t, alert.ID) != 1 {
		t.Fatalf("after the failure: delivery state saved=%v (%v) keys=%d, want both committed", saved, err, f.firedKeys(t, alert.ID))
	}

	f.now = f.now.Add(time.Minute)
	if err := f.evaluator().Run(ctx); err != nil { // a new process
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	if err := f.evaluator().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.deliverer.notifications) != 1 || f.deliverer.calls != 2 {
		t.Fatalf("successful deliveries = %d over %d attempts, want exactly 1 over 2", len(f.deliverer.notifications), f.deliverer.calls)
	}
	if f.firedKeys(t, alert.ID) != 1 {
		t.Fatalf("keys = %d, want 1", f.firedKeys(t, alert.ID))
	}
}

func TestSaveDeliveryStateWithKeyRefusesARepeat(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.newIssueRule(t)
	ctx := context.Background()
	state := DeliveryState{Notification: Notification{AlertID: alert.ID}, ChannelIDs: []int64{f.channelID}}
	if err := f.service.SaveDeliveryStateWithKey(ctx, alert.ID, state, "issue:1", f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SaveDeliveryStateWithKey(ctx, alert.ID, state, "issue:1", f.now); err != ErrAlreadyFired {
		t.Fatalf("second save error = %v, want ErrAlreadyFired", err)
	}
	// A failed state save must not leave the key behind.
	if _, err := f.pool.Exec(ctx, `update alerts set enabled=false where id=$1`, alert.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SaveDeliveryStateWithKey(ctx, alert.ID, state, "issue:2", f.now); err == nil {
		t.Fatal("save on a disabled alert: want an error")
	}
	if f.firedKeys(t, alert.ID) != 1 {
		t.Fatalf("keys = %d, want 1 (the failed save rolled back)", f.firedKeys(t, alert.ID))
	}
}

func TestRuleWithoutChannelsRecordsTheKeyAndSendsNothing(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.newRule(t, "Assign only", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: []Action{{Type: ActionAssign, UserID: f.principal.UserID}}})
	f.source.newIssues = []AppIssue{{ID: 7, Title: "t", Kind: "crash", FirstSeen: f.now}}
	for i := 0; i < 2; i++ {
		if err := f.evaluator().Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// The key plus its "never sent" marker.
	if f.deliverer.calls != 0 || f.firedKeys(t, alert.ID) != 2 {
		t.Fatalf("deliveries=%d keys=%d, want 0 and 2", f.deliverer.calls, f.firedKeys(t, alert.ID))
	}
}

func TestRuleFiltersReachTheSourceAndDropMismatches(t *testing.T) {
	f := newRuleFixture(t)
	f.newRule(t, "Prod only", Rule{
		Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}},
		Filters: Filters{Environments: []string{"production"}}, Actions: chanAction(f.channelID),
	})
	f.source.newIssues = []AppIssue{
		{ID: 7, Title: "t", Kind: "crash", FirstSeen: f.now},
		{ID: 8, Title: "u", Kind: "crash", FirstSeen: f.now},
	}
	f.source.matching = map[int64]bool{8: true}
	if err := f.evaluator().Run(context.Background()); err != nil || f.deliverer.calls != 1 {
		t.Fatalf("Run() = %v with %d deliveries, want 1", err, f.deliverer.calls)
	}
	if got := f.deliverer.notifications[0].App.IssueID; got != 8 {
		t.Fatalf("delivered issue %d, want the matching issue 8", got)
	}
	if len(f.source.matchFilters.Environments) != 1 || f.source.matchFilters.Environments[0] != "production" {
		t.Fatalf("source saw filters %+v", f.source.matchFilters)
	}
}

func TestRuleJobSkipsWebKinds(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	rule := f.newIssueRule(t)
	if _, err := f.service.CreateAlert(ctx, f.principal, f.webSiteID, AlertInput{Kind: AlertTrafficSpike, Threshold: 5, ChannelIDs: []int64{f.channelID}}); err != nil {
		t.Fatal(err)
	}
	rules, err := f.service.EnabledRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].ID != rule.ID || rules[0].Rule == nil {
		t.Fatalf("EnabledRules() = %+v, %v, want only the rule", rules, err)
	}
	if job := f.evaluator().Job(); job.Name != "alert-rules" || job.Every != time.Minute {
		t.Fatalf("Job() = %+v", job)
	}
}

func TestLegacyJobSkipsRules(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	f.newIssueRule(t)
	web, err := f.service.CreateAlert(ctx, f.principal, f.webSiteID, AlertInput{Kind: AlertTrafficSpike, Threshold: 5, ChannelIDs: []int64{f.channelID}})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := f.service.EnabledAlerts(ctx)
	if err != nil || len(legacy) != 1 || legacy[0].ID != web.ID {
		t.Fatalf("EnabledAlerts() = %+v, %v, want only the web alert", legacy, err)
	}
}

func TestIncidentStoreOpenUpdateAndUniqueOpenGroup(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.newIssueRule(t)
	ctx := context.Background()
	in := Incident{AlertID: alert.ID, SiteID: f.siteID, GroupKey: "release:2.1.0", Severity: "critical", OpenedAt: f.now, LastValue: 90, PeakValue: 90, Threshold: 95}
	opened, err := f.service.OpenIncident(ctx, in)
	if err != nil || opened.ID == 0 {
		t.Fatalf("OpenIncident() = %+v, %v", opened, err)
	}
	if _, err := f.service.OpenIncident(ctx, in); err == nil {
		t.Fatal("second open incident for the same group: want an error")
	}
	open, err := f.service.OpenIncidents(ctx, alert.ID)
	if err != nil || len(open) != 1 {
		t.Fatalf("OpenIncidents() = %+v, %v", open, err)
	}
	resolved := f.now.Add(time.Hour)
	opened.ResolvedAt = &resolved
	if err := f.service.UpdateIncident(ctx, opened); err != nil {
		t.Fatal(err)
	}
	if open, _ = f.service.OpenIncidents(ctx, alert.ID); len(open) != 0 {
		t.Fatalf("OpenIncidents() after resolve = %+v", open)
	}
	if _, err := f.service.OpenIncident(ctx, in); err != nil {
		t.Fatalf("a new incident after resolving: %v", err)
	}
}
