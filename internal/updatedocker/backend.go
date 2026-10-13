// Package updatedocker applies a signed Supermetry container release to the
// fixed Docker Compose deployment produced by the official installer.
package updatedocker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"go.yaml.in/yaml/v3"

	"supermetry/internal/updatehelper"
	"supermetry/internal/updater/release"
)

const (
	ComposeDir      = "/opt/supermetry"
	ComposeFilePath = "/opt/supermetry/docker-compose.yml"
	ComposeService  = "supermetry"
	jobStateDir     = "/var/lib/supermetry-updater/jobs"
	officialImage   = "ghcr.io/velixio/supermetry"
)

type FileSystem interface {
	ReadFile(path string) ([]byte, error)
	AtomicWrite(path string, data []byte, mode uint32) error
}

type Backup interface {
	Create(context.Context, BackupRequest) (BackupResult, error)
}

type BackupRequest struct {
	JobID          string
	OldImage       string
	ComposeFile    string
	ComposeService string
	DestinationDir string
}

type BackupResult struct {
	Path          string
	SHA256        string
	Size          int64
	SchemaVersion int64
}

type ImageInfo struct {
	Digest string
	OS     string
	Arch   string
}

type Probe interface {
	InspectImage(ctx context.Context, exactRef string) (ImageInfo, error)
	ReadyVersion(ctx context.Context, readinessURL string) (string, error)
	SchemaVersion(ctx context.Context) (int64, error)
}

type Config struct {
	Runner         updatehelper.Runner
	Files          FileSystem
	Backup         Backup
	Probe          Probe
	Descriptor     updatehelper.DeploymentDescriptor
	Manifest       release.Manifest
	CurrentVersion string
	OS             string
	Arch           string
}

type Backend struct {
	runner         updatehelper.Runner
	files          FileSystem
	backup         Backup
	probe          Probe
	descriptor     updatehelper.DeploymentDescriptor
	manifest       release.Manifest
	currentVersion string
	platformOS     string
	platformArch   string
	targetRef      string
	targetDigest   string
}

type backupMetadata struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	SchemaVersion int64  `json:"schema_version"`
}

func New(config Config) (*Backend, error) {
	if config.Runner == nil || config.Files == nil || config.Backup == nil || config.Probe == nil {
		return nil, errors.New("Docker update backend is not configured")
	}
	if err := config.Descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("validate deployment descriptor: %w", err)
	}
	if config.Descriptor.Mode != updatehelper.DeploymentDocker {
		return nil, errors.New("deployment descriptor is not Docker Compose")
	}
	if config.Descriptor.ComposeDir != ComposeDir || config.Descriptor.ComposeService != ComposeService {
		return nil, errors.New("deployment descriptor does not name the managed Compose deployment")
	}
	if _, err := release.MarshalCanonical(config.Manifest); err != nil {
		return nil, fmt.Errorf("validate verified release manifest: %w", err)
	}
	platformOS, platformArch := config.OS, config.Arch
	if platformOS == "" {
		platformOS = runtime.GOOS
	}
	if platformArch == "" {
		platformArch = runtime.GOARCH
	}
	selection, err := release.SelectContainer(config.Manifest, config.CurrentVersion, platformOS, platformArch)
	if err != nil {
		return nil, fmt.Errorf("select signed container: %w", err)
	}
	return &Backend{
		runner: config.Runner, files: config.Files, backup: config.Backup, probe: config.Probe,
		descriptor: config.Descriptor, manifest: config.Manifest, currentVersion: config.CurrentVersion,
		platformOS: platformOS, platformArch: platformArch,
		targetRef: selection.Image + "@" + selection.Digest, targetDigest: selection.Digest,
	}, nil
}

