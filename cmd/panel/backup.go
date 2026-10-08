package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"panel/internal/db"
)

// Keep this in sync with the highest embedded migration in internal/db/migrations.
const latestMigrationVersion = 25

const (
	maxManifestSize = 1 << 20
	maxDumpSize     = int64(16) << 40
)

type backupManifest struct {
	CreatedAt     string `json:"created_at"`
	PanelVersion  string `json:"panel_version,omitempty"`
	SchemaVersion int    `json:"schema_version"`
	PGDumpVersion string `json:"pg_dump_version"`
}

type backupRuntime struct {
	lookupPath    func(string) (string, error)
	command       func(context.Context, string, ...string) *exec.Cmd
	schemaVersion func(context.Context, string) (int, error)
	reachable     func(context.Context, string) error
	now           func() time.Time
}

func defaultBackupRuntime() backupRuntime {
	return backupRuntime{
		lookupPath: exec.LookPath,
		command:    exec.CommandContext,
		schemaVersion: func(ctx context.Context, dsn string) (int, error) {
			pool, err := db.Open(ctx, dsn)
			if err != nil {
				return 0, errors.New("database is not reachable")
			}
			defer pool.Close()
			var schemaVersion int
			if err := pool.QueryRow(ctx, `select coalesce(max(version), 0) from schema_migrations`).Scan(&schemaVersion); err != nil {
				return 0, errors.New("could not read schema_migrations")
			}
			return schemaVersion, nil
		},
		reachable: func(ctx context.Context, dsn string) error {
			pool, err := db.Open(ctx, dsn)
			if err != nil {
				return errors.New("target database is not reachable")
			}
			pool.Close()
			return nil
		},
		now: time.Now,
	}
}

func backupCommand(ctx context.Context, args []string, output io.Writer, getenv func(string) string) error {
	return backupCommandWithRuntime(ctx, args, output, getenv, defaultBackupRuntime())
}

func backupCommandWithRuntime(ctx context.Context, args []string, output io.Writer, getenv func(string) string, rt backupRuntime) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		fmt.Fprintln(output, "usage: panel backup [--out FILE]")
		fmt.Fprintln(output, "PANEL_SECRET_KEY is not included; restore it separately to decrypt TOTP secrets.")
		fmt.Fprintln(output, "GeoIP .mmdb files are excluded because they can be downloaded again.")
		fmt.Fprintln(output, "Replay files are excluded; back them up separately from the replay storage directory.")
		flags.PrintDefaults()
	}
	defaultName := "panel-backup-" + rt.now().UTC().Format("20060102-150405") + ".tar.gz"
	outName := flags.String("out", defaultName, "output archive path")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: panel backup [--out FILE]")
	}
	dsn := getenv("PANEL_DATABASE_URL")
	if dsn == "" {
		return errors.New("PANEL_DATABASE_URL is required")
	}
	pgEnv, err := postgresEnvironment(dsn)
	if err != nil {
		return fmt.Errorf("PANEL_DATABASE_URL: %w", err)
	}
	pgDump, err := rt.lookupPath("pg_dump")
	if err != nil {
		return errors.New("pg_dump not found; install postgresql-client")
	}
	pgDumpVersion, err := postgresClientVersion(ctx, rt, pgDump, pgEnv)
	if err != nil {
		return err
	}
	schemaVersion, err := rt.schemaVersion(ctx, dsn)
	if err != nil {
		return fmt.Errorf("read database schema version: %w", err)
	}

	dumpFile, err := os.CreateTemp("", "panel-database-*.dump")
	if err != nil {
		return fmt.Errorf("create temporary dump: %w", err)
	}
	dumpName := dumpFile.Name()
	defer os.Remove(dumpName)
	cmd := postgresCommandWithRuntime(ctx, rt, pgDump, []string{"--format=custom", "--no-owner"}, pgEnv)
	var commandErrors limitedBuffer
	cmd.Stderr = &commandErrors
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = dumpFile.Close()
		return fmt.Errorf("open pg_dump output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = dumpFile.Close()
		return postgresClientError("pg_dump", err, commandErrors.String())
	}
	written, copyErr := io.Copy(dumpFile, io.LimitReader(stdout, maxDumpSize+1))
	if copyErr != nil || written > maxDumpSize {
		_ = cmd.Process.Kill()
	}
	runErr := cmd.Wait()
	closeErr := dumpFile.Close()
	if copyErr != nil {
		return fmt.Errorf("write temporary dump: %w", copyErr)
	}
	if written > maxDumpSize {
		return fmt.Errorf("pg_dump output exceeds %d bytes", maxDumpSize)
	}
	if runErr != nil {
		return postgresClientError("pg_dump", runErr, commandErrors.String())
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary dump: %w", closeErr)
	}

	manifest := backupManifest{
		CreatedAt:     rt.now().UTC().Format(time.RFC3339),
		SchemaVersion: schemaVersion,
		PGDumpVersion: pgDumpVersion,
	}
	if version != "" && version != "dev" {
		manifest.PanelVersion = version
	}
	if err := writeBackupArchive(*outName, dumpName, manifest); err != nil {
		return err
	}
	fmt.Fprintf(output, "Backup written to %s\n", *outName)
	return nil
}

