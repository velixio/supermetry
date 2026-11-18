package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/platform/authz"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestSilenceWindow(t *testing.T) {
	end := at(t, "2026-11-16T11:00:00Z")
	s := Silence{Kind: SilenceKindSilence, StartsAt: at(t, "2026-11-16T10:00:00Z"), EndsAt: &end}
	for _, tc := range []struct {
		when   string
		active bool
	}{
		{"2026-11-16T09:59:59Z", false}, {"2026-11-16T10:00:00Z", true},
		{"2026-11-16T10:59:59Z", true}, {"2026-11-16T11:00:00Z", false},
	} {
		if got := s.ActiveAt(at(t, tc.when)); got != tc.active {
			t.Errorf("active at %s = %v, want %v", tc.when, got, tc.active)
		}
	}
}

func maintenance(tz, clock string, minutes int, days ...int16) Silence {
	return Silence{Kind: SilenceKindMaintenance, StartsAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Weekdays: days, StartTime: clock, DurationMinutes: minutes, TZ: tz}
}

func TestMaintenanceWeekly(t *testing.T) {
	s := maintenance("Europe/Berlin", "02:00", 120, 0) // Sundays 02:00-04:00 CET
	for _, tc := range []struct {
		when   string
		active bool
	}{
		{"2026-11-22T01:30:00Z", true},  // Sunday 02:30 CET
		{"2026-11-22T00:59:00Z", false}, // 01:59 CET
		{"2026-11-22T02:59:00Z", true},  // 03:59 CET
		{"2026-11-22T03:00:00Z", false}, // 04:00 CET, the end is exclusive
		{"2026-11-21T01:30:00Z", false}, // Saturday 02:30 CET
	} {
		if got := s.ActiveAt(at(t, tc.when)); got != tc.active {
			t.Errorf("active at %s = %v, want %v", tc.when, got, tc.active)
		}
	}
}

func TestMaintenanceOvernight(t *testing.T) {
	s := maintenance("UTC", "23:00", 180, 6) // Saturday 23:00 for 3 h
	if !s.ActiveAt(at(t, "2026-11-22T01:00:00Z")) {
		t.Error("Sunday 01:00Z is inside the Saturday window (day-before check)")
	}
	if s.ActiveAt(at(t, "2026-11-22T02:00:00Z")) || !s.ActiveAt(at(t, "2026-11-21T23:00:00Z")) {
		t.Error("window is [Sat 23:00, Sun 02:00)")
	}
}

// Review focus 3: Sunday 02:00 Berlin on 2027-03-28 does not exist (clocks jump
// from 02:00 CET to 03:00 CEST). The window still lasts exactly 120 minutes and
// covers 01:30Z.
func TestMaintenanceDST(t *testing.T) {
	s := maintenance("Europe/Berlin", "02:00", 120, 0)
	if !s.ActiveAt(at(t, "2027-03-28T01:30:00Z")) {
		t.Fatal("01:30Z must be inside the window")
	}
	var first, last time.Time
	for m := time.Date(2027, 3, 27, 22, 0, 0, 0, time.UTC); m.Before(time.Date(2027, 3, 28, 8, 0, 0, 0, time.UTC)); m = m.Add(time.Minute) {
		if s.ActiveAt(m) {
			if first.IsZero() {
				first = m
			}
			last = m
		}
	}
	if got := last.Sub(first) + time.Minute; got != 120*time.Minute {
		t.Fatalf("window lasted %v (%v to %v), want exactly 120m", got, first, last)
	}
	// The autumn switch (2026-10-25, 03:00 CEST back to 02:00 CET) too.
	var n int
	for m := time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC); m.Before(time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)); m = m.Add(time.Minute) {
		if s.ActiveAt(m) {
			n++
		}
	}
	if n != 120 {
		t.Fatalf("autumn window lasted %d minutes, want 120", n)
	}
}

