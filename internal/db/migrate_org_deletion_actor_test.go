package db

import (
	"context"
	"testing"

	"supermetry/internal/testpg"
)

func TestOrgDeletionActorMigrationBackfillsCurrentDeletion(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.version >= 244 {
			break
		}
		if err := applyMigration(ctx, conn, migration); err != nil {
			conn.Release()
			t.Fatal(err)
		}
	}
	conn.Release()

	var adminID, ownerID, adminOrgID, ownerOrgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('admin@example.test','user12','hash',true) returning id`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('owner@example.test','owner','hash') returning id`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug,deleted_at) values ('Admin deleted','admin-deleted',now()) returning id`).Scan(&adminOrgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug,deleted_at) values ('Owner deleted','owner-deleted',now()) returning id`).Scan(&ownerOrgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into audit_log(actor_user_id,action,target_type,target_id,metadata)
		values ($1,'instance.organization.deleted','organization',$2,'{}')`, adminID, adminOrgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into audit_log(org_id,actor_user_id,action,target_type,target_id,metadata)
		values ($1,$2,'organization.deleted','organization',$1,'{}')`, ownerOrgID, ownerID); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		orgID  int64
		kind   string
		userID int64
	}{
		{orgID: adminOrgID, kind: "instance_admin", userID: adminID},
		{orgID: ownerOrgID, kind: "owner", userID: ownerID},
	} {
		var kind string
		var userID int64
		if err := pool.QueryRow(ctx, `select deleted_by_kind,deleted_by_user_id from orgs where id=$1`, test.orgID).Scan(&kind, &userID); err != nil {
			t.Fatal(err)
		}
		if kind != test.kind || userID != test.userID {
			t.Fatalf("org %d provenance = %q/%d, want %q/%d", test.orgID, kind, userID, test.kind, test.userID)
		}
	}
}
