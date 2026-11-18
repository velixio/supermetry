package appissues_test

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appissues"
)

// digestFixture is project 7 in America/New_York with the week of Monday
// 2026-11-09 (EST, UTC-5, so the week is 2026-11-09T05:00Z up to
// 2026-11-16T05:00Z) filled with hand-countable data.
type digestFixture struct {
	notifyFixture
	digester *appissues.Digester
	weekFrom time.Time
	dave     int64
}

func newDigestFixture(t *testing.T) digestFixture {
	t.Helper()
	n := newNotifyFixture(t)
	ctx := context.Background()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := n.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`update sites set timezone='America/New_York' where id=7`)
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	f := digestFixture{notifyFixture: n, weekFrom: time.Date(2026, 11, 9, 0, 0, 0, 0, ny)}
	f.digester = appissues.NewDigester(n.pool, n.sender, "https://stats.example.test", n.clock.Now)

	// Three issues first seen in the local week: one early, one late Sunday and
	// one at 23:30 EST on Sunday, which is already Monday in UTC. The decoys
	// sit just outside, 23:30 EST the Sunday before and just after the week.
	for i, at := range []string{"2026-11-10T12:00:00Z", "2026-11-15T12:00:00Z", "2026-11-16T04:30:00Z", "2026-11-09T04:30:00Z", "2026-11-16T05:30:00Z"} {
		mustExec(`insert into app_issues(site_id,fingerprint,kind,title,first_seen,last_seen,event_count) values (7,$1,'error','New issue','`+at+`','`+at+`',0)`, fmt.Sprintf("new%d", i))
	}
	mustExec(`update app_issues set regressed_at='2026-11-12T10:00:00Z' where id=$1`, n.issue)
	mustExec(`update app_issues set title='Failure for jane@corp.test' where id=$1`, n.other)
	n.events(t, n.issue, 120, time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), nil)
	n.events(t, n.other, 30, time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC), nil)
	n.events(t, n.issue, 50, time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), nil) // after the week

	session := func(i int, started, install, status string) {
		mustExec(`insert into app_sessions(site_id,started,session_hash,install_hash,status,updated_at) values (7,$1,$2,$3,$4,$1)`,
			started, []byte(fmt.Sprintf("s-%s-%d", started, i)), []byte(install), status)
	}
	for i := 0; i < 40; i++ { // this week: 1 of 40 installs crashed = 97.5%
		status := "exited"
		if i == 0 {
			status = "crashed"
		}
		session(i, "2026-11-12T12:00:00Z", fmt.Sprintf("inst%d", i), status)
	}
	for i := 0; i < 40; i++ { // the week before: 2 of 40 = 95%
		status := "exited"
		if i < 2 {
			status = "crashed"
		}
		session(i, "2026-11-05T12:00:00Z", fmt.Sprintf("inst%d", i), status)
	}
	session(99, "2026-11-16T05:30:00Z", "late", "crashed") // after the week: not counted

	mustExec(`insert into users(email,name,password_hash) values ('dave2@example.test','Dave2','h')`)
	if err := n.pool.QueryRow(ctx, `select id from users where email='dave2@example.test'`).Scan(&f.dave); err != nil {
		t.Fatal(err)
	}
	mustExec(`insert into memberships(org_id,user_id,role) values (1,$1,'viewer')`, f.dave)
	// alice opted in; dave (a viewer) did not; bob has no access to project 7
	// and carol is in another organisation: both opted in on paper.
	for _, u := range []int64{n.alice, n.bob, n.outsider} {
		mustExec(`insert into user_notification_prefs(user_id,site_id,weekly_digest) values ($1,7,true)`, u)
	}
	mustExec(`insert into user_notification_prefs(user_id,site_id,weekly_digest) values ($1,7,false)`, f.dave)
	return f
}

