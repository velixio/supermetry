package updatedocker_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"supermetry/internal/updatedocker"
	"supermetry/internal/updatehelper"
	"supermetry/internal/updater/release"
)

const (
	oldDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func TestBackendRunStepUpdatesOnlyManagedServiceUsingSignedPlatformDigest(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	job := updatehelper.Job{ID: "42", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}

	steps := []updatehelper.State{
		updatehelper.StatePreflight,
		updatehelper.StateDownload,
		updatehelper.StateArtifactVerified,
		updatehelper.StateStopping,
		updatehelper.StateBackup,
		updatehelper.StateActivating,
		updatehelper.StateRestarting,
		updatehelper.StateHealthCheck,
	}
	for _, step := range steps {
		if err := fixture.backend.RunStep(context.Background(), step, job); err != nil {
			t.Fatalf("RunStep(%s): %v", step, err)
		}
	}

	wantRef := "ghcr.io/velixio/supermetry@" + newDigest
	if got := fixture.fs.files[updatedocker.ComposeFilePath]; !strings.Contains(string(got), "    image: "+wantRef+"\n") {
		t.Fatalf("updated Compose =\n%s", got)
	}
	if got := fixture.fs.files[updatedocker.ComposeFilePath]; !strings.Contains(string(got), "postgres:18-alpine") || !strings.Contains(string(got), "postgres-data:/var/lib/postgresql") {
		t.Fatalf("Postgres service or volume changed:\n%s", got)
	}
	if !containsCall(fixture.runner.calls, "pull", wantRef) {
		t.Fatalf("runner calls = %v, want exact digest pull", fixture.runner.calls)
	}
	if !containsExactCall(fixture.runner.calls, "compose", "--project-directory", updatedocker.ComposeDir, "-f", updatedocker.ComposeFilePath, "stop", updatedocker.ComposeService) {
		t.Fatalf("runner calls = %v, want service-only stop", fixture.runner.calls)
	}
	if !containsExactCall(fixture.runner.calls, "compose", "--project-directory", updatedocker.ComposeDir, "-f", updatedocker.ComposeFilePath, "up", "-d", "--no-deps", updatedocker.ComposeService) {
		t.Fatalf("runner calls = %v, want service-only recreate", fixture.runner.calls)
	}
	for _, call := range fixture.runner.calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "down") || strings.Contains(joined, "volume") || strings.Contains(joined, "postgres") {
			t.Fatalf("unsafe Docker command: %s %s", call.executable, joined)
		}
	}
	if fixture.backup.request.OldImage != "ghcr.io/velixio/supermetry@"+oldDigest || fixture.backup.request.ComposeFile != updatedocker.ComposeFilePath {
		t.Fatalf("backup request = %+v", fixture.backup.request)
	}
	if fixture.probe.inspected != wantRef || fixture.probe.readyURL != fixture.descriptor.ReadinessURL {
		t.Fatalf("probes inspected=%q readiness=%q", fixture.probe.inspected, fixture.probe.readyURL)
	}
}

