package updatebinary

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"supermetry/internal/appbackup"
	"supermetry/internal/updatehelper"
	"supermetry/internal/updater/release"
)

func TestBackendRunsVerifiedBinaryUpdate(t *testing.T) {
	t.Parallel()
	backend, fixture := newFixture(t, release.Schema{Version: 150, RollbackCompatible: false})
	job := updatehelper.Job{ID: "12345678", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	steps := []updatehelper.State{
		updatehelper.StatePreflight, updatehelper.StateDownload, updatehelper.StateArtifactVerified,
		updatehelper.StateBackup, updatehelper.StateStopping, updatehelper.StateActivating,
		updatehelper.StateRestarting, updatehelper.StateHealthCheck,
	}
	for _, step := range steps {
		if err := backend.RunStep(context.Background(), step, job); err != nil {
			t.Fatalf("RunStep(%s): %v", step, err)
		}
	}

	wantCommands := []commandCall{
		{executable: systemctlPath, args: []string{"stop", "supermetry.service"}},
		{executable: systemctlPath, args: []string{"start", "supermetry.service"}},
	}
	if !reflect.DeepEqual(fixture.runner.calls, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", fixture.runner.calls, wantCommands)
	}
	if fixture.backup.request.Executable != "/opt/supermetry/versions/v1.1.0/supermetry" {
		t.Fatalf("backup executable = %q", fixture.backup.request.Executable)
	}
	if !reflect.DeepEqual(fixture.backup.request.Environment, fixture.environment) {
		t.Fatalf("backup environment changed")
	}
	if !fixture.backup.verified {
		t.Fatal("mandatory backup was not verified")
	}
	current, err := fixture.fs.Readlink("/opt/supermetry/current")
	if err != nil {
		t.Fatal(err)
	}
	if current != "/opt/supermetry/versions/v1.2.0" {
		t.Fatalf("current link = %q", current)
	}
	oldBinary, err := fixture.fs.ReadFile("/opt/supermetry/versions/v1.1.0/supermetry")
	if err != nil || string(oldBinary) != "old binary" {
		t.Fatalf("old version was not preserved: %q, %v", oldBinary, err)
	}
	newBinary, err := fixture.fs.ReadFile("/opt/supermetry/versions/v1.2.0/supermetry")
	if err != nil || string(newBinary) != "new binary" {
		t.Fatalf("new binary = %q, %v", newBinary, err)
	}
	if len(fixture.fs.chowns) == 0 || len(fixture.fs.lchowns) == 0 {
		t.Fatal("version slot and current symlink were not assigned root ownership")
	}
}

func TestBackendRejectsOversizedArtifactBeforeActivation(t *testing.T) {
	t.Parallel()
	backend, fixture := newFixture(t, release.Schema{Version: 150})
	fixture.source.extraArtifactByte = true
	job := updatehelper.Job{ID: "12345679", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	if err := backend.RunStep(context.Background(), updatehelper.StatePreflight, job); err != nil {
		t.Fatal(err)
	}
	if err := backend.RunStep(context.Background(), updatehelper.StateDownload, job); err == nil {
		t.Fatal("oversized artifact accepted")
	}
	if len(fixture.runner.calls) != 0 {
		t.Fatalf("service command ran before verification: %#v", fixture.runner.calls)
	}
}

func TestBackendRecoveryRestoresOldVersionOnlyWhenSchemaAllows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		schema     release.Schema
		liveSchema int64
		wantErr    error
		wantOld    bool
	}{
		{name: "schema unchanged", schema: release.Schema{Version: 150}, liveSchema: 149, wantOld: true},
		{name: "manifest permits rollback", schema: release.Schema{Version: 150, RollbackCompatible: true}, liveSchema: 150, wantOld: true},
		{name: "migration blocks rollback", schema: release.Schema{Version: 150}, liveSchema: 150, wantErr: ErrRecoveryRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, fixture := newFixture(t, tt.schema)
			job := updatehelper.Job{ID: "12345680", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
			for _, step := range []updatehelper.State{updatehelper.StatePreflight, updatehelper.StateDownload, updatehelper.StateArtifactVerified, updatehelper.StateBackup, updatehelper.StateStopping, updatehelper.StateActivating} {
				if err := backend.RunStep(context.Background(), step, job); err != nil {
					t.Fatalf("RunStep(%s): %v", step, err)
				}
			}
			fixture.probe.schema = tt.liveSchema
			if tt.wantOld {
				fixture.probe.statuses = []HealthStatus{{Version: "v1.1.0", SchemaVersion: 149}}
			}
			err := backend.Recover(context.Background(), updatehelper.RecoveryFailSafely, job)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Recover error = %v, want %v", err, tt.wantErr)
			}
			current, readErr := fixture.fs.Readlink("/opt/supermetry/current")
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := "/opt/supermetry/versions/v1.2.0"
			if tt.wantOld {
				want = "/opt/supermetry/versions/v1.1.0"
			}
			if current != want {
				t.Fatalf("current link = %q, want %q", current, want)
			}
		})
	}
}

