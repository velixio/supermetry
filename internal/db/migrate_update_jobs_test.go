package db_test

import (
	"context"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestUpdateJobsMigrationEnforcesOneActiveJob(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var userID int64
	if err := pool.QueryRow(ctx, `
		insert into users(email,name,password_hash,is_admin)
		values ('updater@example.test','Updater','hash',true)
		returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var firstID int64
	if err := pool.QueryRow(ctx, `
		insert into instance_update_jobs(
			requested_by,idempotency_key,from_version,target_version,deployment_mode,manifest_sha256,status
		) values ($1,'request-one','v1.0.0','v1.1.0','binary',$2,'requested')
		returning id`, userID, digest).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into instance_update_jobs(
			requested_by,idempotency_key,from_version,target_version,deployment_mode,manifest_sha256,status
		) values ($1,'request-two','v1.0.0','v1.1.0','binary',$2,'preflight')`, userID, digest); err == nil {
		t.Fatal("accepted a second active update job")
	}
	if _, err := pool.Exec(ctx, `update instance_update_jobs set status='complete',finished_at=now() where id=$1`, firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into instance_update_jobs(
			requested_by,idempotency_key,from_version,target_version,deployment_mode,manifest_sha256,status
		) values ($1,'request-two','v1.1.0','v1.2.0','docker',$2,'requested')`, userID, digest); err != nil {
		t.Fatalf("insert after completion: %v", err)
	}
}

func TestUpdateJobsMigrationRejectsInvalidPersistentState(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('invalid-update@example.test','Updater','hash',true) returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		name       string
		mode       string
		digest     string
		status     string
		backupSize int64
	}{
		{name: "mode", mode: "kubernetes", digest: digest, status: "requested"},
		{name: "digest", mode: "binary", digest: "not-a-digest", status: "requested"},
		{name: "status", mode: "binary", digest: digest, status: "unknown"},
		{name: "backup size", mode: "binary", digest: digest, status: "failed", backupSize: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `
				insert into instance_update_jobs(
					requested_by,idempotency_key,from_version,target_version,deployment_mode,
					manifest_sha256,status,backup_size
				) values ($1,$2,'v1.0.0','v1.1.0',$3,$4,$5,$6)`,
				userID, "invalid-"+test.name, test.mode, test.digest, test.status, test.backupSize); err == nil {
				t.Fatal("invalid update job was accepted")
			}
		})
	}
}
