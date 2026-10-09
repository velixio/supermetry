package db_test

import (
	"context"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestTOTPSecurityMigrationAddsPendingEnrollmentAndReplayState(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into users(email,name,password_hash,totp_secret_enc,totp_enabled,totp_recovery)
		values ('enabled@example.test','Enabled','hash',decode('aa','hex'),true,array[decode('bb','hex')])`); err != nil {
		t.Fatal(err)
	}
	var pendingSecretNil, pendingRecoveryEmpty, lastStepNil bool
	if err := pool.QueryRow(ctx, `
		select totp_pending_secret_enc is null,
		       cardinality(totp_pending_recovery)=0,
		       totp_last_step is null
		from users where email='enabled@example.test'`).Scan(&pendingSecretNil, &pendingRecoveryEmpty, &lastStepNil); err != nil {
		t.Fatal(err)
	}
	if !pendingSecretNil || !pendingRecoveryEmpty || !lastStepNil {
		t.Fatalf("migration changed active enrollment: pending_secret_nil=%v pending_recovery_empty=%v last_step_nil=%v", pendingSecretNil, pendingRecoveryEmpty, lastStepNil)
	}
}
