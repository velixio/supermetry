// Package appbackup creates and verifies complete Supermetry database backups.
package appbackup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"supermetry/internal/db"
)

const (
	maxManifestSize = 1 << 20
	maxDumpSize     = int64(16) << 40
	minimumHeadroom = uint64(64 << 20)
)

// Manifest describes the database dump contained in an archive.
type Manifest struct {
	CreatedAt         string `json:"created_at"`
	SupermetryVersion string `json:"supermetry_version,omitempty"`
	SchemaVersion     int    `json:"schema_version"`
	PGDumpVersion     string `json:"pg_dump_version"`
}

// Options identifies the database and the explicit directory where backup
// staging and the final archive must be written.
type Options struct {
	DatabaseURL       string
	Directory         string
	Filename          string
	SupermetryVersion string
}

// Result is safe to persist in updater state and audit records.
type Result struct {
	Path          string
	SHA256        string
	Size          int64
	SchemaVersion int
	CreatedAt     time.Time
}

// Runtime permits deterministic tests without running PostgreSQL clients.
// Production callers should use Create and Verify.
type Runtime struct {
	LookupPath      func(string) (string, error)
	Command         func(context.Context, string, ...string) *exec.Cmd
	InspectDatabase func(context.Context, string) (schemaVersion int, databaseSize int64, err error)
	FreeBytes       func(string) (uint64, error)
	Now             func() time.Time
}

// ResolveDatabaseURL uses the same precedence and defaults as the application
// config: a URL wins, otherwise split Supermetry variables fall back to the
// bundled PostgreSQL variables.
func ResolveDatabaseURL(getenv func(string) string) (string, error) {
	if raw := getenv("SUPERMETRY_DATABASE_URL"); raw != "" {
		return raw, nil
	}
	host := getenv("SUPERMETRY_DATABASE_HOST")
	if host == "" {
		return "", errors.New("SUPERMETRY_DATABASE_URL is required (or SUPERMETRY_DATABASE_HOST, with the user and password as separate settings)")
	}
	first := func(values ...string) string {
		for _, value := range values {
			if value != "" {
				return value
			}
		}
		return ""
	}
	u := url.URL{
		Scheme: "postgres",
		User: url.UserPassword(
			first(getenv("SUPERMETRY_DATABASE_USER"), getenv("POSTGRES_USER"), "supermetry"),
			first(getenv("SUPERMETRY_DATABASE_PASSWORD"), getenv("POSTGRES_PASSWORD"), "supermetry"),
		),
		Host:     net.JoinHostPort(host, first(getenv("SUPERMETRY_DATABASE_PORT"), "5432")),
		Path:     "/" + first(getenv("SUPERMETRY_DATABASE_NAME"), getenv("POSTGRES_DB"), "supermetry"),
		RawQuery: "sslmode=" + first(getenv("SUPERMETRY_DATABASE_SSLMODE"), "disable"),
	}
	return u.String(), nil
}

// Create writes, validates, hashes, and atomically publishes a backup.
func Create(ctx context.Context, opts Options) (Result, error) {
	return CreateWithRuntime(ctx, opts, defaultRuntime())
}

// DefaultRuntime returns the production process and filesystem dependencies.
// It is exposed for command adapters that need to override selected callbacks.
func DefaultRuntime() Runtime { return defaultRuntime() }

