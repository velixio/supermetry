// Package updatebinary applies signed releases to installer-managed Linux
// binary deployments. It never invokes a shell and preserves the previous
// version slot for explicit recovery.
package updatebinary

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"supermetry/internal/appbackup"
	"supermetry/internal/updatehelper"
	"supermetry/internal/updater/release"
)

const (
	defaultStagingDir = "/var/lib/supermetry-updater/staging"
	systemctlPath     = "/usr/bin/systemctl"
	maxBinaryBytes    = int64(512 << 20)
)

var ErrRecoveryRequired = updatehelper.ErrRecoveryRequired

type ReleaseClient interface {
	FetchManifest(context.Context, string) ([]byte, []byte, error)
	FetchArtifact(context.Context, string, release.Artifact, io.Writer) error
}

type File interface {
	io.Reader
	io.ReaderAt
	io.Writer
	io.Seeker
	io.Closer
	Stat() (os.FileInfo, error)
	Sync() error
	Chmod(os.FileMode) error
}

type FileSystem interface {
	MkdirAll(string, os.FileMode) error
	Open(string) (File, error)
	OpenFile(string, int, os.FileMode) (File, error)
	Rename(string, string) error
	Remove(string) error
	RemoveAll(string) error
	Readlink(string) (string, error)
	Symlink(string, string) error
	Stat(string) (os.FileInfo, error)
	Lstat(string) (os.FileInfo, error)
	Chmod(string, os.FileMode) error
	Chown(string, int, int) error
	Lchown(string, int, int) error
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, os.FileMode) error
	SyncDir(string) error
}

type BackupRequest struct {
	Executable  string
	Environment []string
	OutputPath  string
	Version     string
}

type BackupCreator interface {
	CreateAndVerify(context.Context, BackupRequest) (appbackup.Result, error)
}

type HealthStatus struct {
	Version       string
	SchemaVersion int64
}

type Probe interface {
	Check(context.Context, string) (HealthStatus, error)
	CurrentSchema(context.Context) (int64, error)
}

type Config struct {
	Descriptor     updatehelper.DeploymentDescriptor
	Client         ReleaseClient
	Keyring        release.Keyring
	Runner         updatehelper.Runner
	Filesystem     FileSystem
	Backup         BackupCreator
	Probe          Probe
	CurrentVersion string
	CurrentSchema  int64
	Environment    []string
	GOOS           string
	GOARCH         string
	StagingDir     string
	PollInterval   time.Duration
	HealthTimeout  time.Duration
	Now            func() time.Time
	Sleep          func(context.Context, time.Duration) error
}

type Backend struct {
	descriptor     updatehelper.DeploymentDescriptor
	client         ReleaseClient
	keyring        release.Keyring
	runner         updatehelper.Runner
	fs             FileSystem
	backup         BackupCreator
	probe          Probe
	currentVersion string
	currentSchema  int64
	environment    []string
	goos           string
	goarch         string
	stagingDir     string
	pollInterval   time.Duration
	healthTimeout  time.Duration
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
}

