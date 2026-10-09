package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"panel/internal/db"
)

func TestBackupHelpDocumentsReplayExclusion(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := backupCommandWithRuntime(context.Background(), []string{"--help"}, &output, testGetenv, defaultBackupRuntime()); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "Replay files are excluded") || !strings.Contains(got, "back them up separately") {
		t.Fatalf("backup help = %q", got)
	}
}

func TestPostgresEnvironment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		dsn  string
		want map[string]string
	}{
		{
			name: "escaped credentials and explicit port",
			dsn:  "postgres://panel:p%40ss%3Aword%2F%25@db.example:5544/analytics?sslmode=require",
			want: map[string]string{"PGHOST": "db.example", "PGPORT": "5544", "PGUSER": "panel", "PGPASSWORD": "p@ss:word/%", "PGDATABASE": "analytics", "PGSSLMODE": "require"},
		},
		{
			name: "missing port uses PostgreSQL default",
			dsn:  "postgresql://panel:secret@localhost/panel?sslmode=disable",
			want: map[string]string{"PGHOST": "localhost", "PGPORT": "5432", "PGUSER": "panel", "PGPASSWORD": "secret", "PGDATABASE": "panel", "PGSSLMODE": "disable"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := postgresEnvironment(tt.dsn)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("postgresEnvironment() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestPostgresCommandDoesNotPutPasswordInArgv(t *testing.T) {
	t.Parallel()
	env, err := postgresEnvironment("postgres://panel:very-secret@localhost/panel?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	cmd := postgresCommand(context.Background(), "/usr/bin/pg_dump", []string{"--format=custom", "--no-owner"}, env)
	if strings.Contains(strings.Join(cmd.Args, " "), "very-secret") {
		t.Fatalf("password leaked into argv: %q", cmd.Args)
	}
	if !containsEnv(cmd.Env, "PGPASSWORD=very-secret") {
		t.Fatal("password missing from child environment")
	}
}

func TestBackupArchiveLayoutAndManifest(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "backup.tar.gz")
	rt := fakeRuntime(t, dir)
	rt.now = func() time.Time { return time.Date(2026, 10, 8, 1, 2, 3, 0, time.FixedZone("test", 3600)) }
	rt.schemaVersion = func(context.Context, string) (int, error) { return 14, nil }

	if err := backupCommandWithRuntime(context.Background(), []string{"--out", out}, io.Discard, testGetenv, rt); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode = %o, want 600", info.Mode().Perm())
	}
	files := readArchive(t, out)
	if got := string(files["database.dump"]); got != "fake database dump\n" {
		t.Fatalf("database.dump = %q", got)
	}
	if len(files) != 2 {
		t.Fatalf("archive contains %v, want exactly database.dump and manifest.json", mapKeys(files))
	}
	var got backupManifest
	if err := json.Unmarshal(files["manifest.json"], &got); err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt != "2026-10-08T00:02:03Z" || got.PanelVersion != "" || got.SchemaVersion != 14 || got.PGDumpVersion != "pg_dump (PostgreSQL) 17.2" {
		t.Fatalf("manifest = %#v", got)
	}
}

func TestRestoreRefusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	latestMigrationVersion := latestEmbeddedMigrationVersion(t)
	valid := filepath.Join(dir, "valid.tar.gz")
	writeTestArchive(t, valid, backupManifest{CreatedAt: time.Now().UTC().Format(time.RFC3339), SchemaVersion: latestMigrationVersion, PGDumpVersion: "pg_dump 17"}, []byte("dump"))
	newer := filepath.Join(dir, "newer.tar.gz")
	writeTestArchive(t, newer, backupManifest{CreatedAt: time.Now().UTC().Format(time.RFC3339), SchemaVersion: latestMigrationVersion + 1, PGDumpVersion: "pg_dump 17"}, []byte("dump"))
	unreadable := filepath.Join(dir, "bad.tar.gz")
	if err := os.WriteFile(unreadable, []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := fakeRuntime(t, dir)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "confirmation required", args: []string{valid}, want: "--yes"},
		{name: "newer schema", args: []string{newer, "--yes"}, want: "newer than this binary"},
		{name: "missing archive", args: []string{filepath.Join(dir, "missing.tar.gz"), "--yes"}, want: "open backup archive"},
		{name: "unreadable archive", args: []string{unreadable, "--yes"}, want: "read backup archive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := restoreCommandWithRuntime(context.Background(), tt.args, io.Discard, testGetenv, rt)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestBackupRemovesPartialOutputOnFailure(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "partial.tar.gz")
	rt := fakeRuntime(t, dir)
	rt.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if len(args) == 1 && args[0] == "--version" {
			return exec.CommandContext(ctx, "sh", "-c", "printf 'pg_dump 17\\n'")
		}
		return exec.CommandContext(ctx, "sh", "-c", "printf partial; exit 7")
	}
	err := backupCommandWithRuntime(context.Background(), []string{"--out", out}, io.Discard, testGetenv, rt)
	if err == nil {
		t.Fatal("backup unexpectedly succeeded")
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial output remains: %v", statErr)
	}
}

func TestFakePostgresClientsExerciseBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	latestMigrationVersion := latestEmbeddedMigrationVersion(t)
	logFile := filepath.Join(dir, "client.log")
	writeFakeClient(t, dir, "pg_dump", `
if [ "$1" = "--version" ]; then echo 'pg_dump (PostgreSQL) 17.2'; exit 0; fi
printf 'dump from fake client\n'
printf 'dump argv=%s password=%s database=%s\n' "$*" "$PGPASSWORD" "$PGDATABASE" >> "$PANEL_CLIENT_LOG"
`)
	writeFakeClient(t, dir, "pg_restore", `
printf 'restore argv=%s password=%s database=%s\n' "$*" "$PGPASSWORD" "$PGDATABASE" >> "$PANEL_CLIENT_LOG"
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PANEL_CLIENT_LOG", logFile)
	dsn := "postgres://panel:s%40fe%20password@localhost/panel?sslmode=disable"
	getenv := func(key string) string {
		if key == "PANEL_DATABASE_URL" {
			return dsn
		}
		return os.Getenv(key)
	}
	rt := defaultBackupRuntime()
	rt.schemaVersion = func(context.Context, string) (int, error) { return latestMigrationVersion, nil }
	rt.reachable = func(context.Context, string) error { return nil }
	out := filepath.Join(dir, "full.tar.gz")
	if err := backupCommandWithRuntime(context.Background(), []string{"--out", out}, io.Discard, getenv, rt); err != nil {
		t.Fatal(err)
	}
	var restoreOutput strings.Builder
	if err := restoreCommandWithRuntime(context.Background(), []string{out, "--yes"}, &restoreOutput, getenv, rt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restoreOutput.String(), "WARNING") || !strings.Contains(restoreOutput.String(), "replaces existing data") {
		t.Fatalf("restore warning missing: %q", restoreOutput.String())
	}
	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	if strings.Contains(logText, "s%40fe") || strings.Contains(logText, "postgres://") {
		t.Fatalf("connection URL leaked: %q", logText)
	}
	if !strings.Contains(logText, "restore argv=--clean --if-exists --no-owner --dbname panel") {
		t.Fatalf("restore argv missing: %q", logText)
	}
}

func TestBackupRestoreIntegration(t *testing.T) {
	// Restore runs pg_restore --clean over the whole database, and the backup
	// dumps all of it, so this must never use the shared PANEL_TEST_DSN
	// database other tests run against (it is large and in use).
	dsn := os.Getenv("PANEL_TEST_BACKUP_DSN")
	if dsn == "" {
		t.Skip("PANEL_TEST_BACKUP_DSN is not set (point it at an empty, disposable database)")
	}
	if dsn == os.Getenv("PANEL_TEST_DSN") {
		t.Fatal("PANEL_TEST_BACKUP_DSN must not be the shared PANEL_TEST_DSN database")
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump is not installed")
	}
	if _, err := exec.LookPath("pg_restore"); err != nil {
		t.Skip("pg_restore is not installed")
	}
	t.Setenv("PANEL_DATABASE_URL", dsn)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	schema := "panel_backup_test_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(context.Background(), "create schema "+identifier+"; create table "+identifier+".marker (value text); insert into "+identifier+".marker values ('restored')"); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(context.Background(), "drop schema if exists "+identifier+" cascade")
	out := filepath.Join(t.TempDir(), "integration.tar.gz")
	if err := backupCommand(context.Background(), []string{"--out", out}, io.Discard, os.Getenv); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), "drop schema "+identifier+" cascade"); err != nil {
		t.Fatal(err)
	}
	if err := restoreCommand(context.Background(), []string{out, "--yes"}, io.Discard, os.Getenv); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := pool.QueryRow(context.Background(), "select value from "+identifier+".marker").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "restored" {
		t.Fatalf("restored marker = %q", value)
	}
}

func fakeRuntime(t *testing.T, dir string) backupRuntime {
	t.Helper()
	latestMigrationVersion := latestEmbeddedMigrationVersion(t)
	client := filepath.Join(dir, "fake-client")
	writeFakeClient(t, dir, "fake-client", `
if [ "$1" = "--version" ]; then echo 'pg_dump (PostgreSQL) 17.2'; else echo 'fake database dump'; fi
`)
	rt := defaultBackupRuntime()
	rt.lookupPath = func(string) (string, error) { return client, nil }
	rt.schemaVersion = func(context.Context, string) (int, error) { return latestMigrationVersion, nil }
	rt.reachable = func(context.Context, string) error { return nil }
	return rt
}

func latestEmbeddedMigrationVersion(t *testing.T) int {
	t.Helper()
	version, err := db.LatestMigrationVersion()
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func testGetenv(key string) string {
	if key == "PANEL_DATABASE_URL" {
		return "postgres://panel:secret@localhost/panel?sslmode=disable"
	}
	return os.Getenv(key)
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}

func readArchive(t *testing.T, name string) map[string][]byte {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name], err = io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func writeTestArchive(t *testing.T, name string, manifest backupManifest, dump []byte) {
	t.Helper()
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for entryName, contents := range map[string][]byte{"database.dump": dump, "manifest.json": manifestBytes} {
		if err := tw.WriteHeader(&tar.Header{Name: entryName, Mode: 0o600, Size: int64(len(contents))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
}

func writeFakeClient(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func mapKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
