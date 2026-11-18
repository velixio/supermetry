package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/sites"
)

var ruleSeq int

// storeEvaluator is a RuleEvaluator that reads the real app tables.
func (f *ruleFixture) storeEvaluator(t *testing.T) *RuleEvaluator {
	t.Helper()
	if err := db.EnsureAppEventPartitions(context.Background(), f.pool, f.now.AddDate(0, -2, 0), f.now.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	registry := staticRegistry{site: sites.Site{ID: f.siteID, Kind: sites.KindApp, TZ: time.UTC, Slug: "voicestudio"}}
	store := NewAppStore(f.pool)
	return NewRuleEvaluator(f.service, f.service, store, store, f.deliverer, registry, func() time.Time { return f.now })
}

// seedIssue inserts an app issue; assignee 0 leaves it unassigned.
func (f *ruleFixture) seedIssue(t *testing.T, kind, level, release string, assignee int64) int64 {
	t.Helper()
	ruleSeq++
	var owner any
	if assignee != 0 {
		owner = assignee
	}
	var id int64
	err := f.pool.QueryRow(context.Background(), `insert into app_issues(site_id,fingerprint,kind,title,first_seen,last_seen,first_release,last_release,level,assignee_user_id,event_count)
values ($1,$2,$3,$4,$5,$5,$6,$6,$7,$8,1) returning id`, f.siteID, fmt.Sprintf("fp%d", ruleSeq), kind, fmt.Sprintf("Issue %d", ruleSeq), f.now.Add(-24*time.Hour), release, level, owner).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *ruleFixture) seedTags(t *testing.T, issue int64, tags map[string]string) {
	t.Helper()
	for k, v := range tags {
		if _, err := f.pool.Exec(context.Background(), `insert into app_issue_tags(site_id,issue_id,key,value,events,last_seen) values ($1,$2,$3,$4,1,$5)`, f.siteID, issue, k, v, f.now); err != nil {
			t.Fatal(err)
		}
	}
}

type seedEvent struct {
	ts          time.Time
	user        string // empty leaves user_hash null
	environment string
	tags        string // JSON document, empty for {}
}

func (f *ruleFixture) seedEvents(t *testing.T, issue int64, events ...seedEvent) {
	t.Helper()
	if err := db.EnsureAppEventPartitions(context.Background(), f.pool, f.now.AddDate(0, -2, 0), f.now.AddDate(0, 2, 0)); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		ruleSeq++
		var hash any
		if e.user != "" {
			hash = []byte(e.user)
		}
		tags := e.tags
		if tags == "" {
			tags = "{}"
		}
		if _, err := f.pool.Exec(context.Background(), `insert into app_events(site_id,ts,received_at,event_id,issue_id,kind,environment,user_hash,tags)
values ($1,$2,$2,$3,$4,'crash',$5,$6,$7::jsonb)`, f.siteID, e.ts, fmt.Sprintf("e%d", ruleSeq), issue, e.environment, hash, tags); err != nil {
			t.Fatal(err)
		}
	}
}

func repeat(n int, e seedEvent) []seedEvent {
	out := make([]seedEvent, n)
	for i := range out {
		out[i] = e
	}
	return out
}

func (f *ruleFixture) firedKeyNames(t *testing.T, alertID int64) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `select dedup_key from app_alert_fired where alert_id=$1 order by dedup_key`, alertID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		out = append(out, k)
	}
	return out
}

func eventRule(f *ruleFixture, t *testing.T, trigger EventTrigger, filters Filters) Alert {
	return f.newRule(t, "Event rule", Rule{Trigger: Trigger{Type: TriggerEvent, Event: &trigger}, Filters: filters, Actions: chanAction(f.channelID)})
}

