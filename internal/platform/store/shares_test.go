package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
	platformcrypto "supermetry/internal/platform/crypto"
	"supermetry/internal/testpg"
)

func TestSharedLinkPersistenceScopesMutationsToOrganization(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	var userID, orgID, otherOrgID int64
	if err := pool.QueryRow(ctx, `insert into users(email, name, password_hash) values ('owner@example.test', 'Owner', 'hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Test', 'test') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into orgs(name, slug) values ('Other', 'other') returning id`).Scan(&otherOrgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `
		insert into sites(org_id, public_id, name, timezone, retention_class)
		values ($1, 'testsite0001', 'Test site', 'UTC', 30)
		returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	slugHash := platformcrypto.HashToken("shr_example")
	var created SharedLink
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var createErr error
		created, createErr = CreateSharedLink(ctx, tx, orgID, SharedLink{
			SiteID: siteID, SlugHash: slugHash, CreatedBy: userID,
		})
		return createErr
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := SharedLinkBySlugHash(ctx, &Store{Pool: pool}, slugHash)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != created.ID || loaded.SiteID != siteID || loaded.OrgID != orgID {
		t.Fatalf("loaded shared link = %+v", loaded)
	}

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return RevokeSharedLink(ctx, tx, otherOrgID, created.ID)
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-org revoke error = %v, want pgx.ErrNoRows", err)
	}
	loaded, err = SharedLinkBySlugHash(ctx, &Store{Pool: pool}, slugHash)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RevokedAt != nil {
		t.Fatal("cross-org revoke changed the shared link")
	}

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return RevokeSharedLink(ctx, tx, orgID, created.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = SharedLinkBySlugHash(ctx, &Store{Pool: pool}, slugHash)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RevokedAt == nil {
		t.Fatal("shared link was not revoked")
	}
}
