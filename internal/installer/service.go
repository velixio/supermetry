package installer

import (
	"context"
	_ "embed"
	"errors"
	"os"
)

//go:embed templates/supermetry.service
var serviceUnit []byte

// Runner executes external commands.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

func installService(ctx context.Context, runner Runner, writeFile func(string, []byte, os.FileMode) error) error {
	if runner == nil {
		return errors.New("service installation needs a command runner")
	}
	if _, err := runner.Run(ctx, "id", "-u", "supermetry"); err != nil {
		if _, createErr := runner.Run(ctx, "useradd", "--system", "--home", "/var/lib/supermetry", "--shell", "/usr/sbin/nologin", "supermetry"); createErr != nil {
			return createErr
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	if err := writeFile("/usr/local/bin/supermetry", binary, 0o755); err != nil {
		return err
	}
	if err := writeFile("/etc/systemd/system/supermetry.service", serviceUnit, 0o644); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "chown", "root:supermetry", "/etc/supermetry/supermetry.env"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err = runner.Run(ctx, "systemctl", "enable", "--now", "supermetry")
	return err
}
