// Command release-manifest creates and signs the metadata consumed by the
// privileged updater. The signing key is accepted only through the process
// environment so it cannot accidentally appear in a command line or archive.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"supermetry/internal/db"
	"supermetry/internal/updater/release"
)

const (
	manifestName   = "release-manifest.json"
	signatureName  = manifestName + ".sig"
	containerImage = "ghcr.io/velixio/supermetry"
)

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type options struct {
	dist               string
	version            string
	minimumVersion     string
	commit             string
	publishedAt        time.Time
	schemaVersion      int64
	rollbackCompatible bool
	containerDigest    string
	containerPlatforms []string
	privateKey         string
	publicKey          string
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "release-manifest: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("release-manifest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var opts options
	var publishedAt string
	var generateKeyDir string
	var platforms stringList
	flags.StringVar(&generateKeyDir, "generate-key-dir", "", "write a new signing key pair into an existing private directory")
	flags.StringVar(&opts.dist, "dist", "dist", "directory containing release archives")
	flags.StringVar(&opts.version, "version", "", "canonical release version, including v prefix")
	flags.StringVar(&opts.minimumVersion, "minimum-version", "v0.1.0", "oldest version supported for in-app update")
	flags.StringVar(&opts.commit, "commit", "", "full source commit hash")
	flags.StringVar(&publishedAt, "published-at", "", "publication time in canonical UTC RFC3339")
	flags.Int64Var(&opts.schemaVersion, "schema-version", -1, "target database schema version (default: latest embedded migration)")
	flags.BoolVar(&opts.rollbackCompatible, "rollback-compatible", false, "declare the target schema safe for binary rollback")
	flags.StringVar(&opts.containerDigest, "container-digest", "", "immutable multi-platform image digest")
	flags.Var(&platforms, "container-platform", "platform digest in os/arch=sha256:... form (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if generateKeyDir != "" {
		return generateKeyPair(generateKeyDir)
	}
	if publishedAt == "" {
		return errors.New("--published-at is required")
	}
	parsedTime, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil || parsedTime.Location() != time.UTC || parsedTime.Format(time.RFC3339) != publishedAt {
		return errors.New("--published-at must be canonical UTC RFC3339")
	}
	opts.publishedAt = parsedTime
	opts.containerPlatforms = platforms
	opts.privateKey = strings.TrimSpace(os.Getenv("RELEASE_SIGNING_KEY"))
	opts.publicKey = strings.TrimSpace(os.Getenv("RELEASE_SIGNING_PUBLIC_KEY"))
	if opts.schemaVersion < 0 {
		version, err := db.LatestMigrationVersion()
		if err != nil {
			return fmt.Errorf("resolve latest schema version: %w", err)
		}
		opts.schemaVersion = int64(version)
	}
	return generate(opts)
}

func generateKeyPair(directory string) error {
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("inspect key directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("key destination is not a directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("key directory must not be accessible by group or other users")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate Ed25519 signing key: %w", err)
	}
	privatePath := filepath.Join(directory, "RELEASE_SIGNING_KEY")
	publicPath := filepath.Join(directory, "RELEASE_SIGNING_PUBLIC_KEY")
	if err := writeNewKeyFile(privatePath, []byte(base64.StdEncoding.EncodeToString(privateKey)+"\n")); err != nil {
		return err
	}
	if err := writeNewKeyFile(publicPath, []byte(base64.StdEncoding.EncodeToString(publicKey)+"\n")); err != nil {
		_ = os.Remove(privatePath)
		return err
	}
	return nil
}

func writeNewKeyFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	return nil
}

func generate(opts options) error {
	privateKey, publicKey, err := decodeKeys(opts.privateKey, opts.publicKey)
	if err != nil {
		return err
	}
	artifacts, err := collectArtifacts(opts.dist, opts.version)
	if err != nil {
		return err
	}
	platforms, err := parseContainerPlatforms(opts.containerPlatforms)
	if err != nil {
		return err
	}
	manifest := release.Manifest{
		FormatVersion:  release.ManifestFormatVersion,
		Version:        opts.version,
		Commit:         opts.commit,
		PublishedAt:    opts.publishedAt.Format(time.RFC3339),
		Stable:         semver.Prerelease(opts.version) == "",
		MinimumVersion: opts.minimumVersion,
		Schema:         release.Schema{Version: opts.schemaVersion, RollbackCompatible: opts.rollbackCompatible},
		Artifacts:      artifacts,
		Container:      &release.Container{Image: containerImage, Digest: opts.containerDigest, Platforms: platforms},
	}
	manifestBytes, err := release.MarshalCanonical(manifest)
	if err != nil {
		return fmt.Errorf("create canonical manifest: %w", err)
	}
	signatureBytes, err := release.MarshalSignature(release.DetachedSignature{
		KeyID:     keyID(publicKey),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestBytes)),
	})
	if err != nil {
		return fmt.Errorf("create manifest signature: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(opts.dist, manifestName), manifestBytes); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(opts.dist, signatureName), signatureBytes); err != nil {
		return err
	}
	return nil
}