func restoreCommand(ctx context.Context, args []string, output io.Writer, getenv func(string) string) error {
	return restoreCommandWithRuntime(ctx, args, output, getenv, defaultBackupRuntime())
}

func restoreCommandWithRuntime(ctx context.Context, args []string, output io.Writer, getenv func(string) string, rt backupRuntime) error {
	archiveName, confirmed, help, err := parseRestoreArgs(args)
	if err != nil {
		return err
	}
	if help {
		fmt.Fprintln(output, "usage: panel restore FILE --yes")
		fmt.Fprintln(output, "WARNING: restore replaces existing data.")
		fmt.Fprintln(output, "PANEL_SECRET_KEY is not in the archive; restore it separately to decrypt TOTP secrets.")
		return nil
	}
	if !confirmed {
		return errors.New("restore replaces existing data; re-run with --yes to confirm")
	}

	manifest, dumpName, err := extractBackupArchive(archiveName)
	if err != nil {
		return err
	}
	defer os.Remove(dumpName)
	if manifest.SchemaVersion > latestMigrationVersion {
		return fmt.Errorf("backup schema version %d is newer than this binary (latest migration %d)", manifest.SchemaVersion, latestMigrationVersion)
	}
	dsn := getenv("PANEL_DATABASE_URL")
	if dsn == "" {
		return errors.New("PANEL_DATABASE_URL is required")
	}
	pgEnv, err := postgresEnvironment(dsn)
	if err != nil {
		return fmt.Errorf("PANEL_DATABASE_URL: %w", err)
	}
	if err := rt.reachable(ctx, dsn); err != nil {
		return errors.New("target database is not reachable")
	}
	pgRestore, err := rt.lookupPath("pg_restore")
	if err != nil {
		return errors.New("pg_restore not found; install postgresql-client")
	}

	fmt.Fprintln(output, "WARNING: restore replaces existing data in the target database.")
	argv := []string{"--clean", "--if-exists", "--no-owner", "--dbname", pgEnv["PGDATABASE"], dumpName}
	cmd := postgresCommandWithRuntime(ctx, rt, pgRestore, argv, pgEnv)
	cmd.Stdout = output
	var commandErrors limitedBuffer
	cmd.Stderr = &commandErrors
	if err := cmd.Run(); err != nil {
		return postgresClientError("pg_restore", err, commandErrors.String())
	}
	fmt.Fprintln(output, "Restore completed. Ensure PANEL_SECRET_KEY is restored separately for encrypted TOTP secrets.")
	return nil
}

