package auth_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

type accountEnv struct {
	a      *auth.Auth
	store  *store.Store
	users  *users.Users
	clock  *fakeClock
	mailer *mail.Fake
}

func newAccountEnv(t *testing.T) accountEnv {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	clock := newFakeClock()
	sender := &mail.Fake{}
	return accountEnv{
		a:     auth.New(data, sender, box, "https://supermetry.example", auth.WithClock(clock)),
		store: data, users: users.New(data), clock: clock, mailer: sender,
	}
}

func TestUpdateName(t *testing.T) {
	env := newAccountEnv(t)
	userID := createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "u@x.io", "pw-123456789")
	if err := env.a.UpdateName(ctx, userID, "  Updated User  "); err != nil {
		t.Fatal(err)
	}
	user, err := env.store.UserByID(ctx, userID)
	if err != nil || user.Name != "Updated User" {
		t.Fatalf("user = %+v, %v", user, err)
	}
	if err := env.a.UpdateName(ctx, userID, "  "); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("empty name = %v", err)
	}
}

func TestEmailChangeRequiresSMTPAndConfirmsOnce(t *testing.T) {
	env := newAccountEnv(t)
	userID := createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "old@example.test", "pw-123456789")
	disabled := newAuth(t)
	disabledID := createUser(t, disabled, "disabled@example.test", "pw-123456789")
	if err := disabled.a.StartEmailChange(ctx, disabledID, "new@example.test", "pw-123456789"); !errors.Is(err, auth.ErrSMTPRequired) {
		t.Fatalf("disabled SMTP = %v", err)
	}
	if err := env.a.StartEmailChange(ctx, userID, "new@example.test", "wrong"); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("wrong password = %v", err)
	}
	if err := env.a.StartEmailChange(ctx, userID, "new@example.test", "pw-123456789"); err != nil {
		t.Fatal(err)
	}
	if len(env.mailer.Sent) != 1 || env.mailer.Sent[0].To != "new@example.test" {
		t.Fatalf("confirmation messages = %+v", env.mailer.Sent)
	}
	token := messageToken(t, env.mailer.Sent[0].Text)
	if err := env.a.ConfirmEmailChange(ctx, userID, token); err != nil {
		t.Fatal(err)
	}
	if err := env.a.ConfirmEmailChange(ctx, userID, token); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("token reused = %v", err)
	}
	user, err := env.store.UserByID(ctx, userID)
	if err != nil || user.Email != "new@example.test" {
		t.Fatalf("user = %+v, %v", user, err)
	}
	if len(env.mailer.Sent) != 2 || env.mailer.Sent[1].To != "old@example.test" {
		t.Fatalf("notification messages = %+v", env.mailer.Sent)
	}
}

