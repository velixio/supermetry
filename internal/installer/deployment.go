package installer

import (
	"encoding/json"
	"fmt"

	"supermetry/internal/updatehelper"
)

const (
	deploymentDescriptorPath = "/etc/supermetry/deployment.json"
	updaterGroup             = "supermetry-updater"
	updaterGroupIDMarker     = "__SUPERMETRY_UPDATER_GID__"
)

func renderDeploymentDescriptor(mode updatehelper.DeploymentMode, listen string) ([]byte, error) {
	descriptor := updatehelper.DeploymentDescriptor{
		Version:        updatehelper.DeploymentDescriptorVersion,
		Mode:           mode,
		ServiceName:    "supermetry.service",
		VersionsDir:    "/opt/supermetry/versions",
		CurrentLink:    "/opt/supermetry/current",
		ComposeDir:     "/opt/supermetry",
		ComposeService: "supermetry",
		BackupDir:      "/var/lib/supermetry-updater/backups",
		ReadinessURL:   localBaseURL(listen) + "/readyz",
		SocketGroup:    updaterGroup,
	}
	if err := descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("create deployment descriptor: %w", err)
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode deployment descriptor: %w", err)
	}
	return append(data, '\n'), nil
}
