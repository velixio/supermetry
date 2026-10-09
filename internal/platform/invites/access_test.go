package invites_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/invites"
	"supermetry/internal/platform/mail"
)

func TestInvitationAccessRoundTripPreservesBillingOnlyScope(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	caps := []string{"profiles.read", "replay.read", "data.export"}
	link, err := env.service.Create(ctx, env.owner, "billing@example.test", authz.RoleViewer, []int32{}, invites.Access{Capabilities: caps, BillingAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	id, err := env.service.Accept(ctx, tokenFromLink(t, link), 0, "Billing", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	var gotCaps []string
	var gotSites []int32
	var billing bool
	if err := env.pool.QueryRow(ctx, `select site_ids,capabilities,billing_admin from memberships where org_id=$1 and user_id=$2`, env.owner.OrgID, id).Scan(&gotSites, &gotCaps, &billing); err != nil {
		t.Fatal(err)
	}
	if gotSites == nil || len(gotSites) != 0 || !reflect.DeepEqual(gotCaps, caps) || !billing {
		t.Fatalf("roundtrip: sites=%v nil=%t capabilities=%v billing=%t", gotSites, gotSites == nil, gotCaps, billing)
	}
	var hasToken bool
	if err := env.pool.QueryRow(ctx, `select exists(select 1 from audit_log where metadata::text like '%' || $1 || '%')`, tokenFromLink(t, link)).Scan(&hasToken); err != nil {
		t.Fatal(err)
	}
	if hasToken {
		t.Fatal("invitation token leaked into audit")
	}
}

func TestInvitationCannotGrantUnknownOrAdminBillingAccess(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	if _, err := env.service.Create(ctx, env.owner, "unknown@example.test", authz.RoleViewer, nil, invites.Access{Capabilities: []string{"billing.manage"}}); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("unknown capability = %v", err)
	}
	if _, err := env.service.Create(ctx, env.admin, "billing@example.test", authz.RoleViewer, nil, invites.Access{BillingAdmin: true}); !errors.Is(err, invites.ErrForbidden) {
		t.Fatalf("admin billing invitation = %v", err)
	}
	if _, err := env.service.Create(ctx, env.owner, "foreign@example.test", authz.RoleAdmin, []int32{999999}); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("unrestricted preset silently ignored invalid sites: %v", err)
	}
}

func TestInvitationManagerAuthorityIsRevalidated(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	link, err := env.service.Create(ctx, env.admin, "stale@example.test", authz.RoleViewer, nil, invites.Access{Capabilities: []string{"replay.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `update memberships set role='viewer' where org_id=$1 and user_id=$2`, env.admin.OrgID, env.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Create(ctx, env.admin, "new@example.test", authz.RoleViewer, nil); !errors.Is(err, invites.ErrForbidden) {
		t.Fatalf("stale creation = %v", err)
	}
	var inviteID int64
	if err := env.pool.QueryRow(ctx, `select id from invitations where email='stale@example.test'`).Scan(&inviteID); err != nil {
		t.Fatal(err)
	}
	if err := env.service.Revoke(ctx, env.admin, inviteID); !errors.Is(err, invites.ErrForbidden) {
		t.Fatalf("stale revocation = %v", err)
	}
	if _, err := env.service.Accept(ctx, tokenFromLink(t, link), 0, "Stale", "password-1234"); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("stale inviter acceptance = %v", err)
	}
	var accepted bool
	if err := env.pool.QueryRow(ctx, `select accepted_at is not null from invitations where id=$1`, inviteID).Scan(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("denied acceptance consumed invitation")
	}
}

func TestInvitationBillingGrantRequiresCurrentOwnerAtAcceptance(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	link, err := env.service.Create(ctx, env.owner, "billing@example.test", authz.RoleViewer, []int32{}, invites.Access{BillingAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `update memberships set role='admin' where org_id=$1 and user_id=$2`, env.owner.OrgID, env.owner.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Accept(ctx, tokenFromLink(t, link), 0, "Billing", "password-1234"); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("demoted owner billing invitation = %v", err)
	}
}

func TestInvitationRejectsWrongEmailAndDeletedSiteWithoutConsuming(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	site := createSite(t, env, "invitecheck1")
	link, err := env.service.Create(ctx, env.owner, "correct@example.test", authz.RoleViewer, []int32{site})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Accept(ctx, tokenFromLink(t, link), env.admin.UserID, "", ""); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("wrong email = %v", err)
	}
	if _, err := env.pool.Exec(ctx, `update sites set deleted_at=now() where id=$1`, site); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Accept(ctx, tokenFromLink(t, link), 0, "Correct", "password-1234"); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("deleted site invitation = %v", err)
	}
}

func TestInvitationConcurrentAcceptanceIsSingleUse(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	link, err := env.service.Create(ctx, env.owner, "single@example.test", authz.RoleViewer, nil, invites.Access{Capabilities: []string{"profiles.read"}})
	if err != nil {
		t.Fatal(err)
	}
	token := tokenFromLink(t, link)
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			_, err := env.service.Accept(ctx, token, 0, "Single", "password-1234")
			results <- err
		}()
	}
	close(start)
	success, invalid := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, invites.ErrInvalid) {
			invalid++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || invalid != 1 {
		t.Fatalf("concurrent acceptance: succeeded=%d invalid=%d", success, invalid)
	}
}

func TestInvitationAuditFailureRollsBackAcceptanceAndGrants(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	link, err := env.service.Create(ctx, env.owner, "rollback@example.test", authz.RoleViewer, []int32{}, invites.Access{Capabilities: []string{"data.export"}, BillingAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `create function reject_invite_audit() returns trigger language plpgsql as $$ begin raise exception 'audit unavailable'; end $$; create trigger reject_invite_audit before insert on audit_log for each row execute function reject_invite_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Accept(ctx, tokenFromLink(t, link), 0, "Rollback", "password-1234"); err == nil {
		t.Fatal("acceptance succeeded without audit")
	}
	var accepted bool
	if err := env.pool.QueryRow(ctx, `select accepted_at is not null from invitations where email='rollback@example.test'`).Scan(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("failed audit consumed invitation")
	}
	var users int
	if err := env.pool.QueryRow(ctx, `select count(*) from users where email='rollback@example.test'`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Fatal("failed acceptance persisted user or grants")
	}
}