func TestEmailChangeTokenExpires(t *testing.T) {
	env := newAccountEnv(t)
	userID := createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "old@example.test", "pw-123456789")
	if err := env.a.StartEmailChange(ctx, userID, "new@example.test", "pw-123456789"); err != nil {
		t.Fatal(err)
	}
	env.clock.Advance(time.Hour + time.Second)
	if err := env.a.ConfirmEmailChange(ctx, userID, messageToken(t, env.mailer.Sent[0].Text)); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("expired token = %v", err)
	}
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	env := newAccountEnv(t)
	userID := createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "u@x.io", "old-password-1")
	current, err := env.a.Login(ctx, "u@x.io", "old-password-1", "", "current")
	if err != nil {
		t.Fatal(err)
	}
	other, err := env.a.Login(ctx, "u@x.io", "old-password-1", "", "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.a.ChangePassword(ctx, userID, "wrong", "new-password-1", current); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("wrong password = %v", err)
	}
	if err := env.a.ChangePassword(ctx, userID, "old-password-1", "new-password-1", current); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.a.Session(ctx, current); !ok {
		t.Fatal("current session revoked")
	}
	if _, ok := env.a.Session(ctx, other); ok {
		t.Fatal("other session remains")
	}
	if _, err := env.a.Login(ctx, "u@x.io", "new-password-1", "", "new"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAccountRequiresSecondFactorAndRefusesLastOwner(t *testing.T) {
	env := newAccountEnv(t)
	token, err := env.a.Signup(auth.WithClientKey(ctx, "delete-client"), "owner@example.test", "Owner", "pw-123456789", "Acme", "current")
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.store.UserByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	otpauthURL, _, err := env.a.EnrollTOTP(ctx, user.ID, "pw-123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	code := codeFor(t, otpauthURL, env.clock.Now())
	if err := env.a.ConfirmTOTP(ctx, user.ID, code); err != nil {
		t.Fatal(err)
	}
	if err := env.a.DeleteAccount(ctx, user.ID, "pw-123456789", code); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("replayed confirmation TOTP = %v", err)
	}
	if err := env.a.DeleteAccount(ctx, user.ID, "pw-123456789", ""); !errors.Is(err, auth.ErrTOTPRequired) {
		t.Fatalf("missing TOTP = %v", err)
	}
	env.clock.Advance(30 * time.Second)
	code = codeFor(t, otpauthURL, env.clock.Now())
	if err := env.a.DeleteAccount(ctx, user.ID, "pw-123456789", code); !errors.Is(err, auth.ErrLastOwner) {
		t.Fatalf("last owner = %v", err)
	}
	var orgID int64
	if err := env.store.Pool.QueryRow(ctx, `select org_id from memberships where user_id=$1`, user.ID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	secondID := createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "second@example.test", "pw-123456789")
	if _, err := env.store.Pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, secondID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.Pool.Exec(ctx, `insert into invitations(org_id,email,role,token_hash,invited_by,expires_at) values ($1,'invitee@example.test','viewer',$2,$3,$4)`, orgID, make([]byte, 32), user.ID, env.clock.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	env.clock.Advance(30 * time.Second)
	code = codeFor(t, otpauthURL, env.clock.Now())
	if err := env.a.DeleteAccount(ctx, user.ID, "pw-123456789", code); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.a.Session(ctx, token); ok {
		t.Fatal("deleted account session remains")
	}
	var deletedAt *time.Time
	var invitations, memberships int
	if err := env.store.Pool.QueryRow(ctx, `select deleted_at from users where id=$1`, user.ID).Scan(&deletedAt); err != nil || deletedAt == nil {
		t.Fatalf("deleted_at = %v, %v", deletedAt, err)
	}
	_ = env.store.Pool.QueryRow(ctx, `select count(*) from invitations where invited_by=$1`, user.ID).Scan(&invitations)
	_ = env.store.Pool.QueryRow(ctx, `select count(*) from memberships where user_id=$1`, user.ID).Scan(&memberships)
	if invitations != 1 || memberships != 0 {
		t.Fatalf("invitations=%d memberships=%d", invitations, memberships)
	}
}

func messageToken(t *testing.T, text string) string {
	t.Helper()
	for _, field := range strings.Fields(text) {
		parsed, err := url.Parse(field)
		if err == nil && parsed.Query().Get("token") != "" {
			return parsed.Query().Get("token")
		}
	}
	t.Fatalf("message has no token: %q", text)
	return ""
}

func TestEmailChangeToTakenEmailIsSilent(t *testing.T) {
	env := newAccountEnv(t)
	userID := createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "old@example.test", "pw-123456789")
	createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "taken@example.test", "pw-123456789")
	if err := env.a.StartEmailChange(ctx, userID, "TAKEN@example.test", "pw-123456789"); err != nil {
		t.Fatalf("taken email must look like success, got %v", err)
	}
	var pending int
	_ = env.store.Pool.QueryRow(ctx, `select count(*) from email_changes where user_id=$1`, userID).Scan(&pending)
	if len(env.mailer.Sent) != 0 || pending != 0 {
		t.Fatalf("sent=%d pending=%d", len(env.mailer.Sent), pending)
	}
}

