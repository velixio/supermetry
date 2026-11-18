package appissues_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"supermetry/internal/appissues"
)

func TestAssignIfUnassignedNeverReassigns(t *testing.T) {
	n := newNotifyFixture(t)
	ctx := context.Background()

	done, err := n.svc.AssignIfUnassigned(ctx, 7, n.issue, n.alice)
	if err != nil || !done {
		t.Fatalf("first assignment = %v, %v, want true", done, err)
	}
	// The issue is assigned now: a second rule run must leave it alone.
	done, err = n.svc.AssignIfUnassigned(ctx, 7, n.issue, n.dave)
	if err != nil || done {
		t.Fatalf("second assignment = %v, %v, want false", done, err)
	}
	var assignee int64
	var rows int
	if err := n.pool.QueryRow(ctx, `select assignee_user_id from app_issues where id=$1`, n.issue).Scan(&assignee); err != nil || assignee != n.alice {
		t.Fatalf("assignee = %d (%v), want alice", assignee, err)
	}
	if err := n.pool.QueryRow(ctx, `select count(*) from app_issue_activity where issue_id=$1 and kind='assigned'`, n.issue).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("assigned activity rows = %d (%v), want 1", rows, err)
	}
}

func TestAssignIfUnassignedChecksMembership(t *testing.T) {
	n := newNotifyFixture(t)
	var outsider int64
	if err := n.pool.QueryRow(context.Background(), `insert into users(email,name,password_hash) values ('zed@example.test','Zed','h') returning id`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	if _, err := n.svc.AssignIfUnassigned(context.Background(), 7, n.issue, outsider); !errors.Is(err, appissues.ErrInvalidAssignee) {
		t.Fatalf("err = %v, want ErrInvalidAssignee", err)
	}
}

func TestMailRuleOwnerSendsOnePlainMailToReaders(t *testing.T) {
	n := newNotifyFixture(t)
	ctx := context.Background()
	if err := n.notifier.MailRuleOwner(ctx, 7, n.issue, n.alice, "Prod crashes"); err != nil {
		t.Fatal(err)
	}
	if len(n.sender.sent) != 1 || n.sender.sent[0].To != "alice@example.test" {
		t.Fatalf("sent = %v, want one mail to alice", n.recipients())
	}
	m := n.sender.sent[0]
	if !strings.Contains(m.Subject, "Alert") || !strings.Contains(m.Text, `"Prod crashes"`) || !strings.Contains(m.Text, "https://stats.example.test/app/org/") {
		t.Fatalf("mail = %q / %q", m.Subject, m.Text)
	}
	var outsider int64
	if err := n.pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('zed@example.test','Zed','h') returning id`).Scan(&outsider); err != nil {
		t.Fatal(err)
	}
	if err := n.notifier.MailRuleOwner(ctx, 7, n.issue, outsider, "Prod crashes"); err != nil || len(n.sender.sent) != 1 {
		t.Fatalf("a user without access was mailed: %v (%v)", n.recipients(), err)
	}
}
