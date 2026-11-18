package alerts

import (
	"context"
	"strconv"
	"testing"
	"time"

	"supermetry/internal/db"
)

// revertAlertRules puts the schema back to its shape before migration 0316 so
// the test can seed v1 rows and run the real migration over them.
func revertAlertRules(t *testing.T, ctx context.Context, f *ruleFixture) {
	t.Helper()
	for _, stmt := range []string{
		`drop table alert_incidents`,
		`alter table alerts drop column name, drop column rule`,
		`alter table alerts drop constraint alerts_kind_check`,
		`alter table alerts add constraint alerts_kind_check check (kind in ('traffic_spike','traffic_drop','goal_reached','app_new_issue','app_issue_regressed','app_crash_free_below','app_issue_spike'))`,
		`alter table alerts drop constraint alerts_channel_ids_check`,
		`alter table alerts add constraint alerts_channel_ids_check check (not enabled or cardinality(channel_ids) > 0)`,
		`delete from schema_migrations where version=316`,
	} {
		if _, err := f.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func TestV1AlertsConvertToRulesWithoutRepeating(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	revertAlertRules(t, ctx, f)
	seed := func(kind string, threshold float64, config string) int64 {
		var id int64
		var cfg any
		if config != "" {
			cfg = config
		}
		if err := f.pool.QueryRow(ctx, `insert into alerts(site_id,kind,threshold,channel_ids,enabled,config,created_at) values ($1,$2,$3,$4,true,$5::jsonb,now()-interval '48 hours') returning id`,
			f.siteID, kind, threshold, []int64{f.channelID}, cfg).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	newID := seed("app_new_issue", 1, `{"kinds":["crash"]}`)
	regID := seed("app_issue_regressed", 1, `{}`)
	spikeID := seed("app_issue_spike", 1, `{}`)
	crashID := seed("app_crash_free_below", 98.5, `{"metric":"sessions"}`)
	fired := f.now.Add(-24 * time.Hour)
	crashFired := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	for _, row := range []struct {
		alert int64
		key   string
		at    time.Time
	}{{newID, "issue:42", fired}, {crashID, "crashfree:2.1.0", crashFired}, {crashID, "crashfree:2.0.0", time.Now().Add(-30 * time.Hour)}} {
		if _, err := f.pool.Exec(ctx, `insert into app_alert_fired(alert_id,dedup_key,fired_at) values ($1,$2,$3)`, row.alert, row.key, row.at); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Migrate(ctx, f.pool); err != nil {
		t.Fatal(err)
	}

	ch := `[` + strconv.FormatInt(f.channelID, 10) + `]`
	actions := `[{"type":"channel","channel_ids":` + ch + `}]`
	want := map[int64]struct{ name, rule string }{
		newID:   {"New issue", `{"trigger":{"type":"issue","issue":{"on":["new"]}},"filters":{"kinds":["crash"]},"actions":` + actions + `}`},
		regID:   {"Regressed issue", `{"trigger":{"type":"issue","issue":{"on":["regressed"]}},"filters":{},"actions":` + actions + `}`},
		spikeID: {"Issue spike", `{"trigger":{"type":"event","event":{"spike":{"factor":3,"min_events":10,"baseline_days":7}}},"filters":{},"actions":` + actions + `}`},
		crashID: {"Crash-free rate below threshold", `{"trigger":{"type":"metric","metric":{"name":"crash_free_sessions","op":"below","critical":98.5,"window_minutes":1440,"min_sample":100,"release":"latest","renotify_minutes":1440}},"filters":{},"actions":` + actions + `,"notify_on_resolve":false}`},
	}
	for id, w := range want {
		var kind, name string
		var equal bool
		if err := f.pool.QueryRow(ctx, `select kind, name, rule = $2::jsonb from alerts where id=$1`, id, w.rule).Scan(&kind, &name, &equal); err != nil {
			t.Fatal(err)
		}
		if kind != "app_rule" || name != w.name || !equal {
			var got string
			_ = f.pool.QueryRow(ctx, `select rule::text from alerts where id=$1`, id).Scan(&got)
			t.Fatalf("alert %d: kind=%s name=%q equal=%v rule=%s", id, kind, name, equal, got)
		}
	}
	// Every converted document passes validation and loads through the service.
	rules, err := f.service.EnabledRules(ctx)
	if err != nil || len(rules) != 4 {
		t.Fatalf("EnabledRules() = %d rules, %v", len(rules), err)
	}
	for _, r := range rules {
		if _, err := ValidateRule(*r.Rule); err != nil {
			t.Fatalf("converted rule %d (%s) is invalid: %v", r.ID, r.Name, err)
		}
	}

	// The crash-free key from 3 h ago is an open, already notified incident; the
	// 30 h old key is not.
	var group string
	var notified time.Time
	var open int
	if err := f.pool.QueryRow(ctx, `select count(*), min(group_key), min(notified_at) from alert_incidents where alert_id=$1 and resolved_at is null`, crashID).Scan(&open, &group, &notified); err != nil {
		t.Fatal(err)
	}
	if open != 1 || group != "release:2.1.0" || !notified.Equal(crashFired) {
		t.Fatalf("incidents: open=%d group=%q notified=%v, want 1, release:2.1.0, %v", open, group, notified, crashFired)
	}

	// Issue 42 already fired for the old alert: the rule must not send it again.
	f.now = time.Now().UTC()
	f.source.newIssues = []AppIssue{{ID: 42, Title: "old", Kind: "crash", FirstSeen: f.now.Add(-20 * time.Hour)}}
	if err := f.evaluator().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if f.deliverer.calls != 0 {
		t.Fatalf("deliveries = %d, want none for issue 42", f.deliverer.calls)
	}
	// A different issue does fire, proving the rule is live.
	f.source.newIssues = append(f.source.newIssues, AppIssue{ID: 43, Title: "fresh", Kind: "crash", FirstSeen: f.now.Add(-time.Minute)})
	if err := f.evaluator().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if f.deliverer.calls != 1 || f.deliverer.notifications[0].App.IssueID != 43 {
		t.Fatalf("deliveries = %d (%+v), want one for issue 43", f.deliverer.calls, f.deliverer.notifications)
	}
}
