package release

import (
	"errors"
	"fmt"

	"golang.org/x/mod/semver"
)

type ContainerSelection struct {
	Image  string
	Digest string
}

func SelectArtifact(manifest Manifest, currentVersion, goos, goarch string) (Artifact, error) {
	if err := validateSelection(manifest, currentVersion); err != nil {
		return Artifact{}, err
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.OS == goos && artifact.Arch == goarch {
			return artifact, nil
		}
	}
	return Artifact{}, fmt.Errorf("release has no artifact for platform %s/%s", goos, goarch)
}

func SelectContainer(manifest Manifest, currentVersion, goos, goarch string) (ContainerSelection, error) {
	if err := validateSelection(manifest, currentVersion); err != nil {
		return ContainerSelection{}, err
	}
	if manifest.Container == nil {
		return ContainerSelection{}, errors.New("release has no container image")
	}
	for _, platform := range manifest.Container.Platforms {
		if platform.OS == goos && platform.Arch == goarch {
			return ContainerSelection{Image: manifest.Container.Image, Digest: platform.Digest}, nil
		}
	}
	return ContainerSelection{}, fmt.Errorf("release has no container for platform %s/%s", goos, goarch)
}

func validateSelection(manifest Manifest, currentVersion string) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if !manifest.Stable || semver.Prerelease(manifest.Version) != "" {
		return errors.New("release target is not stable")
	}
	if !semver.IsValid(currentVersion) || semver.Canonical(currentVersion) != currentVersion || semver.Prerelease(currentVersion) != "" {
		return errors.New("current version must be a canonical stable semantic version")
	}
	if semver.Compare(currentVersion, manifest.MinimumVersion) < 0 {
		return fmt.Errorf("current version is below release minimum %s", manifest.MinimumVersion)
	}
	if semver.Compare(manifest.Version, currentVersion) <= 0 {
		return errors.New("release target must be newer than current version")
	}
	return nil
}
