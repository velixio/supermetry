package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/sites"
)

const testRoutingKey = "R0UT1NGKEYabcdefghijklmnopqrstuv"

func boolPtr(v bool) *bool { return &v }

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func staticRegistryFor(f *ruleFixture) staticRegistry {
	return staticRegistry{site: sites.Site{ID: f.siteID, Kind: sites.KindApp, TZ: time.UTC, Slug: "voicestudio"}}
}

func TestBuildPagerDutyEventCriticalOpen(t *testing.T) {
	n := Notification{
		AlertID: 12, SiteID: 3, Kind: AlertRule, FiredAt: time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC),
		App:  &AppNotification{AppName: "VoiceStudio", IssueURL: "/app/acme/voicestudio/issues/7"},
		Rule: &RuleNotification{ID: 12, Name: "Crash-free users", TriggerType: TriggerMetric, Severity: severityCritical, Status: ruleStatusFiring, IncidentID: 41},
	}
	got, err := BuildPagerDutyEvent(testRoutingKey, n, "Crash-free users (firing)\nvalue 92.50", "https://stats.example.test/")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"routing_key":"` + testRoutingKey + `","event_action":"trigger","dedup_key":"supermetry-12-41",` +
		`"payload":{"summary":"Crash-free users (firing) value 92.50","source":"VoiceStudio","severity":"critical","timestamp":"2026-11-18T09:30:00Z"},` +
		`"links":[{"href":"https://stats.example.test/app/acme/voicestudio/issues/7"}]}`
	if string(got) != want {
		t.Fatalf("trigger body\n got %s\nwant %s", got, want)
	}

	n.Rule.Status = ruleStatusResolve
	resolve, err := BuildPagerDutyEvent(testRoutingKey, n, "ignored", "")
	if err != nil {
		t.Fatal(err)
	}
	wantResolve := `{"routing_key":"` + testRoutingKey + `","event_action":"resolve","dedup_key":"supermetry-12-41"}`
	if string(resolve) != wantResolve {
		t.Fatalf("resolve body\n got %s\nwant %s", resolve, wantResolve)
	}
}

func TestBuildPagerDutyEventKeyedCandidateAndSummaryLimit(t *testing.T) {
	n := Notification{AlertID: 5, FiredAt: time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC),
		Rule: &RuleNotification{ID: 5, Status: ruleStatusFiring, Key: "issue:7"}}
	body, err := BuildPagerDutyEvent(testRoutingKey, n, strings.Repeat("é", 3000), "")
	if err != nil {
		t.Fatal(err)
	}
	var event struct {
		DedupKey string `json:"dedup_key"`
		Payload  struct {
			Summary  string `json:"summary"`
			Severity string `json:"severity"`
			Source   string `json:"source"`
		} `json:"payload"`
		Links []any `json:"links"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	if event.DedupKey != "supermetry-5-issue:7" || event.Payload.Severity != "error" || event.Payload.Source != "Supermetry" || len(event.Links) != 0 {
		t.Fatalf("event = %+v", event)
	}
	if n := len([]rune(event.Payload.Summary)); n != 1024 {
		t.Fatalf("summary runes = %d, want 1024", n)
	}
}

type pdServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
	status int
}

func newPDServer(status int) *pdServer {
	s := &pdServer{status: status}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(raw))
		code := s.status
		s.mu.Unlock()
		w.WriteHeader(code)
	}))
	return s
}

func (s *pdServer) setStatus(code int) { s.mu.Lock(); s.status = code; s.mu.Unlock() }

