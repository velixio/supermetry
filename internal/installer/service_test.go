package installer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type serviceRunner struct {
	calls []string
}

func (r *serviceRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if call == "id -u supermetry" {
		return nil, errors.New("missing user")
	}
	return nil, nil
}

func TestInstallServiceCreatesUserAndEnablesUnit(t *testing.T) {
	t.Parallel()

	runner := &serviceRunner{}
	writes := make(map[string]struct {
		data []byte
		mode os.FileMode
	})
	err := installService(context.Background(), runner, func(path string, data []byte, mode os.FileMode) error {
		writes[path] = struct {
			data []byte
			mode os.FileMode
		}{data: data, mode: mode}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	unit := writes["/etc/systemd/system/supermetry.service"]
	if unit.mode != 0o644 || !strings.Contains(string(unit.data), "ExecStart=/usr/local/bin/supermetry\n") || strings.Contains(string(unit.data), "supermetry serve") {
		t.Fatalf("unit = %q, mode %o", unit.data, unit.mode)
	}
	if binary := writes["/usr/local/bin/supermetry"]; binary.mode != 0o755 || len(binary.data) == 0 {
		t.Fatalf("binary mode/size = %o/%d", binary.mode, len(binary.data))
	}
	wantCalls := []string{
		"id -u supermetry",
		"useradd --system --home /var/lib/supermetry --shell /usr/sbin/nologin supermetry",
		"chown root:supermetry /etc/supermetry/supermetry.env",
		"systemctl daemon-reload",
		"systemctl enable --now supermetry",
	}
	if strings.Join(runner.calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %v, want %v", runner.calls, wantCalls)
	}
}