func TestEventFrequencyWindow(t *testing.T) {
	setup := func(t *testing.T, window int) (*ruleFixture, Alert, int64) {
		f := newRuleFixture(t)
		issue := f.seedIssue(t, "crash", "error", "1.0.0", 0)
		at := func(m time.Duration) time.Time { return f.now.Add(-m * time.Minute) }
		f.seedEvents(t, issue, repeat(3, seedEvent{ts: at(50)})...)
		f.seedEvents(t, issue, repeat(4, seedEvent{ts: at(20)})...)
		f.seedEvents(t, issue, repeat(5, seedEvent{ts: at(5)})...)
		alert := eventRule(f, t, EventTrigger{Measure: "events", WindowMinutes: window, Above: 8}, Filters{})
		return f, alert, issue
	}
	t.Run("fires with the window count and repeats no sooner than the frequency", func(t *testing.T) {
		f, alert, issue := setup(t, 30)
		ctx := context.Background()
		start := f.now.Add(-30 * time.Minute).Unix()
		if err := f.storeEvaluator(t).Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(f.deliverer.notifications) != 1 {
			t.Fatalf("deliveries = %d, want 1", len(f.deliverer.notifications))
		}
		n := f.deliverer.notifications[0]
		if n.Value != 9 || n.Threshold != 8 || n.App.IssueID != issue {
			t.Fatalf("notification = value %v threshold %v issue %d, want 9, 8, %d", n.Value, n.Threshold, n.App.IssueID, issue)
		}
		want := []string{fmt.Sprintf("freq:%d:%d", issue, start)}
		if got := f.firedKeyNames(t, alert.ID); !reflect.DeepEqual(got, want) {
			t.Fatalf("keys = %v, want %v", got, want)
		}
		f.now = f.now.Add(2 * time.Minute)
		if err := f.storeEvaluator(t).Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(f.deliverer.notifications) != 1 {
			t.Fatalf("deliveries after 2 minutes = %d, want the frequency to hold it at 1", len(f.deliverer.notifications))
		}
		f.now = f.now.Add(61 * time.Minute)
		f.seedEvents(t, issue, repeat(9, seedEvent{ts: f.now.Add(-time.Minute)})...)
		if err := f.storeEvaluator(t).Run(ctx); err != nil {
			t.Fatal(err)
		}
		if len(f.deliverer.notifications) != 2 {
			t.Fatalf("deliveries after the frequency = %d, want 2", len(f.deliverer.notifications))
		}
	})
	t.Run("a shorter window counts fewer events", func(t *testing.T) {
		f, _, _ := setup(t, 15)
		if err := f.storeEvaluator(t).Run(context.Background()); err != nil || len(f.deliverer.notifications) != 0 {
			t.Fatalf("Run() = %v with %d deliveries, want none (5 events in 15 minutes)", err, len(f.deliverer.notifications))
		}
	})
}

func TestEventUsersMeasure(t *testing.T) {
	run := func(t *testing.T, above int64) int {
		f := newRuleFixture(t)
		issue := f.seedIssue(t, "crash", "error", "1.0.0", 0)
		ts := f.now.Add(-5 * time.Minute)
		f.seedEvents(t, issue, seedEvent{ts: ts, user: "a"}, seedEvent{ts: ts, user: "a"}, seedEvent{ts: ts, user: "b"}, seedEvent{ts: ts, user: "c"}, seedEvent{ts: ts}, seedEvent{ts: ts})
		eventRule(f, t, EventTrigger{Measure: "users", WindowMinutes: 30, Above: above}, Filters{})
		if err := f.storeEvaluator(t).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(f.deliverer.notifications) == 1 && f.deliverer.notifications[0].Value != 3 {
			t.Fatalf("value = %v, want 3 distinct users", f.deliverer.notifications[0].Value)
		}
		return len(f.deliverer.notifications)
	}
	if got := run(t, 2); got != 1 {
		t.Errorf("above 2: deliveries = %d, want 1 (3 users)", got)
	}
	if got := run(t, 3); got != 0 {
		t.Errorf("above 3: deliveries = %d, want 0 (3 users is not above 3)", got)
	}
}