func (s *pdServer) events(t *testing.T) []map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, len(s.bodies))
	for i, raw := range s.bodies {
		if err := json.Unmarshal([]byte(raw), &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestPagerDutyStatusClassification(t *testing.T) {
	cases := []struct {
		status    int
		wantErr   bool
		permanent bool
	}{{202, false, false}, {200, false, false}, {400, true, true}, {403, true, true}, {429, true, false}, {503, true, false}}
	for _, tc := range cases {
		srv := newPDServer(tc.status)
		client := NewWebhookClient(WebhookOptions{AllowPrivate: true})
		err := client.SendPagerDuty(context.Background(), srv.URL, []byte(`{"routing_key":"`+testRoutingKey+`"}`))
		srv.Close()
		if (err != nil) != tc.wantErr || (err != nil && isPermanentDelivery(err) != tc.permanent) {
			t.Fatalf("status %d: err=%v permanent=%v, want err=%v permanent=%v", tc.status, err, err != nil && isPermanentDelivery(err), tc.wantErr, tc.permanent)
		}
		if err != nil && strings.Contains(err.Error(), testRoutingKey) {
			t.Fatalf("error leaks the routing key: %v", err)
		}
		if len(srv.bodies) != 1 {
			t.Fatalf("status %d: %d requests, want exactly 1 (the delivery state owns retries)", tc.status, len(srv.bodies))
		}
	}
}

// Testing a PagerDuty channel pages and resolves in one go, so no incident is
// left open on call.
func TestPagerDutyChannelTestResolvesItself(t *testing.T) {
	deliverer := &fakeDeliverer{}
	service, _, principal, _, _ := newServiceTestWithDeliverer(t, deliverer)
	ctx := context.Background()
	pd, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelPagerDuty, Name: "On call", RoutingKey: testRoutingKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.TestChannel(ctx, principal, pd.ID); err != nil {
		t.Fatal(err)
	}
	if len(deliverer.notifications) != 2 {
		t.Fatalf("deliveries = %d, want a trigger then a resolve", len(deliverer.notifications))
	}
	first, second := deliverer.notifications[0], deliverer.notifications[1]
	if first.Rule.Status != ruleStatusFiring || second.Rule.Status != ruleStatusResolve || first.Rule.Key != second.Rule.Key || first.Rule.Key == "" {
		t.Fatalf("test notifications = %+v / %+v", first.Rule, second.Rule)
	}
}

func TestPagerDutyBlocksPrivateEndpointByDefault(t *testing.T) {
	srv := newPDServer(202)
	defer srv.Close()
	err := NewWebhookClient(WebhookOptions{}).SendPagerDuty(context.Background(), srv.URL, []byte(`{}`))
	if err == nil || len(srv.bodies) != 0 {
		t.Fatalf("private endpoint reached: err=%v requests=%d", err, len(srv.bodies))
	}
}

func TestServicePagerDutyChannelSealsAndNeverReturnsKey(t *testing.T) {
	service, pool, principal, _, _ := newServiceTest(t)
	ctx := context.Background()
	created, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelPagerDuty, Name: "On call", RoutingKey: testRoutingKey})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Config.Configured {
		t.Fatalf("created = %+v", created)
	}
	var raw string
	if err := pool.QueryRow(ctx, `select config::text from notification_channels where id=$1`, created.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, testRoutingKey) {
		t.Fatalf("stored config holds the routing key in plaintext: %s", raw)
	}
	listed, err := service.ListChannels(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := json.Marshal(listed); strings.Contains(string(encoded), testRoutingKey) || strings.Contains(string(encoded), "routing_key") {
		t.Fatalf("API output carries the routing key: %s", encoded)
	}
	channels, err := service.DeliveryChannels(ctx, []int64{created.ID})
	if err != nil || len(channels) != 1 || channels[0].RoutingKey != testRoutingKey {
		t.Fatalf("DeliveryChannels() = %+v, %v", channels, err)
	}
	// Renaming without a key keeps the stored one; a malformed key is refused.
	if _, err := service.UpdateChannel(ctx, principal, created.ID, ChannelInput{Kind: ChannelPagerDuty, Name: "On call 2"}); err != nil {
		t.Fatal(err)
	}
	if channels, _ = service.DeliveryChannels(ctx, []int64{created.ID}); channels[0].RoutingKey != testRoutingKey || channels[0].Name != "On call 2" {
		t.Fatalf("rename lost the key: %+v", channels[0])
	}
	if _, err := service.UpdateChannel(ctx, principal, created.ID, ChannelInput{Kind: ChannelPagerDuty, Name: "x", RoutingKey: "short"}); err == nil {
		t.Fatal("malformed routing key accepted")
	}
	if _, err := service.CreateChannel(ctx, principal, ChannelInput{Kind: ChannelPagerDuty, Name: "No key"}); err == nil {
		t.Fatal("PagerDuty channel without a key accepted")
	}
}

// A metric incident pages with a trigger and, once resolved, a resolve with the
// same dedup key, sent only to the PagerDuty channels that took the trigger.
func TestPagerDutyTriggerAndResolveRouting(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	srv := newPDServer(202)
	defer srv.Close()
	dispatcher := NewDispatcher(nil, NewWebhookClient(WebhookOptions{AllowPrivate: true}), nil)
	dispatcher.SetPagerDuty(srv.URL, "https://stats.example.test")

	pdA, err := f.service.CreateChannel(ctx, f.principal, ChannelInput{Kind: ChannelPagerDuty, Name: "Primary", RoutingKey: testRoutingKey})
	if err != nil {
		t.Fatal(err)
	}
	otherKey := "0THERKEYabcdefghijklmnopqrstuvwx"
	pdB, err := f.service.CreateChannel(ctx, f.principal, ChannelInput{Kind: ChannelPagerDuty, Name: "Secondary", RoutingKey: otherKey, Enabled: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	alert := f.newRule(t, "Crash-free users", Rule{
		Trigger: Trigger{Type: TriggerMetric, Metric: &MetricTrigger{Name: "crash_free_users", Op: "below", Critical: 95, WindowMinutes: 1440}},
		Actions: chanAction(pdA.ID, pdB.ID),
	})
	registry := staticRegistryFor(f)
	ev := NewRuleEvaluator(f.service, f.service, f.source, NewAppStore(f.pool), dispatcher, registry, func() time.Time { return f.now })

	open := Notification{AlertID: alert.ID, SiteID: alert.SiteID, Kind: AlertRule, Value: 92.5, Threshold: 95, FiredAt: f.now,
		App:  &AppNotification{AppName: "VoiceStudio"},
		Rule: &RuleNotification{ID: alert.ID, Name: alert.Name, TriggerType: TriggerMetric, Severity: severityCritical, Status: ruleStatusFiring, IncidentID: 77}}
	if err := ev.base.fireWithKey(ctx, alert, open, nil, "incident:77:open"); err != nil {
		t.Fatal(err)
	}
	// The secondary channel is enabled only after the trigger went out.
	if _, err := f.service.UpdateChannel(ctx, f.principal, pdB.ID, ChannelInput{Kind: ChannelPagerDuty, Name: "Secondary", Enabled: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	resolved := open
	resolved.FiredAt = f.now.Add(time.Hour)
	resolved.Rule = &RuleNotification{ID: alert.ID, Name: alert.Name, TriggerType: TriggerMetric, Severity: severityCritical, Status: ruleStatusResolve, IncidentID: 77}
	if err := ev.base.fireWithKey(ctx, alert, resolved, nil, "incident:77:resolved"); err != nil {
		t.Fatal(err)
	}

	events := srv.events(t)
	if len(events) != 2 {
		t.Fatalf("events = %d (%v), want a trigger and one resolve", len(events), events)
	}
	if events[0]["event_action"] != "trigger" || events[1]["event_action"] != "resolve" {
		t.Fatalf("actions = %v, %v", events[0]["event_action"], events[1]["event_action"])
	}
	if events[0]["dedup_key"] != events[1]["dedup_key"] || events[0]["dedup_key"] != "supermetry-"+itoa(alert.ID)+"-77" {
		t.Fatalf("dedup keys = %v, %v", events[0]["dedup_key"], events[1]["dedup_key"])
	}
	if events[1]["routing_key"] != testRoutingKey {
		t.Fatalf("resolve went to the wrong integration: %v", events[1]["routing_key"])
	}
	for _, e := range events {
		if e["routing_key"] == otherKey {
			t.Fatal("the channel that never took the trigger received an event")
		}
	}
}

// A rejected event is a permanent failure at once, recorded without the key.
func TestPagerDutyPermanentFailureIsSettledAndScrubbed(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	srv := newPDServer(400)
	defer srv.Close()
	dispatcher := NewDispatcher(nil, NewWebhookClient(WebhookOptions{AllowPrivate: true}), nil)
	dispatcher.SetPagerDuty(srv.URL, "")
	pd, err := f.service.CreateChannel(ctx, f.principal, ChannelInput{Kind: ChannelPagerDuty, Name: "Primary", RoutingKey: testRoutingKey})
	if err != nil {
		t.Fatal(err)
	}
	alert := f.newRule(t, "New issue", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: chanAction(pd.ID)})
	var logged []string
	ev := NewRuleEvaluator(f.service, f.service, f.source, NewAppStore(f.pool), dispatcher, staticRegistryFor(f), func() time.Time { return f.now },
		func(_, _ int64, _ ChannelKind, err error) { logged = append(logged, err.Error()) })
	n := Notification{AlertID: alert.ID, SiteID: alert.SiteID, Kind: AlertRule, FiredAt: f.now, Rule: &RuleNotification{ID: alert.ID, Status: ruleStatusFiring}}
	_ = ev.base.fireWithKey(ctx, alert, n, nil, "issue:7")

	var status, text string
	var attempts int
	if err := f.pool.QueryRow(ctx, `select status, error_text, attempts from alert_delivery_log where alert_id=$1`, alert.ID).Scan(&status, &text, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || attempts != 1 || len(srv.bodies) != 1 {
		t.Fatalf("status=%s attempts=%d requests=%d, want failed after one attempt", status, attempts, len(srv.bodies))
	}
	for _, s := range append(logged, text) {
		if strings.Contains(s, testRoutingKey) {
			t.Fatalf("routing key leaked: %q", s)
		}
	}
	if len(logged) != 1 {
		t.Fatalf("logged failures = %v", logged)
	}
	var state any
	if err := f.pool.QueryRow(ctx, `select delivery_state from alerts where id=$1`, alert.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != nil {
		t.Fatalf("delivery state left pending after a permanent failure: %v", state)
	}
}

// A 503 stays pending and is retried by the next run.
func TestPagerDutyTransientFailureRetries(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	srv := newPDServer(503)
	defer srv.Close()
	dispatcher := NewDispatcher(nil, NewWebhookClient(WebhookOptions{AllowPrivate: true}), nil)
	dispatcher.SetPagerDuty(srv.URL, "")
	pd, err := f.service.CreateChannel(ctx, f.principal, ChannelInput{Kind: ChannelPagerDuty, Name: "Primary", RoutingKey: testRoutingKey})
	if err != nil {
		t.Fatal(err)
	}
	alert := f.newRule(t, "New issue", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: chanAction(pd.ID)})
	ev := NewRuleEvaluator(f.service, f.service, f.source, NewAppStore(f.pool), dispatcher, staticRegistryFor(f), func() time.Time { return f.now })
	n := Notification{AlertID: alert.ID, SiteID: alert.SiteID, Kind: AlertRule, FiredAt: f.now, Rule: &RuleNotification{ID: alert.ID, Status: ruleStatusFiring}}
	if err := ev.base.fireWithKey(ctx, alert, n, nil, "issue:7"); err == nil {
		t.Fatal("503 reported as success")
	}
	var status string
	if err := f.pool.QueryRow(ctx, `select status from alert_delivery_log where alert_id=$1`, alert.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("status = %q (%v), want pending", status, err)
	}
	srv.setStatus(202)
	reloaded, err := f.service.EnabledRules(ctx)
	if err != nil || len(reloaded) == 0 || reloaded[0].DeliveryState == nil {
		t.Fatalf("pending delivery state missing: %v", err)
	}
	if err := ev.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `select status from alert_delivery_log where alert_id=$1`, alert.ID).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("status = %q (%v), want succeeded after the retry", status, err)
	}
}