func decodeKeys(privateEncoded, publicEncoded string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if privateEncoded == "" || publicEncoded == "" {
		return nil, nil, errors.New("RELEASE_SIGNING_KEY and RELEASE_SIGNING_PUBLIC_KEY are required")
	}
	privateKey, err := base64.StdEncoding.Strict().DecodeString(privateEncoded)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, nil, errors.New("RELEASE_SIGNING_KEY must be standard base64 for a 64-byte Ed25519 private key")
	}
	publicKey, err := base64.StdEncoding.Strict().DecodeString(publicEncoded)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, nil, errors.New("RELEASE_SIGNING_PUBLIC_KEY must be standard base64 for a 32-byte Ed25519 public key")
	}
	if !bytes.Equal(privateKey[ed25519.SeedSize:], publicKey) {
		return nil, nil, errors.New("release signing private key does not match public key")
	}
	return ed25519.PrivateKey(privateKey), ed25519.PublicKey(publicKey), nil
}

func keyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return "ed25519-" + hex.EncodeToString(digest[:8])
}

func collectArtifacts(dist, version string) ([]release.Artifact, error) {
	if dist == "" {
		return nil, errors.New("--dist is required")
	}
	versionName := strings.TrimPrefix(version, "v")
	pattern := regexp.MustCompile(`^supermetry_` + regexp.QuoteMeta(versionName) + `_(linux|darwin|windows)_(amd64|arm64|armv6|armv7)\.(tar\.gz|zip)$`)
	// Bare binaries are published for manual downloads and covered by SHA256SUMS; updates use the archives.
	barePattern := regexp.MustCompile(`^supermetry_` + regexp.QuoteMeta(versionName) + `_((linux|darwin)_(amd64|arm64|armv6|armv7)|windows_(amd64|arm64)\.exe)$`)
	entries, err := os.ReadDir(dist)
	if err != nil {
		return nil, fmt.Errorf("read release directory: %w", err)
	}
	var artifacts []release.Artifact
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "supermetry_"+versionName+"_") || barePattern.MatchString(entry.Name()) {
			continue
		}
		matches := pattern.FindStringSubmatch(entry.Name())
		if matches == nil || matches[1] == "windows" && matches[3] != "zip" || matches[1] != "windows" && matches[3] != "tar.gz" {
			return nil, fmt.Errorf("unsupported release artifact %q", entry.Name())
		}
		path := filepath.Join(dist, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect release artifact %q: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("release artifact %q is not a regular file", entry.Name())
		}
		digest, err := hashFile(path)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, release.Artifact{Name: entry.Name(), OS: matches[1], Arch: matches[2], Size: info.Size(), SHA256: digest})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Name < artifacts[j].Name })
	if len(artifacts) == 0 {
		return nil, errors.New("no release archives found")
	}
	return artifacts, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open release artifact: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash release artifact: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func parseContainerPlatforms(values []string) ([]release.ContainerPlatform, error) {
	platforms := make([]release.ContainerPlatform, 0, len(values))
	for _, value := range values {
		platform, digest, ok := strings.Cut(value, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --container-platform %q", value)
		}
		goos, goarch, ok := strings.Cut(platform, "/")
		if !ok {
			return nil, fmt.Errorf("invalid --container-platform %q", value)
		}
		platforms = append(platforms, release.ContainerPlatform{OS: goos, Arch: goarch, Digest: digest})
	}
	sort.Slice(platforms, func(i, j int) bool {
		return platforms[i].OS+"/"+platforms[i].Arch < platforms[j].OS+"/"+platforms[j].Arch
	})
	return platforms, nil
}

func writeFileAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", filepath.Base(path), err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary manifest permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("publish %s: %w", filepath.Base(path), err)
	}
	return nil
}
