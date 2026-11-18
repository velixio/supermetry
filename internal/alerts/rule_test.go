package alerts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func chanAction(ids ...int64) []Action { return []Action{{Type: ActionChannel, ChannelIDs: ids}} }

func f64(v float64) *float64 { return &v }

func TestValidateRule(t *testing.T) {
	issue := Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}
	metric := func(m MetricTrigger) Trigger { return Trigger{Type: TriggerMetric, Metric: &m} }
	tests := []struct {
		name     string
		rule     Rule
		wantPath string // empty: valid
	}{
		{"issue", Rule{Trigger: issue, Actions: chanAction(1)}, ""},
		{"event above", Rule{Trigger: Trigger{Type: TriggerEvent, Event: &EventTrigger{WindowMinutes: 30, Above: 8}}, Actions: chanAction(1)}, ""},
		{"event spike", Rule{Trigger: Trigger{Type: TriggerEvent, Event: &EventTrigger{Spike: &Spike{}}}, Actions: chanAction(1)}, ""},
		{"metric", Rule{Trigger: metric(MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 15, WindowMinutes: 60}), Actions: chanAction(1)}, ""},
		{"monitor", Rule{Trigger: Trigger{Type: TriggerMonitor, Monitor: &MonitorTrigger{Slugs: []string{"nightly-backup"}}}, Actions: chanAction(1)}, ""},
		{"event window too small", Rule{Trigger: Trigger{Type: TriggerEvent, Event: &EventTrigger{WindowMinutes: 4, Above: 8}}, Actions: chanAction(1)}, "trigger.event.window_minutes"},
		{"event above and spike", Rule{Trigger: Trigger{Type: TriggerEvent, Event: &EventTrigger{Above: 8, Spike: &Spike{}}}, Actions: chanAction(1)}, "trigger.event.above"},
		{"metric window", Rule{Trigger: metric(MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 15, WindowMinutes: 1441}), Actions: chanAction(1)}, "trigger.metric.window_minutes"},
		{"crash free needs whole hours", Rule{Trigger: metric(MetricTrigger{Name: MetricCrashFreeUsers, Op: "below", Critical: 95, WindowMinutes: 90}), Actions: chanAction(1)}, "trigger.metric.window_minutes"},
		{"warning less severe", Rule{Trigger: metric(MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 15, Warning: f64(20), WindowMinutes: 60}), Actions: chanAction(1)}, "trigger.metric.warning"},
		{"transaction on error count", Rule{Trigger: metric(MetricTrigger{Name: MetricErrorCount, Transaction: "GET /", Op: "above", Critical: 1, WindowMinutes: 60}), Actions: chanAction(1)}, "trigger.metric.transaction"},
		{"metric assign", Rule{Trigger: metric(MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 15, WindowMinutes: 60}), Actions: []Action{{Type: ActionAssign, UserID: 3}}}, "actions[0].type"},
		{"metric owner filter", Rule{Trigger: metric(MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 15, WindowMinutes: 60}), Filters: Filters{Owner: &OwnerFilter{}}, Actions: chanAction(1)}, "filters.owner"},
		{"issue assign", Rule{Trigger: issue, Actions: []Action{{Type: ActionAssign, UserID: 3}}}, ""},
		{"no actions", Rule{Trigger: issue}, "actions"},
		{"two sub-triggers", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}, Monitor: &MonitorTrigger{}}, Actions: chanAction(1)}, "trigger"},
		{"unknown issue on", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"closed"}}}, Actions: chanAction(1)}, "trigger.issue.on"},
		{"bad kind filter", Rule{Trigger: issue, Filters: Filters{Kinds: []string{"nope"}}, Actions: chanAction(1)}, "filters.kinds"},
		{"repeated channel", Rule{Trigger: issue, Actions: chanAction(1, 1)}, "actions[0].channel_ids"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateRule(tt.rule)
			if tt.wantPath == "" {
				if err != nil {
					t.Fatalf("ValidateRule() = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantPath+" ") {
				t.Fatalf("ValidateRule() = %v, want ErrInvalid naming %q", err, tt.wantPath)
			}
		})
	}
}