func TestDigestHandComputed(t *testing.T) {
	f := newDigestFixture(t)
	d, err := f.digester.BuildDigest(context.Background(), 7, f.weekFrom)
	if err != nil {
		t.Fatal(err)
	}
	if d.NewIssues != 3 || d.RegressedIssues != 1 {
		t.Fatalf("new=%d regressed=%d, want 3 and 1", d.NewIssues, d.RegressedIssues)
	}
	if len(d.Top) != 2 || d.Top[0].ID != f.issue || d.Top[0].Events != 120 || d.Top[1].Events != 30 {
		t.Fatalf("top = %+v, want 120 then 30 events", d.Top)
	}
	if !d.HasCrashFree || !d.HasPriorCrashFree || math.Abs(d.CrashFreeUsers-0.975) > 1e-9 || math.Abs(d.PriorCrashFreeUsers-0.95) > 1e-9 {
		t.Fatalf("crash-free users = %v (prior %v), want 0.975 and 0.95", d.CrashFreeUsers, d.PriorCrashFreeUsers)
	}
}

func TestDigestSendsOncePerWeekToOptedInReaders(t *testing.T) {
	f := newDigestFixture(t)
	ctx := context.Background()
	run := func(at string) int {
		t.Helper()
		parsed, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatal(err)
		}
		f.clock.t = parsed
		sent, err := f.digester.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return sent
	}
	// Outside Monday 09:00-09:59 in New York nothing is sent.
	for _, at := range []string{"2026-11-16T13:59:00Z", "2026-11-16T15:00:00Z", "2026-11-17T14:30:00Z"} {
		if n := run(at); n != 0 {
			t.Fatalf("%s: sent %d, want 0 outside the Monday 09:00 hour", at, n)
		}
	}
	if n := run("2026-11-16T14:30:00Z"); n != 1 {
		t.Fatalf("Monday 09:30 EST: sent %d, want 1 (alice only)", n)
	}
	if n := run("2026-11-16T14:45:00Z"); n != 0 {
		t.Fatalf("second run in the same hour sent %d, want 0", n)
	}
	if len(f.sender.sent) != 1 || f.sender.sent[0].To != "alice@example.test" {
		t.Fatalf("mail = %+v, want exactly one to alice", f.sender.sent)
	}
	text := f.sender.sent[0].Text
	for _, want := range []string{"New issues: 3", "Regressed issues: 1", "120 events", "97.5%", "95.0%", "https://stats.example.test/app/org/"} {
		if !strings.Contains(text, want) {
			t.Errorf("digest lacks %q:\n%s", want, text)
		}
	}
	// No user identifiers: the only address anywhere is the recipient, the
	// error title's address is redacted, and no install hash shows up.
	for _, m := range regexp.MustCompile(`[[:alnum:]._+-]+@[[:alnum:].-]+`).FindAllString(f.sender.sent[0].Subject+"\n"+text, -1) {
		if m != "alice@example.test" && !strings.HasPrefix(m, "alice@") {
			t.Errorf("digest carries another address %q:\n%s", m, text)
		}
	}
	if strings.Contains(text, "jane@") || strings.Contains(text, "inst0") {
		t.Fatalf("digest leaks user data:\n%s", text)
	}
	// The next ISO week mails again.
	if n := run("2026-11-23T14:30:00Z"); n != 1 {
		t.Fatalf("next Monday: sent %d, want 1", n)
	}
}

func TestDigestRetriesAfterSendFailureWithoutDuplicates(t *testing.T) {
	f := newDigestFixture(t)
	ctx := context.Background()
	f.clock.t = time.Date(2026, 11, 16, 14, 10, 0, 0, time.UTC)
	f.sender.fail = true
	if _, err := f.digester.Run(ctx); err == nil {
		t.Fatal("send failure not reported")
	}
	f.sender.fail = false
	f.clock.t = f.clock.t.Add(20 * time.Minute)
	if n, err := f.digester.Run(ctx); err != nil || n != 1 {
		t.Fatalf("retry sent %d (%v), want 1", n, err)
	}
	if n, _ := f.digester.Run(ctx); n != 0 {
		t.Fatalf("third run sent %d, want 0", n)
	}
}