func (b *Backend) RunStep(ctx context.Context, step updatehelper.State, job updatehelper.Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("validate update job: %w", err)
	}
	if job.TargetVersion != b.manifest.Version {
		return fmt.Errorf("job target %q does not match verified manifest %q", job.TargetVersion, b.manifest.Version)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch step {
	case updatehelper.StatePreflight:
		return b.preserveCompose(job.ID)
	case updatehelper.StateDownload:
		return b.runDocker(ctx, "pull", b.targetRef)
	case updatehelper.StateArtifactVerified:
		return b.verifyPulledImage(ctx)
	case updatehelper.StateStopping:
		return b.stop(ctx)
	case updatehelper.StateBackup:
		return b.createBackup(ctx, job.ID)
	case updatehelper.StateActivating:
		return b.activate(job.ID)
	case updatehelper.StateRestarting:
		return b.start(ctx)
	case updatehelper.StateHealthCheck:
		return b.verifyOrRollback(ctx, job.ID)
	case updatehelper.StateRequested, updatehelper.StateComplete:
		return nil
	default:
		return fmt.Errorf("unsupported Docker update step %q", step)
	}
}

func (b *Backend) Recover(ctx context.Context, action updatehelper.RecoveryAction, job updatehelper.Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("validate update job: %w", err)
	}
	if action != updatehelper.RecoveryFailSafely {
		return fmt.Errorf("unsupported Docker recovery action %q", action)
	}
	// Fail-safe recovery is used only before activation. Restore the exact
	// snapshot defensively, then recreate only the application service.
	if err := b.restoreCompose(job.ID); err != nil {
		return err
	}
	return b.start(ctx)
}

func (b *Backend) preserveCompose(jobID string) error {
	compose, err := b.files.ReadFile(ComposeFilePath)
	if err != nil {
		return fmt.Errorf("read managed Compose file: %w", err)
	}
	oldImage, err := managedImage(compose)
	if err != nil {
		return err
	}
	if _, err := replaceManagedImage(compose, oldImage); err != nil {
		return fmt.Errorf("validate installer-managed Compose layout: %w", err)
	}
	if err := b.files.AtomicWrite(SnapshotPath(jobID), compose, 0o600); err != nil {
		return fmt.Errorf("preserve Compose file: %w", err)
	}
	digest := []byte(DigestBytes(compose) + "\n")
	if err := b.files.AtomicWrite(SnapshotDigestPath(jobID), digest, 0o600); err != nil {
		return fmt.Errorf("preserve Compose digest: %w", err)
	}
	return nil
}

func (b *Backend) verifyPulledImage(ctx context.Context) error {
	info, err := b.probe.InspectImage(ctx, b.targetRef)
	if err != nil {
		return fmt.Errorf("inspect pulled image: %w", err)
	}
	if info.Digest != b.targetDigest || info.OS != b.platformOS || info.Arch != b.platformArch {
		return fmt.Errorf("pulled image identity mismatch: got %s %s/%s", info.Digest, info.OS, info.Arch)
	}
	return nil
}

func (b *Backend) createBackup(ctx context.Context, jobID string) error {
	compose, err := b.verifiedSnapshot(jobID)
	if err != nil {
		return err
	}
	oldImage, err := managedImage(compose)
	if err != nil {
		return err
	}
	result, err := b.backup.Create(ctx, BackupRequest{
		JobID: jobID, OldImage: oldImage, ComposeFile: ComposeFilePath,
		ComposeService: ComposeService, DestinationDir: b.descriptor.BackupDir,
	})
	if err != nil {
		return fmt.Errorf("create mandatory database backup: %w", err)
	}
	if err := validateBackupResult(result, b.descriptor.BackupDir); err != nil {
		return fmt.Errorf("validate mandatory database backup: %w", err)
	}
	metadata, err := json.Marshal(backupMetadata(result))
	if err != nil {
		return fmt.Errorf("encode backup metadata: %w", err)
	}
	metadata = append(metadata, '\n')
	if err := b.files.AtomicWrite(backupMetadataPath(jobID), metadata, 0o600); err != nil {
		return fmt.Errorf("preserve backup metadata: %w", err)
	}
	return nil
}

func (b *Backend) activate(jobID string) error {
	compose, err := b.verifiedSnapshot(jobID)
	if err != nil {
		return err
	}
	updated, err := replaceManagedImage(compose, b.targetRef)
	if err != nil {
		return err
	}
	if err := b.files.AtomicWrite(ComposeFilePath, updated, 0o600); err != nil {
		return fmt.Errorf("activate signed Compose image: %w", err)
	}
	return nil
}