func TestBackendPreservesExactComposeBytesAndDigestBeforeStopping(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	job := updatehelper.Job{ID: "7", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	original := append([]byte(nil), fixture.fs.files[updatedocker.ComposeFilePath]...)

	if err := fixture.backend.RunStep(context.Background(), updatehelper.StatePreflight, job); err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.fs.files[updatedocker.SnapshotPath("7")]
	if string(snapshot) != string(original) {
		t.Fatalf("snapshot changed bytes:\n%s", snapshot)
	}
	if got := string(fixture.fs.files[updatedocker.SnapshotDigestPath("7")]); got != updatedocker.DigestBytes(original)+"\n" {
		t.Fatalf("snapshot digest = %q", got)
	}
	if len(fixture.runner.calls) != 0 {
		t.Fatalf("preflight ran commands: %v", fixture.runner.calls)
	}
}

func TestBackendBackupFailureNeverMutatesComposeAndRecoveryStartsOnlyApp(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.backup.err = errors.New("pg dump failed")
	job := updatehelper.Job{ID: "8", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	original := string(fixture.fs.files[updatedocker.ComposeFilePath])

	if err := fixture.backend.RunStep(context.Background(), updatehelper.StatePreflight, job); err != nil {
		t.Fatal(err)
	}
	if err := fixture.backend.RunStep(context.Background(), updatehelper.StateStopping, job); err != nil {
		t.Fatal(err)
	}
	if err := fixture.backend.RunStep(context.Background(), updatehelper.StateBackup, job); err == nil {
		t.Fatal("backup unexpectedly succeeded")
	}
	if err := fixture.backend.Recover(context.Background(), updatehelper.RecoveryFailSafely, job); err != nil {
		t.Fatal(err)
	}
	if got := string(fixture.fs.files[updatedocker.ComposeFilePath]); got != original {
		t.Fatalf("Compose mutated after backup failure:\n%s", got)
	}
	last := fixture.runner.calls[len(fixture.runner.calls)-1]
	if strings.Join(last.args, " ") != "compose --project-directory /opt/supermetry -f /opt/supermetry/docker-compose.yml up -d --no-deps supermetry" {
		t.Fatalf("recovery call = %v", last.args)
	}
}

func TestBackendRejectsPulledImageIdentityMismatch(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		mutate func(*updatedocker.ImageInfo)
	}{
		{name: "digest", mutate: func(info *updatedocker.ImageInfo) { info.Digest = oldDigest }},
		{name: "operating system", mutate: func(info *updatedocker.ImageInfo) { info.OS = "windows" }},
		{name: "architecture", mutate: func(info *updatedocker.ImageInfo) { info.Arch = "arm64" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newFixture(t)
			tt.mutate(&fixture.probe.image)
			job := updatehelper.Job{ID: "11", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
			if err := fixture.backend.RunStep(context.Background(), updatehelper.StateArtifactVerified, job); err == nil {
				t.Fatal("mismatched pulled image accepted")
			}
		})
	}
}

func TestBackendRejectsTamperedSnapshotAndAmbiguousCompose(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	job := updatehelper.Job{ID: "12", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	if err := fixture.backend.RunStep(context.Background(), updatehelper.StatePreflight, job); err != nil {
		t.Fatal(err)
	}
	fixture.fs.files[updatedocker.SnapshotPath("12")] = append(fixture.fs.files[updatedocker.SnapshotPath("12")], []byte("# tampered\n")...)
	if err := fixture.backend.RunStep(context.Background(), updatehelper.StateActivating, job); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered snapshot error = %v", err)
	}

	ambiguous := newFixture(t)
	ambiguous.fs.files[updatedocker.ComposeFilePath] = []byte("services:\n  supermetry:\n    image: ghcr.io/velixio/supermetry@" + oldDigest + "\n    image: ghcr.io/velixio/supermetry@" + newDigest + "\n")
	job.ID = "13"
	if err := ambiguous.backend.RunStep(context.Background(), updatehelper.StatePreflight, job); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ambiguous Compose error = %v", err)
	}
}

func TestBackendHealthFailureRollsBackOnlyWhenSchemaSafe(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name               string
		schemaAfter        int64
		rollbackCompatible bool
		wantRolledBack     bool
	}{
		{name: "schema unchanged", schemaAfter: 41, wantRolledBack: true},
		{name: "manifest permits schema rollback", schemaAfter: 42, rollbackCompatible: true, wantRolledBack: true},
		{name: "schema changed incompatibly", schemaAfter: 42, wantRolledBack: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.manifest.Schema.RollbackCompatible = tt.rollbackCompatible
			fixture.probe.schema = tt.schemaAfter
			fixture.probe.readyResults = []readyResult{{version: "v1.2.0", err: errors.New("unhealthy")}, {version: "v1.1.0"}}
			fixture.rebuild(t)
			job := updatehelper.Job{ID: "9", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
			for _, step := range []updatehelper.State{updatehelper.StatePreflight, updatehelper.StateBackup, updatehelper.StateActivating, updatehelper.StateRestarting} {
				if err := fixture.backend.RunStep(context.Background(), step, job); err != nil {
					t.Fatalf("RunStep(%s): %v", step, err)
				}
			}
			err := fixture.backend.RunStep(context.Background(), updatehelper.StateHealthCheck, job)
			if tt.wantRolledBack {
				if !errors.Is(err, updatehelper.ErrRolledBack) {
					t.Fatalf("health error = %v, want ErrRolledBack", err)
				}
				if got := string(fixture.fs.files[updatedocker.ComposeFilePath]); !strings.Contains(got, "image: ghcr.io/velixio/supermetry@"+oldDigest) {
					t.Fatalf("old Compose not restored:\n%s", got)
				}
			} else {
				if !errors.Is(err, updatehelper.ErrRecoveryRequired) {
					t.Fatalf("health error = %v, want ErrRecoveryRequired", err)
				}
				if got := string(fixture.fs.files[updatedocker.ComposeFilePath]); !strings.Contains(got, "image: ghcr.io/velixio/supermetry@"+newDigest) {
					t.Fatalf("unsafe rollback occurred:\n%s", got)
				}
			}
		})
	}
}

