package installer

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

type serviceRunner struct {
	calls []string
}

func TestConfigureProxyCaddyBacksUpValidatesAndReloads(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	if err := backupCaddy(context.Background(), runner); err != nil {
		t.Fatal(err)
	}
	err := configureProxy(context.Background(), "caddy", "analytics.example.com", true, NewPrompter(strings.NewReader(""), &strings.Builder{}), func(string) (string, error) { return "/usr/bin/caddy", nil }, runner)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"cp /etc/caddy/Caddyfile /etc/caddy/Caddyfile.supermetry.bak",
		"caddy validate --config /etc/caddy/Caddyfile",
		"systemctl reload caddy",
	}
	if strings.Join(runner.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %v, want %v", runner.calls, want)
	}
}

func TestBackupCaddyIfPresentSkipsMissingFile(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	sys := System{Runner: runner, Stat: func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }}
	if err := backupCaddyIfPresent(context.Background(), sys); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %v, want none", runner.calls)
	}
}

func TestConfigureProxyNginxReportsTestOutput(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{outputs: map[string][]byte{"nginx -t": []byte("duplicate listen 443")}, errors: map[string]error{"nginx -t": errors.New("exit 1")}}
	err := configureProxy(context.Background(), "nginx", "analytics.example.com", true, NewPrompter(strings.NewReader(""), &strings.Builder{}), func(string) (string, error) { return "/usr/bin/nginx", nil }, runner)
	if err == nil || !strings.Contains(err.Error(), "nginx configuration test failed") || !strings.Contains(err.Error(), "duplicate listen 443") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigureProxyOffersCertbot(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	var output strings.Builder
	err := configureProxy(context.Background(), "nginx", "analytics.example.com", false, NewPrompter(strings.NewReader("y\n"), &output), func(file string) (string, error) { return "/usr/bin/" + file, nil }, runner)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Configure HTTPS with certbot") || !slices.Contains(runner.calls, "certbot --nginx -d analytics.example.com") {
		t.Fatalf("output=%q calls=%v", output.String(), runner.calls)
	}
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