func (b *Backend) verifyOrRollback(ctx context.Context, jobID string) error {
	version, healthErr := b.probe.ReadyVersion(ctx, b.descriptor.ReadinessURL)
	if healthErr == nil && version != b.manifest.Version {
		healthErr = fmt.Errorf("running version %q does not match target %q", version, b.manifest.Version)
	}
	if healthErr == nil {
		return nil
	}
	metadata, err := b.readBackupMetadata(jobID)
	if err != nil {
		_ = b.stop(ctx)
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("read rollback metadata: %w", err), healthErr)
	}
	schemaAfter, err := b.probe.SchemaVersion(ctx)
	if err != nil {
		_ = b.stop(ctx)
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("read current schema version: %w", err), healthErr)
	}
	if schemaAfter != metadata.SchemaVersion && !b.manifest.Schema.RollbackCompatible {
		_ = b.stop(ctx)
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("schema advanced from %d to %d and release is not rollback compatible", metadata.SchemaVersion, schemaAfter), healthErr)
	}
	if err := b.restoreCompose(jobID); err != nil {
		_ = b.stop(ctx)
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("restore previous Compose file: %w", err), healthErr)
	}
	if err := b.start(ctx); err != nil {
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("restart previous image: %w", err), healthErr)
	}
	oldVersion, err := b.probe.ReadyVersion(ctx, b.descriptor.ReadinessURL)
	if err != nil || oldVersion != b.currentVersion {
		return errors.Join(updatehelper.ErrRecoveryRequired, fmt.Errorf("previous image verification failed: version=%q: %w", oldVersion, err), healthErr)
	}
	return errors.Join(updatehelper.ErrRolledBack, healthErr)
}

func (b *Backend) restoreCompose(jobID string) error {
	compose, err := b.verifiedSnapshot(jobID)
	if err != nil {
		return err
	}
	return b.files.AtomicWrite(ComposeFilePath, compose, 0o600)
}

func (b *Backend) verifiedSnapshot(jobID string) ([]byte, error) {
	compose, err := b.files.ReadFile(SnapshotPath(jobID))
	if err != nil {
		return nil, fmt.Errorf("read preserved Compose file: %w", err)
	}
	digest, err := b.files.ReadFile(SnapshotDigestPath(jobID))
	if err != nil {
		return nil, fmt.Errorf("read preserved Compose digest: %w", err)
	}
	if string(digest) != DigestBytes(compose)+"\n" {
		return nil, errors.New("preserved Compose file digest mismatch")
	}
	if _, err := managedImage(compose); err != nil {
		return nil, err
	}
	return compose, nil
}

