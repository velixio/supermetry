package auth_test

import (
	"strings"
	"testing"

	"supermetry/internal/platform/auth"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/users"
)

// An account that has never signed in asks for its first password, so a
// "send the link again" or forgot-password request must not read as a reset.
func TestStartResetSendsSetupEmailUntilFirstSignIn(t *testing.T) {
	env := newAuth(t)
	fake := &mail.Fake{}
	a := auth.New(env.store, fake, nil, "https://supermetry.example", auth.WithClock(env.clock))
	if _, err := users.New(env.store).Create(ctx, "new@example.test", "New", "pw-123456789"); err != nil {
		t.Fatal(err)
	}

	if _, err := a.StartReset(auth.WithClientKey(ctx, "c1"), "new@example.test"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Sent) != 1 || fake.Sent[0].Subject != "Welcome to Supermetry: set your password" || !strings.Contains(fake.Sent[0].Text, "?setup=1") {
		t.Fatalf("never-signed-in reset sent %+v", fake.Sent)
	}

	if _, err := a.Login(ctx, "new@example.test", "pw-123456789", "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartReset(auth.WithClientKey(ctx, "c2"), "new@example.test"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Sent) != 2 || fake.Sent[1].Subject != "Reset your Supermetry password" || strings.Contains(fake.Sent[1].Text, "setup=1") {
		t.Fatalf("signed-in reset sent %+v", fake.Sent[1:])
	}
}
