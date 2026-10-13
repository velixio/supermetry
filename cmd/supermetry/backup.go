package main

import (
	"archive/tar"
	"bufio"
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

	"github.com/jackc/pgx/v5"

	"supermetry/internal/appbackup"
	"supermetry/internal/db"
)

const (
	maxManifestSize = 1 << 20
	maxDumpSize     = int64(16) << 40
)

type backupManifest struct {
	CreatedAt         string `json:"created_at"`
	SupermetryVersion string `json:"supermetry_version,omitempty"`
	SchemaVersion     int    `json:"schema_version"`
	PGDumpVersion     string `json:"pg_dump_version"`
}

type backupRuntime struct {
	lookupPath     func(string) (string, error)
	command        func(context.Context, string, ...string) *exec.Cmd
	schemaVersion  func(context.Context, string) (int, error)
	databaseSize   func(context.Context, string) (int64, error)
	reachable      func(context.Context, string) error
	targetSchema   func(context.Context, string) (string, error)
	prepareRestore func(context.Context, string) error
	now            func() time.Time
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
		databaseSize: func(ctx context.Context, dsn string) (int64, error) {
			pool, err := db.Open(ctx, dsn)
			if err != nil {
				return 0, errors.New("database is not reachable")
			}
			defer pool.Close()
			var size int64
			if err := pool.QueryRow(ctx, `select pg_database_size(current_database())`).Scan(&size); err != nil {
				return 0, errors.New("could not read database size")
			}
			return size, nil
		},
		reachable: func(ctx context.Context, dsn string) error {
			pool, err := db.Open(ctx, dsn)
			if err != nil {
				return errors.New("target database is not reachable")
			}
			pool.Close()
			return nil
		},
		targetSchema: func(ctx context.Context, dsn string) (string, error) {
			pool, err := db.Open(ctx, dsn)
			if err != nil {
				return "", errors.New("target database is not reachable")
			}
			defer pool.Close()
			var schema string
			if err := pool.QueryRow(ctx, `select current_schema()`).Scan(&schema); err != nil || schema == "" {
				return "", errors.New("could not determine target schema")
			}
			return schema, nil
		},
		prepareRestore: dropInheritedRelations,
		now:            time.Now,
	}
}

