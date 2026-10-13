package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supermetry/internal/updater/release"
)

func TestGenerateCreatesVerifiableCanonicalManifest(t *testing.T) {
	t.Parallel()
	dist := t.TempDir()
	writeArtifact(t, dist, "supermetry_1.2.3_linux_amd64.tar.gz", "linux")
	writeArtifact(t, dist, "supermetry_1.2.3_linux_armv6.tar.gz", "armv6")
	writeArtifact(t, dist, "supermetry_1.2.3_linux_armv7.tar.gz", "armv7")
	writeArtifact(t, dist, "supermetry_1.2.3_windows_arm64.zip", "windows")
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	opts := options{
		dist: dist, version: "v1.2.3", minimumVersion: "v1.0.0",
		commit: strings.Repeat("a", 40), publishedAt: time.Date(2026, 10, 14, 2, 3, 4, 0, time.UTC),
		schemaVersion: 150, rollbackCompatible: false,
		containerDigest: "sha256:" + strings.Repeat("b", 64),
		containerPlatforms: []string{
			"linux/arm64=sha256:" + strings.Repeat("d", 64),
			"linux/amd64=sha256:" + strings.Repeat("c", 64),
		},
		privateKey: base64.StdEncoding.EncodeToString(private),
		publicKey:  base64.StdEncoding.EncodeToString(public),
	}
	if err := generate(opts); err != nil {
		t.Fatal(err)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(dist, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	signatureBytes, err := os.ReadFile(filepath.Join(dist, signatureName))
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := release.NewKeyring(map[string]ed25519.PublicKey{keyID(public): public})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := keyring.Verify(manifestBytes, signatureBytes)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "v1.2.3" || manifest.Schema.Version != 150 || manifest.Schema.RollbackCompatible {
		t.Fatalf("unexpected manifest metadata: %+v", manifest)
	}
	if len(manifest.Artifacts) != 4 || manifest.Artifacts[0].Name != "supermetry_1.2.3_linux_amd64.tar.gz" {
		t.Fatalf("artifacts are not deterministic: %+v", manifest.Artifacts)
	}
	if manifest.Artifacts[0].Size != 5 || manifest.Artifacts[0].SHA256 != hashString("linux") {
		t.Fatalf("artifact metadata = %+v", manifest.Artifacts[0])
	}
	if manifest.Artifacts[1].Arch != "armv6" || manifest.Artifacts[2].Arch != "armv7" {
		t.Fatalf("Raspberry Pi artifact metadata = %+v", manifest.Artifacts[1:3])
	}
	if len(manifest.Container.Platforms) != 2 || manifest.Container.Platforms[0].Arch != "amd64" {
		t.Fatalf("container platforms are not deterministic: %+v", manifest.Container.Platforms)
	}
}

func TestGenerateRejectsMismatchedKeyAndUnexpectedArtifact(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	base := options{
		dist: t.TempDir(), version: "v1.2.3", minimumVersion: "v1.0.0",
		commit: strings.Repeat("a", 40), publishedAt: time.Date(2026, 10, 14, 2, 3, 4, 0, time.UTC),
		containerDigest:    "sha256:" + strings.Repeat("b", 64),
		containerPlatforms: []string{"linux/amd64=sha256:" + strings.Repeat("c", 64)},
		privateKey:         base64.StdEncoding.EncodeToString(private), publicKey: base64.StdEncoding.EncodeToString(otherPublic),
	}
	writeArtifact(t, base.dist, "supermetry_1.2.3_linux_amd64.tar.gz", "linux")
	if err := generate(base); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("generate() error = %v, want key mismatch", err)
	}

	base.publicKey = base64.StdEncoding.EncodeToString(public)
	if err := os.WriteFile(filepath.Join(base.dist, "supermetry_1.2.3_freebsd_amd64.tar.gz"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := generate(base); err == nil || !strings.Contains(err.Error(), "unsupported release artifact") {
		t.Fatalf("generate() error = %v, want unsupported artifact", err)
	}
}

func TestGenerateKeyPairWritesPrivateFilesWithoutOverwriting(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := generateKeyPair(directory); err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(directory, "RELEASE_SIGNING_KEY")
	publicPath := filepath.Join(directory, "RELEASE_SIGNING_PUBLIC_KEY")
	privateEncoded, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	publicEncoded, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, publicKey, err := decodeKeys(strings.TrimSpace(string(privateEncoded)), strings.TrimSpace(string(publicEncoded)))
	if err != nil {
		t.Fatal(err)
	}
	if len(privateKey) != ed25519.PrivateKeySize || len(publicKey) != ed25519.PublicKeySize {
		t.Fatal("generated key sizes are invalid")
	}
	for _, path := range []string{privatePath, publicPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %o, want 600", filepath.Base(path), info.Mode().Perm())
		}
	}
	if err := generateKeyPair(directory); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("second generateKeyPair() error = %v, want exists", err)
	}
}

func writeArtifact(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