func TestEventWindowFiltersApplyToTheEvents(t *testing.T) {
	f := newRuleFixture(t)
	issue := f.seedIssue(t, "crash", "error", "1.0.0", 0)
	ts := f.now.Add(-5 * time.Minute)
	f.seedEvents(t, issue,
		seedEvent{ts: ts, environment: "prod", tags: `{"region":"eu"}`}, seedEvent{ts: ts, environment: "prod", tags: `{"region":"eu"}`},
		seedEvent{ts: ts, environment: "prod", tags: `{"region":"us"}`}, seedEvent{ts: ts, environment: "prod"},
		seedEvent{ts: ts, environment: "staging", tags: `{"region":"eu"}`})
	store := NewAppStore(f.pool)
	if err := db.EnsureAppEventPartitions(context.Background(), f.pool, f.now.AddDate(0, -2, 0), f.now.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	count := func(fl Filters) int64 {
		rows, err := store.IssueWindowCounts(context.Background(), f.siteID, f.now.Add(-time.Hour), f.now, fl, "events", 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			return 0
		}
		return rows[0].Count
	}
	for name, tc := range map[string]struct {
		filters Filters
		want    int64
	}{
		"no filter":                 {Filters{}, 5},
		"prod":                      {Filters{Environments: []string{"prod"}}, 4},
		"prod or staging":           {Filters{Environments: []string{"prod", "staging"}}, 5},
		"prod and region eu":        {Filters{Environments: []string{"prod"}, Tags: []TagFilter{{Key: "region", Value: "eu"}}}, 2},
		"prod and region not eu":    {Filters{Environments: []string{"prod"}, Tags: []TagFilter{{Key: "region", Value: "eu", Negate: true}}}, 2},
		"owner unassigned":          {Filters{Owner: &OwnerFilter{}}, 5},
		"owner somebody":            {Filters{Owner: &OwnerFilter{UserID: f.principal.UserID}}, 0},
		"release that has no event": {Filters{Releases: []string{"9.9.9"}}, 0},
	} {
		if got := count(tc.filters); got != tc.want {
			t.Errorf("%s: count = %d, want %d", name, got, tc.want)
		}
	}
}

// The v1 spike test values: 30 events in the last closed hour against a
// 7-day baseline sum of 70 (average 10) fire at factor 3; 29 do not.
func TestEventSpikeMatchesV1(t *testing.T) {
	for _, tc := range []struct {
		events int64
		want   int
	}{{30, 1}, {29, 0}} {
		f := newRuleFixture(t)
		alert := eventRule(f, t, EventTrigger{Spike: &Spike{}}, Filters{})
		hour := f.now.UTC().Truncate(time.Hour).Add(-time.Hour)
		f.source.spikes = []IssueHour{{AppIssue: AppIssue{ID: 11, Title: "spiky", Kind: "crash"}, Events: tc.events, BaselineSum: 70}}
		if err := f.evaluator().Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(f.deliverer.notifications) != tc.want {
			t.Fatalf("%d events: deliveries = %d, want %d", tc.events, len(f.deliverer.notifications), tc.want)
		}
		if tc.want == 1 {
			if got, want := f.firedKeyNames(t, alert.ID), []string{fmt.Sprintf("spike:11:%d", hour.Unix())}; !reflect.DeepEqual(got, want) {
				t.Fatalf("keys = %v, want %v", got, want)
			}
			if n := f.deliverer.notifications[0]; n.Value != 30 || n.Threshold != 30 || n.App.Baseline != 10 {
				t.Fatalf("notification = %+v", n)
			}
		}
	}
}

func TestIssueTriggerEscalated(t *testing.T) {
	f := newRuleFixture(t)
	alert := f.newRule(t, "Escalated", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"escalated"}}}, Actions: chanAction(f.channelID)})
	f.source.escalated = []AppIssue{{ID: 4, Title: "Back again", Kind: "error", ActivityID: 88, EscalatedAt: f.now.Add(-time.Minute)}}
	for i := 0; i < 2; i++ {
		if err := f.evaluator().Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(f.deliverer.notifications))
	}
	if got, want := f.firedKeyNames(t, alert.ID), []string{"escalate:4:88"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func TestEscalatedIssuesReadsTheActivityLog(t *testing.T) {
	f := newRuleFixture(t)
	issue := f.seedIssue(t, "error", "error", "1.0.0", 0)
	var activity int64
	if err := f.pool.QueryRow(context.Background(), `insert into app_issue_activity(site_id,issue_id,at,kind) values ($1,$2,$3,'escalated') returning id`, f.siteID, issue, f.now.Add(-time.Minute)).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `insert into app_issue_activity(site_id,issue_id,at,kind) values ($1,$2,$3,'status')`, f.siteID, issue, f.now); err != nil {
		t.Fatal(err)
	}
	rows, err := NewAppStore(f.pool).EscalatedIssues(context.Background(), f.siteID, f.now.Add(-time.Hour), 10)
	if err != nil || len(rows) != 1 || rows[0].ID != issue || rows[0].ActivityID != activity {
		t.Fatalf("EscalatedIssues() = %+v, %v, want the one escalated row %d", rows, err, activity)
	}
}

