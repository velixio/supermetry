package updaterdaemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"supermetry/internal/appbackup"
	"supermetry/internal/db"
	"supermetry/internal/updatebinary"
	"supermetry/internal/updatedocker"
	"supermetry/internal/updatehelper"
	"supermetry/internal/updater/release"
)

const maxCommandOutput = 1 << 20

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, executable string, args []string, options updatehelper.RunOptions) (updatehelper.RunResult, error) {
	if executable == "docker" {
		executable = "/usr/bin/docker"
	}
	if executable == "" || !filepath.IsAbs(executable) {
		return updatehelper.RunResult{}, errors.New("executable must be an absolute path or the fixed docker command")
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = options.Dir
	if options.Env != nil {
		command.Env = append([]string(nil), options.Env...)
	}
	var stdout, stderr boundedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	result := updatehelper.RunResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

type boundedBuffer struct {
	data     bytes.Buffer
	overflow bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	available := maxCommandOutput - b.data.Len()
	if available > 0 {
		_, _ = b.data.Write(data[:min(available, len(data))])
	}
	if len(data) > available {
		b.overflow = true
	}
	return len(data), nil
}

func (b *boundedBuffer) Bytes() []byte { return bytes.Clone(b.data.Bytes()) }

func LoadDescriptor(path string) (updatehelper.DeploymentDescriptor, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return updatehelper.DeploymentDescriptor{}, fmt.Errorf("inspect deployment descriptor: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return updatehelper.DeploymentDescriptor{}, errors.New("deployment descriptor must be a non-writable regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return updatehelper.DeploymentDescriptor{}, err
	}
	defer file.Close()
	return updatehelper.ParseDeploymentDescriptor(file)
}

type ProductionConfig struct {
	Descriptor  updatehelper.DeploymentDescriptor
	Keyring     release.Keyring
	JobsDir     string
	Environment []string
	Runner      updatehelper.Runner
	HTTPClient  *http.Client
}

func NewBackendFactory(_ context.Context, config ProductionConfig) (BackendFactory, error) {
	if err := config.Descriptor.Validate(); err != nil {
		return nil, err
	}
	if config.Runner == nil {
		config.Runner = ExecRunner{}
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("readiness redirects are not allowed")
			},
		}
	}
	releaseClient := release.NewClient(nil)
	if config.JobsDir == "" {
		config.JobsDir = updatehelper.DefaultJobStoreDir
	}
	switch config.Descriptor.Mode {
	case updatehelper.DeploymentBinary:
		databaseURL, err := appbackup.ResolveDatabaseURL(envLookup(config.Environment))
		if err != nil {
			return nil, err
		}
		probe := &binaryProbe{runner: config.Runner, http: config.HTTPClient, databaseURL: databaseURL, executable: filepath.Join(config.Descriptor.CurrentLink, "supermetry")}
		return func(jobContext context.Context, _ updatehelper.Job) (updatehelper.Backend, error) {
			currentSchema, err := probe.CurrentSchema(jobContext)
			if err != nil {
				return nil, err
			}
			versionResult, err := config.Runner.Run(jobContext, probe.executable, []string{"version"}, updatehelper.RunOptions{})
			if err != nil || versionResult.ExitCode != 0 || parseVersion(versionResult.Stdout) == "" {
				return nil, errors.New("read current binary version")
			}
			return updatebinary.New(updatebinary.Config{
				Descriptor: config.Descriptor, Client: releaseClient, Keyring: config.Keyring,
				Runner: config.Runner, Filesystem: updatebinary.OSFileSystem{},
				Backup: updatebinary.NewBinaryBackupCreator(config.Runner), Probe: probe,
				CurrentVersion: parseVersion(versionResult.Stdout), CurrentSchema: currentSchema,
				Environment: append([]string(nil), config.Environment...),
			})
		}, nil
	case updatehelper.DeploymentDocker:
		probe := &dockerProbe{runner: config.Runner, http: config.HTTPClient, readinessURL: config.Descriptor.ReadinessURL}
		backup := &dockerBackup{runner: config.Runner}
		return func(ctx context.Context, job updatehelper.Job) (updatehelper.Backend, error) {
			currentVersion, err := previousDockerVersion(ctx, config.JobsDir, probe, job)
			if err != nil {
				return nil, err
			}
			manifestPath := filepath.Join(config.JobsDir, job.ID+".release-manifest.json")
			signaturePath := manifestPath + ".sig"
			var manifestBytes, signatureBytes []byte
			if job.State == updatehelper.StateRequested {
				manifestBytes, signatureBytes, err = releaseClient.FetchManifest(ctx, job.TargetVersion)
				if err != nil {
					return nil, err
				}
			} else {
				manifestBytes, err = os.ReadFile(manifestPath)
				if err == nil {
					signatureBytes, err = os.ReadFile(signaturePath)
				}
				if err != nil {
					return nil, errors.New("verified Docker release metadata is unavailable")
				}
			}
			manifest, err := config.Keyring.Verify(manifestBytes, signatureBytes)
			if err != nil {
				return nil, fmt.Errorf("verify release manifest: %w", err)
			}
			if manifest.Version != job.TargetVersion {
				return nil, errors.New("release manifest target mismatch")
			}
			if job.State == updatehelper.StateRequested {
				if err := atomicPrivateFile(manifestPath, manifestBytes); err != nil {
					return nil, fmt.Errorf("persist verified Docker manifest: %w", err)
				}
				if err := atomicPrivateFile(signaturePath, signatureBytes); err != nil {
					return nil, fmt.Errorf("persist verified Docker signature: %w", err)
				}
			}
			return updatedocker.New(updatedocker.Config{
				Runner: config.Runner, Files: updatedocker.OSFileSystem{}, Backup: backup, Probe: probe,
				Descriptor: config.Descriptor, Manifest: manifest, CurrentVersion: currentVersion,
			})
		}, nil
	default:
		return nil, errors.New("unsupported deployment mode")
	}
}

