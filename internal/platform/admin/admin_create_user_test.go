package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
)

func TestAdminCreateUserWithOrg(t *testing.T) {
	service, data, actorID, _ := newFixture(t)
	ctx := context.Background()
	fake := &mail.Fake{}
	authService := platformauth.New(data, fake, nil, "https://app.example.test")
	service.SetResetStarter(authService.StartReset)

	user, err := service.CreateUser(ctx, actorID, CreateUserInput{Email: " New@X.test ", Name: "New Person", OrgName: "Newco", SendReset: true})
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "new@x.test" || user.Name != "New Person" || user.InstanceAdmin || user.Disabled {
		t.Fatalf("user = %+v", user)
	}
	if len(user.Memberships) != 1 || user.Memberships[0].OrgName != "Newco" || user.Memberships[0].Role != "owner" {
		t.Fatalf("memberships = %+v", user.Memberships)
	}
	if len(fake.Sent) != 1 || fake.Sent[0].To != "new@x.test" || !strings.Contains(fake.Sent[0].Text, "/password-reset?token=") {
		t.Fatalf("sent = %+v", fake.Sent)
	}
	var actor int64
	var action string
	if err := data.Pool.QueryRow(ctx, `select actor_user_id,action from audit_log where action='instance.user.created' and target_id=$1`, user.ID).Scan(&actor, &action); err != nil || actor != actorID {
		t.Fatalf("audit actor=%d err=%v", actor, err)
	}
}

func TestAdminCreateUserNoOrg(t *testing.T) {
	service, _, actorID, _ := newFixture(t)
	user, err := service.CreateUser(context.Background(), actorID, CreateUserInput{Email: "solo@x.test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(user.Memberships) != 0 || user.Name != "solo" {
		t.Fatalf("user = %+v", user)
	}
}

func TestAdminCreateUserSendResetWithoutStarter(t *testing.T) {
	service, _, actorID, _ := newFixture(t)
	if _, err := service.CreateUser(context.Background(), actorID, CreateUserInput{Email: "a@x.test", SendReset: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAdminCreateUserDuplicateEmail(t *testing.T) {
	service, data, actorID, _ := newFixture(t)
	ctx := context.Background()
	if _, err := service.CreateUser(ctx, actorID, CreateUserInput{Email: "a@x.test"}); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := data.Pool.QueryRow(ctx, `select password_hash from users where email='a@x.test'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"A@x.test", "member@example.test"} {
		_, err := service.CreateUser(ctx, actorID, CreateUserInput{Email: email, OrgName: "Dup"})
		if !errors.Is(err, ErrEmailTaken) {
			t.Fatalf("%s error = %v, want ErrEmailTaken", email, err)
		}
	}
	var after string
	var orgs int
	if err := data.Pool.QueryRow(ctx, `select password_hash from users where email='a@x.test'`).Scan(&after); err != nil || after != before {
		t.Fatalf("hash changed err=%v", err)
	}
	if err := data.Pool.QueryRow(ctx, `select count(*) from orgs where name='Dup'`).Scan(&orgs); err != nil || orgs != 0 {
		t.Fatalf("orphan orgs=%d err=%v", orgs, err)
	}
}

func TestAdminCreateUserReservedSystemEmail(t *testing.T) {
	service, data, actorID, _ := newFixture(t)
	ctx := context.Background()
	if id, err := store.AdminServiceUserID(ctx, data.Pool); err != nil || id == 0 {
		t.Fatalf("system user id=%d err=%v", id, err)
	}
	if _, err := service.CreateUser(ctx, actorID, CreateUserInput{Email: strings.ToUpper(store.AdminServiceEmail)}); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("error = %v, want ErrEmailTaken", err)
	}
}

func TestAdminCreateUserInvalidAndForbidden(t *testing.T) {
	service, _, actorID, memberID := newFixture(t)
	ctx := context.Background()
	for _, in := range []CreateUserInput{{Email: ""}, {Email: "nope"}, {Email: "Bob <b@x.test>"}, {Email: "a@x.test", Name: strings.Repeat("n", 201)}, {Email: "a@x.test", OrgName: strings.Repeat("o", 101)}} {
		if _, err := service.CreateUser(ctx, actorID, in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v error = %v, want ErrInvalid", in, err)
		}
	}
	if _, err := service.CreateUser(ctx, memberID, CreateUserInput{Email: "a@x.test"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin error = %v", err)
	}
}