func New(config Config) (*Backend, error) {
	if err := config.Descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("deployment descriptor: %w", err)
	}
	if config.Descriptor.Mode != updatehelper.DeploymentBinary {
		return nil, errors.New("binary updater requires a binary deployment descriptor")
	}
	if config.Client == nil || config.Runner == nil || config.Filesystem == nil || config.Backup == nil || config.Probe == nil {
		return nil, errors.New("binary updater dependencies are incomplete")
	}
	if !stableVersion(config.CurrentVersion) {
		return nil, errors.New("current version must be a canonical stable semantic version")
	}
	if config.CurrentSchema < 0 {
		return nil, errors.New("current schema version cannot be negative")
	}
	if config.GOOS == "" {
		config.GOOS = runtime.GOOS
	}
	if config.GOARCH == "" {
		config.GOARCH = runtime.GOARCH
	}
	if config.GOOS != "linux" {
		return nil, errors.New("binary updater supports Linux only")
	}
	if config.StagingDir == "" {
		config.StagingDir = defaultStagingDir
	}
	if !filepath.IsAbs(config.StagingDir) || filepath.Clean(config.StagingDir) != config.StagingDir {
		return nil, errors.New("staging directory must be an absolute clean path")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.HealthTimeout <= 0 {
		config.HealthTimeout = 30 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Sleep == nil {
		config.Sleep = sleepContext
	}
	return &Backend{
		descriptor: config.Descriptor, client: config.Client, keyring: config.Keyring,
		runner: config.Runner, fs: config.Filesystem, backup: config.Backup, probe: config.Probe,
		currentVersion: config.CurrentVersion, currentSchema: config.CurrentSchema,
		environment: append([]string(nil), config.Environment...), goos: config.GOOS, goarch: config.GOARCH,
		stagingDir: config.StagingDir, pollInterval: config.PollInterval, healthTimeout: config.HealthTimeout,
		now: config.Now, sleep: config.Sleep,
	}, nil
}

func (b *Backend) RunStep(ctx context.Context, step updatehelper.State, job updatehelper.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := job.Validate(); err != nil {
		return fmt.Errorf("validate update job: %w", err)
	}
	switch step {
	case updatehelper.StatePreflight:
		return b.preflight(job)
	case updatehelper.StateDownload:
		return b.download(ctx, job)
	case updatehelper.StateArtifactVerified:
		return b.verifyAndExtract(ctx, job)
	case updatehelper.StateStopping:
		return b.service(ctx, "stop")
	case updatehelper.StateBackup:
		return b.createBackup(ctx, job)
	case updatehelper.StateActivating:
		return b.activate(ctx, job)
	case updatehelper.StateRestarting:
		return b.service(ctx, "start")
	case updatehelper.StateHealthCheck:
		return b.waitForJobVersion(ctx, job)
	default:
		return fmt.Errorf("unsupported binary update step %q", step)
	}
}

func (b *Backend) Recover(ctx context.Context, action updatehelper.RecoveryAction, job updatehelper.Job) error {
	if action != updatehelper.RecoveryFailSafely {
		return fmt.Errorf("unsupported binary recovery action %q", action)
	}
	state, err := b.loadState(job)
	if err != nil {
		return err
	}
	liveSchema, err := b.probe.CurrentSchema(ctx)
	if err != nil {
		return fmt.Errorf("read current database schema: %w", err)
	}
	if liveSchema != state.CurrentSchema && !state.Manifest.Schema.RollbackCompatible {
		return ErrRecoveryRequired
	}
	if err := b.switchCurrent(job, state.OldTarget); err != nil {
		return fmt.Errorf("restore previous version: %w", err)
	}
	if err := b.service(ctx, "start"); err != nil {
		return err
	}
	if err := b.waitForVersion(ctx, state.CurrentVersion, nil); err != nil {
		return fmt.Errorf("verify recovered version: %w", err)
	}
	return nil
}

type persistedState struct {
	CurrentVersion string           `json:"current_version"`
	CurrentSchema  int64            `json:"current_schema"`
	OldTarget      string           `json:"old_target"`
	Manifest       release.Manifest `json:"manifest"`
	Artifact       release.Artifact `json:"artifact"`
	BinarySHA256   string           `json:"binary_sha256"`
	BackupPath     string           `json:"backup_path"`
}

func (b *Backend) preflight(job updatehelper.Job) error {
	if err := b.fs.RemoveAll(b.jobDir(job)); err != nil {
		return fmt.Errorf("clear update staging: %w", err)
	}
	if err := b.fs.MkdirAll(b.jobDir(job), 0o700); err != nil {
		return fmt.Errorf("create update staging: %w", err)
	}
	if err := b.fs.Chown(b.jobDir(job), 0, 0); err != nil {
		return fmt.Errorf("own update staging: %w", err)
	}
	info, err := b.fs.Lstat(b.descriptor.CurrentLink)
	if err != nil {
		return fmt.Errorf("inspect current version link: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return errors.New("current version path is not a symlink")
	}
	target, err := b.fs.Readlink(b.descriptor.CurrentLink)
	if err != nil {
		return fmt.Errorf("read current version link: %w", err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(b.descriptor.CurrentLink), target)
	}
	target = filepath.Clean(target)
	if !within(b.descriptor.VersionsDir, target) {
		return errors.New("current version link escapes versions directory")
	}
	binary := filepath.Join(target, "supermetry")
	binaryInfo, err := b.fs.Stat(binary)
	if err != nil {
		return fmt.Errorf("inspect current binary: %w", err)
	}
	if !binaryInfo.Mode().IsRegular() || binaryInfo.Mode().Perm()&0o111 == 0 {
		return errors.New("current binary is not a regular executable file")
	}
	return b.saveState(job, persistedState{CurrentVersion: b.currentVersion, CurrentSchema: b.currentSchema, OldTarget: target})
}

func (b *Backend) download(ctx context.Context, job updatehelper.Job) error {
	state, err := b.loadState(job)
	if err != nil {
		return err
	}
	manifestBytes, signatureBytes, err := b.client.FetchManifest(ctx, job.TargetVersion)
	if err != nil {
		return err
	}
	manifest, err := b.keyring.Verify(manifestBytes, signatureBytes)
	if err != nil {
		return fmt.Errorf("verify release manifest: %w", err)
	}
	if manifest.Version != job.TargetVersion {
		return errors.New("release manifest version does not match requested target")
	}
	artifact, err := release.SelectArtifact(manifest, state.CurrentVersion, b.goos, b.goarch)
	if err != nil {
		return fmt.Errorf("select release artifact: %w", err)
	}
	if err := b.writeAtomic(b.manifestPath(job), manifestBytes, 0o600); err != nil {
		return err
	}
	if err := b.writeAtomic(b.signaturePath(job), signatureBytes, 0o600); err != nil {
		return err
	}
	partial := b.archivePath(job) + ".partial"
	_ = b.fs.Remove(partial)
	file, err := b.fs.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create staged artifact: %w", err)
	}
	bounded := &boundedWriter{writer: file, remaining: artifact.Size}
	fetchErr := b.client.FetchArtifact(ctx, job.TargetVersion, artifact, bounded)
	syncErr := file.Sync()
	closeErr := file.Close()
	if fetchErr != nil || bounded.remaining != 0 || syncErr != nil || closeErr != nil {
		_ = b.fs.Remove(partial)
		if fetchErr == nil && bounded.remaining != 0 {
			fetchErr = errors.New("release artifact size mismatch")
		}
		return fmt.Errorf("stage release artifact: %w", errors.Join(fetchErr, syncErr, closeErr))
	}
	if err := b.fs.Chown(partial, 0, 0); err != nil {
		_ = b.fs.Remove(partial)
		return fmt.Errorf("own staged artifact: %w", err)
	}
	if err := b.fs.Rename(partial, b.archivePath(job)); err != nil {
		_ = b.fs.Remove(partial)
		return fmt.Errorf("publish staged artifact: %w", err)
	}
	if err := b.fs.SyncDir(b.jobDir(job)); err != nil {
		return fmt.Errorf("sync staged artifact directory: %w", err)
	}
	state.Manifest, state.Artifact = manifest, artifact
	return b.saveState(job, state)
}

func (b *Backend) verifyAndExtract(ctx context.Context, job updatehelper.Job) error {
	state, err := b.loadVerifiedState(job)
	if err != nil {
		return err
	}
	archive, err := b.fs.Open(b.archivePath(job))
	if err != nil {
		return fmt.Errorf("open staged artifact: %w", err)
	}
	validateErr := release.ValidateArchive(ctx, archive, state.Artifact)
	closeErr := archive.Close()
	if validateErr != nil || closeErr != nil {
		return errors.Join(validateErr, closeErr)
	}
	archive, err = b.fs.Open(b.archivePath(job))
	if err != nil {
		return err
	}
	digest, extractErr := b.extractBinary(ctx, archive, b.binaryPath(job))
	closeErr = archive.Close()
	if extractErr != nil || closeErr != nil {
		return errors.Join(extractErr, closeErr)
	}
	state.BinarySHA256 = digest
	return b.saveState(job, state)
}

func (b *Backend) createBackup(ctx context.Context, job updatehelper.Job) error {
	state, err := b.loadVerifiedState(job)
	if err != nil {
		return err
	}
	if err := b.fs.MkdirAll(b.descriptor.BackupDir, 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if err := b.fs.Chown(b.descriptor.BackupDir, 0, 0); err != nil {
		return fmt.Errorf("own backup directory: %w", err)
	}
	output := filepath.Join(b.descriptor.BackupDir, "supermetry-pre-update-"+job.ID+".tar.gz")
	result, err := b.backup.CreateAndVerify(ctx, BackupRequest{
		Executable: filepath.Join(state.OldTarget, "supermetry"), Environment: append([]string(nil), b.environment...),
		OutputPath: output, Version: state.CurrentVersion,
	})
	if err != nil {
		return fmt.Errorf("create mandatory database backup: %w", err)
	}
	if result.Path != output || int64(result.SchemaVersion) != state.CurrentSchema {
		return errors.New("verified backup does not match the current deployment schema")
	}
	state.BackupPath = result.Path
	return b.saveState(job, state)
}

func (b *Backend) activate(ctx context.Context, job updatehelper.Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := b.loadVerifiedState(job)
	if err != nil {
		return err
	}
	if state.BackupPath == "" {
		return errors.New("mandatory verified backup is missing")
	}
	target := filepath.Join(b.descriptor.VersionsDir, job.TargetVersion)
	if info, statErr := b.fs.Stat(filepath.Join(target, "supermetry")); statErr == nil && info.Mode().IsRegular() {
		digest, hashErr := b.hashFile(ctx, filepath.Join(target, "supermetry"))
		if hashErr != nil {
			return hashErr
		}
		if digest != state.BinarySHA256 {
			return errors.New("existing target version slot contains a different binary")
		}
	} else if statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect target version slot: %w", statErr)
	} else {
		temporary := target + ".partial-" + job.ID
		if err := b.fs.RemoveAll(temporary); err != nil {
			return err
		}
		if err := b.fs.MkdirAll(temporary, 0o755); err != nil {
			return fmt.Errorf("create version slot: %w", err)
		}
		// The helper runs with a restrictive umask, which MkdirAll applies; the service user
		// must be able to enter the slot to run the new binary.
		if err := b.fs.Chmod(temporary, 0o755); err != nil {
			return fmt.Errorf("set version slot mode: %w", err)
		}
		if err := b.fs.Chown(temporary, 0, 0); err != nil {
			return fmt.Errorf("own version slot: %w", err)
		}
		if err := b.copyBinary(ctx, b.binaryPath(job), filepath.Join(temporary, "supermetry")); err != nil {
			_ = b.fs.RemoveAll(temporary)
			return err
		}
		if err := b.fs.Rename(temporary, target); err != nil {
			_ = b.fs.RemoveAll(temporary)
			return fmt.Errorf("publish version slot: %w", err)
		}
		if err := b.fs.SyncDir(b.descriptor.VersionsDir); err != nil {
			return fmt.Errorf("sync versions directory: %w", err)
		}
	}
	return b.switchCurrent(job, target)
}

func (b *Backend) service(ctx context.Context, operation string) error {
	result, err := b.runner.Run(ctx, systemctlPath, []string{operation, b.descriptor.ServiceName}, updatehelper.RunOptions{})
	if err != nil {
		return fmt.Errorf("systemctl %s: %w", operation, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("systemctl %s exited with status %d", operation, result.ExitCode)
	}
	return nil
}

func (b *Backend) waitForVersion(ctx context.Context, expected string, expectedSchema *int64) error {
	deadline := b.now().Add(b.healthTimeout)
	var lastErr error
	for {
		status, err := b.probe.Check(ctx, b.descriptor.ReadinessURL)
		if err == nil && status.Version == expected && (expectedSchema == nil || status.SchemaVersion == *expectedSchema) {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("reported version %q schema %d", status.Version, status.SchemaVersion)
		}
		if !b.now().Before(deadline) {
			return fmt.Errorf("readiness did not report exact version %s: %w", expected, lastErr)
		}
		if err := b.sleep(ctx, b.pollInterval); err != nil {
			return err
		}
	}
}

func (b *Backend) waitForJobVersion(ctx context.Context, job updatehelper.Job) error {
	state, err := b.loadVerifiedState(job)
	if err != nil {
		return err
	}
	expectedSchema := state.Manifest.Schema.Version
	healthErr := b.waitForVersion(ctx, job.TargetVersion, &expectedSchema)
	if healthErr == nil {
		return nil
	}
	liveSchema, err := b.probe.CurrentSchema(ctx)
	if err != nil {
		_ = b.service(ctx, "stop")
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("read schema after failed activation: %w", err), healthErr)
	}
	if liveSchema != state.CurrentSchema && !state.Manifest.Schema.RollbackCompatible {
		_ = b.service(ctx, "stop")
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("schema advanced from %d to %d and release is not rollback compatible", state.CurrentSchema, liveSchema), healthErr)
	}
	if err := b.service(ctx, "stop"); err != nil {
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("stop failed release: %w", err), healthErr)
	}
	if err := b.switchCurrent(job, state.OldTarget); err != nil {
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("restore previous version: %w", err), healthErr)
	}
	if err := b.service(ctx, "start"); err != nil {
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("restart previous version: %w", err), healthErr)
	}
	if err := b.waitForVersion(ctx, state.CurrentVersion, &state.CurrentSchema); err != nil {
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("verify previous version: %w", err), healthErr)
	}
	return errors.Join(updatehelper.ErrRolledBack, healthErr)
}

