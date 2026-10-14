package release_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"supermetry/internal/updater/release"
)

func TestManifestCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	manifest := validManifest()

	encoded, err := release.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := release.ParseCanonical(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := release.MarshalCanonical(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, encoded) {
		t.Fatalf("canonical bytes changed:\n%s\n%s", encoded, reencoded)
	}
	if !bytes.HasSuffix(encoded, []byte("\n")) {
		t.Fatal("canonical manifest must end with one newline")
	}
}

func TestManifestAcceptsRaspberryPiArtifactsButRejectsArmVariantContainers(t *testing.T) {
	t.Parallel()
	manifest := validManifest()
	manifest.Artifacts = append(manifest.Artifacts,
		release.Artifact{Name: "supermetry_1.2.0_linux_armv6.tar.gz", OS: "linux", Arch: "armv6", Size: 7, SHA256: strings.Repeat("e", 64)},
		release.Artifact{Name: "supermetry_1.2.0_linux_armv7.tar.gz", OS: "linux", Arch: "armv7", Size: 7, SHA256: strings.Repeat("f", 64)},
	)
	if _, err := release.MarshalCanonical(manifest); err != nil {
		t.Fatalf("Raspberry Pi artifacts rejected: %v", err)
	}

	manifest.Container.Platforms = append(manifest.Container.Platforms, release.ContainerPlatform{
		OS: "linux", Arch: "armv7", Digest: "sha256:" + strings.Repeat("9", 64),
	})
	if _, err := release.MarshalCanonical(manifest); err == nil || !strings.Contains(err.Error(), "container platform") {
		t.Fatalf("armv7 container error = %v, want unsupported container platform", err)
	}
}

func TestParseCanonicalRejectsNonCanonicalAndInvalidManifests(t *testing.T) {
	t.Parallel()
	valid, err := release.MarshalCanonical(validManifest())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "whitespace", data: append([]byte(" "), valid...), want: "canonical"},
		{name: "unknown field", data: []byte(strings.Replace(string(valid), `"format_version":1`, `"extra":true,"format_version":1`, 1)), want: "unknown field"},
		{name: "unsupported format", data: []byte(strings.Replace(string(valid), `"format_version":1`, `"format_version":2`, 1)), want: "format version"},
		{name: "invalid version", data: []byte(strings.Replace(string(valid), `"version":"v1.2.0"`, `"version":"latest"`, 1)), want: "version"},
		{name: "invalid digest", data: []byte(strings.Replace(string(valid), strings.Repeat("a", 64), "abc", 1)), want: "sha256"},
		{name: "unsafe artifact name", data: []byte(strings.Replace(string(valid), "supermetry_1.2.0_linux_amd64.tar.gz", "..", 1)), want: "unsafe"},
		{name: "duplicate platform", data: duplicateArtifact(t), want: "duplicate platform"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := release.ParseCanonical(tt.data)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("ParseCanonical() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestKeyringVerifiesCanonicalManifestAndSupportsRotation(t *testing.T) {
	t.Parallel()
	oldPublic, oldPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newPublic, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := release.MarshalCanonical(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := release.NewKeyring(map[string]ed25519.PublicKey{"2026-old": oldPublic, "2027-new": newPublic})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		key  string
		priv ed25519.PrivateKey
	}{
		{name: "old key", key: "2026-old", priv: oldPrivate},
		{name: "rotated key", key: "2027-new", priv: newPrivate},
	} {
		t.Run(tt.name, func(t *testing.T) {
			signature := release.DetachedSignature{KeyID: tt.key, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(tt.priv, manifestBytes))}
			signatureBytes, err := release.MarshalSignature(signature)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := keyring.Verify(manifestBytes, signatureBytes); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKeyringRejectsUnknownKeyTamperingAndNonCanonicalSignature(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := release.MarshalCanonical(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := release.NewKeyring(map[string]ed25519.PublicKey{"current": public})
	if err != nil {
		t.Fatal(err)
	}
	signed := func(keyID string, bytes []byte) []byte {
		t.Helper()
		result, marshalErr := release.MarshalSignature(release.DetachedSignature{
			KeyID: keyID, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, bytes)),
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return result
	}

	tests := []struct {
		name      string
		manifest  []byte
		signature []byte
		want      string
	}{
		{name: "unknown key", manifest: manifestBytes, signature: signed("retired", manifestBytes), want: "unknown"},
		{name: "tampered manifest", manifest: bytes.Replace(manifestBytes, []byte("v1.2.0"), []byte("v1.2.1"), 1), signature: signed("current", manifestBytes), want: "signature"},
		{name: "non canonical signature", manifest: manifestBytes, signature: append([]byte(" "), signed("current", manifestBytes)...), want: "canonical"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := keyring.Verify(tt.manifest, tt.signature)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("Verify() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func validManifest() release.Manifest {
	return release.Manifest{
		FormatVersion:  release.ManifestFormatVersion,
		Version:        "v1.2.0",
		Commit:         strings.Repeat("1", 40),
		PublishedAt:    "2026-10-14T02:03:04Z",
		Stable:         true,
		MinimumVersion: "v1.0.0",
		Schema:         release.Schema{Version: 42, RollbackCompatible: true},
		Artifacts: []release.Artifact{
			{Name: "supermetry_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Size: 7, SHA256: strings.Repeat("a", 64)},
			{Name: "supermetry_1.2.0_windows_amd64.zip", OS: "windows", Arch: "amd64", Size: 9, SHA256: strings.Repeat("b", 64)},
		},
		Container: &release.Container{
			Image:     "ghcr.io/velixio/supermetry",
			Digest:    "sha256:" + strings.Repeat("c", 64),
			Platforms: []release.ContainerPlatform{{OS: "linux", Arch: "amd64", Digest: "sha256:" + strings.Repeat("d", 64)}},
		},
	}
}

func duplicateArtifact(t *testing.T) []byte {
	t.Helper()
	manifest := validManifest()
	manifest.Artifacts = append(manifest.Artifacts, manifest.Artifacts[0])
	data, err := release.MarshalCanonical(manifest)
	if err == nil {
		t.Fatal("MarshalCanonical accepted duplicate platform")
	}
	// Use valid canonical JSON so parsing reaches semantic validation.
	manifest.Artifacts = manifest.Artifacts[:2]
	data, err = release.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	needle := `]` + `,"container"`
	duplicate := `,{"name":"supermetry_copy.tar.gz","os":"linux","arch":"amd64","size":7,"sha256":"` + strings.Repeat("e", 64) + `"}]` + `,"container"`
	return []byte(strings.Replace(string(data), needle, duplicate, 1))
}
