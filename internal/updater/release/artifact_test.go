package release_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"supermetry/internal/updater/release"
)

func TestVerifyArtifactAcceptsExactSizeAndDigest(t *testing.T) {
	t.Parallel()
	contents := []byte("signed release archive")
	digest := sha256.Sum256(contents)
	artifact := release.Artifact{Name: "supermetry_1.2.0_linux_amd64.tar.gz", OS: "linux", Arch: "amd64", Size: int64(len(contents)), SHA256: fmt.Sprintf("%x", digest)}
	if err := release.VerifyArtifact(bytes.NewReader(contents), artifact); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyArtifactRejectsSizeAndDigestMismatch(t *testing.T) {
	t.Parallel()
	contents := []byte("release")
	digest := sha256.Sum256(contents)
	tests := []struct {
		name     string
		artifact release.Artifact
		want     string
	}{
		{name: "too short", artifact: release.Artifact{Name: "release.tar.gz", OS: "linux", Arch: "amd64", Size: int64(len(contents) + 1), SHA256: fmt.Sprintf("%x", digest)}, want: "size"},
		{name: "too long", artifact: release.Artifact{Name: "release.tar.gz", OS: "linux", Arch: "amd64", Size: int64(len(contents) - 1), SHA256: fmt.Sprintf("%x", digest)}, want: "size"},
		{name: "digest mismatch", artifact: release.Artifact{Name: "release.tar.gz", OS: "linux", Arch: "amd64", Size: int64(len(contents)), SHA256: strings.Repeat("0", 64)}, want: "digest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := release.VerifyArtifact(bytes.NewReader(contents), tt.artifact)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("VerifyArtifact() error = %v, want %q", err, tt.want)
			}
		})
	}
}