// dropInheritedRelations works around a PostgreSQL restore ordering problem:
// pg_restore --clean cannot drop constraints inherited by partition children.
// current_schema() is the safest supported boundary for an app in a shared database.
func dropInheritedRelations(ctx context.Context, dsn string) error {
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		return errors.New("target database is not reachable")
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin inherited table cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
WITH RECURSIVE inherited(oid, depth) AS (
    SELECT inhrelid, 1 FROM pg_inherits
    UNION ALL
    SELECT child.inhrelid, parent.depth + 1
    FROM pg_inherits child
    JOIN inherited parent ON child.inhparent = parent.oid
)
SELECT namespace.nspname, class.relname, max(inherited.depth) AS depth
FROM inherited
JOIN pg_class class ON class.oid = inherited.oid
JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
WHERE namespace.nspname = current_schema()
  AND class.relkind IN ('r', 'p')
GROUP BY namespace.nspname, class.relname
ORDER BY depth DESC, namespace.nspname, class.relname`)
	if err != nil {
		return fmt.Errorf("list inherited tables before restore: %w", err)
	}
	defer rows.Close()
	type relation struct{ schema, name string }
	var relations []relation
	for rows.Next() {
		var item relation
		var depth int
		if err := rows.Scan(&item.schema, &item.name, &depth); err != nil {
			return fmt.Errorf("read inherited table before restore: %w", err)
		}
		relations = append(relations, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list inherited tables before restore: %w", err)
	}
	rows.Close()
	for _, item := range relations {
		identifier := pgx.Identifier{item.schema, item.name}.Sanitize()
		if _, err := tx.Exec(ctx, "DROP TABLE IF EXISTS "+identifier+" CASCADE"); err != nil {
			return fmt.Errorf("drop inherited table %s before restore: %w", identifier, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit inherited table cleanup: %w", err)
	}
	return nil
}

func backupCommand(ctx context.Context, args []string, output io.Writer, getenv func(string) string) error {
	return backupCommandWithRuntime(ctx, args, output, getenv, defaultBackupRuntime())
}

func backupCommandWithRuntime(ctx context.Context, args []string, output io.Writer, getenv func(string) string, rt backupRuntime) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		fmt.Fprintln(output, "usage: supermetry backup [--out FILE]")
		fmt.Fprintln(output, "SUPERMETRY_SECRET_KEY is not included; restore it separately to decrypt TOTP secrets.")
		fmt.Fprintln(output, "GeoIP .mmdb files are excluded because they can be downloaded again.")
		fmt.Fprintln(output, "Replay files are excluded; back them up separately from the replay storage directory.")
		flags.PrintDefaults()
	}
	defaultName := "supermetry-backup-" + rt.now().UTC().Format("20060102-150405") + ".tar.gz"
	outName := flags.String("out", defaultName, "output archive path")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: supermetry backup [--out FILE]")
	}
	dsn, err := appbackup.ResolveDatabaseURL(getenv)
	if err != nil {
		return err
	}
	appRuntime := appbackup.DefaultRuntime()
	appRuntime.LookupPath = rt.lookupPath
	appRuntime.Command = rt.command
	appRuntime.Now = rt.now
	appRuntime.InspectDatabase = func(ctx context.Context, dsn string) (int, int64, error) {
		schemaVersion, err := rt.schemaVersion(ctx, dsn)
		if err != nil {
			return 0, 0, err
		}
		databaseSize, err := rt.databaseSize(ctx, dsn)
		return schemaVersion, databaseSize, err
	}
	_, err = appbackup.CreateWithRuntime(ctx, appbackup.Options{
		DatabaseURL:       dsn,
		Directory:         filepath.Dir(*outName),
		Filename:          filepath.Base(*outName),
		SupermetryVersion: version,
	}, appRuntime)
	if err != nil {
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
		fmt.Fprintln(output, "usage: supermetry restore FILE --yes")
		fmt.Fprintln(output, "WARNING: restore replaces existing data.")
		fmt.Fprintln(output, "SUPERMETRY_SECRET_KEY is not in the archive; restore it separately to decrypt TOTP secrets.")
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
	latestMigrationVersion, err := db.LatestMigrationVersion()
	if err != nil {
		return fmt.Errorf("read embedded migration version: %w", err)
	}
	if manifest.SchemaVersion > latestMigrationVersion {
		return fmt.Errorf("backup schema version %d is newer than this binary (latest migration %d)", manifest.SchemaVersion, latestMigrationVersion)
	}
	dsn := getenv("SUPERMETRY_DATABASE_URL")
	if dsn == "" {
		return errors.New("SUPERMETRY_DATABASE_URL is required")
	}
	pgEnv, err := postgresEnvironment(dsn)
	if err != nil {
		return fmt.Errorf("SUPERMETRY_DATABASE_URL: %w", err)
	}
	if err := rt.reachable(ctx, dsn); err != nil {
		return errors.New("target database is not reachable")
	}
	targetSchema, err := rt.targetSchema(ctx, dsn)
	if err != nil {
		return fmt.Errorf("determine target database schema: %w", err)
	}
	pgRestore, err := rt.lookupPath("pg_restore")
	if err != nil {
		return errors.New("pg_restore not found; install postgresql-client")
	}
	if err := validateRestoreDump(ctx, rt, pgRestore, dumpName, targetSchema, pgEnv); err != nil {
		return err
	}
	if err := dryRunRestoreDump(ctx, rt, pgRestore, dumpName, pgEnv); err != nil {
		return err
	}

	fmt.Fprintln(output, "WARNING: restore replaces existing data in the target database.")
	if err := rt.prepareRestore(ctx, dsn); err != nil {
		return fmt.Errorf("prepare target database for restore: %w", err)
	}
	argv := []string{"--clean", "--if-exists", "--no-owner", "--single-transaction", "--exit-on-error", "--dbname", pgEnv["PGDATABASE"], dumpName}
	cmd := postgresCommandWithRuntime(ctx, rt, pgRestore, argv, pgEnv)
	cmd.Stdout = output
	var commandErrors limitedBuffer
	cmd.Stderr = &commandErrors
	if err := cmd.Run(); err != nil {
		return postgresClientError("pg_restore", err, commandErrors.String())
	}
	fmt.Fprintln(output, "Restore completed. Ensure SUPERMETRY_SECRET_KEY is restored separately for encrypted TOTP secrets.")
	return nil
}

func validateRestoreDump(ctx context.Context, rt backupRuntime, pgRestore, dumpName, targetSchema string, pgEnv map[string]string) error {
	cmd := postgresCommandWithRuntime(ctx, rt, pgRestore, []string{"--list", dumpName}, pgEnv)
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("validate database dump: read pg_restore --list output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("validate database dump: %w", postgresClientError("pg_restore --list", err, stderr.String()))
	}
	hasEntry := false
	hasSchemaMigrations := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, ";") {
			hasEntry = true
			fields := strings.Fields(line)
			if len(fields) >= 6 && fields[3] == "TABLE" && fields[4] == targetSchema && fields[5] == "schema_migrations" {
				hasSchemaMigrations = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("validate database dump: read pg_restore --list output: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("validate database dump: %w", postgresClientError("pg_restore --list", err, stderr.String()))
	}
	if !hasEntry {
		return errors.New("validate database dump: pg_restore --list contains no entries")
	}
	if !hasSchemaMigrations {
		return fmt.Errorf("validate database dump: archive is not a Supermetry database in schema %q", targetSchema)
	}
	return nil
}

// dryRunRestoreDump reads the whole dump to stdout; pg_restore 18 refuses to run
// without --file or --dbname.
func dryRunRestoreDump(ctx context.Context, rt backupRuntime, pgRestore, dumpName string, pgEnv map[string]string) error {
	cmd := postgresCommandWithRuntime(ctx, rt, pgRestore, []string{"--file", "-", dumpName}, pgEnv)
	cmd.Stdout = io.Discard
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("validate complete database dump: %w", postgresClientError("pg_restore dry run", err, stderr.String()))
	}
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
	dump, err := os.CreateTemp("", "supermetry-restore-*.dump")
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
				return "", false, false, errors.New("usage: supermetry restore FILE --yes")
			}
			archiveName = arg
		}
	}
	if help {
		return "", confirmed, true, nil
	}
	if archiveName == "" {
		return "", false, false, errors.New("usage: supermetry restore FILE --yes")
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