func TestFilters(t *testing.T) {
	f := newRuleFixture(t)
	owner := f.principal.UserID
	// 1 prod/ios/fatal, 2 prod/android/error (owned), 3 staging/ios/error, 4 prod/ios/error + region=eu.
	i1 := f.seedIssue(t, "crash", "fatal", "1.9.0", 0)
	i2 := f.seedIssue(t, "error", "error", "1.9.0", owner)
	i3 := f.seedIssue(t, "error", "error", "1.10.0", 0)
	i4 := f.seedIssue(t, "error", "error", "1.10.0", 0)
	f.seedTags(t, i1, map[string]string{"environment": "prod", "platform": "ios"})
	f.seedTags(t, i2, map[string]string{"environment": "prod", "platform": "android"})
	f.seedTags(t, i3, map[string]string{"environment": "staging", "platform": "ios"})
	f.seedTags(t, i4, map[string]string{"environment": "prod", "platform": "ios", "region": "eu"})
	name := map[int64]int{i1: 1, i2: 2, i3: 3, i4: 4}
	store := NewAppStore(f.pool)
	match := func(fl Filters) []int {
		got, err := store.MatchIssue(context.Background(), f.siteID, []int64{i1, i2, i3, i4}, fl)
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for id := range got {
			out = append(out, name[id])
		}
		sort.Ints(out)
		return out
	}
	prod, ios := []string{"prod"}, []string{"ios"}
	for _, tc := range []struct {
		name    string
		filters Filters
		want    []int
	}{
		{"no filter", Filters{}, []int{1, 2, 3, 4}},
		{"prod and ios", Filters{Environments: prod, Platforms: ios}, []int{1, 4}},
		{"prod and ios and fatal", Filters{Environments: prod, Platforms: ios, Levels: []string{"fatal"}}, []int{1}},
		{"tag region eu", Filters{Tags: []TagFilter{{Key: "region", Value: "eu"}}}, []int{4}},
		{"prod and tag region not eu", Filters{Environments: prod, Tags: []TagFilter{{Key: "region", Value: "eu", Negate: true}}}, []int{1, 2}},
		{"unassigned", Filters{Owner: &OwnerFilter{}}, []int{1, 3, 4}},
		{"owned by the owner", Filters{Owner: &OwnerFilter{UserID: owner}}, []int{2}},
		{"two environments are OR", Filters{Environments: []string{"prod", "staging"}, Platforms: ios}, []int{1, 3, 4}},
		{"release 1.10.0", Filters{Releases: []string{"1.10.0"}}, []int{3, 4}},
		{"kind crash", Filters{Kinds: []string{"crash"}}, []int{1}},
	} {
		if got := match(tc.filters); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: issues %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLatestReleaseFilterIsResolvedBeforeTheQuery(t *testing.T) {
	f := newRuleFixture(t)
	f.newRule(t, "Latest only", Rule{
		Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}},
		Filters: Filters{Releases: []string{"latest"}}, Actions: chanAction(f.channelID),
	})
	f.source.newIssues = []AppIssue{{ID: 7, Title: "t", Kind: "crash", FirstSeen: f.now}}
	f.source.release = &AppRelease{Release: "1.10.0", Installs: 5}
	if err := f.evaluator().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.source.matchFilters.Releases; !reflect.DeepEqual(got, []string{"1.10.0"}) {
		t.Fatalf("releases filter = %v, want the resolved [1.10.0]", got)
	}
	// With no release at all, nothing can match and nothing fires.
	f2 := newRuleFixture(t)
	f2.newRule(t, "Latest only", Rule{
		Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}},
		Filters: Filters{Releases: []string{"latest"}}, Actions: chanAction(f2.channelID),
	})
	f2.source.newIssues = []AppIssue{{ID: 7, Title: "t", Kind: "crash", FirstSeen: f2.now}}
	if err := f2.evaluator().Run(context.Background()); err != nil || f2.deliverer.calls != 0 {
		t.Fatalf("Run() = %v with %d deliveries, want none without a release", err, f2.deliverer.calls)
	}
}

func TestActionWebhookPayloadPrivacy(t *testing.T) {
	n := Notification{
		Value: 9, Threshold: 8,
		Rule: &RuleNotification{ID: 3, Name: "Frequent", TriggerType: TriggerEvent, Status: ruleStatusFiring},
		App: &AppNotification{
			AppName: "VoiceStudio", IssueID: 9, IssueTitle: "Crash in render", IssueURL: "/app/acme/voice/issues/9", IssueLevel: "fatal", IssueState: "open",
			Culprit: "render()", FirstSeen: "2026-11-18T10:00:00Z", LastSeen: "2026-11-18T11:00:00Z", Events: 12,
		},
	}
	body, err := webhookBody(ChannelWebhook, n)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"alert.fired","rule":{"id":3,"name":"Frequent"},"issue":{"id":9,"title":"Crash in render","culprit":"render()","level":"fatal","status":"open","url":"/app/acme/voice/issues/9","first_seen":"2026-11-18T10:00:00Z","last_seen":"2026-11-18T11:00:00Z","events":12},"value":9,"threshold":8}`
	if string(body) != want {
		t.Fatalf("body = %s\nwant   %s", body, want)
	}
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"user_hash", "tags", "frames", "email", "install_hash"} {
		if strings.Contains(string(body), banned) {
			t.Errorf("body contains %q", banned)
		}
	}
}