func stableVersion(value string) bool {
	return semver.IsValid(value) && semver.Canonical(value) == value && semver.Prerelease(value) == ""
}

func within(parent, child string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (b *Backend) jobDir(job updatehelper.Job) string { return filepath.Join(b.stagingDir, job.ID) }
func (b *Backend) statePath(job updatehelper.Job) string {
	return filepath.Join(b.jobDir(job), "state.json")
}
func (b *Backend) manifestPath(job updatehelper.Job) string {
	return filepath.Join(b.jobDir(job), "release-manifest.json")
}
func (b *Backend) signaturePath(job updatehelper.Job) string {
	return filepath.Join(b.jobDir(job), "release-manifest.json.sig")
}
func (b *Backend) archivePath(job updatehelper.Job) string {
	return filepath.Join(b.jobDir(job), "artifact")
}
func (b *Backend) binaryPath(job updatehelper.Job) string {
	return filepath.Join(b.jobDir(job), "supermetry")
}

func (b *Backend) saveState(job updatehelper.Job, state persistedState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return b.writeAtomic(b.statePath(job), data, 0o600)
}

func (b *Backend) loadState(job updatehelper.Job) (persistedState, error) {
	data, err := b.fs.ReadFile(b.statePath(job))
	if err != nil {
		return persistedState{}, fmt.Errorf("read update state: %w", err)
	}
	if len(data) > 1<<20 {
		return persistedState{}, errors.New("update state is too large")
	}
	var state persistedState
	decoder := json.NewDecoder(bytesReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return persistedState{}, fmt.Errorf("decode update state: %w", err)
	}
	return state, nil
}

func (b *Backend) loadVerifiedState(job updatehelper.Job) (persistedState, error) {
	state, err := b.loadState(job)
	if err != nil {
		return state, err
	}
	manifestBytes, err := b.fs.ReadFile(b.manifestPath(job))
	if err != nil {
		return state, fmt.Errorf("read staged manifest: %w", err)
	}
	signatureBytes, err := b.fs.ReadFile(b.signaturePath(job))
	if err != nil {
		return state, fmt.Errorf("read staged signature: %w", err)
	}
	manifest, err := b.keyring.Verify(manifestBytes, signatureBytes)
	if err != nil {
		return state, fmt.Errorf("reverify staged manifest: %w", err)
	}
	if manifest.Version != job.TargetVersion || manifest.Version != state.Manifest.Version {
		return state, errors.New("staged manifest does not match update job")
	}
	artifact, err := release.SelectArtifact(manifest, state.CurrentVersion, b.goos, b.goarch)
	if err != nil {
		return state, err
	}
	if artifact != state.Artifact {
		return state, errors.New("staged artifact metadata changed")
	}
	state.Manifest = manifest
	return state, nil
}

func (b *Backend) writeAtomic(name string, data []byte, mode os.FileMode) error {
	temporary := name + ".partial"
	_ = b.fs.Remove(temporary)
	if err := b.fs.WriteFile(temporary, data, mode); err != nil {
		return err
	}
	if err := b.fs.Chown(temporary, 0, 0); err != nil {
		_ = b.fs.Remove(temporary)
		return err
	}
	if err := b.fs.Rename(temporary, name); err != nil {
		_ = b.fs.Remove(temporary)
		return err
	}
	return b.fs.SyncDir(filepath.Dir(name))
}

func (b *Backend) extractBinary(ctx context.Context, input io.Reader, destination string) (string, error) {
	gz, err := gzip.NewReader(input)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	partial := destination + ".partial"
	_ = b.fs.Remove(partial)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if header.Name != "supermetry" {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return "", errors.New("release binary is not a regular file")
		}
		if header.Size <= 0 || header.Size > maxBinaryBytes {
			return "", errors.New("release binary has invalid size")
		}
		file, err := b.fs.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return "", err
		}
		hasher := sha256.New()
		written, copyErr := io.CopyN(io.MultiWriter(file, hasher), &contextReader{ctx: ctx, reader: tarReader}, header.Size)
		syncErr := file.Sync()
		closeErr := file.Close()
		if copyErr != nil || written != header.Size || syncErr != nil || closeErr != nil {
			_ = b.fs.Remove(partial)
			return "", errors.Join(copyErr, syncErr, closeErr)
		}
		if err := b.fs.Chmod(partial, 0o755); err != nil {
			_ = b.fs.Remove(partial)
			return "", err
		}
		if err := b.fs.Chown(partial, 0, 0); err != nil {
			_ = b.fs.Remove(partial)
			return "", err
		}
		if err := b.fs.Rename(partial, destination); err != nil {
			_ = b.fs.Remove(partial)
			return "", err
		}
		return hex.EncodeToString(hasher.Sum(nil)), nil
	}
	return "", errors.New("release archive is missing supermetry binary")
}

