package db_test

import (
	"context"
	"fmt"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestAdminMigrationReservesSlugsAndCreatesPolicyState(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash,is_admin) values ('admin@example.test','Admin','hash',true) returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Admin','admin') returning id`).Scan(&orgID); err == nil {
		t.Fatal("reserved organization slug was accepted")
	}

	var scope, signupOverride string
	var graceDays int
	var replayOverride *bool
	if err := pool.QueryRow(ctx, `select two_factor_scope,two_factor_grace_days,coalesce(signup_mode_override,''),replay_visible_override from instance_settings where id`).Scan(&scope, &graceDays, &signupOverride, &replayOverride); err != nil {
		t.Fatal(err)
	}
	if scope != "off" || graceDays != 7 || signupOverride != "" || replayOverride != nil {
		t.Fatalf("policy defaults = scope %q grace %d signup %q replay %v", scope, graceDays, signupOverride, replayOverride)
	}

	for _, table := range []string{"instance_settings", "two_factor_exemptions", "audit_log"} {
		var exists bool
		if err := pool.QueryRow(ctx, `select to_regclass($1) is not null`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("table %s does not exist", table)
		}
	}
	var auditOrgNullable string
	if err := pool.QueryRow(ctx, `select is_nullable from information_schema.columns where table_schema=current_schema() and table_name='audit_log' and column_name='org_id'`).Scan(&auditOrgNullable); err != nil {
		t.Fatal(err)
	}
	if auditOrgNullable != "YES" {
		t.Fatalf("audit_log.org_id nullable = %q, want YES", auditOrgNullable)
	}
}

func TestAdminMigrationRenamesExistingReservedSlug(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	// Simulate the pre-0060 state in a schema migrated only through 0053 by
	// removing the check, inserting a clash, and applying the migration body.
	if _, err := pool.Exec(ctx, `alter table orgs drop constraint orgs_slug_not_reserved`); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Account','account') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		update orgs set slug=slug || '-org-' || id::text where slug = any(array['account','admin','api','auth','invite','login','logout','password-reset','reset','setup','share','signup']);
		alter table orgs add constraint orgs_slug_not_reserved check (slug <> all(array['account','admin','api','auth','invite','login','logout','password-reset','reset','setup','share','signup']));`); err != nil {
		t.Fatal(err)
	}
	var slug string
	if err := pool.QueryRow(ctx, `select slug from orgs where id=$1`, orgID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	want := "account-org-" + fmt.Sprint(orgID)
	if slug != want {
		t.Fatalf("renamed slug = %q, want %q", slug, want)
	}
}