func postgresEnvironment(dsn string) (map[string]string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, errors.New("URL scheme must be postgres or postgresql")
	}
	if u.Hostname() == "" || u.User == nil || u.User.Username() == "" {
		return nil, errors.New("URL must include a host and user")
	}
	database, err := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/"))
	if err != nil || database == "" || strings.Contains(database, "/") {
		return nil, errors.New("URL must include one database name")
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	password, _ := u.User.Password()
	sslmode := u.Query().Get("sslmode")
	if sslmode == "" {
		sslmode = "prefer"
	}
	return map[string]string{
		"PGHOST":     u.Hostname(),
		"PGPORT":     port,
		"PGUSER":     u.User.Username(),
		"PGPASSWORD": password,
		"PGDATABASE": database,
		"PGSSLMODE":  sslmode,
	}, nil
}

func postgresCommand(ctx context.Context, executable string, args []string, pgEnv map[string]string) *exec.Cmd {
	return postgresCommandWithRuntime(ctx, defaultBackupRuntime(), executable, args, pgEnv)
}

func postgresCommandWithRuntime(ctx context.Context, rt backupRuntime, executable string, args []string, pgEnv map[string]string) *exec.Cmd {
	cmd := rt.command(ctx, executable, args...)
	env := make([]string, 0, len(os.Environ())+len(pgEnv))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := pgEnv[key]; !replaced {
			env = append(env, item)
		}
	}
	for _, key := range []string{"PGHOST", "PGPORT", "PGUSER", "PGPASSWORD", "PGDATABASE", "PGSSLMODE"} {
		env = append(env, key+"="+pgEnv[key])
	}
	cmd.Env = env
	return cmd
}

func postgresClientVersion(ctx context.Context, rt backupRuntime, executable string, pgEnv map[string]string) (string, error) {
	cmd := postgresCommandWithRuntime(ctx, rt, executable, []string{"--version"}, pgEnv)
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", postgresClientError("pg_dump --version", err, stderr.String())
	}
	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return "", errors.New("pg_dump --version returned no version")
	}
	return result, nil
}

func writeBackupArchive(outputName, dumpName string, manifest backupManifest) (resultErr error) {
	dump, err := os.Open(dumpName)
	if err != nil {
		return fmt.Errorf("open temporary dump: %w", err)
	}
	defer dump.Close()
	dumpInfo, err := dump.Stat()
	if err != nil {
		return fmt.Errorf("stat temporary dump: %w", err)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode backup manifest: %w", err)
	}

	dir := filepath.Dir(outputName)
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(outputName)+".partial-*")
	if err != nil {
		return fmt.Errorf("create backup archive: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		if resultErr != nil {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure backup archive: %w", err)
	}
	gz := gzip.NewWriter(temporary)
	tw := tar.NewWriter(gz)
	fail := func(err error) error {
		return errors.Join(err, tw.Close(), gz.Close(), temporary.Close())
	}
	if err := writeTarEntry(tw, "database.dump", dumpInfo.Size(), dump); err != nil {
		return fail(err)
	}
	if err := writeTarEntry(tw, "manifest.json", int64(len(manifestBytes)), strings.NewReader(string(manifestBytes))); err != nil {
		return fail(err)
	}
	if err := errors.Join(tw.Close(), gz.Close(), temporary.Sync(), temporary.Close()); err != nil {
		return fmt.Errorf("finish backup archive: %w", err)
	}
	if err := os.Rename(temporaryName, outputName); err != nil {
		return fmt.Errorf("publish backup archive: %w", err)
	}
	return nil
}

func writeTarEntry(tw *tar.Writer, name string, size int64, source io.Reader) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: time.Unix(0, 0).UTC()}); err != nil {
		return fmt.Errorf("write %s header: %w", name, err)
	}
	written, err := io.CopyN(tw, source, size)
	if err != nil {
		return fmt.Errorf("write %s after %d bytes: %w", name, written, err)
	}
	return nil
}