func (b *Backend) copyBinary(ctx context.Context, source, destination string) error {
	input, err := b.fs.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := b.fs.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, &contextReader{ctx: ctx, reader: input})
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := b.fs.Chmod(destination, 0o755); err != nil {
		return err
	}
	return b.fs.Chown(destination, 0, 0)
}

func (b *Backend) hashFile(ctx context.Context, name string) (string, error) {
	file, err := b.fs.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, &contextReader{ctx: ctx, reader: file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (b *Backend) switchCurrent(job updatehelper.Job, target string) error {
	if !within(b.descriptor.VersionsDir, target) {
		return errors.New("recovery target escapes versions directory")
	}
	temporary := b.descriptor.CurrentLink + ".next-" + job.ID
	_ = b.fs.Remove(temporary)
	if err := b.fs.Symlink(target, temporary); err != nil {
		return err
	}
	if err := b.fs.Lchown(temporary, 0, 0); err != nil {
		_ = b.fs.Remove(temporary)
		return err
	}
	if err := b.fs.Rename(temporary, b.descriptor.CurrentLink); err != nil {
		_ = b.fs.Remove(temporary)
		return err
	}
	return b.fs.SyncDir(filepath.Dir(b.descriptor.CurrentLink))
}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errors.New("release artifact exceeds signed size")
	}
	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func bytesReader(data []byte) io.Reader { return bytes.NewReader(data) }

var _ updatehelper.Backend = (*Backend)(nil)