// CreateWithRuntime is Create with injectable process and filesystem probes.
func CreateWithRuntime(ctx context.Context, opts Options, rt Runtime) (result Result, resultErr error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(opts.Directory) == "" {
		return Result{}, errors.New("backup directory is required")
	}
	if opts.Filename == "" {
		opts.Filename = "supermetry-backup-" + rt.Now().UTC().Format("20060102-150405") + ".tar.gz"
	}
	if filepath.Base(opts.Filename) != opts.Filename || opts.Filename == "." || opts.Filename == ".." {
		return Result{}, errors.New("backup filename must not contain a directory")
	}
	pgEnv, secrets, err := postgresEnvironment(opts.DatabaseURL)
	if err != nil {
		return Result{}, fmt.Errorf("database configuration: %w", err)
	}
	schemaVersion, databaseSize, err := rt.InspectDatabase(ctx, opts.DatabaseURL)
	if err != nil {
		return Result{}, sanitizeError("inspect database", err, secrets)
	}
	if databaseSize < 0 {
		return Result{}, errors.New("inspect database: invalid database size")
	}
	free, err := rt.FreeBytes(opts.Directory)
	if err != nil {
		return Result{}, fmt.Errorf("check backup free space: %w", err)
	}
	required := requiredHeadroom(uint64(databaseSize))
	if free < required {
		return Result{}, fmt.Errorf("insufficient backup free space: %d bytes available, %d required", free, required)
	}
	pgDump, err := rt.LookupPath("pg_dump")
	if err != nil {
		return Result{}, errors.New("pg_dump not found; install postgresql-client")
	}
	pgRestore, err := rt.LookupPath("pg_restore")
	if err != nil {
		return Result{}, errors.New("pg_restore not found; install postgresql-client")
	}
	pgDumpVersion, err := clientVersion(ctx, rt, pgDump, pgEnv, secrets)
	if err != nil {
		return Result{}, err
	}

	dump, err := os.CreateTemp(opts.Directory, ".supermetry-database-*.dump")
	if err != nil {
		return Result{}, fmt.Errorf("create temporary dump: %w", err)
	}
	dumpName := dump.Name()
	defer os.Remove(dumpName)
	if err := dump.Chmod(0o600); err != nil {
		_ = dump.Close()
		return Result{}, fmt.Errorf("secure temporary dump: %w", err)
	}
	cmd := postgresCommand(ctx, rt, pgDump, []string{"--format=custom", "--no-owner"}, pgEnv)
	var stderr limitedBuffer
	cmd.Stdout = &boundedWriter{writer: dump, remaining: maxDumpSize}
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	closeErr := dump.Close()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	if runErr != nil {
		return Result{}, clientError("pg_dump", runErr, stderr.String(), secrets)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("close temporary dump: %w", closeErr)
	}
	if err := verifyDump(ctx, rt, pgRestore, dumpName, secrets); err != nil {
		return Result{}, err
	}

	createdAt := rt.Now().UTC()
	manifest := Manifest{
		CreatedAt:         createdAt.Format(time.RFC3339),
		SupermetryVersion: normalizedVersion(opts.SupermetryVersion),
		SchemaVersion:     schemaVersion,
		PGDumpVersion:     pgDumpVersion,
	}
	finalName := filepath.Join(opts.Directory, opts.Filename)
	temporary, err := os.CreateTemp(opts.Directory, "."+opts.Filename+".partial-*")
	if err != nil {
		return Result{}, fmt.Errorf("create backup archive: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if resultErr != nil {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return Result{}, fmt.Errorf("secure backup archive: %w", err)
	}
	if err := writeArchive(ctx, temporary, dumpName, manifest); err != nil {
		return Result{}, err
	}
	if err := temporary.Sync(); err != nil {
		return Result{}, fmt.Errorf("sync backup archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Result{}, fmt.Errorf("close backup archive: %w", err)
	}
	if _, err := VerifyWithRuntime(ctx, temporaryName, rt); err != nil {
		return Result{}, fmt.Errorf("verify created backup: %w", err)
	}
	sum, size, err := hashFile(ctx, temporaryName)
	if err != nil {
		return Result{}, err
	}
	if err := os.Rename(temporaryName, finalName); err != nil {
		return Result{}, fmt.Errorf("publish backup archive: %w", err)
	}
	if err := syncDirectory(opts.Directory); err != nil {
		_ = os.Remove(finalName)
		return Result{}, err
	}
	return Result{Path: finalName, SHA256: sum, Size: size, SchemaVersion: schemaVersion, CreatedAt: createdAt}, nil
}

// Verify validates archive structure and asks pg_restore to read the complete
// custom-format dump without connecting to a database.
func Verify(ctx context.Context, archivePath string) (Manifest, error) {
	return VerifyWithRuntime(ctx, archivePath, defaultRuntime())
}

// VerifyWithRuntime is Verify with injectable PostgreSQL clients.
func VerifyWithRuntime(ctx context.Context, archivePath string, rt Runtime) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	pgRestore, err := rt.LookupPath("pg_restore")
	if err != nil {
		return Manifest{}, errors.New("pg_restore not found; install postgresql-client")
	}
	manifest, dumpName, err := extractArchive(ctx, archivePath, filepath.Dir(archivePath), rt.FreeBytes)
	if err != nil {
		return Manifest{}, err
	}
	defer os.Remove(dumpName)
	if err := verifyDump(ctx, rt, pgRestore, dumpName, nil); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func defaultRuntime() Runtime {
	return Runtime{
		LookupPath: exec.LookPath,
		Command:    exec.CommandContext,
		InspectDatabase: func(ctx context.Context, dsn string) (int, int64, error) {
			pool, err := db.Open(ctx, dsn)
			if err != nil {
				return 0, 0, errors.New("database is not reachable")
			}
			defer pool.Close()
			var schemaVersion int
			var size int64
			if err := pool.QueryRow(ctx, `select coalesce(max(version), 0), pg_database_size(current_database()) from schema_migrations`).Scan(&schemaVersion, &size); err != nil {
				return 0, 0, errors.New("could not inspect database")
			}
			return schemaVersion, size, nil
		},
		FreeBytes: freeBytes,
		Now:       time.Now,
	}
}

func requiredHeadroom(databaseSize uint64) uint64 {
	if databaseSize > (^uint64(0)-minimumHeadroom)/3 {
		return ^uint64(0)
	}
	return databaseSize*3 + minimumHeadroom
}

func normalizedVersion(value string) string {
	if value == "" || value == "dev" {
		return ""
	}
	return value
}

func postgresEnvironment(dsn string) (map[string]string, []string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, nil, errors.New("invalid PostgreSQL URL")
	}
	if u.Hostname() == "" || u.User == nil || u.User.Username() == "" {
		return nil, nil, errors.New("PostgreSQL URL must include a host and user")
	}
	database, err := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/"))
	if err != nil || database == "" || strings.Contains(database, "/") {
		return nil, nil, errors.New("PostgreSQL URL must include one database name")
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
		"PGHOST": u.Hostname(), "PGPORT": port, "PGUSER": u.User.Username(),
		"PGPASSWORD": password, "PGDATABASE": database, "PGSSLMODE": sslmode,
	}, []string{dsn, password, url.QueryEscape(password), url.PathEscape(password)}, nil
}

func postgresCommand(ctx context.Context, rt Runtime, executable string, args []string, pgEnv map[string]string) *exec.Cmd {
	cmd := rt.Command(ctx, executable, args...)
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

func clientVersion(ctx context.Context, rt Runtime, executable string, pgEnv map[string]string, secrets []string) (string, error) {
	cmd := postgresCommand(ctx, rt, executable, []string{"--version"}, pgEnv)
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", clientError("pg_dump --version", err, stderr.String(), secrets)
	}
	result := strings.TrimSpace(stdout.String())
	if result == "" {
		return "", errors.New("pg_dump --version returned no version")
	}
	return result, nil
}

func verifyDump(ctx context.Context, rt Runtime, pgRestore, dumpName string, secrets []string) error {
	cmd := rt.Command(ctx, pgRestore, "--list", dumpName)
	var stderr limitedBuffer
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("validate database dump: read pg_restore --list output: %w", err)
	}
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return clientError("validate database dump", err, stderr.String(), secrets)
	}
	hasEntry, hasSchemaMigrations := false, false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		hasEntry = true
		fields := strings.Fields(line)
		if len(fields) >= 6 && fields[3] == "TABLE" && fields[5] == "schema_migrations" {
			hasSchemaMigrations = true
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("validate database dump: read pg_restore --list output: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return clientError("validate database dump", err, stderr.String(), secrets)
	}
	if !hasEntry {
		return errors.New("validate database dump: pg_restore --list contains no entries")
	}
	if !hasSchemaMigrations {
		return errors.New("validate database dump: archive is not a Supermetry database")
	}
	cmd = rt.Command(ctx, pgRestore, "--file", "-", dumpName)
	cmd.Stdout = io.Discard
	stderr = limitedBuffer{}
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return clientError("validate complete database dump", err, stderr.String(), secrets)
	}
	return nil
}

