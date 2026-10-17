package invites_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/invites"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

type disabledMail struct{ sent int }

func (m *disabledMail) Send(context.Context, mail.Message) error {
	m.sent++
	return nil
}

func (*disabledMail) Enabled() bool { return false }

type inviteEnv struct {
	service *invites.Invites
	pool    *pgxpool.Pool
	clock   *fakeClock
	owner   authz.Principal
	admin   authz.Principal
}

func newInviteEnv(t *testing.T, sender mail.Sender) inviteEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var ownerID, adminID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','Owner','hash') returning id`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('admin@example.test','Admin','hash') returning id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Test','test') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner'),($1,$3,'admin')`, orgID, ownerID, adminID); err != nil {
		t.Fatal(err)
	}

	data := &store.Store{Pool: pool}
	clock := &fakeClock{now: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	return inviteEnv{
		service: invites.New(data, users.New(data), sender, "https://supermetry.example", invites.WithClock(clock)),
		pool:    pool,
		clock:   clock,
		owner:   authz.Principal{UserID: ownerID, OrgID: orgID, Role: authz.RoleOwner},
		admin:   authz.Principal{UserID: adminID, OrgID: orgID, Role: authz.RoleAdmin},
	}
}

func TestAcceptCreatesRestrictedMembershipAndIsSingleUse(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	invitedSiteIDs := []int32{
		createSite(t, env, "abcdefgh1234"),
		createSite(t, env, "ijklmnop5678"),
	}
	link, err := env.service.Create(ctx, env.owner, "New@Example.Test ", authz.RoleViewer, invitedSiteIDs)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := env.service.Accept(ctx, tokenFromLink(t, link), 0, "New User", "password-1234")
	if err != nil {
		t.Fatal(err)
	}

	var email, role string
	var siteIDs []int32
	if err := env.pool.QueryRow(ctx, `
		select u.email,m.role,m.site_ids
		from users u join memberships m on m.user_id=u.id
		where u.id=$1 and m.org_id=$2`, userID, env.owner.OrgID).Scan(&email, &role, &siteIDs); err != nil {
		t.Fatal(err)
	}
	if email != "new@example.test" || role != "viewer" || len(siteIDs) != 2 || siteIDs[0] != invitedSiteIDs[0] || siteIDs[1] != invitedSiteIDs[1] {
		t.Fatalf("membership = email %q role %q sites %v", email, role, siteIDs)
	}
	if _, err := env.service.Accept(ctx, tokenFromLink(t, link), 0, "Again", "password-1234"); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("second Accept error = %v, want ErrInvalid", err)
	}
}