func TestEmailChangeConfirmationBindsToRequestingUser(t *testing.T) {
	env := newAccountEnv(t)
	fixture := authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}
	userID := createUser(t, fixture, "old@example.test", "pw-123456789")
	otherID := createUser(t, fixture, "other@example.test", "pw-123456789")
	if err := env.a.StartEmailChange(ctx, userID, "new@example.test", "pw-123456789"); err != nil {
		t.Fatal(err)
	}
	token := messageToken(t, env.mailer.Sent[0].Text)
	if err := env.a.ConfirmEmailChange(ctx, otherID, token); !errors.Is(err, auth.ErrInvalid) {
		t.Fatalf("other user confirm = %v", err)
	}
	if err := env.a.ConfirmEmailChange(ctx, userID, token); err != nil {
		t.Fatalf("token must survive a foreign attempt: %v", err)
	}
}

func TestAccountChecksAreRateLimited(t *testing.T) {
	env := newAccountEnv(t)
	userID := createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "u@x.io", "pw-123456789")
	session, err := env.a.Login(ctx, "u@x.io", "pw-123456789", "", "current")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_ = env.a.ChangePassword(ctx, userID, "wrong", "new-password-1", session)
	}
	if err := env.a.ChangePassword(ctx, userID, "pw-123456789", "new-password-1", session); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("password change = %v", err)
	}
	if err := env.a.StartEmailChange(ctx, userID, "new@example.test", "pw-123456789"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("email change = %v", err)
	}
	if err := env.a.DeleteAccount(ctx, userID, "pw-123456789", ""); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("delete = %v", err)
	}
}

func TestAccountChangesAreAuditedAndKeysSurviveDeletion(t *testing.T) {
	env := newAccountEnv(t)
	session, err := env.a.Signup(auth.WithClientKey(ctx, "audit-client"), "owner@example.test", "Owner", "pw-123456789", "Acme", "current")
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.store.UserByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := env.store.Pool.QueryRow(ctx, `select org_id from memberships where user_id=$1`, user.ID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := env.a.ChangePassword(ctx, user.ID, "pw-123456789", "new-password-1", session); err != nil {
		t.Fatal(err)
	}
	if err := env.a.StartEmailChange(ctx, user.ID, "new@example.test", "new-password-1"); err != nil {
		t.Fatal(err)
	}
	if err := env.a.ConfirmEmailChange(ctx, user.ID, messageToken(t, env.mailer.Sent[0].Text)); err != nil {
		t.Fatal(err)
	}
	secondID := createUser(t, authEnv{a: env.a, users: env.users, clock: env.clock, store: env.store}, "second@example.test", "pw-123456789")
	if _, err := env.store.Pool.Exec(ctx, `insert into memberships(org_id,user_id,role) values ($1,$2,'owner')`, orgID, secondID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.Pool.Exec(ctx, `insert into api_keys(org_id,name,prefix,hash,scopes,created_by) values ($1,'ci','abcdefgh',$2,'{read}',$3)`, orgID, make([]byte, 32), user.ID); err != nil {
		t.Fatal(err)
	}
	if err := env.a.DeleteAccount(ctx, user.ID, "new-password-1", ""); err != nil {
		t.Fatal(err)
	}
	var actions []string
	rows, err := env.store.Pool.Query(ctx, `select action from audit_log where org_id=$1 and actor_user_id=$2 order by id`, orgID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var action string
		_ = rows.Scan(&action)
		actions = append(actions, action)
	}
	rows.Close()
	if strings.Join(actions, ",") != "account.created,account.password_changed,account.email_changed,account.deleted" {
		t.Fatalf("audit actions = %v", actions)
	}
	var revoked *time.Time
	if err := env.store.Pool.QueryRow(ctx, `select revoked_at from api_keys where created_by=$1`, user.ID).Scan(&revoked); err != nil || revoked != nil {
		t.Fatalf("org API key revoked_at=%v err=%v", revoked, err)
	}
}
