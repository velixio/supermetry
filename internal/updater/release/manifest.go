// Package release verifies and selects official Supermetry release artifacts.
// It does not download, extract, or apply an update.
package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	ManifestFormatVersion  = 1
	maxArtifactSize        = int64(512 << 20)
	officialContainerImage = "ghcr.io/velixio/supermetry"
)

type Manifest struct {
	FormatVersion  int        `json:"format_version"`
	Version        string     `json:"version"`
	Commit         string     `json:"commit"`
	PublishedAt    string     `json:"published_at"`
	Stable         bool       `json:"stable"`
	MinimumVersion string     `json:"minimum_version"`
	Schema         Schema     `json:"schema"`
	Artifacts      []Artifact `json:"artifacts"`
	Container      *Container `json:"container,omitempty"`
}

type Schema struct {
	Version            int64 `json:"version"`
	RollbackCompatible bool  `json:"rollback_compatible"`
}

type Artifact struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Container struct {
	Image     string              `json:"image"`
	Digest    string              `json:"digest"`
	Platforms []ContainerPlatform `json:"platforms"`
}

type ContainerPlatform struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Digest string `json:"digest"`
}

type DetachedSignature struct {
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

func MarshalCanonical(manifest Manifest) ([]byte, error) {
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	return marshalCanonical(manifest)
}

func ParseCanonical(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := decodeStrict(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	canonical, err := marshalCanonical(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.Equal(data, canonical) {
		return Manifest{}, errors.New("release manifest is not canonical JSON")
	}
	return manifest, nil
}

func MarshalSignature(signature DetachedSignature) ([]byte, error) {
	if err := validateSignature(signature); err != nil {
		return nil, err
	}
	return marshalCanonical(signature)
}

type Keyring struct {
	keys map[string]ed25519.PublicKey
}

func NewKeyring(keys map[string]ed25519.PublicKey) (Keyring, error) {
	if len(keys) == 0 {
		return Keyring{}, errors.New("release keyring is empty")
	}
	result := Keyring{keys: make(map[string]ed25519.PublicKey, len(keys))}
	for id, key := range keys {
		if !validKeyID(id) {
			return Keyring{}, fmt.Errorf("invalid release key ID %q", id)
		}
		if len(key) != ed25519.PublicKeySize {
			return Keyring{}, fmt.Errorf("release key %q has invalid size", id)
		}
		result.keys[id] = bytes.Clone(key)
	}
	return result, nil
}

func (k Keyring) Verify(manifestBytes, signatureBytes []byte) (Manifest, error) {
	manifest, err := ParseCanonical(manifestBytes)
	if err != nil {
		return Manifest{}, err
	}
	var signature DetachedSignature
	if err := decodeStrict(signatureBytes, &signature); err != nil {
		return Manifest{}, fmt.Errorf("decode release signature: %w", err)
	}
	if err := validateSignature(signature); err != nil {
		return Manifest{}, err
	}
	canonical, err := marshalCanonical(signature)
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.Equal(signatureBytes, canonical) {
		return Manifest{}, errors.New("release signature is not canonical JSON")
	}
	key, ok := k.keys[signature.KeyID]
	if !ok {
		return Manifest{}, fmt.Errorf("unknown release signing key %q", signature.KeyID)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(signature.Signature)
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return Manifest{}, errors.New("release signature is invalid")
	}
	if !ed25519.Verify(key, manifestBytes, decoded) {
		return Manifest{}, errors.New("release signature verification failed")
	}
	return manifest, nil
}

func marshalCanonical(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return append(encoded, '\n'), nil
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("unexpected data after JSON object")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.FormatVersion != ManifestFormatVersion {
		return fmt.Errorf("unsupported release manifest format version %d", manifest.FormatVersion)
	}
	if !semver.IsValid(manifest.Version) || semver.Canonical(manifest.Version) != manifest.Version {
		return errors.New("release manifest version must be canonical semantic version")
	}
	if manifest.Stable && semver.Prerelease(manifest.Version) != "" {
		return errors.New("stable release manifest contains a prerelease version")
	}
	if !validHex(manifest.Commit, 40) {
		return errors.New("release manifest commit must be a 40-character lowercase hexadecimal hash")
	}
	publishedAt, err := time.Parse(time.RFC3339, manifest.PublishedAt)
	if err != nil || publishedAt.Location() != time.UTC || publishedAt.Format(time.RFC3339) != manifest.PublishedAt {
		return errors.New("release manifest published_at must be canonical UTC RFC3339")
	}
	if !semver.IsValid(manifest.MinimumVersion) || semver.Canonical(manifest.MinimumVersion) != manifest.MinimumVersion || semver.Prerelease(manifest.MinimumVersion) != "" {
		return errors.New("release manifest minimum_version must be a canonical stable semantic version")
	}
	if semver.Compare(manifest.MinimumVersion, manifest.Version) > 0 {
		return errors.New("release manifest minimum_version cannot be newer than the release")
	}
	if manifest.Schema.Version < 0 {
		return errors.New("release manifest schema version cannot be negative")
	}
	if len(manifest.Artifacts) == 0 {
		return errors.New("release manifest has no binary artifacts")
	}
	names := make(map[string]struct{}, len(manifest.Artifacts))
	platforms := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if err := validateArtifact(artifact); err != nil {
			return err
		}
		if _, duplicate := names[artifact.Name]; duplicate {
			return fmt.Errorf("duplicate artifact name %q", artifact.Name)
		}
		names[artifact.Name] = struct{}{}
		platform := artifact.OS + "/" + artifact.Arch
		if _, duplicate := platforms[platform]; duplicate {
			return fmt.Errorf("duplicate platform %q in binary artifacts", platform)
		}
		platforms[platform] = struct{}{}
	}
	if manifest.Container != nil {
		if err := validateContainer(*manifest.Container); err != nil {
			return err
		}
	}
	return nil
}

func validateArtifact(artifact Artifact) error {
	if artifact.Name == "" || artifact.Name == "." || artifact.Name == ".." || path.Base(artifact.Name) != artifact.Name || strings.ContainsAny(artifact.Name, `\\`) || !validArtifactName(artifact.Name) {
		return fmt.Errorf("artifact name %q is unsafe", artifact.Name)
	}
	if !supportedArtifactPlatform(artifact.OS, artifact.Arch) {
		return fmt.Errorf("artifact %q has unsupported platform %s/%s", artifact.Name, artifact.OS, artifact.Arch)
	}
	if artifact.Size <= 0 || artifact.Size > maxArtifactSize {
		return fmt.Errorf("artifact %q has invalid size", artifact.Name)
	}
	if !validHex(artifact.SHA256, 64) {
		return fmt.Errorf("artifact %q has invalid sha256", artifact.Name)
	}
	return nil
}

func validArtifactName(name string) bool {
	for _, character := range name {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '.' && character != '_' && character != '+' && character != '-' {
			return false
		}
	}
	return true
}

func validateContainer(container Container) error {
	if container.Image != officialContainerImage {
		return errors.New("release manifest container image is not official")
	}
	if !validDigest(container.Digest) {
		return errors.New("release manifest container digest is invalid")
	}
	if len(container.Platforms) == 0 {
		return errors.New("release manifest has no container platforms")
	}
	seen := make(map[string]struct{}, len(container.Platforms))
	for _, platform := range container.Platforms {
		if !supportedContainerPlatform(platform.OS, platform.Arch) {
			return fmt.Errorf("unsupported container platform %s/%s", platform.OS, platform.Arch)
		}
		if !validDigest(platform.Digest) {
			return fmt.Errorf("container platform %s/%s has invalid digest", platform.OS, platform.Arch)
		}
		key := platform.OS + "/" + platform.Arch
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate platform %q in container artifacts", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateSignature(signature DetachedSignature) error {
	if !validKeyID(signature.KeyID) {
		return errors.New("release signature key ID is invalid")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(signature.Signature)
	if err != nil || len(decoded) != ed25519.SignatureSize {
		return errors.New("release signature is invalid")
	}
	return nil
}

func validKeyID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, character := range id {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func supportedArtifactPlatform(goos, goarch string) bool {
	switch goos {
	case "darwin", "windows":
		return goarch == "amd64" || goarch == "arm64"
	case "linux":
		return goarch == "amd64" || goarch == "arm64" || goarch == "armv6" || goarch == "armv7"
	default:
		return false
	}
}

func supportedContainerPlatform(goos, goarch string) bool {
	return goos == "linux" && (goarch == "amd64" || goarch == "arm64")
}

func validDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validHex(strings.TrimPrefix(value, "sha256:"), 64)
}

func validHex(value string, length int) bool {
	if len(value) != length || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
