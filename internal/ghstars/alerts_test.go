package ghstars

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/alerts"
)

// The test clock is 2026-10-10T12:00:00Z, so the six hour window is
// (06:00, 12:00] and the baseline is (2026-09-26T06:00, 2026-10-10T06:00],
// which holds 56 six hour slots.
const (
	windowEvent = "2026-10-10T08:00:00Z"
	baseEvent   = "2026-10-01T00:00:00Z"
)

var userSeq int64 = 1000

type sentAlert struct {
	channelID  int64
	kind, text string
}

// fakeAlerts stands in for the alerts service and dispatcher.
type fakeAlerts struct {
	mu       sync.Mutex
	channels map[int64]alerts.DeliveryChannel
	sent     []sentAlert
	failing  map[int64]bool
}

func (f *fakeAlerts) DeliveryChannels(_ context.Context, ids []int64) ([]alerts.DeliveryChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []alerts.DeliveryChannel
	for _, id := range ids {
		if c, ok := f.channels[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeAlerts) DeliverMessage(_ context.Context, c alerts.DeliveryChannel, kind, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing[c.ID] {
		return errors.New("webhook delivery returned status 500 for https://hooks.example.test/secret-path")
	}
	f.sent = append(f.sent, sentAlert{c.ID, kind, text})
	return nil
}

func (f *fakeAlerts) messages() []sentAlert {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentAlert(nil), f.sent...)
}

// alertEnv wires a service with the fakes and one repository per call.
type alertEnv struct {
	svc  *Service
	fake *fakeAlerts
}

func newAlertEnv(t *testing.T) *alertEnv {
	t.Helper()
	svc, _ := newTestService(t)
	fake := &fakeAlerts{channels: map[int64]alerts.DeliveryChannel{}, failing: map[int64]bool{}}
	svc.AlertChannels = fake
	svc.AlertSender = fake
	return &alertEnv{svc: svc, fake: fake}
}

func (e *alertEnv) channel(t *testing.T, orgID int64, name string) int64 {
	t.Helper()
	var id int64
	err := e.svc.pool.QueryRow(context.Background(),
		`insert into notification_channels(org_id,kind,name,config) values ($1,'slack',$2,'{}') returning id`, orgID, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	e.fake.channels[id] = alerts.DeliveryChannel{ID: id, OrgID: orgID, Kind: alerts.ChannelSlack, Name: name, Enabled: true}
	return id
}

// repo creates a finished-import repository with the given verified count and
// alert settings (all five events on, unless events is given).
func (e *alertEnv) repo(t *testing.T, orgID int64, name string, verified int, channelIDs []int64, on ...AlertKind) int64 {
	t.Helper()
	id := insertRepoRow(t, e.svc.pool, orgID, name)
	if _, err := e.svc.pool.Exec(context.Background(), `update github_star_repos set backfill_complete=true, verified_stars=$2 where id=$1`, id, verified); err != nil {
		t.Fatal(err)
	}
	events := map[AlertKind]bool{}
	for _, k := range on {
		events[k] = true
	}
	if _, err := e.svc.SaveAlertSettings(context.Background(), orgID, id, AlertSettings{Events: events, ChannelIDs: channelIDs}, 0); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *alertEnv) setVerified(t *testing.T, id int64, stars int) {
	t.Helper()
	if _, err := e.svc.pool.Exec(context.Background(), `update github_star_repos set verified_stars=$2 where id=$1`, id, stars); err != nil {
		t.Fatal(err)
	}
}

func (e *alertEnv) evaluate(t *testing.T) {
	t.Helper()
	if err := e.svc.EvaluateAlerts(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// addEvents inserts n events of one action, one minute apart from start.
func addEvents(t *testing.T, svc *Service, repoID int64, action, source string, n int, start string) {
	t.Helper()
	first := userSeq
	userSeq += int64(n)
	_, err := svc.pool.Exec(context.Background(), `
		insert into github_star_events(repo_id,user_id,login,action,occurred_at,source)
		select $1, $2 + g, 'secretlogin' || g, $3, $4::timestamptz + g * interval '1 minute', $5
		from generate_series(0, $6 - 1) g`, repoID, first, action, start, source, n)
	if err != nil {
		t.Fatal(err)
	}
}

var allKinds = []AlertKind{AlertStarMilestone, AlertStarSpike, AlertUnstarSurge, AlertTrendingEntered, AlertTrendingLeft}

func TestMilestoneFiresOnlyWhenCrossedUpwardAndNeverOnFirstSight(t *testing.T) {
	e := newAlertEnv(t)
	org := newOrg(t, e.svc, "milestones")
	ch := e.channel(t, org, "ops")
	// Enabling alerts on a repository that already has 5,000 stars must not
	// replay the milestones below it.
	big := e.repo(t, org, "acme/big", 5000, []int64{ch}, allKinds...)
	e.evaluate(t)
	e.evaluate(t)
	if got := e.fake.messages(); len(got) != 0 {
		t.Fatalf("first evaluations sent %v", got)
	}
	e.setVerified(t, big, 5100)
	e.evaluate(t)
	if got := e.fake.messages(); len(got) != 0 {
		t.Fatalf("no milestone crossed, sent %v", got)
	}

	small := e.repo(t, org, "acme/small", 2400, []int64{ch}, allKinds...)
	e.evaluate(t) // baseline
	e.setVerified(t, small, 2600)
	e.evaluate(t)
	got := e.fake.messages()
	if len(got) != 1 || got[0].kind != "github_star_star_milestone" || !strings.Contains(got[0].text, "acme/small reached 2,500 stars") {
		t.Fatalf("messages = %+v", got)
	}
	if !strings.Contains(got[0].text, "https://github.com/acme/small") || strings.Contains(got[0].text, "secretlogin") {
		t.Fatalf("text = %q", got[0].text)
	}
	e.evaluate(t)
	if len(e.fake.messages()) != 1 {
		t.Fatalf("an unchanged count must not resend: %+v", e.fake.messages())
	}
	// Dropping below and crossing again does not repeat the alert (same key).
	e.setVerified(t, small, 2400)
	e.evaluate(t)
	e.setVerified(t, small, 2600)
	e.evaluate(t)
	if len(e.fake.messages()) != 1 {
		t.Fatalf("a re-crossed milestone must not resend: %+v", e.fake.messages())
	}
	// One jump over three milestones (90 -> 600) reports only the highest.
	jump := e.repo(t, org, "acme/jump", 90, []int64{ch}, allKinds...)
	e.evaluate(t)
	e.setVerified(t, jump, 600)
	e.evaluate(t)
	got = e.fake.messages()
	if len(got) != 2 || !strings.Contains(got[1].text, "acme/jump reached 500 stars") {
		t.Fatalf("messages = %+v", got)
	}
}

func TestSpikeAndSurgeThresholdsFromHandComputedFixtures(t *testing.T) {
	tests := []struct {
		name   string
		action string
		base   int // events in the 14 days before the window
		window int
		kind   string
		want   int
	}{
		// 112 stars / 56 slots = 2 per slot; 3 x 2 = 6 is under the floor of 10.
		{"spike below floor", ActionStarred, 112, 9, "github_star_star_spike", 0},
		{"spike at floor", ActionStarred, 112, 10, "github_star_star_spike", 1},
		// 560 / 56 = 10 per slot; 3 x 10 = 30 beats the floor.
		{"spike below 3x average", ActionStarred, 560, 29, "github_star_star_spike", 0},
		{"spike at 3x average", ActionStarred, 560, 30, "github_star_star_spike", 1},
		// 56 / 56 = 1 per slot; 3 is under the floor of 5.
		{"surge below floor", ActionUnstarred, 56, 4, "github_star_unstar_surge", 0},
		{"surge at floor", ActionUnstarred, 56, 5, "github_star_unstar_surge", 1},
		// 280 / 56 = 5 per slot; 15 beats the floor.
		{"surge below 3x average", ActionUnstarred, 280, 14, "github_star_unstar_surge", 0},
		{"surge at 3x average", ActionUnstarred, 280, 15, "github_star_unstar_surge", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newAlertEnv(t)
			org := newOrg(t, e.svc, "rates")
			ch := e.channel(t, org, "ops")
			id := e.repo(t, org, "acme/rates", 50, []int64{ch}, allKinds...)
			addEvents(t, e.svc, id, tt.action, SourceWebhook, tt.base, baseEvent)
			addEvents(t, e.svc, id, tt.action, SourceWebhook, tt.window, windowEvent)
			e.evaluate(t)
			count := 0
			for _, m := range e.fake.messages() {
				if m.kind == tt.kind {
					count++
					if strings.Contains(m.text, "secretlogin") || !strings.Contains(m.text, "https://github.com/acme/rates") {
						t.Fatalf("text = %q", m.text)
					}
				}
			}
			if count != tt.want {
				t.Fatalf("alerts = %+v, want %d", e.fake.messages(), tt.want)
			}
		})
	}
}

func TestImportedHistoryAndUnfinishedImportsRaiseNoRateAlerts(t *testing.T) {
	e := newAlertEnv(t)
	org := newOrg(t, e.svc, "import")
	ch := e.channel(t, org, "ops")
	// 20 stars dated inside the window but imported by the backfill.
	imported := e.repo(t, org, "acme/imported", 50, []int64{ch}, allKinds...)
	addEvents(t, e.svc, imported, ActionStarred, SourceBackfill, 20, windowEvent)
	// A real spike, but the history import has not finished.
	unfinished := e.repo(t, org, "acme/unfinished", 50, []int64{ch}, allKinds...)
	addEvents(t, e.svc, unfinished, ActionStarred, SourceWebhook, 40, windowEvent)
	if _, err := e.svc.pool.Exec(context.Background(), `update github_star_repos set backfill_complete=false where id=$1`, unfinished); err != nil {
		t.Fatal(err)
	}
	e.evaluate(t)
	if got := e.fake.messages(); len(got) != 0 {
		t.Fatalf("sent %+v", got)
	}
}

func TestDefaultsOffAndEventsMustBeTicked(t *testing.T) {
	e := newAlertEnv(t)
	org := newOrg(t, e.svc, "off")
	ch := e.channel(t, org, "ops")
	// Nothing ticked: a real spike sends nothing.
	off := e.repo(t, org, "acme/off", 50, []int64{ch})
	addEvents(t, e.svc, off, ActionStarred, SourceWebhook, 40, windowEvent)
	// Spike ticked but no channel selected: nothing is sent and nothing is claimed.
	noChannel := e.repo(t, org, "acme/nochannel", 50, nil, AlertStarSpike)
	addEvents(t, e.svc, noChannel, ActionStarred, SourceWebhook, 40, windowEvent)
	e.evaluate(t)
	if got := e.fake.messages(); len(got) != 0 {
		t.Fatalf("sent %+v", got)
	}
	var claimed int
	if err := e.svc.pool.QueryRow(context.Background(), `select count(*) from github_star_alert_log`).Scan(&claimed); err != nil || claimed != 0 {
		t.Fatalf("claimed = %d, %v", claimed, err)
	}
	s, err := e.svc.AlertSettings(context.Background(), org, off)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range allKinds {
		if s.Events[k] {
			t.Fatalf("%s defaults to on", k)
		}
	}
}

func TestSameAlertIsSentOnceAndThrottledPerKind(t *testing.T) {
	e := newAlertEnv(t)
	org := newOrg(t, e.svc, "dedup")
	ch := e.channel(t, org, "ops")
	id := e.repo(t, org, "acme/dedup", 50, []int64{ch}, AlertStarSpike, AlertUnstarSurge)
	addEvents(t, e.svc, id, ActionStarred, SourceWebhook, 12, windowEvent)
	e.evaluate(t)
	e.evaluate(t) // the next 10 minute run sees the same slot
	if got := e.fake.messages(); len(got) != 1 || !strings.Contains(got[0].text, "gained 12 stars") {
		t.Fatalf("messages = %+v", got)
	}
	// The same kind in a later slot inside the 10 minute interval is dropped;
	// another kind is unaffected.
	e.svc.Now = func() time.Time { return at("2026-10-10T12:05:00Z") }
	addEvents(t, e.svc, id, ActionUnstarred, SourceWebhook, 6, "2026-10-10T11:00:00Z")
	e.evaluate(t)
	if got := e.fake.messages(); len(got) != 2 || !strings.Contains(got[1].text, "lost 6 stars") {
		t.Fatalf("messages = %+v", got)
	}
}

func TestOrganizationIsolation(t *testing.T) {
	e := newAlertEnv(t)
	orgA, orgB := newOrg(t, e.svc, "org-a"), newOrg(t, e.svc, "org-b")
	chA, chB := e.channel(t, orgA, "a"), e.channel(t, orgB, "b")
	repoA := e.repo(t, orgA, "acme/tool", 50, []int64{chA}, AlertStarSpike)
	repoB := e.repo(t, orgB, "acme/tool", 50, []int64{chB}, AlertStarSpike) // same GitHub repo, other org
	addEvents(t, e.svc, repoA, ActionStarred, SourceWebhook, 12, windowEvent)
	addEvents(t, e.svc, repoB, ActionStarred, SourceWebhook, 2, windowEvent)
	e.evaluate(t)
	if got := e.fake.messages(); len(got) != 1 || got[0].channelID != chA {
		t.Fatalf("messages = %+v", got)
	}
	// Another organization's channel id is rejected, and so is a missing one.
	ctx := context.Background()
	if _, err := e.svc.SaveAlertSettings(ctx, orgA, repoA, AlertSettings{ChannelIDs: []int64{chB}}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign channel err = %v, want ErrInvalid", err)
	}
	if _, err := e.svc.SaveAlertSettings(ctx, orgA, repoA, AlertSettings{ChannelIDs: []int64{999999}}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing channel err = %v, want ErrInvalid", err)
	}
	// Another organization's repository is not found.
	if _, err := e.svc.AlertSettings(ctx, orgA, repoB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign repo err = %v, want ErrNotFound", err)
	}
	if _, err := e.svc.SaveAlertSettings(ctx, orgA, repoB, AlertSettings{}, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign repo save err = %v, want ErrNotFound", err)
	}
	if _, err := e.svc.SendTestAlert(ctx, orgA, repoB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign repo test err = %v, want ErrNotFound", err)
	}
}

func TestAFailingChannelDoesNotBlockTheOthers(t *testing.T) {
	e := newAlertEnv(t)
	org := newOrg(t, e.svc, "failing")
	bad, good := e.channel(t, org, "bad"), e.channel(t, org, "good")
	e.fake.failing[bad] = true
	id := e.repo(t, org, "acme/fail", 50, []int64{bad, good}, AlertStarSpike)
	addEvents(t, e.svc, id, ActionStarred, SourceWebhook, 12, windowEvent)
	e.evaluate(t)
	if got := e.fake.messages(); len(got) != 1 || got[0].channelID != good {
		t.Fatalf("messages = %+v", got)
	}
	if st := e.svc.AlertStats(); st.Sent != 1 || st.Failed != 1 {
		t.Fatalf("stats = %+v", st)
	}
	// The test alert reports each channel with a scrubbed error.
	results, err := e.svc.SendTestAlert(context.Background(), org, id)
	if err != nil || len(results) != 2 {
		t.Fatalf("results = %+v, %v", results, err)
	}
	for _, r := range results {
		if strings.Contains(r.Error, "hooks.example.test") || strings.Contains(r.Error, "secret-path") {
			t.Fatalf("error not scrubbed: %q", r.Error)
		}
	}
	if _, err := e.svc.SendTestAlert(context.Background(), org, id); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("second test = %v, want ErrTooSoon", err)
	}
	// When every channel fails the claim is released, so the alert can retry.
	e.fake.failing[good] = true
	e2 := e.svc
	e2.Now = func() time.Time { return at("2026-10-10T12:30:00Z") }
	addEvents(t, e.svc, id, ActionUnstarred, SourceWebhook, 6, "2026-10-10T12:00:00Z")
	if _, err := e.svc.SaveAlertSettings(context.Background(), org, id, AlertSettings{Events: map[AlertKind]bool{AlertUnstarSurge: true}, ChannelIDs: []int64{bad, good}}, 0); err != nil {
		t.Fatal(err)
	}
	e.evaluate(t)
	var n int
	if err := e.svc.pool.QueryRow(context.Background(), `select count(*) from github_star_alert_log where kind='unstar_surge'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("log rows after total failure = %d, %v", n, err)
	}
}

func TestDeletingAChannelRemovesItFromSettings(t *testing.T) {
	e := newAlertEnv(t)
	org := newOrg(t, e.svc, "cascade")
	keep, drop := e.channel(t, org, "keep"), e.channel(t, org, "drop")
	id := e.repo(t, org, "acme/cascade", 50, []int64{keep, drop}, AlertStarSpike)
	if _, err := e.svc.pool.Exec(context.Background(), `delete from notification_channels where id=$1`, drop); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.AlertSettings(context.Background(), org, id)
	if err != nil || len(s.ChannelIDs) != 1 || s.ChannelIDs[0] != keep {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	if _, err := e.svc.SaveAlertSettings(context.Background(), org, id, AlertSettings{Events: map[AlertKind]bool{"nope": true}}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind err = %v", err)
	}
}

func TestTrendingAlertsUseTheSameSettingsAndDedup(t *testing.T) {
	e := newAlertEnv(t)
	org := newOrg(t, e.svc, "trending")
	ch := e.channel(t, org, "ops")
	id := e.repo(t, org, "acme/hot", 50, []int64{ch}, AlertTrendingEntered)
	ctx := context.Background()
	d := TrendingDetails{Period: "daily", Language: "go", Rank: 4}
	e.svc.TrendingEntered(ctx, id, d)
	e.svc.TrendingEntered(ctx, id, d)
	e.svc.TrendingLeft(ctx, id, d) // not ticked
	got := e.fake.messages()
	if len(got) != 1 || !strings.Contains(got[0].text, "acme/hot is now trending on GitHub (go daily). Rank 4.") {
		t.Fatalf("messages = %+v", got)
	}
}

func TestHighestCrossedAndFormatCount(t *testing.T) {
	for _, c := range []struct{ prev, cur, want int }{
		{0, 99, 0}, {99, 100, 100}, {100, 100, 0}, {100, 249, 0}, {100, 250, 250},
		{0, 100000, 100000}, {99999, 100001, 100000}, {200000, 300000, 0}, {600, 400, 0},
	} {
		if got := highestCrossed(c.prev, c.cur); got != c.want {
			t.Errorf("highestCrossed(%d,%d) = %d, want %d", c.prev, c.cur, got, c.want)
		}
	}
	for n, want := range map[int]string{100: "100", 1000: "1,000", 25000: "25,000", 100000: "100,000"} {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q", n, got)
		}
	}
}