func (b *Backend) readBackupMetadata(jobID string) (backupMetadata, error) {
	data, err := b.files.ReadFile(backupMetadataPath(jobID))
	if err != nil {
		return backupMetadata{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var metadata backupMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return backupMetadata{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return backupMetadata{}, errors.New("backup metadata contains trailing data")
	}
	if err := validateBackupResult(BackupResult(metadata), b.descriptor.BackupDir); err != nil {
		return backupMetadata{}, err
	}
	return metadata, nil
}

func (b *Backend) stop(ctx context.Context) error {
	return b.runDocker(ctx, composeArgs("stop", ComposeService)...)
}

func (b *Backend) start(ctx context.Context) error {
	return b.runDocker(ctx, composeArgs("up", "-d", "--no-deps", ComposeService)...)
}

func (b *Backend) runDocker(ctx context.Context, args ...string) error {
	result, err := b.runner.Run(ctx, "docker", args, updatehelper.RunOptions{Dir: ComposeDir})
	if err != nil {
		return fmt.Errorf("run Docker command: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("Docker command exited with status %d", result.ExitCode)
	}
	return nil
}

func composeArgs(args ...string) []string {
	base := []string{"compose", "--project-directory", ComposeDir, "-f", ComposeFilePath}
	return append(base, args...)
}

func SnapshotPath(jobID string) string       { return filepath.Join(jobStateDir, jobID+".compose.previous") }
func SnapshotDigestPath(jobID string) string { return SnapshotPath(jobID) + ".sha256" }
func backupMetadataPath(jobID string) string { return filepath.Join(jobStateDir, jobID+".backup.json") }

func DigestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validateBackupResult(result BackupResult, backupDir string) error {
	if result.Size <= 0 || result.SchemaVersion < 0 || len(result.SHA256) != 64 {
		return errors.New("backup result is incomplete")
	}
	if _, err := hex.DecodeString(result.SHA256); err != nil || strings.ToLower(result.SHA256) != result.SHA256 {
		return errors.New("backup digest is invalid")
	}
	clean := filepath.Clean(result.Path)
	if clean == backupDir || filepath.Dir(clean) != backupDir {
		return errors.New("backup path is outside the managed backup directory")
	}
	return nil
}

func managedImage(compose []byte) (string, error) {
	line, image, err := managedImageLine(compose)
	_ = line
	return image, err
}

func replaceManagedImage(compose []byte, targetRef string) ([]byte, error) {
	line, oldImage, err := managedImageLine(compose)
	if err != nil {
		return nil, err
	}
	lines := bytes.SplitAfter(compose, []byte("\n"))
	if line < 1 || line > len(lines) {
		return nil, errors.New("managed image line is outside Compose file")
	}
	want := []byte("    image: " + oldImage)
	current := bytes.TrimSuffix(lines[line-1], []byte("\n"))
	current = bytes.TrimSuffix(current, []byte("\r"))
	if !bytes.Equal(current, want) {
		return nil, errors.New("managed image must use the installer-generated plain image line")
	}
	ending := []byte{}
	if bytes.HasSuffix(lines[line-1], []byte("\r\n")) {
		ending = []byte("\r\n")
	} else if bytes.HasSuffix(lines[line-1], []byte("\n")) {
		ending = []byte("\n")
	}
	lines[line-1] = append([]byte("    image: "+targetRef), ending...)
	return bytes.Join(lines, nil), nil
}

func managedImageLine(compose []byte) (int, string, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(compose))
	if err := decoder.Decode(&document); err != nil {
		return 0, "", fmt.Errorf("parse managed Compose file: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return 0, "", errors.New("managed Compose file contains multiple documents")
	} else if !errors.Is(err, io.EOF) {
		return 0, "", fmt.Errorf("parse trailing managed Compose data: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return 0, "", errors.New("managed Compose file must be a mapping")
	}
	if err := validateUniqueMapping(document.Content[0]); err != nil {
		return 0, "", err
	}
	services := mappingValue(document.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return 0, "", errors.New("managed Compose file has no services mapping")
	}
	if err := validateUniqueMapping(services); err != nil {
		return 0, "", err
	}
	service := mappingValue(services, ComposeService)
	if service == nil || service.Kind != yaml.MappingNode {
		return 0, "", errors.New("managed Compose file has no supermetry service")
	}
	if err := validateUniqueMapping(service); err != nil {
		return 0, "", err
	}
	if mappingValue(service, "build") != nil {
		return 0, "", errors.New("local-build Docker deployments cannot be updated")
	}
	image := mappingValue(service, "image")
	if image == nil || image.Kind != yaml.ScalarNode || image.Tag != "!!str" || image.Style != 0 {
		return 0, "", errors.New("managed supermetry image must be a plain scalar")
	}
	if !strings.HasPrefix(image.Value, officialImage+"@sha256:") || len(strings.TrimPrefix(image.Value, officialImage+"@sha256:")) != 64 {
		return 0, "", errors.New("managed supermetry image is not pinned to the official digest")
	}
	digest := strings.TrimPrefix(image.Value, officialImage+"@sha256:")
	if _, err := hex.DecodeString(digest); err != nil || strings.ToLower(digest) != digest {
		return 0, "", errors.New("managed supermetry image digest is invalid")
	}
	return image.Line, image.Value, nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func validateUniqueMapping(mapping *yaml.Node) error {
	seen := make(map[string]struct{}, len(mapping.Content)/2)
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return errors.New("managed Compose mappings must use string keys")
		}
		if _, duplicate := seen[key.Value]; duplicate {
			return fmt.Errorf("managed Compose file contains duplicate key %q", key.Value)
		}
		seen[key.Value] = struct{}{}
	}
	return nil
}

var _ updatehelper.Backend = (*Backend)(nil)
