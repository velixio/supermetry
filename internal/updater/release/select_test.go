package release_test

import (
	"strings"
	"testing"

	"supermetry/internal/updater/release"
)

func TestSelectArtifactChoosesExactStableUpgradePlatform(t *testing.T) {
	t.Parallel()
	manifest := validManifest()
	artifact, err := release.SelectArtifact(manifest, "v1.1.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Name != "supermetry_1.2.0_linux_amd64.tar.gz" {
		t.Fatalf("artifact = %+v", artifact)
	}
}

func TestSelectArtifactDistinguishesRaspberryPiARMVersions(t *testing.T) {
	t.Parallel()
	manifest := validManifest()
	manifest.Artifacts = append(manifest.Artifacts,
		release.Artifact{Name: "supermetry_1.2.0_linux_armv6.tar.gz", OS: "linux", Arch: "armv6", Size: 7, SHA256: strings.Repeat("e", 64)},
		release.Artifact{Name: "supermetry_1.2.0_linux_armv7.tar.gz", OS: "linux", Arch: "armv7", Size: 7, SHA256: strings.Repeat("f", 64)},
	)
	for _, arch := range []string{"armv6", "armv7"} {
		artifact, err := release.SelectArtifact(manifest, "v1.1.0", "linux", arch)
		if err != nil {
			t.Fatalf("SelectArtifact(%s): %v", arch, err)
		}
		if artifact.Arch != arch {
			t.Fatalf("SelectArtifact(%s) arch = %q", arch, artifact.Arch)
		}
	}
}

func TestSelectArtifactRejectsUnsafeTargets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*release.Manifest)
		current string
		goos    string
		goarch  string
		want    string
	}{
		{name: "same version", current: "v1.2.0", goos: "linux", goarch: "amd64", want: "newer"},
		{name: "downgrade", current: "v2.0.0", goos: "linux", goarch: "amd64", want: "newer"},
		{name: "below minimum source", current: "v0.9.0", goos: "linux", goarch: "amd64", want: "minimum"},
		{name: "unsupported platform", current: "v1.1.0", goos: "freebsd", goarch: "amd64", want: "platform"},
		{name: "prerelease", current: "v1.1.0", goos: "linux", goarch: "amd64", want: "stable", mutate: func(m *release.Manifest) { m.Version = "v1.2.0-rc.1"; m.Stable = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest()
			if tt.mutate != nil {
				tt.mutate(&manifest)
			}
			_, err := release.SelectArtifact(manifest, tt.current, tt.goos, tt.goarch)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("SelectArtifact() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSelectContainerDigestUsesSignedPlatformDigest(t *testing.T) {
	t.Parallel()
	manifest := validManifest()
	selection, err := release.SelectContainer(manifest, "v1.1.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Image != "ghcr.io/velixio/supermetry" || selection.Digest != "sha256:"+strings.Repeat("d", 64) {
		t.Fatalf("selection = %+v", selection)
	}
}
