package installer

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"supermetry/internal/updatehelper"
)

//go:embed templates/supermetry.service
var serviceUnit []byte

//go:embed templates/supermetry-updater.socket
var updaterSocketUnit []byte

//go:embed templates/supermetry-updater.service
var updaterServiceUnit []byte

var safeReleaseVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

func backupCaddy(ctx context.Context, runner Runner) error {
	if runner == nil {
		return errors.New("Caddy backup needs a command runner")
	}
	if output, err := runner.Run(ctx, "cp", "/etc/caddy/Caddyfile", "/etc/caddy/Caddyfile.supermetry.bak"); err != nil {
		return fmt.Errorf("back up /etc/caddy/Caddyfile: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func backupCaddyIfPresent(ctx context.Context, sys System) error {
	if _, err := sys.Stat("/etc/caddy/Caddyfile"); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect /etc/caddy/Caddyfile: %w", err)
	}
	return backupCaddy(ctx, sys.Runner)
}

// Runner executes external commands.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

func installService(ctx context.Context, runner Runner, writeFile func(string, []byte, os.FileMode) error, version, listen string) error {
	if runner == nil {
		return errors.New("service installation needs a command runner")
	}
	if !safeReleaseVersion.MatchString(version) {
		return fmt.Errorf("invalid installed version %q", version)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	descriptor, err := renderDeploymentDescriptor(updatehelper.DeploymentBinary, listen)
	if err != nil {
		return err
	}
	if err := ensureApplicationUser(ctx, runner); err != nil {
		return err
	}
	if _, err := ensureUpdaterGroup(ctx, runner, true); err != nil {
		return err
	}
	versionDir := filepath.Join("/opt/supermetry/versions", version)
	if _, err := runner.Run(ctx, "install", "-d", "-o", "root", "-g", "root", "-m", "0755", versionDir, "/opt/supermetry/updater"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "install", "-d", "-o", "root", "-g", updaterGroup, "-m", "0750", "/var/lib/supermetry-updater", "/var/lib/supermetry-updater/backups", "/var/lib/supermetry-updater/jobs"); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(versionDir, "supermetry"), binary, 0o755); err != nil {
		return err
	}
	if err := writeUpdaterFiles(writeFile, binary, descriptor); err != nil {
		return err
	}
	if err := writeFile("/etc/systemd/system/supermetry.service", serviceUnit, 0o644); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "ln", "-sfn", versionDir, "/opt/supermetry/current.next"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "mv", "-Tf", "/opt/supermetry/current.next", "/opt/supermetry/current"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "chown", "root:supermetry", "/etc/supermetry/supermetry.env"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "systemctl", "enable", "--now", "supermetry-updater.socket"); err != nil {
		return err
	}
	_, err = runner.Run(ctx, "systemctl", "enable", "--now", "supermetry")
	return err
}

func ensureApplicationUser(ctx context.Context, runner Runner) error {
	if _, err := runner.Run(ctx, "id", "-u", "supermetry"); err == nil {
		return nil
	}
	_, err := runner.Run(ctx, "useradd", "--system", "--home", "/var/lib/supermetry", "--shell", "/usr/sbin/nologin", "supermetry")
	return err
}

func ensureUpdaterGroup(ctx context.Context, runner Runner, addApplicationUser bool) (string, error) {
	output, err := runner.Run(ctx, "getent", "group", updaterGroup)
	if err != nil || len(strings.TrimSpace(string(output))) == 0 {
		if _, createErr := runner.Run(ctx, "groupadd", "--system", updaterGroup); createErr != nil {
			return "", createErr
		}
		output, err = runner.Run(ctx, "getent", "group", updaterGroup)
	}
	if err != nil {
		return "", fmt.Errorf("read updater group: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(output)), ":")
	if len(parts) < 3 || parts[2] == "" || parts[2] == "0" || strings.IndexFunc(parts[2], func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", errors.New("read updater group: getent returned an invalid record")
	}
	if addApplicationUser {
		if _, err := runner.Run(ctx, "usermod", "--append", "--groups", updaterGroup, "supermetry"); err != nil {
			return "", err
		}
	}
	return parts[2], nil
}

func installDockerUpdater(ctx context.Context, runner Runner, writeFile func(string, []byte, os.FileMode) error, listen string) (string, error) {
	if runner == nil {
		return "", errors.New("updater installation needs a command runner")
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		return "", err
	}
	descriptor, err := renderDeploymentDescriptor(updatehelper.DeploymentDocker, listen)
	if err != nil {
		return "", err
	}
	gid, err := ensureUpdaterGroup(ctx, runner, false)
	if err != nil {
		return "", err
	}
	if _, err := runner.Run(ctx, "install", "-d", "-o", "root", "-g", "root", "-m", "0755", "/opt/supermetry/updater"); err != nil {
		return "", err
	}
	if _, err := runner.Run(ctx, "install", "-d", "-o", "root", "-g", updaterGroup, "-m", "0750", "/var/lib/supermetry-updater", "/var/lib/supermetry-updater/backups", "/var/lib/supermetry-updater/jobs"); err != nil {
		return "", err
	}
	if err := writeUpdaterFiles(writeFile, binary, descriptor); err != nil {
		return "", err
	}
	if _, err := runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return "", err
	}
	if _, err := runner.Run(ctx, "systemctl", "enable", "--now", "supermetry-updater.socket"); err != nil {
		return "", err
	}
	return gid, nil
}

func writeUpdaterFiles(writeFile func(string, []byte, os.FileMode) error, binary, descriptor []byte) error {
	files := []plannedFile{
		{Path: "/opt/supermetry/updater/supermetry", Data: binary, Mode: 0o755},
		{Path: deploymentDescriptorPath, Data: descriptor, Mode: 0o644},
		{Path: "/etc/systemd/system/supermetry-updater.socket", Data: updaterSocketUnit, Mode: 0o644},
		{Path: "/etc/systemd/system/supermetry-updater.service", Data: updaterServiceUnit, Mode: 0o644},
	}
	return writePlannedFiles(files, writeFile)
}
