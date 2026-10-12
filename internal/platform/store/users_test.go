package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestPendingTOTPEnrollmentPreservesActiveCredentialsUntilActivation(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,totp_secret_enc,totp_enabled,totp_recovery)
		values ('user@example.test','User','hash',$1,true,$2) returning id`, []byte("active-secret"), [][]byte{[]byte("active-recovery")}).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	if err := data.SetPendingTOTPEnrollment(ctx, userID, []byte("pending-secret"), [][]byte{[]byte("pending-recovery")}); err != nil {
		t.Fatal(err)
	}
	user, err := data.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !user.TOTPEnabled || string(user.TOTPSecretEnc) != "active-secret" || string(user.TOTPRecovery[0]) != "active-recovery" {
		t.Fatalf("active credentials changed during enrollment: %+v", user)
	}
	if string(user.TOTPPendingSecretEnc) != "pending-secret" || string(user.TOTPPendingRecovery[0]) != "pending-recovery" {
		t.Fatalf("pending credentials = %+v", user)
	}

	if err := data.Tx(ctx, func(tx pgx.Tx) error {
		activated, err := store.ActivatePendingTOTP(ctx, tx, userID, 42, []byte("replaced-secret"))
		if err != nil {
			return err
		}
		if activated {
			t.Fatal("activated an enrollment whose pending secret was replaced")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := data.Tx(ctx, func(tx pgx.Tx) error {
		activated, err := store.ActivatePendingTOTP(ctx, tx, userID, 42, []byte("pending-secret"))
		if err != nil {
			return err
		}
		if !activated {
			t.Fatal("pending enrollment was not activated")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	user, err = data.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !user.TOTPEnabled || string(user.TOTPSecretEnc) != "pending-secret" || string(user.TOTPRecovery[0]) != "pending-recovery" || user.TOTPLastStep == nil || *user.TOTPLastStep != 42 {
		t.Fatalf("activated credentials = %+v", user)
	}
	if len(user.TOTPPendingSecretEnc) != 0 || len(user.TOTPPendingRecovery) != 0 {
		t.Fatalf("pending credentials remain: %+v", user)
	}
}

func TestAcceptTOTPStepRejectsReplayAndOlderWindowCodes(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,totp_secret_enc,totp_enabled)
		values ('user@example.test','User','hash',$1,true) returning id`, []byte("secret")).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		step int64
		want bool
	}{
		{name: "first", step: 100, want: true},
		{name: "same step replay", step: 100, want: false},
		{name: "older skew step", step: 99, want: false},
		{name: "new step", step: 101, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted, err := store.AcceptTOTPStep(ctx, pool, userID, tc.step)
			if err != nil {
				t.Fatal(err)
			}
			if accepted != tc.want {
				t.Fatalf("accepted = %v, want %v", accepted, tc.want)
			}
		})
	}
}

func TestUserPreferencesRoundTrip(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('preferences@example.test','User','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	siteID := int32(42)
	want := store.UserPreferences{LandingPage: "overview", SitesView: "table", LastSiteID: &siteID}
	if err := data.UpdateUserProfile(ctx, userID, nil, &want); err != nil {
		t.Fatal(err)
	}
	got, err := data.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Preferences.LandingPage != want.LandingPage || got.Preferences.SitesView != want.SitesView || got.Preferences.LastSiteID == nil || *got.Preferences.LastSiteID != siteID {
		t.Fatalf("preferences=%+v want=%+v", got.Preferences, want)
	}
}
