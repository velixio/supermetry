package appmonitors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func ts(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

func TestTransition(t *testing.T) {
	l := Limits{FailureThreshold: 2, RecoveryThreshold: 2}
	s := State{Status: StatusOK}
	s, o := Transition(s, l, StatusError)
	if s.Status != StatusOK || o != OutcomeNone || s.Failures != 1 {
		t.Fatalf("first failure under threshold 2: %+v %v", s, o)
	}
	s, o = Transition(s, l, StatusError)
	if s.Status != StatusError || o != OutcomeFailure {
		t.Fatalf("second failure should open: %+v %v", s, o)
	}
	s, o = Transition(s, l, StatusMissed)
	if s.Status != StatusMissed || o != OutcomeNone {
		t.Fatalf("further failures do not alert again: %+v %v", s, o)
	}
	s, o = Transition(s, l, StatusOK)
	if s.Status != StatusMissed || o != OutcomeNone {
		t.Fatalf("one ok under recovery threshold 2: %+v %v", s, o)
	}
	s, o = Transition(s, l, StatusOK)
	if s.Status != StatusOK || o != OutcomeRecovery || s.Failures != 0 {
		t.Fatalf("second ok recovers: %+v %v", s, o)
	}
	if got, o := Transition(State{Status: StatusDisabled}, l, StatusError); got.Status != StatusDisabled || o != OutcomeNone {
		t.Fatal("disabled monitors never change")
	}
	// A lone failure between oks never crosses the threshold.
	s = State{Status: StatusOK}
	s, _ = Transition(s, l, StatusError)
	s, _ = Transition(s, l, StatusOK)
	if s.Failures != 0 {
		t.Fatalf("ok resets the failure streak: %+v", s)
	}
}

func TestParseSchedule(t *testing.T) {
	if _, err := ParseSchedule(ScheduleInterval, "2 hour", "UTC"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][3]string{{"interval", "0 hour", "UTC"}, {"interval", "2 fortnight", "UTC"}, {"crontab", "61 * * * *", "UTC"}, {"crontab", "* * * * *", "Nowhere/City"}, {"weekly", "x", "UTC"}, {"crontab", "0 0 31 2 *", "UTC"}} {
		if _, err := ParseSchedule(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("ParseSchedule(%v) should fail", bad)
		}
	}
	if _, ok := NormalizeUUID("0123456789ABCDEF0123456789abcdef"); !ok {
		t.Fatal("32 hex digits are a uuid")
	}
	if _, ok := NormalizeUUID("not-a-uuid"); ok {
		t.Fatal("garbage is not a uuid")
	}
}

type fixture struct {
	pool  *pgxpool.Pool
	store *Store
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('Org','org');
insert into sites(id,org_id,public_id,name,timezone,retention_class,kind) overriding system value
values(7,1,'appsite00001','App','UTC',30,'app')`); err != nil {
		t.Fatal(err)
	}
	f := &fixture{pool: pool, now: ts("2026-11-20T10:00:00Z")}
	f.store = NewStore(pool, func() time.Time { return f.now })
	return f
}

func intervalCfg(n string, margin, maxRun int) *Config {
	return &Config{ScheduleType: ScheduleInterval, Schedule: n, MarginMinutes: margin, MaxRuntimeMinutes: maxRun}
}

func (f *fixture) checkin(t *testing.T, at, id, status string, cfg *Config) error {
	t.Helper()
	return f.store.Record(context.Background(), CheckIn{SiteID: 7, Slug: "nightly", Environment: "production", ID: id, Status: status, At: ts(at), Config: cfg})
}

func (f *fixture) sweep(t *testing.T, at string) (int, int) {
	t.Helper()
	m, to, err := f.store.Sweep(context.Background(), ts(at))
	if err != nil {
		t.Fatal(err)
	}
	return m, to
}

func (f *fixture) events(t *testing.T) []AlertEvent {
	t.Helper()
	ev, err := f.store.AlertEvents(context.Background(), 7, ts("2026-11-01T00:00:00Z"), 100)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

const (
	id1 = "11111111-1111-4111-8111-111111111111"
	id2 = "22222222-2222-4222-8222-222222222222"
	id3 = "33333333-3333-4333-8333-333333333333"
)

func TestUnknownSlugWithoutConfigIsRejected(t *testing.T) {
	f := newFixture(t)
	if err := f.checkin(t, "2026-11-20T10:00:00Z", id1, StatusOK, nil); !errors.Is(err, ErrUnknownMonitor) {
		t.Fatalf("err = %v, want ErrUnknownMonitor", err)
	}
	if err := f.checkin(t, "2026-11-20T10:00:00Z", id1, StatusOK, &Config{ScheduleType: "interval", Schedule: "nope"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

// Interval 2 hours after an ok at 10:00Z expects 12:00Z; margin 5 gives no
// missed at 12:04Z, exactly one at 12:06Z and none at 12:07Z.
func TestMissedAfterMargin(t *testing.T) {
	f := newFixture(t)
	if err := f.checkin(t, "2026-11-20T10:00:00Z", id1, StatusOK, intervalCfg("2 hour", 5, 30)); err != nil {
		t.Fatal(err)
	}
	ms, _ := f.store.ListMonitors(context.Background(), 7)
	if len(ms) != 1 || ms[0].NextExpectedAt == nil || !ms[0].NextExpectedAt.Equal(ts("2026-11-20T12:00:00Z")) {
		t.Fatalf("monitor after first ok: %+v", ms)
	}
	if m, _ := f.sweep(t, "2026-11-20T12:04:00Z"); m != 0 {
		t.Fatal("missed inside the margin")
	}
	if m, _ := f.sweep(t, "2026-11-20T12:06:00Z"); m != 1 {
		t.Fatalf("missed at 12:06 = %d, want 1", m)
	}
	if m, _ := f.sweep(t, "2026-11-20T12:07:00Z"); m != 0 {
		t.Fatal("second miss for the same slot")
	}
	ev := f.events(t)
	if len(ev) != 1 || ev[0].Status != StatusMissed || ev[0].Recovery || !ev[0].ExpectedAt.Equal(ts("2026-11-20T12:00:00Z")) {
		t.Fatalf("alert events: %+v", ev)
	}
	// A check-in that arrives well before the next slot is the late one: it
	// completes the missed slot, recovers the monitor, and raises no second failure.
	if err := f.checkin(t, "2026-11-20T12:10:00Z", id2, StatusOK, nil); err != nil {
		t.Fatal(err)
	}
	ev = f.events(t)
	if len(ev) != 2 || !ev[1].Recovery || ev[0].Recovery {
		t.Fatalf("expected one failure then one recovery: %+v", ev)
	}
	rows, _ := f.store.ListCheckins(context.Background(), 7, ms[0].ID, 10)
	if len(rows) != 2 || rows[0].Status != StatusOK && rows[1].Status != StatusOK {
		t.Fatalf("check-ins after late ok: %+v", rows)
	}
	for _, r := range rows {
		if r.Status == StatusMissed {
			t.Fatalf("the missed slot should be completed: %+v", rows)
		}
	}
	ms, _ = f.store.ListMonitors(context.Background(), 7)
	if ms[0].Status != StatusOK || !ms[0].NextExpectedAt.Equal(ts("2026-11-20T14:10:00Z")) {
		t.Fatalf("monitor after late ok: %+v", ms[0])
	}
}

// in_progress at 10:00Z with max runtime 30: timeout at 10:31Z (once); the
// later ok with the same id completes it and does not alert again.
func TestTimeoutThenLateOK(t *testing.T) {
	f := newFixture(t)
	if err := f.checkin(t, "2026-11-20T10:00:00Z", id1, StatusInProgress, intervalCfg("1 day", 5, 30)); err != nil {
		t.Fatal(err)
	}
	if _, to := f.sweep(t, "2026-11-20T10:29:00Z"); to != 0 {
		t.Fatal("timeout before the max runtime")
	}
	if _, to := f.sweep(t, "2026-11-20T10:31:00Z"); to != 1 {
		t.Fatal("expected one timeout at 10:31")
	}
	if _, to := f.sweep(t, "2026-11-20T10:32:00Z"); to != 0 {
		t.Fatal("timeout repeated")
	}
	ev := f.events(t)
	if len(ev) != 1 || ev[0].Status != StatusTimeout || ev[0].Recovery {
		t.Fatalf("events after timeout: %+v", ev)
	}
	if err := f.checkin(t, "2026-11-20T10:40:00Z", id1, StatusOK, nil); err != nil {
		t.Fatal(err)
	}
	ev = f.events(t)
	failures := 0
	for _, e := range ev {
		if !e.Recovery {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("a late ok must not add a failure alert: %+v", ev)
	}
	if len(ev) != 2 || !ev[1].Recovery {
		t.Fatalf("the late ok should count as the recovery: %+v", ev)
	}
}

func TestFailureThresholdTwo(t *testing.T) {
	f := newFixture(t)
	cfg := intervalCfg("1 hour", 5, 30)
	cfg.FailureThreshold = 2
	if err := f.checkin(t, "2026-11-20T10:00:00Z", id1, StatusError, cfg); err != nil {
		t.Fatal(err)
	}
	if got := f.events(t); len(got) != 0 {
		t.Fatalf("one error under threshold 2 must not alert: %+v", got)
	}
	ms, _ := f.store.ListMonitors(context.Background(), 7)
	if ms[0].Status != StatusOK {
		t.Fatalf("status = %s, want ok", ms[0].Status)
	}
	if err := f.checkin(t, "2026-11-20T11:00:00Z", id2, StatusError, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.events(t); len(got) != 1 || got[0].Status != StatusError {
		t.Fatalf("second error should alert once: %+v", got)
	}
	if err := f.checkin(t, "2026-11-20T12:00:00Z", id3, StatusError, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.events(t); len(got) != 1 {
		t.Fatalf("a third error must not alert again: %+v", got)
	}
}

func TestInProgressThenErrorCompletesRow(t *testing.T) {
	f := newFixture(t)
	if err := f.checkin(t, "2026-11-20T10:00:00Z", id1, StatusInProgress, intervalCfg("1 hour", 5, 30)); err != nil {
		t.Fatal(err)
	}
	dur := int64(1500)
	if err := f.store.Record(context.Background(), CheckIn{SiteID: 7, Slug: "nightly", Environment: "production", ID: id1, Status: StatusError, DurationMS: &dur, At: ts("2026-11-20T10:00:02Z")}); err != nil {
		t.Fatal(err)
	}
	ms, _ := f.store.ListMonitors(context.Background(), 7)
	rows, _ := f.store.ListCheckins(context.Background(), 7, ms[0].ID, 10)
	if len(rows) != 1 || rows[0].Status != StatusError || rows[0].DurationMS == nil || *rows[0].DurationMS != 1500 {
		t.Fatalf("rows: %+v", rows)
	}
	if got := f.events(t); len(got) != 1 {
		t.Fatalf("events: %+v", got)
	}
	// Replaying the final check-in changes nothing.
	if err := f.store.Record(context.Background(), CheckIn{SiteID: 7, Slug: "nightly", Environment: "production", ID: id1, Status: StatusError, At: ts("2026-11-20T10:00:03Z")}); err != nil {
		t.Fatal(err)
	}
	if got := f.events(t); len(got) != 1 {
		t.Fatalf("replay changed events: %+v", got)
	}
}

func TestCrontabSlotAndDisable(t *testing.T) {
	f := newFixture(t)
	cfg := &Config{ScheduleType: ScheduleCrontab, Schedule: "*/15 * * * *", MarginMinutes: 2}
	if err := f.checkin(t, "2026-11-20T10:07:00Z", id1, StatusOK, cfg); err != nil {
		t.Fatal(err)
	}
	ms, _ := f.store.ListMonitors(context.Background(), 7)
	if !ms[0].NextExpectedAt.Equal(ts("2026-11-20T10:15:00Z")) {
		t.Fatalf("next = %v", ms[0].NextExpectedAt)
	}
	yes := true
	if _, err := f.store.Update(context.Background(), 7, ms[0].ID, Patch{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.sweep(t, "2026-11-20T11:00:00Z"); m != 0 {
		t.Fatal("disabled monitors are never missed")
	}
	no := false
	if m, err := f.store.Update(context.Background(), 7, ms[0].ID, Patch{Disabled: &no}); err != nil || m.Status != StatusOK {
		t.Fatalf("re-enable: %+v %v", m, err)
	}
	if err := f.store.Delete(context.Background(), 7, ms[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Delete(context.Background(), 7, ms[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestMonitorLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `insert into app_monitors(site_id, slug, schedule_type, schedule)
  select 7, 'm' || g, 'interval', '1 hour' from generate_series(1, $1) g`, MaxMonitorsPerSite); err != nil {
		t.Fatal(err)
	}
	if err := f.checkin(t, "2026-11-20T10:00:00Z", id1, StatusOK, intervalCfg("1 hour", 1, 30)); !errors.Is(err, ErrMonitorLimit) {
		t.Fatalf("err = %v, want ErrMonitorLimit", err)
	}
}