// A +5:45 zone: Monday 09:00 in Kathmandu is 03:15Z, and an overnight window
// crosses local midnight, not UTC midnight.
func TestMaintenanceKathmandu(t *testing.T) {
	s := maintenance("Asia/Kathmandu", "09:00", 60, 1)
	for _, tc := range []struct {
		when   string
		active bool
	}{
		{"2026-11-16T03:14:59Z", false}, {"2026-11-16T03:15:00Z", true},
		{"2026-11-16T04:14:59Z", true}, {"2026-11-16T04:15:00Z", false},
		{"2026-11-15T03:15:00Z", false}, // Sunday
	} {
		if got := s.ActiveAt(at(t, tc.when)); got != tc.active {
			t.Errorf("active at %s = %v, want %v", tc.when, got, tc.active)
		}
	}
	night := maintenance("Asia/Kathmandu", "23:30", 90, 0) // Sunday 23:30 to Monday 01:00 NPT
	for _, tc := range []struct {
		when   string
		active bool
	}{
		{"2026-11-22T17:44:59Z", false}, {"2026-11-22T17:45:00Z", true}, // 23:30 NPT
		{"2026-11-22T19:14:59Z", true}, {"2026-11-22T19:15:00Z", false}, // 01:00 NPT Monday
	} {
		if got := night.ActiveAt(at(t, tc.when)); got != tc.active {
			t.Errorf("night active at %s = %v, want %v", tc.when, got, tc.active)
		}
	}
}

func TestMaintenanceBounds(t *testing.T) {
	s := maintenance("UTC", "10:00", 60, 1)
	s.StartsAt = at(t, "2026-11-17T00:00:00Z")
	end := at(t, "2026-11-30T00:00:00Z")
	s.EndsAt = &end
	if s.ActiveAt(at(t, "2026-11-16T10:30:00Z")) || !s.ActiveAt(at(t, "2026-11-23T10:30:00Z")) || s.ActiveAt(at(t, "2026-11-30T10:30:00Z")) {
		t.Error("starts_at and ends_at bound the recurrence")
	}
}

func TestSilencedScope(t *testing.T) {
	end := at(t, "2026-11-16T11:00:00Z")
	scoped := int64(5)
	list := []Silence{{AlertID: &scoped, Kind: SilenceKindSilence, StartsAt: at(t, "2026-11-16T10:00:00Z"), EndsAt: &end}}
	now := at(t, "2026-11-16T10:30:00Z")
	if _, ok := Silenced(list, 5, now); !ok {
		t.Error("alert 5 is silenced")
	}
	if _, ok := Silenced(list, 6, now); ok {
		t.Error("alert 6 is not covered by an alert-scoped silence")
	}
	list[0].AlertID = nil
	if _, ok := Silenced(list, 6, now); !ok {
		t.Error("a site-wide silence covers every alert")
	}
}

func (f *ruleFixture) silenceEval(src RuleMetricSource) *RuleEvaluator {
	return f.evaluator().SetMetricSource(src).SetSilences(f.service)
}