func previousDockerVersion(ctx context.Context, jobsDir string, probe *dockerProbe, job updatehelper.Job) (string, error) {
	path := filepath.Join(jobsDir, job.ID+".previous-version")
	if job.State == updatehelper.StateRequested {
		version, err := probe.ReadyVersion(ctx, probe.readinessURL)
		if err != nil {
			return "", fmt.Errorf("read current Docker version: %w", err)
		}
		if err := atomicPrivateFile(path, []byte(version+"\n")); err != nil {
			return "", fmt.Errorf("persist previous Docker version: %w", err)
		}
		return version, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("previous Docker version is unavailable")
	}
	version := strings.TrimSpace(string(data))
	if version == "" || strings.ContainsAny(version, " \t\r\n") {
		return "", errors.New("previous Docker version is invalid")
	}
	return version, nil
}

func atomicPrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".metadata-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

type binaryProbe struct {
	runner      updatehelper.Runner
	http        *http.Client
	databaseURL string
	executable  string
}

func (p *binaryProbe) Check(ctx context.Context, readinessURL string) (updatebinary.HealthStatus, error) {
	if err := ready(ctx, p.http, readinessURL); err != nil {
		return updatebinary.HealthStatus{}, err
	}
	result, err := p.runner.Run(ctx, p.executable, []string{"version"}, updatehelper.RunOptions{})
	if err != nil || result.ExitCode != 0 {
		return updatebinary.HealthStatus{}, errors.New("read running binary version")
	}
	schema, err := p.CurrentSchema(ctx)
	if err != nil {
		return updatebinary.HealthStatus{}, err
	}
	return updatebinary.HealthStatus{Version: parseVersion(result.Stdout), SchemaVersion: schema}, nil
}

func (p *binaryProbe) CurrentSchema(ctx context.Context) (int64, error) {
	return databaseSchema(ctx, p.databaseURL)
}

type dockerProbe struct {
	runner       updatehelper.Runner
	http         *http.Client
	readinessURL string
}

