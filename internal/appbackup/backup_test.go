package appbackup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveDatabaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "URL wins",
			env: map[string]string{
				"SUPERMETRY_DATABASE_URL":  "postgres://direct/db",
				"SUPERMETRY_DATABASE_HOST": "ignored",
			},
			want: "postgres://direct/db",
		},
		{
			name: "Docker split variables",
			env: map[string]string{
				"SUPERMETRY_DATABASE_HOST":     "postgres",
				"SUPERMETRY_DATABASE_USER":     "app user",
				"SUPERMETRY_DATABASE_PASSWORD": "p@ss/word",
				"SUPERMETRY_DATABASE_NAME":     "analytics",
				"SUPERMETRY_DATABASE_SSLMODE":  "disable",
			},
			want: "postgres://app%20user:p%40ss%2Fword@postgres:5432/analytics?sslmode=disable",
		},
		{
			name: "Postgres fallbacks",
			env: map[string]string{
				"SUPERMETRY_DATABASE_HOST": "db",
				"POSTGRES_USER":            "postgres-user",
				"POSTGRES_PASSWORD":        "secret",
				"POSTGRES_DB":              "supermetry-db",
			},
			want: "postgres://postgres-user:secret@db:5432/supermetry-db?sslmode=disable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveDatabaseURL(func(key string) string { return tt.env[key] })
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("ResolveDatabaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateAndVerify(t *testing.T) {
	dir := t.TempDir()
	rt := fakeRuntime(t, dir)
	result, err := CreateWithRuntime(context.Background(), Options{
		DatabaseURL:       "postgres://supermetry:secret@localhost/supermetry?sslmode=disable",
		Directory:         dir,
		Filename:          "update.tar.gz",
		SupermetryVersion: "v1.2.3",
	}, rt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != filepath.Join(dir, "update.tar.gz") || result.Size <= 0 || len(result.SHA256) != 64 {
		t.Fatalf("result = %#v", result)
	}
	if result.SchemaVersion != 42 {
		t.Fatalf("schema version = %d, want 42", result.SchemaVersion)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	manifest, err := VerifyWithRuntime(context.Background(), result.Path, rt)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 42 || manifest.SupermetryVersion != "v1.2.3" {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestCreateChecksHeadroomBeforeDump(t *testing.T) {
	dir := t.TempDir()
	rt := fakeRuntime(t, dir)
	rt.FreeBytes = func(string) (uint64, error) { return 10, nil }
	called := false
	rt.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		called = true
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}
	_, err := CreateWithRuntime(context.Background(), Options{
		DatabaseURL: "postgres://supermetry:secret@localhost/supermetry",
		Directory:   dir,
		Filename:    "backup.tar.gz",
	}, rt)
	if err == nil || !strings.Contains(err.Error(), "free space") {
		t.Fatalf("error = %v, want free-space error", err)
	}
	if called {
		t.Fatal("PostgreSQL client ran after failed headroom check")
	}
}

func TestCreateCancellationCleansPartialFiles(t *testing.T) {
	dir := t.TempDir()
	rt := fakeRuntime(t, dir)
	rt.Command = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) == 1 && args[0] == "--version" {
			return exec.CommandContext(ctx, "sh", "-c", "printf 'pg_dump 17.2\\n'")
		}
		return exec.CommandContext(ctx, "sh", "-c", "printf partial; while :; do sleep 1; done")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CreateWithRuntime(ctx, Options{
		DatabaseURL: "postgres://supermetry:secret@localhost/supermetry",
		Directory:   dir,
		Filename:    "backup.tar.gz",
	}, rt)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	assertNoBackupFiles(t, dir)
}

func TestCreateSanitizesClientErrorsAndCleansPartials(t *testing.T) {
	dir := t.TempDir()
	rt := fakeRuntime(t, dir)
	rt.Command = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) == 1 && args[0] == "--version" {
			return exec.CommandContext(ctx, "sh", "-c", "printf 'pg_dump 17.2\\n'")
		}
		return exec.CommandContext(ctx, "sh", "-c", "printf 'password=very-secret postgres://supermetry:very-secret@localhost/db' >&2; exit 7")
	}
	_, err := CreateWithRuntime(context.Background(), Options{
		DatabaseURL: "postgres://supermetry:very-secret@localhost/db",
		Directory:   dir,
		Filename:    "backup.tar.gz",
	}, rt)
	if err == nil {
		t.Fatal("CreateWithRuntime() unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "very-secret") || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("error leaks credentials: %q", err)
	}
	assertNoBackupFiles(t, dir)
}

func TestVerifyRejectsMalformedArchive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.tar.gz")
	if err := os.WriteFile(path, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyWithRuntime(context.Background(), path, fakeRuntime(t, dir))
	if err == nil || !strings.Contains(err.Error(), "backup archive") {
		t.Fatalf("error = %v", err)
	}
}

func fakeRuntime(t *testing.T, dir string) Runtime {
	t.Helper()
	client := filepath.Join(dir, "pg-client")
	script := `#!/bin/sh
set -eu
if [ "$1" = "--version" ]; then printf 'pg_dump (PostgreSQL) 17.2\n'; exit 0; fi
if [ "$1" = "--list" ]; then printf '1; 1259 1 TABLE public schema_migrations owner\n'; exit 0; fi
if [ "$1" = "--file" ]; then exit 0; fi
printf 'fake custom dump\n'
`
	if err := os.WriteFile(client, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return Runtime{
		LookupPath: func(string) (string, error) { return client, nil },
		Command:    exec.CommandContext,
		InspectDatabase: func(context.Context, string) (int, int64, error) {
			return 42, 1024, nil
		},
		FreeBytes: func(string) (uint64, error) { return 1 << 40, nil },
		Now:       func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
	}
}

func assertNoBackupFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".partial-") || strings.HasSuffix(entry.Name(), ".dump") || strings.HasSuffix(entry.Name(), ".tar.gz") {
			t.Fatalf("partial backup remains: %s", entry.Name())
		}
	}
}
