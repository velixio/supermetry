package main

import (
	"os"
	"strings"

	"golang.org/x/mod/semver"

	"supermetry/internal/updateclient"
	"supermetry/internal/updatehelper"
)

func managedUpdater(currentVersion string) (*updateclient.Client, updatehelper.DeploymentMode) {
	canonical := "v" + strings.TrimPrefix(strings.TrimSpace(currentVersion), "v")
	if !semver.IsValid(canonical) || semver.Canonical(canonical) != canonical || semver.Prerelease(canonical) != "" {
		return nil, ""
	}
	file, err := os.Open(updatehelper.DeploymentDescriptorPath)
	if err != nil {
		return nil, ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ""
	}
	descriptor, err := updatehelper.ParseDeploymentDescriptor(file)
	if err != nil {
		return nil, ""
	}
	return updateclient.New(nil), descriptor.Mode
}