func (f *ruleFixture) suppressedRows(t *testing.T, alertID int64) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `select count(*) from alert_delivery_log where alert_id=$1 and status='suppressed' and channel_kind='none' and channel_id=0`, alertID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSilencedFlow(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	base := f.now.Truncate(time.Hour).Add(time.Hour) // a future 10:00-style hour start
	end := base.Add(time.Hour)
	if _, err := f.service.CreateSilence(ctx, f.principal, f.siteID, SilenceInput{Kind: SilenceKindSilence, StartsAt: &base, EndsAt: &end, Reason: "deploy"}); err != nil {
		t.Fatal(err)
	}
	crash := f.metricRule(t, MetricTrigger{Name: MetricCrashFreeSessions, Op: "below", Critical: 95, WindowMinutes: 60, MinSample: 20})
	issue := f.newIssueRule(t)
	src := &fakeMetrics{ratio: 0.925, sample: 40}

	f.now = base.Add(20 * time.Minute) // crash-free drops inside the silence
	if err := f.silenceEval(src).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if open, _ := f.incidents(t, crash.ID); open != 1 || f.deliverer.calls != 0 || f.suppressedRows(t, crash.ID) != 1 {
		t.Fatalf("10:20: open=%d deliveries=%d suppressed=%d, want 1, 0, 1", open, f.deliverer.calls, f.suppressedRows(t, crash.ID))
	}

	f.source.newIssues = []AppIssue{{ID: 7, Title: "Crash", Kind: "crash", Events: 3, FirstSeen: f.now}}
	f.now = base.Add(30 * time.Minute)
	if err := f.silenceEval(src).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if f.deliverer.calls != 0 || f.suppressedRows(t, issue.ID) != 1 || f.suppressedRows(t, crash.ID) != 1 {
		t.Fatalf("10:30: deliveries=%d suppressed issue=%d incident=%d, want 0, 1, 1 (logged once)", f.deliverer.calls, f.suppressedRows(t, issue.ID), f.suppressedRows(t, crash.ID))
	}

	f.now = base.Add(65 * time.Minute) // silence over, still below
	if err := f.silenceEval(src).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.deliverer.notifications) != 1 || f.lastRule(t).IncidentID == 0 || f.lastRule(t).Status != "firing" {
		t.Fatalf("11:05: notifications = %+v, want exactly the incident", f.deliverer.notifications)
	}
	f.now = base.Add(66 * time.Minute)
	if err := f.silenceEval(src).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.deliverer.notifications) != 1 {
		t.Fatalf("the silenced issue was sent after the silence: %d notifications", len(f.deliverer.notifications))
	}
}

// An incident that opens and recovers inside one silence sends nothing at all.
func TestSilencedIncidentRecoversQuietly(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	base := f.now.Truncate(time.Hour).Add(time.Hour)
	end := base.Add(time.Hour)
	if _, err := f.service.CreateSilence(ctx, f.principal, f.siteID, SilenceInput{Kind: SilenceKindSilence, StartsAt: &base, EndsAt: &end}); err != nil {
		t.Fatal(err)
	}
	crash := f.metricRule(t, MetricTrigger{Name: MetricCrashFreeSessions, Op: "below", Critical: 95, WindowMinutes: 60, MinSample: 20})
	src := &fakeMetrics{ratio: 0.9, sample: 40}
	f.now = base.Add(10 * time.Minute)
	if err := f.silenceEval(src).Run(ctx); err != nil {
		t.Fatal(err)
	}
	src.ratio = 0.99
	f.now = base.Add(20 * time.Minute)
	if err := f.silenceEval(src).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if open, total := f.incidents(t, crash.ID); open != 0 || total != 1 || f.deliverer.calls != 0 {
		t.Fatalf("open=%d total=%d deliveries=%d, want a closed incident and no notification", open, total, f.deliverer.calls)
	}
}

// An alert-scoped silence leaves other rules alone.
func TestSilenceScopedToAlert(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	muted := f.newIssueRule(t)
	other := f.newRule(t, "Regressions", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: chanAction(f.channelID)})
	start, end := f.now.Add(-time.Minute), f.now.Add(time.Hour)
	if _, err := f.service.CreateSilence(ctx, f.principal, f.siteID, SilenceInput{Kind: SilenceKindSilence, AlertID: &muted.ID, StartsAt: &start, EndsAt: &end}); err != nil {
		t.Fatal(err)
	}
	f.source.newIssues = []AppIssue{{ID: 7, Title: "Crash", Kind: "crash", Events: 3, FirstSeen: f.now}}
	if err := f.silenceEval(&fakeMetrics{}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if f.deliverer.calls != 1 || f.suppressedRows(t, muted.ID) != 1 || f.suppressedRows(t, other.ID) != 0 {
		t.Fatalf("deliveries=%d muted suppressed=%d other suppressed=%d, want 1, 1, 0", f.deliverer.calls, f.suppressedRows(t, muted.ID), f.suppressedRows(t, other.ID))
	}
}

