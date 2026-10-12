package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestUpdateUserProtectsLastAdminAndAuditsMutation(t *testing.T) {
	service, data, actorID, memberID := newFixture(t)
	ctx := context.Background()

	disabled := true
	if err := service.UpdateUser(ctx, actorID, actorID, UserPatch{Disabled: &disabled}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin error = %v, want ErrLastAdmin", err)
	}
	promoted := true
	if err := service.UpdateUser(ctx, actorID, memberID, UserPatch{InstanceAdmin: &promoted}); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateUser(ctx, memberID, actorID, UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	var isAdmin bool
	var disabledAt *time.Time
	if err := data.Pool.QueryRow(ctx, `select is_admin,disabled_at from users where id=$1`, actorID).Scan(&isAdmin, &disabledAt); err != nil {
		t.Fatal(err)
	}
	if !isAdmin || disabledAt == nil {
		t.Fatalf("updated user is_admin=%t disabled_at=%v", isAdmin, disabledAt)
	}
	var actions []string
	rows, err := data.Pool.Query(ctx, `select action from audit_log where org_id is null order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	if len(actions) != 2 || actions[0] != "instance.user.updated" || actions[1] != "instance.user.updated" {
		t.Fatalf("audit actions = %v", actions)
	}
}

func TestUpdatePolicyPreventsInstanceWideLockout(t *testing.T) {
	service, data, actorID, _ := newFixture(t)
	ctx := context.Background()
	policy := TwoFactorPolicy{Scope: ScopeInstanceAdmins, GraceDays: 0}
	if err := service.UpdateTwoFactorPolicy(ctx, actorID, policy); !errors.Is(err, ErrWouldLockOut) {
		t.Fatalf("unenrolled policy error = %v, want ErrWouldLockOut", err)
	}
	if _, err := data.Pool.Exec(ctx, `update users set totp_secret_enc='x',totp_enabled=true where id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTwoFactorPolicy(ctx, actorID, policy); err != nil {
		t.Fatal(err)
	}
	got, err := service.TwoFactorPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != policy {
		t.Fatalf("policy = %+v, want %+v", got, policy)
	}
}

func TestTwoFactorStatusStartsGraceAndHonorsExemption(t *testing.T) {
	service, data, actorID, memberID := newFixture(t)
	ctx := context.Background()
	if _, err := data.Pool.Exec(ctx, `update users set totp_secret_enc='x',totp_enabled=true where id=$1`, actorID); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateTwoFactorPolicy(ctx, actorID, TwoFactorPolicy{Scope: ScopeEveryone, GraceDays: 2}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	status, err := service.TwoFactorStatus(ctx, memberID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Required || !status.SetupRequired || status.GraceUntil == nil || !status.GraceUntil.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("status = %+v", status)
	}
	status, err = service.TwoFactorStatus(ctx, memberID, now.Add(49*time.Hour))
	if err != nil || !status.SetupRequired || !status.GraceExpired {
		t.Fatalf("expired status = %+v, %v", status, err)
	}
	if err := service.SetExemption(ctx, actorID, memberID, "hardware replacement", nil); err != nil {
		t.Fatal(err)
	}
	status, err = service.TwoFactorStatus(ctx, memberID, now.Add(49*time.Hour))
	if err != nil || !status.Exempt || status.SetupRequired {
		t.Fatalf("exempt status = %+v, %v", status, err)
	}
}

func newFixture(t *testing.T) (*Service, *store.Store, int64, int64) {
	t.Helper()
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	data := &store.Store{Pool: pool}
	var actorID, memberID int64
	if err := pool.QueryRow(context.Background(), `insert into users(email,name,password_hash,is_admin) values ('instance-admin@example.test','Instance Admin','hash',true) returning id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `insert into users(email,name,password_hash) values ('member@example.test','Member','hash') returning id`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(context.Background(), `insert into orgs(name,slug) values ('Acme','acme') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `insert into memberships(org_id,user_id,role) values ($1,$2,'owner'),($1,$3,'viewer')`, orgID, actorID, memberID); err != nil {
		t.Fatal(err)
	}
	return New(data), data, actorID, memberID
}
