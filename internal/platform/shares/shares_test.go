package shares

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/platform/authz"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

func TestShareLifecycle(t *testing.T) {
	service, principal, siteID, pool := newShares(t)
	ctx := context.Background()

	slug, info, err := service.Create(ctx, principal, siteID, "")
	if err != nil {
		t.Fatal(err)
	}
	shared, ok := service.Principal(ctx, slug, "")
	if !ok {
		t.Fatal("passwordless share was refused")
	}
	if shared.Role != authz.RoleShare || shared.OrgID != principal.OrgID || shared.ShareID != info.ID || len(shared.SiteIDs) != 1 || shared.SiteIDs[0] != siteID {
		t.Fatalf("passwordless principal = %+v", shared)
	}

	var storedSlug []byte
	var storedPassword *string
	if err := pool.QueryRow(ctx, `select slug_hash, password_hash from shared_links where id = $1`, info.ID).Scan(&storedSlug, &storedPassword); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedSlug, platformcrypto.HashToken(slug)) || storedPassword != nil || bytes.Contains(storedSlug, []byte(slug)) {
		t.Fatalf("stored passwordless share material = (%x, %v)", storedSlug, storedPassword)
	}

	protectedSlug, protected, err := service.Create(ctx, principal, siteID, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Principal(ctx, protectedSlug, ""); ok {
		t.Fatal("protected share accepted without cookie")
	}
	if _, err := service.Open(ctx, protectedSlug, ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("empty password error = %v, want ErrPasswordRequired", err)
	}
	if _, err := service.Open(ctx, protectedSlug, "wrong"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong password error = %v, want ErrInvalid", err)
	}
	cookie, err := service.Open(ctx, protectedSlug, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if cookie == "" || strings.Contains(cookie, "correct horse battery staple") {
		t.Fatalf("unsafe share cookie %q", cookie)
	}
	shared, ok = service.Principal(ctx, protectedSlug, cookie)
	if !ok || shared.ShareID != protected.ID || shared.Role != authz.RoleShare || len(shared.SiteIDs) != 1 || shared.SiteIDs[0] != siteID {
		t.Fatalf("protected principal = (%+v, %t)", shared, ok)
	}

	viewer := principal
	viewer.Role = authz.RoleViewer
	if err := service.Revoke(ctx, viewer, protected.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer revoke error = %v, want ErrForbidden", err)
	}
	if _, ok := service.Principal(ctx, protectedSlug, cookie); !ok {
		t.Fatal("forbidden revoke changed the share")
	}

	if err := service.Revoke(ctx, principal, protected.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Principal(ctx, protectedSlug, cookie); ok {
		t.Fatal("revoked share accepted a valid cookie")
	}
	if _, err := service.Open(ctx, protectedSlug, "correct horse battery staple"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("revoked share open error = %v, want ErrInvalid", err)
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
	if got := strings.Join(actions, ","); got != "share.created,share.created,share.revoked" {
		t.Fatalf("audit actions = %q", got)
	}
}

func TestShareExpiryLifecycle(t *testing.T) {
	service, principal, siteID, pool := newShares(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	slug, info, err := service.CreateWithExpiry(ctx, principal, siteID, "", intPointer(7))
	if err != nil {
		t.Fatal(err)
	}
	wantExpiry := now.Add(7 * 24 * time.Hour)
	if info.ExpiresAt == nil || !info.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("created expiry = %v, want %v", info.ExpiresAt, wantExpiry)
	}
	if _, ok := service.Principal(ctx, slug, ""); !ok {
		t.Fatal("unexpired share was refused")
	}

	protectedSlug, protected, err := service.CreateWithExpiry(ctx, principal, siteID, "correct horse battery staple", intPointer(7))
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := service.Open(ctx, protectedSlug, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	now = wantExpiry
	if _, ok := service.Principal(ctx, slug, ""); ok {
		t.Fatal("expired passwordless share was accepted")
	}
	if _, ok := service.Principal(ctx, protectedSlug, cookie); ok {
		t.Fatal("expired protected share accepted its unlock cookie")
	}
	if _, err := service.Open(ctx, protectedSlug, "correct horse battery staple"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired share open error = %v, want ErrInvalid", err)
	}

	if err := service.ChangeExpiry(ctx, principal, protected.ID, intPointer(30)); err != nil {
		t.Fatal(err)
	}
	revivedCookie, err := service.Open(ctx, protectedSlug, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.Principal(ctx, protectedSlug, revivedCookie); !ok {
		t.Fatal("extended share was not revived")
	}
	if err := service.ChangeExpiry(ctx, principal, info.ID, nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(366 * 24 * time.Hour)
	if _, ok := service.Principal(ctx, slug, ""); !ok {
		t.Fatal("never-expiring share stopped working")
	}

	var otherUserID, otherOrgID int64
	if err := pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ('other@example.test', 'Other', 'hash') returning id`).Scan(&otherUserID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Other', 'other') returning id`).Scan(&otherOrgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into memberships(org_id, user_id, role) values ($1, $2, 'owner')`, otherOrgID, otherUserID); err != nil {
		t.Fatal(err)
	}
	other := authz.Principal{UserID: otherUserID, OrgID: otherOrgID, Role: authz.RoleOwner}
	if err := service.ChangeExpiry(ctx, other, protected.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org expiry change error = %v, want ErrNotFound", err)
	}

	var createdExpiry, changedExpiry *time.Time
	if err := pool.QueryRow(ctx, `select (metadata->>'expires_at')::timestamptz from audit_log where action='share.created' and target_id=$1`, info.ID).Scan(&createdExpiry); err != nil {
		t.Fatal(err)
	}
	if createdExpiry == nil || !createdExpiry.Equal(wantExpiry) {
		t.Fatalf("created audit expiry = %v, want %v", createdExpiry, wantExpiry)
	}
	if err := pool.QueryRow(ctx, `select (metadata->>'expires_at')::timestamptz from audit_log where action='share.expiry_changed' and target_id=$1 order by id desc limit 1`, protected.ID).Scan(&changedExpiry); err != nil {
		t.Fatal(err)
	}
	if changedExpiry == nil || !changedExpiry.Equal(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("changed audit expiry = %v", changedExpiry)
	}
}

func intPointer(value int) *int { return &value }

func TestCreateRejectsWeakSharePassword(t *testing.T) {
	service, principal, siteID, _ := newShares(t)
	if _, _, err := service.Create(context.Background(), principal, siteID, "password123"); !errors.Is(err, platformcrypto.ErrPasswordCommon) {
		t.Fatalf("Create() error = %v, want ErrPasswordCommon", err)
	}
}

func TestOpenLimitsPasswordAttemptsPerTargetAndClient(t *testing.T) {
	service, principal, siteID, _ := newShares(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	slug, _, err := service.Create(ctx, principal, siteID, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		attemptCtx := WithClientKey(ctx, "client-"+string(rune('a'+i)))
		if _, err := service.Open(attemptCtx, slug, "wrong password"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("target attempt %d error = %v, want ErrInvalid", i+1, err)
		}
	}
	if _, err := service.Open(WithClientKey(ctx, "fresh-client"), slug, "correct horse battery staple"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("target limit error = %v, want ErrRateLimited", err)
	}

	service, _, _, _ = newShares(t)
	service.now = func() time.Time { return now }
	clientCtx := WithClientKey(ctx, "one-client")
	for i := 0; i < 5; i++ {
		if _, err := service.Open(clientCtx, "missing-"+string(rune('a'+i)), "wrong password"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("client attempt %d error = %v, want ErrInvalid", i+1, err)
		}
	}
	if _, err := service.Open(clientCtx, "another-missing", "wrong password"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("client limit error = %v, want ErrRateLimited", err)
	}
}

func TestAttemptLimiterRejectedClientDoesNotConsumeTargetBudget(t *testing.T) {
	limiter := attemptLimiter{
		targets: make(map[string]attemptBucket),
		clients: make(map[string]attemptBucket),
	}
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if !limiter.allow("other-"+strconv.Itoa(i), "flooder", now) {
			t.Fatalf("flooder warm-up attempt %d rejected", i+1)
		}
	}
	if limiter.allow("victim", "flooder", now) {
		t.Fatal("client over its own limit was allowed")
	}
	for i := 0; i < 20; i++ {
		if !limiter.allow("victim", "well-behaved-"+strconv.Itoa(i), now) {
			t.Fatalf("well-behaved client %d lost shared target budget", i+1)
		}
	}
}

func TestPruneAttemptsEnforcesHardCap(t *testing.T) {
	const hardCap = 4096
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	buckets := make(map[string]attemptBucket, hardCap+17)
	for i := 0; i < hardCap+17; i++ {
		buckets[fmt.Sprintf("key-%05d", i)] = attemptBucket{tokens: 1, last: now.Add(time.Duration(i) * time.Nanosecond)}
	}

	pruneAttempts(buckets, now.Add(30*time.Second))

	if got := len(buckets); got != hardCap {
		t.Fatalf("bucket count = %d, want %d", got, hardCap)
	}
	if _, ok := buckets["key-00000"]; ok {
		t.Fatal("oldest bucket was not evicted")
	}
	if _, ok := buckets[fmt.Sprintf("key-%05d", hardCap+16)]; !ok {
		t.Fatal("newest bucket was evicted")
	}
}

func TestUnlockCookieUsesServerSecretNotPasswordHash(t *testing.T) {
	service, principal, siteID, pool := newShares(t)
	ctx := context.Background()
	slug, info, err := service.Create(ctx, principal, siteID, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := service.Open(ctx, slug, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	rehashed, err := platformcrypto.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update shared_links set password_hash=$2 where id=$1`, info.ID, rehashed); err != nil {
		t.Fatal(err)
	}
	if shared, ok := service.Principal(ctx, slug, cookie); !ok || shared.ShareID != info.ID {
		t.Fatal("password rehash invalidated server-signed unlock cookie")
	}
}

func newShares(t *testing.T) (*Shares, authz.Principal, int32, *pgxpool.Pool) {
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
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id, public_id, name, timezone, retention_class)
		values ($1, 'testsite0001', 'Test site', 'UTC', 30)
		returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return New(&store.Store{Pool: pool}, []byte("0123456789abcdef0123456789abcdef")), authz.Principal{
		UserID: userID, OrgID: orgID, Role: authz.RoleOwner,
	}, siteID, pool
}