func TestSilenceServiceRules(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	start, end := f.now.Add(time.Hour), f.now.Add(2*time.Hour)
	input := SilenceInput{Kind: SilenceKindSilence, StartsAt: &start, EndsAt: &end}

	viewer := authz.Principal{UserID: 9, OrgID: f.principal.OrgID, Role: authz.RoleViewer, SiteIDs: []int32{f.siteID}}
	if _, err := f.service.CreateSilence(ctx, viewer, f.siteID, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create = %v, want ErrForbidden", err)
	}
	if _, err := f.service.ListSilences(ctx, viewer, f.siteID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer list = %v, want ErrForbidden", err)
	}
	outsider := f.principal
	outsider.OrgID += 1000
	if _, err := f.service.CreateSilence(ctx, outsider, f.siteID, input); err == nil || errors.Is(err, ErrForbidden) {
		t.Fatalf("other org create = %v, want not found", err)
	}

	for _, bad := range []SilenceInput{
		{Kind: "nap", EndsAt: &end},
		{Kind: SilenceKindSilence, StartsAt: &end, EndsAt: &start},
		{Kind: SilenceKindSilence, StartsAt: &start},
		{Kind: SilenceKindMaintenance, Weekdays: []int16{7}, StartTime: "02:00", DurationMinutes: 60},
		{Kind: SilenceKindMaintenance, Weekdays: []int16{1}, StartTime: "2:00", DurationMinutes: 60},
		{Kind: SilenceKindMaintenance, Weekdays: []int16{1}, StartTime: "02:00", DurationMinutes: 1441},
		{Kind: SilenceKindMaintenance, Weekdays: []int16{1}, StartTime: "02:00", DurationMinutes: 60, TZ: "Mars/Base"},
	} {
		if _, err := f.service.CreateSilence(ctx, f.principal, f.siteID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v = %v, want ErrInvalid", bad, err)
		}
	}
	foreign := int64(999999)
	if _, err := f.service.CreateSilence(ctx, f.principal, f.siteID, SilenceInput{Kind: SilenceKindSilence, AlertID: &foreign, StartsAt: &start, EndsAt: &end}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown alert = %v, want ErrInvalid", err)
	}

	// The maintenance time zone defaults to the site's.
	if _, err := f.pool.Exec(ctx, `update sites set timezone='Asia/Kathmandu' where id=$1`, f.siteID); err != nil {
		t.Fatal(err)
	}
	m, err := f.service.CreateSilence(ctx, f.principal, f.siteID, SilenceInput{Kind: SilenceKindMaintenance, Weekdays: []int16{1, 0}, StartTime: "09:00", DurationMinutes: 60})
	if err != nil || m.TZ != "Asia/Kathmandu" || m.StartTime != "09:00" || len(m.Weekdays) != 2 || m.Weekdays[0] != 0 {
		t.Fatalf("maintenance = %+v, %v", m, err)
	}
	m.StartsAt = time.Time{} // created now; probe a past Monday
	if !m.ActiveAt(at(t, "2026-11-16T03:30:00Z")) {
		t.Error("Monday 09:30 Kathmandu is active")
	}

	// 50 active or future silences per site (one exists already).
	for i := 0; i < MaxSilencesPerSite-1; i++ {
		if _, err := f.service.CreateSilence(ctx, f.principal, f.siteID, input); err != nil {
			t.Fatalf("silence %d: %v", i+2, err)
		}
	}
	if _, err := f.service.CreateSilence(ctx, f.principal, f.siteID, input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("51st silence = %v, want ErrInvalid", err)
	}
	list, err := f.service.ListSilences(ctx, f.principal, f.siteID)
	if err != nil || len(list) != MaxSilencesPerSite {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	if err := f.service.DeleteSilence(ctx, outsider, f.siteID, list[0].ID); err == nil || errors.Is(err, ErrForbidden) {
		t.Fatalf("other org delete = %v, want not found", err)
	}
	if err := f.service.DeleteSilence(ctx, f.principal, f.siteID, list[0].ID); err != nil {
		t.Fatal(err)
	}
}