func TestAcceptRejectsWeakPasswordForNewUser(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	link, err := env.service.Create(ctx, env.owner, "new@example.test", authz.RoleViewer, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := tokenFromLink(t, link)
	if _, err := env.service.Accept(ctx, token, 0, "New User", "short"); !errors.Is(err, platformcrypto.ErrPasswordTooShort) {
		t.Fatalf("Accept() five-character password error = %v, want ErrPasswordTooShort", err)
	}
	if _, err := env.service.Accept(ctx, token, 0, "New User", "sixsix"); err != nil {
		t.Fatalf("Accept() six-character password error = %v", err)
	}
}

func TestAcceptRejectsExpiredAndRevokedInvitations(t *testing.T) {
	tests := []struct {
		name       string
		invalidate func(*testing.T, context.Context, inviteEnv, int64)
	}{
		{
			name: "expired",
			invalidate: func(_ *testing.T, _ context.Context, env inviteEnv, _ int64) {
				env.clock.Advance(7*24*time.Hour + time.Second)
			},
		},
		{
			name: "revoked",
			invalidate: func(t *testing.T, ctx context.Context, env inviteEnv, invitationID int64) {
				if err := env.service.Revoke(ctx, env.owner, invitationID); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newInviteEnv(t, &mail.Fake{})
			ctx := context.Background()
			link, err := env.service.Create(ctx, env.owner, tt.name+"@example.test", authz.RoleAdmin, nil)
			if err != nil {
				t.Fatal(err)
			}
			var invitationID int64
			if err := env.pool.QueryRow(ctx, `select id from invitations where email=$1`, tt.name+"@example.test").Scan(&invitationID); err != nil {
				t.Fatal(err)
			}
			tt.invalidate(t, ctx, env, invitationID)
			if _, err := env.service.Accept(ctx, tokenFromLink(t, link), 0, "User", "password-1234"); !errors.Is(err, invites.ErrInvalid) {
				t.Fatalf("Accept error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestAcceptRejectsExistingMembershipWithoutConsumingInvitation(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	link, err := env.service.Create(ctx, env.owner, "admin@example.test", authz.RoleViewer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Accept(ctx, tokenFromLink(t, link), env.admin.UserID, "", ""); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("Accept error = %v, want ErrInvalid", err)
	}

	var role string
	var siteIDs []int32
	var acceptedAt *time.Time
	if err := env.pool.QueryRow(ctx, `
		select m.role,m.site_ids,i.accepted_at
		from memberships m
		join invitations i on i.org_id=m.org_id and i.email='admin@example.test'
		where m.org_id=$1 and m.user_id=$2`, env.owner.OrgID, env.admin.UserID).Scan(&role, &siteIDs, &acceptedAt); err != nil {
		t.Fatal(err)
	}
	if role != "admin" || siteIDs != nil || acceptedAt != nil {
		t.Fatalf("existing membership = role %q sites %v; accepted_at = %v", role, siteIDs, acceptedAt)
	}
}

func TestAdminCannotInviteOwner(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	_, err := env.service.Create(context.Background(), env.admin, "owner2@example.test", authz.RoleOwner, nil)
	if !errors.Is(err, invites.ErrForbidden) {
		t.Fatalf("Create error = %v, want ErrForbidden", err)
	}
}

func TestCreateRejectsSiteRestrictionsOutsideOrganization(t *testing.T) {
	tests := []struct {
		name    string
		siteIDs func(*testing.T, inviteEnv) []int32
	}{
		{
			name: "site in another organization",
			siteIDs: func(t *testing.T, env inviteEnv) []int32 {
				ctx := context.Background()
				var otherOrgID int64
				if err := env.pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Other','other') returning id`).Scan(&otherOrgID); err != nil {
					t.Fatal(err)
				}
				var siteID int32
				if err := env.pool.QueryRow(ctx, `
					insert into sites(org_id,public_id,name,timezone,retention_class)
					values ($1,'abcdefgh1234','Other','UTC',30) returning id`, otherOrgID).Scan(&siteID); err != nil {
					t.Fatal(err)
				}
				return []int32{siteID}
			},
		},
		{
			name: "unknown site",
			siteIDs: func(_ *testing.T, _ inviteEnv) []int32 {
				return []int32{999999}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newInviteEnv(t, &mail.Fake{})
			_, err := env.service.Create(context.Background(), env.owner, "invitee@example.test", authz.RoleViewer, tt.siteIDs(t, env))
			if !errors.Is(err, invites.ErrInvalid) {
				t.Fatalf("Create error = %v, want ErrInvalid", err)
			}
			var invitations int
			if err := env.pool.QueryRow(context.Background(), `select count(*) from invitations`).Scan(&invitations); err != nil {
				t.Fatal(err)
			}
			if invitations != 0 {
				t.Fatalf("stored invitations = %d, want 0", invitations)
			}
		})
	}
}

func TestCreateSendsOneInvitationContainingReturnedLink(t *testing.T) {
	sender := &mail.Fake{}
	env := newInviteEnv(t, sender)
	link, err := env.service.Create(context.Background(), env.owner, "invitee@example.test", authz.RoleAdmin, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sender.Sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(sender.Sent))
	}
	message := sender.Sent[0]
	if message.To != "invitee@example.test" || !strings.Contains(message.Text, link) {
		t.Fatalf("message = %#v, want recipient and link %q", message, link)
	}
}

func TestCreateReturnsLinkWithoutSendingWhenMailDisabled(t *testing.T) {
	sender := &disabledMail{}
	env := newInviteEnv(t, sender)
	link, err := env.service.Create(context.Background(), env.owner, "invitee@example.test", authz.RoleAdmin, nil)
	if err != nil {
		t.Fatal(err)
	}
	if link == "" || sender.sent != 0 {
		t.Fatalf("Create = link %q, sends %d", link, sender.sent)
	}
}

func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	token := parsed.Query().Get("token")
	if token == "" {
		t.Fatalf("link %q has no token", link)
	}
	return token
}

func createSite(t *testing.T, env inviteEnv, publicID string) int32 {
	t.Helper()
	var siteID int32
	if err := env.pool.QueryRow(context.Background(), `
		insert into sites(org_id,public_id,name,timezone,retention_class)
		values ($1,$2,'Site','UTC',30) returning id`, env.owner.OrgID, publicID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return siteID
}

func TestPreviewDescribesAPendingInvitationWithoutConsumingIt(t *testing.T) {
	env := newInviteEnv(t, &mail.Fake{})
	ctx := context.Background()
	link, err := env.service.Create(ctx, env.owner, "preview@example.test", authz.RoleViewer, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := tokenFromLink(t, link)
	preview, err := env.service.Preview(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if preview.OrgName != "Test" || preview.OrgSlug != "test" || preview.Email != "preview@example.test" || preview.Role != "viewer" || preview.InvitedBy != "Owner" || preview.AccountExists {
		t.Fatalf("preview = %+v", preview)
	}
	if !preview.ExpiresAt.Equal(env.clock.now.Add(7 * 24 * time.Hour)) {
		t.Fatalf("expires_at = %v", preview.ExpiresAt)
	}
	// Previewing leaves the token usable.
	if _, err := env.service.Accept(ctx, token, 0, "Preview", "password-1234"); err != nil {
		t.Fatalf("accept after preview: %v", err)
	}
	if _, err := env.service.Preview(ctx, token); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("preview of an accepted invitation err = %v, want ErrInvalid", err)
	}
	if _, err := env.service.Preview(ctx, "not-a-token"); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("preview of an unknown token err = %v", err)
	}
	existing, err := env.service.Create(ctx, env.owner, "admin@example.test", authz.RoleViewer, nil)
	if err == nil {
		got, err := env.service.Preview(ctx, tokenFromLink(t, existing))
		if err != nil || !got.AccountExists {
			t.Fatalf("existing account preview = %+v, %v", got, err)
		}
	}
	env.clock.Advance(8 * 24 * time.Hour)
	expired, err := env.service.Create(ctx, env.owner, "late@example.test", authz.RoleViewer, nil)
	if err != nil {
		t.Fatal(err)
	}
	env.clock.Advance(8 * 24 * time.Hour)
	if _, err := env.service.Preview(ctx, tokenFromLink(t, expired)); !errors.Is(err, invites.ErrInvalid) {
		t.Fatalf("expired preview err = %v", err)
	}
}
