package updatehelper

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
)

const (
	DeploymentDescriptorVersion = 1
	DeploymentDescriptorPath    = "/etc/supermetry/deployment.json"
)

type DeploymentMode string

const (
	DeploymentBinary DeploymentMode = "binary"
	DeploymentDocker DeploymentMode = "docker"
)

type DeploymentDescriptor struct {
	Version        int            `json:"version"`
	Mode           DeploymentMode `json:"mode"`
	ServiceName    string         `json:"service_name"`
	VersionsDir    string         `json:"versions_dir"`
	CurrentLink    string         `json:"current_link"`
	ComposeDir     string         `json:"compose_dir"`
	ComposeService string         `json:"compose_service"`
	BackupDir      string         `json:"backup_dir"`
	ReadinessURL   string         `json:"readiness_url"`
	SocketGroup    string         `json:"socket_group"`
}

func ParseDeploymentDescriptor(r io.Reader) (DeploymentDescriptor, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFrameSize+1))
	if err != nil {
		return DeploymentDescriptor{}, fmt.Errorf("read deployment descriptor: %w", err)
	}
	if len(data) > MaxFrameSize {
		return DeploymentDescriptor{}, errors.New("deployment descriptor is too large")
	}
	var descriptor DeploymentDescriptor
	if err := decodeStrictObject(data, &descriptor,
		"version", "mode", "service_name", "versions_dir", "current_link",
		"compose_dir", "compose_service", "backup_dir", "readiness_url", "socket_group",
	); err != nil {
		return DeploymentDescriptor{}, fmt.Errorf("decode deployment descriptor: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return DeploymentDescriptor{}, err
	}
	return descriptor, nil
}

func (d DeploymentDescriptor) Validate() error {
	if d.Version != DeploymentDescriptorVersion {
		return fmt.Errorf("unsupported deployment descriptor version %d", d.Version)
	}
	if d.Mode != DeploymentBinary && d.Mode != DeploymentDocker {
		return fmt.Errorf("unsupported deployment mode %q", d.Mode)
	}
	fixed := map[string][2]string{
		"service_name":    {d.ServiceName, "supermetry.service"},
		"versions_dir":    {d.VersionsDir, "/opt/supermetry/versions"},
		"current_link":    {d.CurrentLink, "/opt/supermetry/current"},
		"compose_dir":     {d.ComposeDir, "/opt/supermetry"},
		"compose_service": {d.ComposeService, "supermetry"},
		"backup_dir":      {d.BackupDir, "/var/lib/supermetry-updater/backups"},
		"socket_group":    {d.SocketGroup, "supermetry-updater"},
	}
	for name, values := range fixed {
		if values[0] != values[1] {
			return fmt.Errorf("%s must be %q", name, values[1])
		}
	}
	readiness, err := url.ParseRequestURI(d.ReadinessURL)
	if err != nil || readiness.Scheme != "http" || readiness.Path != "/readyz" || readiness.RawQuery != "" || readiness.Fragment != "" || readiness.User != nil {
		return errors.New("readiness_url must be a plain loopback HTTP /readyz URL")
	}
	host := readiness.Hostname()
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || readiness.Port() == "" {
		return errors.New("readiness_url must use a loopback IP and explicit port")
	}
	return nil
}
