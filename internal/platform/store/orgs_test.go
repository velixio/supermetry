package store

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestCreateOrgNeverUsesReservedApplicationSlug(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID int64
	if err := pool.QueryRow(ctx, `insert into users(email,name,password_hash) values ('slug-owner@example.test','Owner','hash') returning id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Admin", "Account", "Signup"} {
		t.Run(name, func(t *testing.T) {
			var orgID int64
			if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
				var err error
				orgID, err = CreateOrg(ctx, tx, name, userID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var slug string
			if err := pool.QueryRow(ctx, `select slug from orgs where id=$1`, orgID).Scan(&slug); err != nil {
				t.Fatal(err)
			}
			if slug == "admin" || slug == "account" || slug == "signup" || !strings.HasPrefix(slug, OrganizationSlug(name)+"-") {
				t.Fatalf("CreateOrg(%q) slug = %q", name, slug)
			}
		})
	}
}
