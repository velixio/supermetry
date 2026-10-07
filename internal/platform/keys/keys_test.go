package keys

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
	"panel/internal/platform/authz"
	"panel/internal/platform/store"
	"panel/internal/testpg"
)

func TestKeyLifecycle(t *testing.T) {
	keys, principal, pool := newKeys(t)
	ctx := context.Background()

	plain, info, err := keys.Create(ctx, principal, "CI", []string{"stats:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^pnl_[a-z2-7]{8}_[A-Za-z0-9]{40,}$`).MatchString(plain) {
		t.Fatalf("format %q", plain)
	}
	verified, ok := keys.Verify(ctx, plain)
	if !ok || verified.Role != authz.RoleKey || verified.OrgID != principal.OrgID || len(verified.Scopes) != 1 || verified.Scopes[0] != "stats:read" {
		t.Fatalf("verify = (%+v, %t)", verified, ok)
	}
	list, err := keys.List(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(list), plain[13:]) {
		t.Fatal("secret leaked in list")
	}
	if err := keys.Revoke(ctx, principal, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys.Verify(ctx, plain); ok {
		t.Fatal("revoked key accepted")
	}

	expiredAt := time.Now().Add(-time.Minute)
	expired, _, err := keys.Create(ctx, principal, "old", []string{"stats:read"}, nil, &expiredAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys.Verify(ctx, expired); ok {
		t.Fatal("expired key accepted")
	}
	keys.Close()

	var storedPrefix string
	var storedHash []byte
	var lastUsedAt *time.Time
	if err := pool.QueryRow(ctx, `select prefix, hash, last_used_at from api_keys where id = $1`, info.ID).Scan(&storedPrefix, &storedHash, &lastUsedAt); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(plain))
	if storedPrefix != plain[4:12] || !bytes.Equal(storedHash, wantHash[:]) {
		t.Fatalf("stored key material = (%q, %x)", storedPrefix, storedHash)
	}
	if lastUsedAt == nil {
		t.Fatal("verified key last_used_at was not flushed")
	}

	var actions []string
	rows, err := pool.Query(ctx, `select action from audit_log where org_id = $1 order by id`, principal.OrgID)
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
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(actions, ","); got != "api_key.created,api_key.revoked,api_key.created" {
		t.Fatalf("audit actions = %q", got)
	}
}

func newKeys(t *testing.T) (*Keys, authz.Principal, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID, orgID int64
	if err := pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ('owner@example.test', 'Owner', 'hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Test', 'test') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id, user_id, role) values ($1, $2, 'owner')`, orgID, userID); err != nil {
		t.Fatal(err)
	}
	service := New(&store.Store{Pool: pool})
	t.Cleanup(service.Close)
	return service, authz.Principal{UserID: userID, OrgID: orgID, Role: authz.RoleOwner}, pool
}