func (p *dockerProbe) InspectImage(ctx context.Context, exactRef string) (updatedocker.ImageInfo, error) {
	format := `{"repo_digests":{{json .RepoDigests}},"os":{{json .Os}},"arch":{{json .Architecture}}}`
	result, err := p.runner.Run(ctx, "docker", []string{"image", "inspect", "--format", format, exactRef}, updatehelper.RunOptions{})
	if err != nil || result.ExitCode != 0 {
		return updatedocker.ImageInfo{}, errors.New("inspect pulled Docker image")
	}
	var value struct {
		RepoDigests []string `json:"repo_digests"`
		OS          string   `json:"os"`
		Arch        string   `json:"arch"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(result.Stdout), &value); err != nil {
		return updatedocker.ImageInfo{}, errors.New("decode pulled Docker image identity")
	}
	_, wantedDigest, ok := strings.Cut(exactRef, "@")
	if !ok {
		return updatedocker.ImageInfo{}, errors.New("exact Docker image digest is required")
	}
	for _, item := range value.RepoDigests {
		if strings.HasSuffix(item, "@"+wantedDigest) {
			return updatedocker.ImageInfo{Digest: wantedDigest, OS: value.OS, Arch: value.Arch}, nil
		}
	}
	return updatedocker.ImageInfo{}, errors.New("pulled Docker image digest is absent")
}

func (p *dockerProbe) ReadyVersion(ctx context.Context, readinessURL string) (string, error) {
	if err := ready(ctx, p.http, readinessURL); err != nil {
		return "", err
	}
	result, err := p.runner.Run(ctx, "docker", composeCommand("exec", "-T", updatedocker.ComposeService, "/supermetry", "version"), updatehelper.RunOptions{Dir: updatedocker.ComposeDir})
	if err != nil || result.ExitCode != 0 {
		return "", errors.New("read running Docker version")
	}
	version := parseVersion(result.Stdout)
	if version == "" {
		return "", errors.New("running Docker version is invalid")
	}
	return version, nil
}

func (p *dockerProbe) SchemaVersion(ctx context.Context) (int64, error) {
	query := "select coalesce(max(version),0) from schema_migrations"
	result, err := p.runner.Run(ctx, "docker", composeCommand("exec", "-T", "postgres", "psql", "-U", "supermetry", "-d", "supermetry", "-Atc", query), updatehelper.RunOptions{Dir: updatedocker.ComposeDir})
	if err != nil || result.ExitCode != 0 {
		return 0, errors.New("read Docker database schema")
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(result.Stdout)), 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("Docker database returned an invalid schema version")
	}
	return value, nil
}

type dockerBackup struct{ runner updatehelper.Runner }

func (b *dockerBackup) Create(ctx context.Context, request updatedocker.BackupRequest) (updatedocker.BackupResult, error) {
	if request.ComposeFile != updatedocker.ComposeFilePath || request.ComposeService != updatedocker.ComposeService || request.DestinationDir == "" || request.OldImage == "" {
		return updatedocker.BackupResult{}, errors.New("Docker backup request is outside the managed deployment")
	}
	filename := "update-" + request.JobID + ".tar.gz"
	hostPath := filepath.Join(request.DestinationDir, filename)
	containerPath := filepath.Join("/backup", filename)
	if err := os.MkdirAll(request.DestinationDir, 0o700); err != nil {
		return updatedocker.BackupResult{}, err
	}
	args := composeCommand("run", "--rm", "--no-deps", "--user", "0:0", "-v", request.DestinationDir+":/backup", request.ComposeService, "backup", "--out", containerPath)
	result, err := b.runner.Run(ctx, "docker", args, updatehelper.RunOptions{Dir: updatedocker.ComposeDir})
	if err != nil || result.ExitCode != 0 {
		return updatedocker.BackupResult{}, errors.New("create Docker database backup")
	}
	manifest, err := appbackup.Verify(ctx, hostPath)
	if err != nil {
		return updatedocker.BackupResult{}, fmt.Errorf("verify Docker database backup: %w", err)
	}
	file, err := os.Open(hostPath)
	if err != nil {
		return updatedocker.BackupResult{}, err
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return updatedocker.BackupResult{}, errors.Join(copyErr, closeErr)
	}
	return updatedocker.BackupResult{Path: hostPath, SHA256: hex.EncodeToString(hasher.Sum(nil)), Size: size, SchemaVersion: int64(manifest.SchemaVersion)}, nil
}

func ready(ctx context.Context, client *http.Client, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned HTTP %d", response.StatusCode)
	}
	return nil
}

func databaseSchema(ctx context.Context, databaseURL string) (int64, error) {
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		return 0, errors.New("database is not reachable")
	}
	defer pool.Close()
	var value int64
	if err := pool.QueryRow(ctx, `select coalesce(max(version), 0) from schema_migrations`).Scan(&value); err != nil {
		return 0, errors.New("database schema is unavailable")
	}
	return value, nil
}

func parseVersion(output []byte) string {
	fields := strings.Fields(string(output))
	if len(fields) == 2 && fields[0] == "supermetry" {
		version := "v" + strings.TrimPrefix(fields[1], "v")
		if semver.IsValid(version) && semver.Canonical(version) == version && semver.Prerelease(version) == "" {
			return version
		}
	}
	return ""
}

func composeCommand(args ...string) []string {
	base := []string{"compose", "--project-directory", updatedocker.ComposeDir, "-f", updatedocker.ComposeFilePath}
	return append(base, args...)
}

func envLookup(environment []string) func(string) string {
	values := make(map[string]string, len(environment))
	for _, item := range environment {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return func(key string) string { return values[key] }
}
