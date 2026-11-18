package alerts

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"supermetry/internal/appissues"
)

type ownerMail struct {
	site          int32
	issue, userID int64
	rule          string
}

type fakeOwnerMailer struct{ sent []ownerMail }

func (m *fakeOwnerMailer) MailRuleOwner(_ context.Context, site int32, issue, user int64, rule string) error {
	m.sent = append(m.sent, ownerMail{site, issue, user, rule})
	return nil
}

func (f *ruleFixture) member(t *testing.T, email string) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), `insert into users(email,name,password_hash) values ($1,'Member','h') returning id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `insert into memberships(org_id,user_id,role) values ($1,$2,'editor')`, f.principal.OrgID, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestActionAssignOnce(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	first, second := f.principal.UserID, f.member(t, "second@example.test")
	f.newRule(t, "Assign", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: []Action{{Type: ActionAssign, UserID: second}}})
	assigned := f.seedIssue(t, "crash", "error", "1.0.0", first)
	free := f.seedIssue(t, "crash", "error", "1.0.0", 0)
	// The candidate is stale (it still says unassigned): the service guard must hold.
	f.source.newIssues = []AppIssue{
		{ID: assigned, Title: "owned", Kind: "crash", FirstSeen: f.now},
		{ID: free, Title: "free", Kind: "crash", FirstSeen: f.now},
	}
	evaluator := f.evaluator().WithActions(appissues.New(f.pool, func() time.Time { return f.now }), nil)
	if err := evaluator.Run(ctx); err != nil {
		t.Fatal(err)
	}
	owner := func(issue int64) int64 {
		var id int64
		if err := f.pool.QueryRow(ctx, `select coalesce(assignee_user_id,0) from app_issues where id=$1`, issue).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if got := owner(assigned); got != first {
		t.Errorf("assigned issue owner = %d, want %d (never re-assigned)", got, first)
	}
	if got := owner(free); got != second {
		t.Errorf("free issue owner = %d, want %d", got, second)
	}
	var onAssigned, onFree int
	if err := f.pool.QueryRow(ctx, `select count(*) filter (where issue_id=$1), count(*) filter (where issue_id=$2) from app_issue_activity where kind='assigned'`, assigned, free).Scan(&onAssigned, &onFree); err != nil {
		t.Fatal(err)
	}
	if onAssigned != 0 || onFree != 1 {
		t.Errorf("assigned activity rows = %d on the owned issue and %d on the free one, want 0 and 1", onAssigned, onFree)
	}
}

func TestRuleAssignRequiresAMemberOfTheOrganisation(t *testing.T) {
	f := newRuleFixture(t)
	var outsider int64
	if err := f.pool.QueryRow(context.Background(), `insert into users(email,name,password_hash) values ('outsider@example.test','O','h') returning id`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	rule := Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: []Action{{Type: ActionAssign, UserID: outsider}}}
	_, err := f.service.CreateAlert(context.Background(), f.principal, f.siteID, AlertInput{Kind: AlertRule, Name: "x", Rule: &rule})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for a user outside the organisation", err)
	}
	rule.Actions = []Action{{Type: ActionNotifyOwner, FallbackChannelIDs: []int64{987654}}}
	if _, err := f.service.CreateAlert(context.Background(), f.principal, f.siteID, AlertInput{Kind: AlertRule, Name: "x", Rule: &rule}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for an unknown fallback channel", err)
	}
}

func TestActionNotifyOwnerFallback(t *testing.T) {
	f := newRuleFixture(t)
	ctx := context.Background()
	fallback, err := f.service.CreateChannel(ctx, f.principal, ChannelInput{Kind: ChannelEmail, Name: "On call", Recipients: []string{"oncall@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	f.newRule(t, "Owner", Rule{Trigger: Trigger{Type: TriggerIssue, Issue: &IssueTrigger{On: []string{"new"}}}, Actions: []Action{{Type: ActionNotifyOwner, FallbackChannelIDs: []int64{fallback.ID}}}})
	mailer := &fakeOwnerMailer{}

	// Unassigned: the fallback channel gets it, nobody is mailed.
	f.source.newIssues = []AppIssue{{ID: 5, Title: "orphan", Kind: "crash", FirstSeen: f.now}}
	if err := f.evaluator().WithActions(nil, mailer).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.deliverer.channelIDs, []int64{fallback.ID}) || len(mailer.sent) != 0 {
		t.Fatalf("unassigned: channels %v, mails %v, want the fallback only", f.deliverer.channelIDs, mailer.sent)
	}

	// Assigned: one mail to the assignee, no channel delivery.
	f.deliverer.channelIDs = nil
	f.source.newIssues = []AppIssue{{ID: 6, Title: "owned", Kind: "crash", FirstSeen: f.now, AssigneeUserID: f.principal.UserID}}
	if err := f.evaluator().WithActions(nil, mailer).Run(ctx); err != nil {
		t.Fatal(err)
	}
	want := []ownerMail{{f.siteID, 6, f.principal.UserID, "Owner"}}
	if len(f.deliverer.channelIDs) != 0 || !reflect.DeepEqual(mailer.sent, want) {
		t.Fatalf("assigned: channels %v, mails %v, want one mail %v and no channels", f.deliverer.channelIDs, mailer.sent, want)
	}

	// Another run sends nothing more for either key.
	if err := f.evaluator().WithActions(nil, mailer).Run(ctx); err != nil || len(mailer.sent) != 1 || len(f.deliverer.channelIDs) != 0 {
		t.Fatalf("repeat run: %v, mails %v, channels %v", err, mailer.sent, f.deliverer.channelIDs)
	}
}
