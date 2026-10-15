package admin

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
	"supermetry/internal/updatehelper"
)

func TestService_RequestUpdateIsAdminOnlyIdempotentAndSingleActive(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var adminID, userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('job-admin@example.test','Admin','hash',true) returning id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('job-user@example.test','User','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	service := New(&store.Store{Pool: pool})

	if _, _, err := service.RequestUpdate(ctx, userID, UpdateRequest{IdempotencyKey: "ordinary-user-request", FromVersion: "v1.0.0", TargetVersion: "v1.1.0", DeploymentMode: "binary"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin error = %v, want ErrForbidden", err)
	}
	request := UpdateRequest{IdempotencyKey: "admin-request-one", FromVersion: "v1.0.0", TargetVersion: "v1.1.0", DeploymentMode: "binary"}
	first, created, err := service.RequestUpdate(ctx, adminID, request)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first request was not created")
	}
	again, created, err := service.RequestUpdate(ctx, adminID, request)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("idempotent request = %+v, %v; want job %d", again, err, first.ID)
	}
	if _, _, err := service.RequestUpdate(ctx, adminID, UpdateRequest{IdempotencyKey: "admin-request-two", FromVersion: "v1.0.0", TargetVersion: "v1.2.0", DeploymentMode: "binary"}); !errors.Is(err, ErrUpdateActive) {
		t.Fatalf("second active request error = %v, want ErrUpdateActive", err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `select count(*) from audit_log where action='instance.update.requested' and actor_user_id=$1`, adminID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("requested audits = %d, want 1", audits)
	}
	if err := service.SyncUpdateJob(ctx, adminID, first.ID, updatehelper.StateDownload, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.SyncUpdateJob(ctx, adminID, first.ID, updatehelper.StatePreflight, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("regression error = %v, want ErrInvalid", err)
	}
	if err := service.SyncUpdateJob(ctx, adminID, first.ID, updatehelper.StateComplete, ""); err != nil {
		t.Fatal(err)
	}
	latest, err := service.LatestUpdateJob(ctx)
	if err != nil || latest == nil || latest.Status != "complete" || latest.FinishedAt == nil {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	if _, created, err := service.RequestUpdate(ctx, adminID, UpdateRequest{IdempotencyKey: "admin-request-two", FromVersion: "v1.1.0", TargetVersion: "v1.2.0", DeploymentMode: "binary"}); err != nil || !created {
		t.Fatalf("request after completion: created=%v err=%v", created, err)
	}
}

func TestService_RequestUpdateValidatesExactUpgrade(t *testing.T) {
	service := New(nil)
	for _, test := range []struct {
		name    string
		request UpdateRequest
	}{
		{name: "development source", request: UpdateRequest{IdempotencyKey: "request-dev", FromVersion: "dev", TargetVersion: "v1.1.0", DeploymentMode: "binary"}},
		{name: "prerelease target", request: UpdateRequest{IdempotencyKey: "request-pre", FromVersion: "v1.0.0", TargetVersion: "v1.1.0-rc.1", DeploymentMode: "binary"}},
		{name: "same version", request: UpdateRequest{IdempotencyKey: "request-same", FromVersion: "v1.0.0", TargetVersion: "1.0.0", DeploymentMode: "binary"}},
		{name: "downgrade", request: UpdateRequest{IdempotencyKey: "request-down", FromVersion: "v2.0.0", TargetVersion: "v1.0.0", DeploymentMode: "binary"}},
		{name: "unsupported mode", request: UpdateRequest{IdempotencyKey: "request-mode", FromVersion: "v1.0.0", TargetVersion: "v1.1.0", DeploymentMode: "local"}},
		{name: "short idempotency key", request: UpdateRequest{IdempotencyKey: "short", FromVersion: "v1.0.0", TargetVersion: "v1.1.0", DeploymentMode: "binary"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := service.RequestUpdate(context.Background(), 1, test.request); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestService_RecentUpdateJobsListsNewestFirstWithinTheLimit(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	service := New(&store.Store{Pool: pool})

	empty, err := service.RecentUpdateJobs(ctx, 10)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty history = %#v, %v; want an empty non-nil list", empty, err)
	}
	for index := 1; index <= 12; index++ {
		status := "complete"
		if index%4 == 0 {
			status = "failed"
		}
		if _, err := pool.Exec(ctx, `
			insert into instance_update_jobs(idempotency_key,from_version,target_version,deployment_mode,status,finished_at)
			values ($1,'v1.0.0',$2,'binary',$3,now())`,
			fmt.Sprintf("history-key-%02d", index), fmt.Sprintf("v1.0.%d", index), status); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := service.RecentUpdateJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 10 {
		t.Fatalf("history length = %d, want 10", len(jobs))
	}
	if jobs[0].TargetVersion != "v1.0.12" || jobs[0].Status != "failed" || jobs[9].TargetVersion != "v1.0.3" {
		t.Fatalf("history order = %s (%s) ... %s, want v1.0.12 (failed) down to v1.0.3", jobs[0].TargetVersion, jobs[0].Status, jobs[9].TargetVersion)
	}
}
