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
	err := configureProxy(context.Background(), "caddy", "analytics.supermetry.com", true, NewPrompter(strings.NewReader(""), &strings.Builder{}), func(string) (string, error) { return "/usr/bin/caddy", nil }, runner)
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
	err := configureProxy(context.Background(), "nginx", "analytics.supermetry.com", true, NewPrompter(strings.NewReader(""), &strings.Builder{}), func(string) (string, error) { return "/usr/bin/nginx", nil }, runner)
	if err == nil || !strings.Contains(err.Error(), "nginx configuration test failed") || !strings.Contains(err.Error(), "duplicate listen 443") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigureProxyOffersCertbot(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	var output strings.Builder
	err := configureProxy(context.Background(), "nginx", "analytics.supermetry.com", false, NewPrompter(strings.NewReader("y\n"), &output), func(file string) (string, error) { return "/usr/bin/" + file, nil }, runner)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Configure HTTPS with certbot") || !slices.Contains(runner.calls, "certbot --nginx -d analytics.supermetry.com") {
		t.Fatalf("output=%q calls=%v", output.String(), runner.calls)
	}
}

func (r *serviceRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if call == "id -u supermetry" {
		return nil, errors.New("missing user")
	}
	if call == "getent group supermetry-updater" {
		return []byte("supermetry-updater:x:987:\n"), nil
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
	}, "v1.2.3", "127.0.0.1:8787")
	if err != nil {
		t.Fatal(err)
	}
	unit := writes["/etc/systemd/system/supermetry.service"]
	if unit.mode != 0o644 || !strings.Contains(string(unit.data), "ExecStart=/opt/supermetry/current/supermetry\n") || strings.Contains(string(unit.data), "supermetry serve") {
		t.Fatalf("unit = %q, mode %o", unit.data, unit.mode)
	}
	if binary := writes["/opt/supermetry/versions/v1.2.3/supermetry"]; binary.mode != 0o755 || len(binary.data) == 0 {
		t.Fatalf("binary mode/size = %o/%d", binary.mode, len(binary.data))
	}
	if helper := writes["/opt/supermetry/updater/supermetry"]; helper.mode != 0o755 || len(helper.data) == 0 {
		t.Fatalf("helper mode/size = %o/%d", helper.mode, len(helper.data))
	}
	if descriptor := writes["/etc/supermetry/deployment.json"]; descriptor.mode != 0o644 || !strings.Contains(string(descriptor.data), `"mode": "binary"`) {
		t.Fatalf("descriptor = %q, mode %o", descriptor.data, descriptor.mode)
	}
	for _, path := range []string{"/etc/systemd/system/supermetry-updater.socket", "/etc/systemd/system/supermetry-updater.service"} {
		if writes[path].mode != 0o644 {
			t.Errorf("%s mode = %o", path, writes[path].mode)
		}
	}
	socket := string(writes["/etc/systemd/system/supermetry-updater.socket"].data)
	if !strings.Contains(socket, "SocketGroup=supermetry-updater") || !strings.Contains(socket, "SocketMode=0660") || !strings.Contains(socket, "-m 0750 /run/supermetry-updater") {
		t.Fatalf("updater socket does not constrain access: %q", socket)
	}
	helper := string(writes["/etc/systemd/system/supermetry-updater.service"].data)
	if !strings.Contains(helper, "ExecStart=/opt/supermetry/updater/supermetry updater-helper") || !strings.Contains(helper, "ProtectSystem=strict") {
		t.Fatalf("updater service is not the fixed hardened helper: %q", helper)
	}
	wantCalls := []string{
		"id -u supermetry",
		"useradd --system --home /var/lib/supermetry --shell /usr/sbin/nologin supermetry",
		"getent group supermetry-updater",
		"usermod --append --groups supermetry-updater supermetry",
		"install -d -o root -g root -m 0755 /opt/supermetry/versions/v1.2.3 /opt/supermetry/updater",
		"install -d -o root -g supermetry-updater -m 0750 /var/lib/supermetry-updater /var/lib/supermetry-updater/backups /var/lib/supermetry-updater/jobs",
		"ln -sfn /opt/supermetry/versions/v1.2.3 /opt/supermetry/current.next",
		"mv -Tf /opt/supermetry/current.next /opt/supermetry/current",
		"chown root:supermetry /etc/supermetry/supermetry.env",
		"systemctl daemon-reload",
		"systemctl enable --now supermetry-updater.socket",
		"systemctl enable --now supermetry",
	}
	if strings.Join(runner.calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %v, want %v", runner.calls, wantCalls)
	}
}

func TestEnsureUpdaterGroupCreatesMissingGroup(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		outputs: map[string][]byte{"getent group supermetry-updater": []byte("supermetry-updater:x:987:\n")},
		errors:  map[string]error{"getent group supermetry-updater": errors.New("missing")},
	}
	// Return the group record after groupadd rather than keeping the initial
	// getent failure active.
	lookups := 0
	runner.onRun = func(call string) {
		if call == "getent group supermetry-updater" {
			lookups++
			if lookups == 2 {
				delete(runner.errors, call)
			}
		}
	}
	gid, err := ensureUpdaterGroup(context.Background(), runner, false)
	if err != nil {
		t.Fatal(err)
	}
	if gid != "987" || !slices.Contains(runner.calls, "groupadd --system supermetry-updater") {
		t.Fatalf("gid = %q, calls = %v", gid, runner.calls)
	}
}

func TestEnsureUpdaterGroupRejectsRootGroupID(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputs: map[string][]byte{
		"getent group supermetry-updater": []byte("supermetry-updater:x:0:\n"),
	}}
	if _, err := ensureUpdaterGroup(context.Background(), runner, false); err == nil {
		t.Fatal("accepted gid 0 for updater socket access")
	}
}
