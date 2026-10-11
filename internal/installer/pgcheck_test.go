package installer

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
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