func writeArchive(ctx context.Context, destination io.Writer, dumpName string, manifest Manifest) error {
	dump, err := os.Open(dumpName)
	if err != nil {
		return fmt.Errorf("open temporary dump: %w", err)
	}
	defer dump.Close()
	info, err := dump.Stat()
	if err != nil {
		return fmt.Errorf("stat temporary dump: %w", err)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode backup manifest: %w", err)
	}
	gz := gzip.NewWriter(destination)
	tw := tar.NewWriter(gz)
	fail := func(cause error) error { return errors.Join(cause, tw.Close(), gz.Close()) }
	if err := writeTarEntry(ctx, tw, "database.dump", info.Size(), dump); err != nil {
		return fail(err)
	}
	if err := writeTarEntry(ctx, tw, "manifest.json", int64(len(manifestBytes)), strings.NewReader(string(manifestBytes))); err != nil {
		return fail(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return fmt.Errorf("finish backup archive: %w", err)
	}
	return nil
}

func writeTarEntry(ctx context.Context, tw *tar.Writer, name string, size int64, source io.Reader) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: time.Unix(0, 0).UTC()}); err != nil {
		return fmt.Errorf("write %s header: %w", name, err)
	}
	if _, err := io.CopyN(tw, &contextReader{ctx: ctx, reader: source}, size); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func extractArchive(ctx context.Context, archiveName, tempDir string, freeBytes func(string) (uint64, error)) (Manifest, string, error) {
	f, err := os.Open(archiveName)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("open backup archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("read backup archive: %w", err)
	}
	defer gz.Close()
	dump, err := os.CreateTemp(tempDir, ".supermetry-verify-*.dump")
	if err != nil {
		return Manifest{}, "", fmt.Errorf("create temporary verification dump: %w", err)
	}
	dumpName, keep := dump.Name(), false
	defer func() {
		_ = dump.Close()
		if !keep {
			_ = os.Remove(dumpName)
		}
	}()
	if err := dump.Chmod(0o600); err != nil {
		return Manifest{}, "", fmt.Errorf("secure verification dump: %w", err)
	}
	tr := tar.NewReader(&contextReader{ctx: ctx, reader: gz})
	var manifest Manifest
	var haveManifest, haveDump bool
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return Manifest{}, "", fmt.Errorf("read backup archive: %w", nextErr)
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			return Manifest{}, "", fmt.Errorf("read backup archive: %s is not a regular file", hdr.Name)
		}
		switch hdr.Name {
		case "database.dump":
			if haveDump || hdr.Size < 0 || hdr.Size > maxDumpSize {
				return Manifest{}, "", errors.New("read backup archive: invalid database.dump")
			}
			free, err := freeBytes(tempDir)
			if err != nil {
				return Manifest{}, "", fmt.Errorf("check verification free space: %w", err)
			}
			if uint64(hdr.Size) > free || free-uint64(hdr.Size) < minimumHeadroom {
				return Manifest{}, "", fmt.Errorf("insufficient verification free space: %d bytes available, %d required", free, uint64(hdr.Size)+minimumHeadroom)
			}
			if _, err := io.CopyN(dump, &contextReader{ctx: ctx, reader: tr}, hdr.Size); err != nil {
				return Manifest{}, "", fmt.Errorf("read backup archive database.dump: %w", err)
			}
			haveDump = true
		case "manifest.json":
			if haveManifest || hdr.Size < 0 || hdr.Size > maxManifestSize {
				return Manifest{}, "", errors.New("read backup archive: invalid manifest.json")
			}
			decoder := json.NewDecoder(io.LimitReader(tr, hdr.Size))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&manifest); err != nil {
				return Manifest{}, "", fmt.Errorf("read backup archive manifest: %w", err)
			}
			if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
				return Manifest{}, "", errors.New("read backup archive manifest: trailing JSON data")
			}
			haveManifest = true
		default:
			return Manifest{}, "", fmt.Errorf("read backup archive: unexpected entry %q", hdr.Name)
		}
	}
	if !haveManifest || !haveDump {
		return Manifest{}, "", errors.New("read backup archive: database.dump and manifest.json are required")
	}
	if _, err := time.Parse(time.RFC3339, manifest.CreatedAt); err != nil || manifest.SchemaVersion < 0 || strings.TrimSpace(manifest.PGDumpVersion) == "" {
		return Manifest{}, "", errors.New("read backup archive: invalid manifest")
	}
	if err := dump.Close(); err != nil {
		return Manifest{}, "", fmt.Errorf("close verification dump: %w", err)
	}
	keep = true
	return manifest, dumpName, nil
}

func hashFile(ctx context.Context, path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open backup for hashing: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, &contextReader{ctx: ctx, reader: f})
	if err != nil {
		return "", 0, fmt.Errorf("hash backup archive: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync backup directory: %w", err)
	}
	return nil
}

func sanitizeError(prefix string, err error, secrets []string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return fmt.Errorf("%s: %s", prefix, message)
}

func clientError(name string, runErr error, stderr string, secrets []string) error {
	message := strings.TrimSpace(stderr)
	if message == "" {
		message = runErr.Error()
	}
	return sanitizeError(name+" failed", errors.New(message), secrets)
}

type limitedBuffer struct{ data []byte }

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

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("pg_dump output exceeds %d bytes", maxDumpSize)
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