func TestBackendTreatsWrongReportedVersionAsFailedHealth(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	fixture.probe.readyResults = []readyResult{{version: "v9.9.9"}, {version: "v1.1.0"}}
	job := updatehelper.Job{ID: "14", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
	for _, step := range []updatehelper.State{updatehelper.StatePreflight, updatehelper.StateBackup, updatehelper.StateActivating} {
		if err := fixture.backend.RunStep(context.Background(), step, job); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.backend.RunStep(context.Background(), updatehelper.StateHealthCheck, job); !errors.Is(err, updatehelper.ErrRolledBack) {
		t.Fatalf("wrong-version health result = %v", err)
	}
}

func TestNewRejectsMutableDescriptorUnverifiedManifestAndLocalBuild(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*fixture)
	}{
		{name: "non Docker descriptor", mutate: func(f *fixture) { f.descriptor.Mode = updatehelper.DeploymentBinary }},
		{name: "mutable compose directory", mutate: func(f *fixture) { f.descriptor.ComposeDir = "/tmp/compose" }},
		{name: "unsigned-invalid manifest", mutate: func(f *fixture) { f.manifest.Container.Image = "attacker.invalid/image" }},
		{name: "local build Compose", mutate: func(f *fixture) {
			f.fs.files[updatedocker.ComposeFilePath] = []byte("services:\n  supermetry:\n    build: ./src\n")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := baseFixture()
			tt.mutate(fixture)
			backend, err := updatedocker.New(fixture.config())
			if err != nil {
				return
			}
			job := updatehelper.Job{ID: "10", TargetVersion: "v1.2.0", State: updatehelper.StateRequested}
			if err := backend.RunStep(context.Background(), updatehelper.StatePreflight, job); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

type fixture struct {
	backend    *updatedocker.Backend
	runner     *fakeRunner
	fs         *fakeFS
	backup     *fakeBackup
	probe      *fakeProbe
	descriptor updatehelper.DeploymentDescriptor
	manifest   release.Manifest
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fixture := baseFixture()
	fixture.rebuild(t)
	return fixture
}

func baseFixture() *fixture {
	descriptor := updatehelper.DeploymentDescriptor{
		Version: 1, Mode: updatehelper.DeploymentDocker, ServiceName: "supermetry.service",
		VersionsDir: "/opt/supermetry/versions", CurrentLink: "/opt/supermetry/current",
		ComposeDir: updatedocker.ComposeDir, ComposeService: updatedocker.ComposeService,
		BackupDir: "/var/lib/supermetry-updater/backups", ReadinessURL: "http://127.0.0.1:8787/readyz",
		SocketGroup: "supermetry-updater",
	}
	manifest := release.Manifest{
		FormatVersion: 1, Version: "v1.2.0", Commit: strings.Repeat("1", 40),
		PublishedAt: "2026-10-14T02:03:04Z", Stable: true, MinimumVersion: "v1.0.0",
		Schema:    release.Schema{Version: 42},
		Artifacts: []release.Artifact{{Name: "supermetry_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Size: 1, SHA256: strings.Repeat("b", 64)}},
		Container: &release.Container{Image: "ghcr.io/velixio/supermetry", Digest: "sha256:" + strings.Repeat("c", 64), Platforms: []release.ContainerPlatform{{OS: "linux", Arch: "amd64", Digest: newDigest}}},
	}
	compose := "services:\n  supermetry:\n    image: ghcr.io/velixio/supermetry@" + oldDigest + "\n    env_file: .env\n    volumes:\n      - supermetry-data:/data\n  postgres:\n    image: postgres:18-alpine\n    volumes:\n      - postgres-data:/var/lib/postgresql\nvolumes:\n  supermetry-data:\n  postgres-data:\n"
	return &fixture{
		runner: &fakeRunner{}, fs: &fakeFS{files: map[string][]byte{updatedocker.ComposeFilePath: []byte(compose)}},
		backup:     &fakeBackup{result: updatedocker.BackupResult{Path: "/var/lib/supermetry-updater/backups/update-42.dump", SHA256: strings.Repeat("e", 64), Size: 1024, SchemaVersion: 41}},
		probe:      &fakeProbe{image: updatedocker.ImageInfo{Digest: newDigest, OS: "linux", Arch: "amd64"}, version: "v1.2.0", schema: 41},
		descriptor: descriptor, manifest: manifest,
	}
}

func (f *fixture) config() updatedocker.Config {
	return updatedocker.Config{Runner: f.runner, Files: f.fs, Backup: f.backup, Probe: f.probe, Descriptor: f.descriptor, Manifest: f.manifest, CurrentVersion: "v1.1.0", OS: "linux", Arch: "amd64", HealthTimeout: time.Nanosecond, PollInterval: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }}
}

func (f *fixture) rebuild(t *testing.T) {
	t.Helper()
	backend, err := updatedocker.New(f.config())
	if err != nil {
		t.Fatal(err)
	}
	f.backend = backend
}

type command struct {
	executable string
	args       []string
}
type fakeRunner struct {
	calls []command
	err   error
}

func (r *fakeRunner) Run(_ context.Context, executable string, args []string, _ updatehelper.RunOptions) (updatehelper.RunResult, error) {
	r.calls = append(r.calls, command{executable: executable, args: append([]string(nil), args...)})
	return updatehelper.RunResult{}, r.err
}

type fakeFS struct{ files map[string][]byte }

func (f *fakeFS) ReadFile(path string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), data...), nil
}
func (f *fakeFS) AtomicWrite(path string, data []byte, _ uint32) error {
	f.files[path] = append([]byte(nil), data...)
	return nil
}

type fakeBackup struct {
	request updatedocker.BackupRequest
	result  updatedocker.BackupResult
	err     error
}

func (b *fakeBackup) Create(_ context.Context, request updatedocker.BackupRequest) (updatedocker.BackupResult, error) {
	b.request = request
	result := b.result
	if strings.Contains(result.Path, "42") && request.JobID != "42" {
		result.Path = strings.Replace(result.Path, "42", request.JobID, 1)
	}
	return result, b.err
}

type fakeProbe struct {
	inspected    string
	image        updatedocker.ImageInfo
	readyURL     string
	version      string
	schema       int64
	readyResults []readyResult
}

type readyResult struct {
	version string
	err     error
}

func (p *fakeProbe) InspectImage(_ context.Context, ref string) (updatedocker.ImageInfo, error) {
	p.inspected = ref
	return p.image, nil
}
func (p *fakeProbe) ReadyVersion(_ context.Context, url string) (string, error) {
	p.readyURL = url
	if len(p.readyResults) > 0 {
		result := p.readyResults[0]
		p.readyResults = p.readyResults[1:]
		return result.version, result.err
	}
	return p.version, nil
}
func (p *fakeProbe) SchemaVersion(context.Context) (int64, error) { return p.schema, nil }

func containsCall(calls []command, parts ...string) bool {
	for _, call := range calls {
		joined := call.executable + " " + strings.Join(call.args, " ")
		ok := true
		for _, part := range parts {
			ok = ok && strings.Contains(joined, part)
		}
		if ok {
			return true
		}
	}
	return false
}

func containsExactCall(calls []command, args ...string) bool {
	want := strings.Join(args, " ")
	for _, call := range calls {
		if call.executable == "docker" && strings.Join(call.args, " ") == want {
			return true
		}
	}
	return false
}