func TestValidateRuleFillsDefaults(t *testing.T) {
	event, err := ValidateRule(Rule{Trigger: Trigger{Type: TriggerEvent, Event: &EventTrigger{WindowMinutes: 30, Above: 8}}, Actions: chanAction(1)})
	if err != nil || event.FrequencyMinutes != 60 || event.Trigger.Event.Measure != "events" || event.NotifyOnResolve != nil {
		t.Fatalf("event rule = %+v, %v", event, err)
	}
	spike, err := ValidateRule(Rule{Trigger: Trigger{Type: TriggerEvent, Event: &EventTrigger{Spike: &Spike{}}}, Actions: chanAction(1)})
	if err != nil || *spike.Trigger.Event.Spike != (Spike{Factor: 3, MinEvents: 10, BaselineDays: 7}) {
		t.Fatalf("spike rule = %+v, %v", spike, err)
	}
	metric, err := ValidateRule(Rule{Trigger: Trigger{Type: TriggerMetric, Metric: &MetricTrigger{Name: MetricCrashFreeSessions, Op: "below", Critical: 95, WindowMinutes: 1440}}, Actions: chanAction(1)})
	if err != nil || metric.NotifyOnResolve == nil || !*metric.NotifyOnResolve || metric.FrequencyMinutes != 0 ||
		metric.Trigger.Metric.MinSample != 100 || metric.Trigger.Metric.Comparison != "static" {
		t.Fatalf("metric rule = %+v, %v", metric, err)
	}
	count, err := ValidateRule(Rule{Trigger: Trigger{Type: TriggerMetric, Metric: &MetricTrigger{Name: MetricErrorCount, Op: "above", Critical: 5, WindowMinutes: 15}}, Actions: chanAction(1)})
	if err != nil || count.Trigger.Metric.MinSample != 0 {
		t.Fatalf("error_count rule = %+v, %v", count, err)
	}
	monitor, err := ValidateRule(Rule{Trigger: Trigger{Type: TriggerMonitor, Monitor: &MonitorTrigger{}}, Actions: chanAction(1)})
	if err != nil || len(monitor.Trigger.Monitor.On) != 3 || monitor.NotifyOnResolve == nil || !*monitor.NotifyOnResolve {
		t.Fatalf("monitor rule = %+v, %v", monitor, err)
	}
}

func TestServiceRuleCRUDAndLimits(t *testing.T) {
	service, pool, principal, webSiteID, _ := newServiceTest(t)
	ctx := context.Background()
	var appSiteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,kind) values ($1,'appabcdefghi','App','UTC',30,'app') returning id`, principal.OrgID).Scan(&appSiteID); err != nil {
		t.Fatal(err)
	}
	mail, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelEmail, Name: "Owners", Recipients: []string{"owner@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	rule := Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: chanAction(mail.ID)}
	input := AlertInput{Kind: AlertRule, Name: " New crashes ", Rule: &rule}

	if _, err := service.CreateAlert(ctx, principal, webSiteID, input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rule on web site error = %v, want ErrInvalid", err)
	}
	if _, err := service.CreateAlert(ctx, principal, appSiteID, AlertInput{Kind: AlertAppNewIssue, Threshold: 1, ChannelIDs: []int64{mail.ID}}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "use kind app_rule") {
		t.Fatalf("v1 kind error = %v, want ErrInvalid asking for app_rule", err)
	}
	if _, err := service.CreateAlert(ctx, principal, appSiteID, AlertInput{Kind: AlertRule, Name: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rule without document error = %v", err)
	}
	hook := Rule{Trigger: rule.Trigger, Actions: []Action{{Type: ActionWebhook, ChannelIDs: []int64{mail.ID}}}}
	if _, err := service.CreateAlert(ctx, principal, appSiteID, AlertInput{Kind: AlertRule, Name: "hook", Rule: &hook}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "webhook") {
		t.Fatalf("webhook action on an email channel error = %v", err)
	}

	created, err := service.CreateAlert(ctx, principal, appSiteID, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != AlertRule || created.Name != "New crashes" || created.Rule == nil || len(created.ChannelIDs) != 1 || created.ChannelIDs[0] != mail.ID {
		t.Fatalf("created = %+v", created)
	}
	listed, err := service.ListAlerts(ctx, principal, appSiteID)
	if err != nil || len(listed) != 1 || listed[0].Rule == nil || listed[0].Rule.Trigger.Issue.On[0] != "new" {
		t.Fatalf("ListAlerts() = %+v, %v", listed, err)
	}
	rule.Trigger = Trigger{Type: TriggerEvent, Event: &EventTrigger{WindowMinutes: 30, Above: 8}}
	updated, err := service.UpdateAlert(ctx, principal, appSiteID, created.ID, AlertInput{Kind: AlertRule, Name: "Frequent", Rule: &rule})
	if err != nil || updated.Rule.FrequencyMinutes != 60 || updated.Name != "Frequent" {
		t.Fatalf("UpdateAlert() = %+v, %v", updated, err)
	}
	if _, err := service.UpdateAlert(ctx, principal, appSiteID, created.ID, AlertInput{Kind: AlertAppNewIssue, Threshold: 1, ChannelIDs: []int64{mail.ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("converting a rule to a v1 kind error = %v, want ErrInvalid", err)
	}
	if err := service.DeleteChannel(ctx, principal, mail.ID); !errors.Is(err, ErrChannelInUse) {
		t.Fatalf("deleting a channel used by a rule error = %v, want ErrChannelInUse", err)
	}

	// 99 more fill the site; the 101st is refused.
	for i := 1; i < MaxRulesPerSite; i++ {
		if _, err := service.CreateAlert(ctx, principal, appSiteID, input); err != nil {
			t.Fatalf("rule %d: %v", i+1, err)
		}
	}
	if _, err := service.CreateAlert(ctx, principal, appSiteID, input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("101st rule error = %v, want ErrInvalid", err)
	}
}
