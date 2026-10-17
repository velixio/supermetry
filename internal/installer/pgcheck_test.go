package installer

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestCheckPostgresReasons(t *testing.T) {
	dsn := os.Getenv("SUPERMETRY_TEST_DSN")
	if dsn == "" {
		t.Skip("SUPERMETRY_TEST_DSN is not set")
	}
	base, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	user := base.User.Username()

	tests := []struct {
		name       string
		mutate     func(*url.URL)
		wantField  string
		wantReason string
	}{
		{
			name: "wrong password",
			mutate: func(u *url.URL) {
				u.User = url.UserPassword(user, "definitely-wrong-password")
			},
			wantField:  "password",
			wantReason: "password authentication failed",
		},
		{
			name: "missing database",
			mutate: func(u *url.URL) {
				u.Path = "/supermetry_installer_missing_database"
			},
			wantField:  "name",
			wantReason: "does not exist",
		},
		{
			name: "closed port",
			mutate: func(u *url.URL) {
				u.Host = "127.0.0.1:1"
			},
			wantField:  "host",
			wantReason: "connect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := *base
			tt.mutate(&u)
			_, err := CheckPostgres(context.Background(), u.String())
			var checkErr *CheckError
			if !errors.As(err, &checkErr) {
				t.Fatalf("error = %T %v, want *CheckError", err, err)
			}
			if checkErr.Field != tt.wantField {
				t.Errorf("Field = %q, want %q", checkErr.Field, tt.wantField)
			}
			if !strings.Contains(checkErr.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want substring %q", checkErr.Reason, tt.wantReason)
			}
		})
	}
}

func TestClassifyCheckErrorExplainsDNSFailure(t *testing.T) {
	t.Parallel()
	err := classifyCheckError(&net.DNSError{Name: "db.invalid", IsNotFound: true})
	if got := err.Error(); !strings.Contains(got, "does not resolve") || !strings.Contains(got, "--db-host") {
		t.Fatalf("error = %q", got)
	}
}

func TestCheckPostgresReturnsVersion(t *testing.T) {
	dsn := os.Getenv("SUPERMETRY_TEST_DSN")
	if dsn == "" {
		t.Skip("SUPERMETRY_TEST_DSN is not set")
	}
	version, err := CheckPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if version <= 0 {
		t.Fatalf("version = %d, want positive server_version_num", version)
	}
}

func TestCheckAdminExistsMatchesDatabase(t *testing.T) {
	dsn := os.Getenv("SUPERMETRY_TEST_DSN")
	if dsn == "" {
		t.Skip("SUPERMETRY_TEST_DSN is not set")
	}
	db, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	var want bool
	var table *string
	if err := db.QueryRow(context.Background(), "select to_regclass('public.users')::text").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != nil {
		if err := db.QueryRow(context.Background(), "select exists(select 1 from users u where u.is_admin and not coalesce((to_jsonb(u)->>'is_system')::boolean,false) limit 1)").Scan(&want); err != nil {
			t.Fatal(err)
		}
	}
	got, err := CheckAdminExists(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("CheckAdminExists() = %t, want %t", got, want)
	}
}