func TestBackendHealthRequiresExactTargetVersion(t *testing.T) {
	t.Parallel()
	backend, fixture := newFixture(t, release.Schema{Version: 150})
	fixture.probe.statuses = []HealthStatus{{Version: "v1.1.0", SchemaVersion: 149}}
	backend.healthTimeout = time.Millisecond
	backend.pollInterval = time.Millisecond
	job := updatehelper.Job{ID: "12345681", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	for _, step := range []updatehelper.State{updatehelper.StatePreflight, updatehelper.StateDownload, updatehelper.StateArtifactVerified, updatehelper.StateBackup, updatehelper.StateStopping, updatehelper.StateActivating, updatehelper.StateRestarting} {
		if err := backend.RunStep(context.Background(), step, job); err != nil {
			t.Fatal(err)
		}
	}
	err := backend.RunStep(context.Background(), updatehelper.StateHealthCheck, job)
	if !errors.Is(err, updatehelper.ErrRolledBack) {
		t.Fatalf("health error = %v, want verified rollback", err)
	}
	current, readErr := fixture.fs.Readlink("/opt/supermetry/current")
	if readErr != nil || current != "/opt/supermetry/versions/v1.1.0" {
		t.Fatalf("current link = %q, err=%v", current, readErr)
	}
}

type fixture struct {
	fs          *rootedFS
	runner      *fakeRunner
	source      *fakeSource
	backup      *fakeBackup
	probe       *fakeProbe
	environment []string
}

func newFixture(t *testing.T, schema release.Schema) (*Backend, *fixture) {
	t.Helper()
	root := t.TempDir()
	fs := &rootedFS{root: root}
	if err := fs.MkdirAll("/opt/supermetry/versions/v1.1.0", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile("/opt/supermetry/versions/v1.1.0/supermetry", []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.MkdirAll("/opt/supermetry", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.Symlink("/opt/supermetry/versions/v1.1.0", "/opt/supermetry/current"); err != nil {
		t.Fatal(err)
	}
	archive := testArchive(t, []byte("new binary"))
	sum := sha256.Sum256(archive)
	artifact := release.Artifact{Name: "supermetry_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Size: int64(len(archive)), SHA256: hex.EncodeToString(sum[:])}
	manifest := release.Manifest{
		FormatVersion: release.ManifestFormatVersion, Version: "v1.2.0", Commit: "0123456789abcdef0123456789abcdef01234567",
		PublishedAt: "2026-10-14T00:00:00Z", Stable: true, MinimumVersion: "v1.0.0", Schema: schema, Artifacts: []release.Artifact{artifact},
	}
	manifestBytes, err := release.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signatureBytes, err := release.MarshalSignature(release.DetachedSignature{KeyID: "test-key", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, manifestBytes))})
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := release.NewKeyring(map[string]ed25519.PublicKey{"test-key": public})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	source := &fakeSource{manifest: manifestBytes, signature: signatureBytes, artifact: archive}
	backup := &fakeBackup{}
	probe := &fakeProbe{schema: 149, statuses: []HealthStatus{{Version: "v1.2.0", SchemaVersion: schema.Version}}}
	environment := []string{"SUPERMETRY_DATABASE_HOST=db", "SUPERMETRY_DATABASE_USER=supermetry"}
	backend, err := New(Config{
		Descriptor: testDescriptor(), Client: source, Keyring: keyring, Runner: runner, Filesystem: fs,
		Backup: backup, Probe: probe, CurrentVersion: "v1.1.0", CurrentSchema: 149, Environment: environment,
		GOOS: "linux", GOARCH: "amd64", StagingDir: "/var/lib/supermetry-updater/staging",
		PollInterval: time.Millisecond, HealthTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend, &fixture{fs: fs, runner: runner, source: source, backup: backup, probe: probe, environment: environment}
}

func testDescriptor() updatehelper.DeploymentDescriptor {
	return updatehelper.DeploymentDescriptor{Version: 1, Mode: updatehelper.DeploymentBinary, ServiceName: "supermetry.service", VersionsDir: "/opt/supermetry/versions", CurrentLink: "/opt/supermetry/current", ComposeDir: "/opt/supermetry", ComposeService: "supermetry", BackupDir: "/var/lib/supermetry-updater/backups", ReadinessURL: "http://127.0.0.1:8080/readyz", SocketGroup: "supermetry-updater"}
}

func testArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gz)
	for name, contents := range map[string][]byte{"supermetry": binary, "LICENSE": []byte("license"), "NOTICE": []byte("notice"), "supermetry.service": []byte("service")} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type fakeSource struct {
	manifest, signature, artifact []byte
	extraArtifactByte             bool
}

func (s *fakeSource) FetchManifest(context.Context, string) ([]byte, []byte, error) {
	return bytes.Clone(s.manifest), bytes.Clone(s.signature), nil
}
func (s *fakeSource) FetchArtifact(_ context.Context, _ string, _ release.Artifact, destination io.Writer) error {
	data := bytes.Clone(s.artifact)
	if s.extraArtifactByte {
		data = append(data, 0)
	}
	_, err := destination.Write(data)
	return err
}

type commandCall struct {
	executable string
	args       []string
}
type fakeRunner struct{ calls []commandCall }

func (r *fakeRunner) Run(_ context.Context, executable string, args []string, _ updatehelper.RunOptions) (updatehelper.RunResult, error) {
	r.calls = append(r.calls, commandCall{executable: executable, args: append([]string(nil), args...)})
	return updatehelper.RunResult{}, nil
}

type fakeBackup struct {
	request  BackupRequest
	verified bool
}

func (b *fakeBackup) CreateAndVerify(_ context.Context, request BackupRequest) (appbackup.Result, error) {
	b.request = request
	b.verified = true
	return appbackup.Result{Path: request.OutputPath, SchemaVersion: 149}, nil
}

type fakeProbe struct {
	schema   int64
	statuses []HealthStatus
}

func (p *fakeProbe) CurrentSchema(context.Context) (int64, error) { return p.schema, nil }
func (p *fakeProbe) Check(context.Context, string) (HealthStatus, error) {
	if len(p.statuses) == 0 {
		return HealthStatus{}, errors.New("not ready")
	}
	status := p.statuses[0]
	if len(p.statuses) > 1 {
		p.statuses = p.statuses[1:]
	}
	return status, nil
}

type rootedFS struct {
	root            string
	chowns, lchowns []string
}

func (f *rootedFS) path(name string) string { return filepath.Join(f.root, filepath.Clean("/"+name)) }
func (f *rootedFS) logical(name string) string {
	if relative, err := filepath.Rel(f.root, name); err == nil {
		return "/" + filepath.ToSlash(relative)
	}
	return name
}
func (f *rootedFS) MkdirAll(name string, mode os.FileMode) error {
	return os.MkdirAll(f.path(name), mode)
}
func (f *rootedFS) Open(name string) (File, error) { return os.Open(f.path(name)) }
func (f *rootedFS) OpenFile(name string, flag int, mode os.FileMode) (File, error) {
	return os.OpenFile(f.path(name), flag, mode)
}
func (f *rootedFS) CreateTemp(dir, pattern string) (File, error) {
	return os.CreateTemp(f.path(dir), pattern)
}
func (f *rootedFS) Rename(oldName, newName string) error {
	return os.Rename(f.path(oldName), f.path(newName))
}
func (f *rootedFS) Remove(name string) error    { return os.Remove(f.path(name)) }
func (f *rootedFS) RemoveAll(name string) error { return os.RemoveAll(f.path(name)) }
func (f *rootedFS) Readlink(name string) (string, error) {
	value, err := os.Readlink(f.path(name))
	return f.logical(value), err
}
func (f *rootedFS) Symlink(oldName, newName string) error {
	return os.Symlink(f.path(oldName), f.path(newName))
}
func (f *rootedFS) Stat(name string) (os.FileInfo, error)     { return os.Stat(f.path(name)) }
func (f *rootedFS) Lstat(name string) (os.FileInfo, error)    { return os.Lstat(f.path(name)) }
func (f *rootedFS) Chmod(name string, mode os.FileMode) error { return os.Chmod(f.path(name), mode) }
func (f *rootedFS) Chown(name string, uid, gid int) error {
	f.chowns = append(f.chowns, name)
	return nil
}
func (f *rootedFS) Lchown(name string, uid, gid int) error {
	f.lchowns = append(f.lchowns, name)
	return nil
}
func (f *rootedFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(f.path(name)) }
func (f *rootedFS) WriteFile(name string, data []byte, mode os.FileMode) error {
	return os.WriteFile(f.path(name), data, mode)
}
func (f *rootedFS) SyncDir(name string) error {
	directory, err := os.Open(f.path(name))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// The helper runs with a restrictive umask. The new version directory must stay enterable by
// the service user, or the restarted service cannot execute the binary and the update rolls back.
func TestBackendVersionSlotIsEnterableUnderRestrictiveUmask(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	backend, fixture := newFixture(t, release.Schema{Version: 150, RollbackCompatible: false})
	job := updatehelper.Job{ID: "12345678", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	for _, step := range []updatehelper.State{
		updatehelper.StatePreflight, updatehelper.StateDownload, updatehelper.StateArtifactVerified,
		updatehelper.StateBackup, updatehelper.StateStopping, updatehelper.StateActivating,
	} {
		if err := backend.RunStep(context.Background(), step, job); err != nil {
			t.Fatalf("RunStep(%s): %v", step, err)
		}
	}
	info, err := fixture.fs.Stat("/opt/supermetry/versions/v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("version slot mode = %o, want 755", got)
	}
}

func TestPreflightChecksFreeDiskSpace(t *testing.T) {
	t.Parallel()
	job := updatehelper.Job{ID: "12345678", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	for _, tt := range []struct {
		name    string
		free    uint64
		wantErr bool
	}{
		{name: "enough space", free: minFreeBytes},
		{name: "one byte short", free: minFreeBytes - 1, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			backend, _ := newFixture(t, release.Schema{Version: 150})
			backend.freeBytes = func(string) (uint64, error) { return tt.free, nil }
			err := backend.RunStep(context.Background(), updatehelper.StatePreflight, job)
			if (err != nil) != tt.wantErr {
				t.Fatalf("preflight error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