func extractBackupArchive(archiveName string) (backupManifest, string, error) {
	f, err := os.Open(archiveName)
	if err != nil {
		return backupManifest{}, "", fmt.Errorf("open backup archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return backupManifest{}, "", fmt.Errorf("read backup archive: %w", err)
	}
	defer gz.Close()
	dump, err := os.CreateTemp("", "panel-restore-*.dump")
	if err != nil {
		return backupManifest{}, "", fmt.Errorf("create temporary restore file: %w", err)
	}
	dumpName := dump.Name()
	keepDump := false
	defer func() {
		_ = dump.Close()
		if !keepDump {
			_ = os.Remove(dumpName)
		}
	}()

	tr := tar.NewReader(gz)
	var manifest backupManifest
	var haveManifest, haveDump bool
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return backupManifest{}, "", fmt.Errorf("read backup archive: %w", nextErr)
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			return backupManifest{}, "", fmt.Errorf("read backup archive: %s is not a regular file", hdr.Name)
		}
		switch hdr.Name {
		case "database.dump":
			if haveDump || hdr.Size < 0 || hdr.Size > maxDumpSize {
				return backupManifest{}, "", errors.New("read backup archive: invalid database.dump")
			}
			if _, err := io.CopyN(dump, tr, hdr.Size); err != nil {
				return backupManifest{}, "", fmt.Errorf("read backup archive database.dump: %w", err)
			}
			haveDump = true
		case "manifest.json":
			if haveManifest || hdr.Size < 0 || hdr.Size > maxManifestSize {
				return backupManifest{}, "", errors.New("read backup archive: invalid manifest.json")
			}
			manifestBytes, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
			if err != nil {
				return backupManifest{}, "", fmt.Errorf("read backup archive manifest: %w", err)
			}
			decoder := json.NewDecoder(strings.NewReader(string(manifestBytes)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&manifest); err != nil {
				return backupManifest{}, "", fmt.Errorf("read backup archive manifest: %w", err)
			}
			if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
				return backupManifest{}, "", errors.New("read backup archive manifest: trailing JSON data")
			}
			haveManifest = true
		default:
			return backupManifest{}, "", fmt.Errorf("read backup archive: unexpected entry %q", hdr.Name)
		}
	}
	if !haveManifest || !haveDump {
		return backupManifest{}, "", errors.New("read backup archive: database.dump and manifest.json are required")
	}
	if _, err := time.Parse(time.RFC3339, manifest.CreatedAt); err != nil || manifest.SchemaVersion < 0 || strings.TrimSpace(manifest.PGDumpVersion) == "" {
		return backupManifest{}, "", errors.New("read backup archive: invalid manifest")
	}
	if err := dump.Close(); err != nil {
		return backupManifest{}, "", fmt.Errorf("close temporary restore file: %w", err)
	}
	keepDump = true
	return manifest, dumpName, nil
}

func parseRestoreArgs(args []string) (archiveName string, confirmed, help bool, err error) {
	for _, arg := range args {
		switch arg {
		case "--yes":
			confirmed = true
		case "--help", "-h":
			help = true
		default:
			if strings.HasPrefix(arg, "-") || archiveName != "" {
				return "", false, false, errors.New("usage: panel restore FILE --yes")
			}
			archiveName = arg
		}
	}
	if help {
		return "", confirmed, true, nil
	}
	if archiveName == "" {
		return "", false, false, errors.New("usage: panel restore FILE --yes")
	}
	return archiveName, confirmed, false, nil
}

func postgresClientError(name string, runErr error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return fmt.Errorf("%s failed: %w", name, runErr)
	}
	return fmt.Errorf("%s failed: %s", name, stderr)
}

type limitedBuffer struct {
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 64 << 10
	remaining := limit - len(b.data)
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		b.data = append(b.data, p[:remaining]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return string(b.data) }
