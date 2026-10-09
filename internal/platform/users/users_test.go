package users_test

import (
	"context"
	"testing"

	"supermetry/internal/db"
	"supermetry/internal/platform/store"
	"supermetry/internal/platform/users"
	"supermetry/internal/testpg"
)

func TestCreateNormalizesEmailAndHashesPassword(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	service := users.New(&store.Store{Pool: pool})
	id, err := service.Create(ctx, "  Ana@Example.COM ", "Ana", "pw-123456789")
	if err != nil {
		t.Fatal(err)
	}
	var email, passwordHash string
	if err := pool.QueryRow(ctx, `select email,password_hash from users where id=$1`, id).Scan(&email, &passwordHash); err != nil {
		t.Fatal(err)
	}
	if email != "ana@example.com" {
		t.Fatalf("email = %q", email)
	}
	if passwordHash == "pw-123456789" || passwordHash == "" {
		t.Fatal("password was not hashed")
	}
}
