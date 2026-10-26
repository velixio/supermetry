package installer

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls   []string
	outputs map[string][]byte
	errors  map[string]error
	onRun   func(string)
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if r.onRun != nil {
		r.onRun(call)
	}
	return r.outputs[call], r.errors[call]
}

func TestRunDryRunBinaryReportsPlanWithoutSideEffects(t *testing.T) {

	var output bytes.Buffer
	runner := &fakeRunner{}
	writes := 0
	checks := 0
	sys := System{
		Runner:    runner,
		WriteFile: func(string, []byte, os.FileMode) error { writes++; return nil },
		GOOS:      "linux", IsRoot: true,
		Check:    func(context.Context, string) (int, error) { checks++; return 180000, nil },
		LookPath: func(file string) (string, error) { return "/usr/bin/" + file, nil },
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", DB: DBConfig{Host: "database.internal", Port: "5432", User: "example", Password: "sentinel-secret", Name: "supermetry"}, Listen: "127.0.0.1:8787", Proxy: "nginx", DryRun: true}
	if err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &output), sys); err != nil {
		t.Fatal(err)
	}
	if writes != 0 || checks != 0 || len(runner.calls) != 0 {
		t.Fatalf("dry run caused side effects: writes=%d checks=%d calls=%v", writes, checks, runner.calls)
	}
	for _, want := range []string{
		"DRY RUN", "PostgreSQL connection and version", "/etc/supermetry/supermetry.env", "SUPERMETRY_SECRET_KEY=<generated>",
		"useradd --system", "/etc/systemd/system/supermetry.service", "nginx -t", "systemctl reload nginx",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("dry-run output missing %q:\n%s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "sentinel-secret") {
		t.Fatalf("dry-run output exposed database credentials:\n%s", output.String())
	}
}

func TestRunDryRunRejectsPrivilegedApply(t *testing.T) {
	err := Run(context.Background(), Options{DryRun: true, Apply: "/tmp/plan"}, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), System{IsRoot: true})
	if err == nil || !strings.Contains(err.Error(), "--dry-run cannot be combined with --apply") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunDryRunRejectsInvalidDockerImageSource(t *testing.T) {
	err := Run(context.Background(), Options{DryRun: true, Mode: "docker", BaseURL: "https://analytics.supermetry.com", Image: "invalid"}, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), System{GOOS: "linux"})
	if err == nil || !strings.Contains(err.Error(), "--image must be download or build") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunDryRunDockerReportsImageSources(t *testing.T) {

	for _, tt := range []struct {
		name  string
		image string
		want  []string
	}{
		{name: "download", image: "download", want: []string{"docker compose --project-directory /srv/supermetry pull", "docker compose --project-directory /srv/supermetry up -d"}},
		{name: "build", image: "build", want: []string{"source archive", "/srv/supermetry/src", "docker compose --project-directory /srv/supermetry build", "docker compose --project-directory /srv/supermetry up -d"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			runner := &fakeRunner{}
			sys := System{Runner: runner, WriteFile: func(string, []byte, os.FileMode) error { t.Fatal("dry run wrote a file"); return nil }, GOOS: "linux", IsRoot: true}
			opts := Options{Mode: "docker", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "none", Image: tt.image, Dir: "/srv/supermetry", Version: "v1.2.3", DryRun: true}
			if err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &output), sys); err != nil {
				t.Fatal(err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("dry run commands = %v, want none", runner.calls)
			}
			for _, want := range tt.want {
				if !strings.Contains(output.String(), want) {
					t.Errorf("output missing %q:\n%s", want, output.String())
				}
			}
		})
	}
}

func TestRunExistingAdminPrintsSignInGuidance(t *testing.T) {
	t.Chdir(t.TempDir())
	var output bytes.Buffer
	sys := System{
		GOOS:        "darwin",
		Check:       func(context.Context, string) (int, error) { return 180000, nil },
		AdminExists: func(context.Context, string) (bool, error) { return true, nil },
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none", Yes: true}
	if err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &output), sys); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "Open https://analytics.supermetry.com/app and sign in") || strings.Contains(got, "create your admin account") {
		t.Fatalf("completion guidance = %q", got)
	}
}

func TestRunRejectsUnsupportedPostgresAndWarnsBelowBaseline(t *testing.T) {
	base := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none", Yes: true}
	for _, tt := range []struct {
		name    string
		version int
		wantErr string
		wantOut string
	}{
		{name: "below minimum", version: 130000, wantErr: "PostgreSQL 14 or newer is required"},
		{name: "below tested baseline", version: 170000, wantOut: "PostgreSQL 18 is the tested baseline"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			sys := System{GOOS: "darwin", Check: func(context.Context, string) (int, error) { return tt.version, nil }}
			err := Run(context.Background(), base, NewPrompter(strings.NewReader(""), &output), sys)
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if tt.wantOut != "" && !strings.Contains(output.String(), tt.wantOut) {
				t.Fatalf("output = %q, want %q", output.String(), tt.wantOut)
			}
		})
	}
}

func TestRunDockerUsesRequestedImageSource(t *testing.T) {
	for _, tt := range []struct {
		name        string
		image       string
		wantPrepare int
		want        []string
	}{
		{name: "download", image: "download", want: []string{"docker compose --project-directory /srv/supermetry pull", "docker compose --project-directory /srv/supermetry up -d"}},
		{name: "build", image: "build", wantPrepare: 1, want: []string{
			"docker compose --project-directory /srv/supermetry build",
			"docker compose --project-directory /srv/supermetry up -d",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{}
			prepared := 0
			sys := System{
				Runner: runner, GOOS: "darwin",
				WriteFile:     func(string, []byte, os.FileMode) error { return nil },
				Ready:         func(context.Context, string, time.Duration) error { return nil },
				PrepareSource: func(context.Context, Options) error { prepared++; return nil },
			}
			opts := Options{Mode: "docker", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "none", Image: tt.image, Dir: "/srv/supermetry", Version: "v1.2.3", Yes: true}
			if err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys); err != nil {
				t.Fatal(err)
			}
			if strings.Join(runner.calls, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("calls = %v, want %v", runner.calls, tt.want)
			}
			if prepared != tt.wantPrepare {
				t.Fatalf("source preparations = %d, want %d", prepared, tt.wantPrepare)
			}
		})
	}
}

func TestRunManagedDockerInstallsNarrowUpdaterAccess(t *testing.T) {
	var writes = make(map[string][]byte)
	runner := &fakeRunner{outputs: map[string][]byte{
		"getent group supermetry-updater": []byte("supermetry-updater:x:987:\n"),
	}}
	sys := System{
		Runner: runner, GOOS: "linux", IsRoot: true,
		WriteFile: func(path string, data []byte, _ os.FileMode) error {
			writes[path] = append([]byte(nil), data...)
			return nil
		},
		Ready:         func(context.Context, string, time.Duration) error { return nil },
		SetupRequired: func(context.Context, string) (bool, error) { return true, nil },
	}
	opts := Options{Mode: "docker", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "none", Image: "download", Dir: "/opt/supermetry", Version: "v1.2.3", Yes: true}
	if err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys); err != nil {
		t.Fatal(err)
	}
	compose := string(writes["/opt/supermetry/docker-compose.yml"])
	for _, want := range []string{`- "987"`, "/run/supermetry-updater:/run/supermetry-updater:ro", "/etc/supermetry/deployment.json:/etc/supermetry/deployment.json:ro"} {
		if !strings.Contains(compose, want) {
			t.Errorf("Compose output missing %q:\n%s", want, compose)
		}
	}
	if strings.Contains(compose, updaterGroupIDMarker) || strings.Contains(compose, "/var/run/docker.sock") {
		t.Fatalf("Compose contains an unresolved or unsafe mount:\n%s", compose)
	}
	descriptor := string(writes[deploymentDescriptorPath])
	if !strings.Contains(descriptor, `"mode": "docker"`) {
		t.Fatalf("descriptor = %q", descriptor)
	}
	for _, want := range []string{
		"systemctl enable --now supermetry-updater.socket",
		"docker compose --project-directory /opt/supermetry pull",
		"docker compose --project-directory /opt/supermetry up -d",
	} {
		if !slices.Contains(runner.calls, want) {
			t.Errorf("commands missing %q: %v", want, runner.calls)
		}
	}
}

func TestRunLocalBuildDockerDoesNotInstallUpdater(t *testing.T) {
	var writes = make(map[string][]byte)
	runner := &fakeRunner{}
	sys := System{
		Runner: runner, GOOS: "linux", IsRoot: true,
		WriteFile: func(path string, data []byte, _ os.FileMode) error {
			writes[path] = append([]byte(nil), data...)
			return nil
		},
		Ready:         func(context.Context, string, time.Duration) error { return nil },
		SetupRequired: func(context.Context, string) (bool, error) { return true, nil },
		PrepareSource: func(context.Context, Options) error { return nil },
	}
	opts := Options{Mode: "docker", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "none", Image: "build", Dir: "/opt/supermetry", Version: "v1.2.3", Yes: true}
	if err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys); err != nil {
		t.Fatal(err)
	}
	if _, ok := writes[deploymentDescriptorPath]; ok {
		t.Fatal("local-build Docker install wrote a managed deployment descriptor")
	}
	if got := string(writes["/opt/supermetry/docker-compose.yml"]); strings.Contains(got, "supermetry-updater") || strings.Contains(got, "group_add") {
		t.Fatalf("local-build Docker unexpectedly enables UI updates:\n%s", got)
	}
}

func TestRunDockerExistingAdminPrintsSignInGuidance(t *testing.T) {
	var output bytes.Buffer
	sys := System{
		Runner: &fakeRunner{}, GOOS: "darwin",
		WriteFile:     func(string, []byte, os.FileMode) error { return nil },
		Ready:         func(context.Context, string, time.Duration) error { return nil },
		SetupRequired: func(context.Context, string) (bool, error) { return false, nil },
	}
	opts := Options{Mode: "docker", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "none", Image: "download", Dir: "/srv/supermetry", Version: "v1.2.3", Yes: true}
	if err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &output), sys); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "and sign in") || strings.Contains(got, "create your admin account") {
		t.Fatalf("completion guidance = %q", got)
	}
}

func TestRunDockerWaitsForReadinessBeforeProxyChange(t *testing.T) {
	var events []string
	runner := &fakeRunner{onRun: func(call string) { events = append(events, "run "+call) }}
	sys := System{
		Runner: runner, GOOS: "linux", IsRoot: true,
		WriteFile:     func(path string, _ []byte, _ os.FileMode) error { events = append(events, "write "+path); return nil },
		Ready:         func(context.Context, string, time.Duration) error { events = append(events, "ready"); return nil },
		SetupRequired: func(context.Context, string) (bool, error) { return true, nil },
		ReadFile:      func(string) ([]byte, error) { return nil, os.ErrNotExist },
		Stat:          func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		LookPath: func(file string) (string, error) {
			if file == "certbot" {
				return "", errors.New("missing")
			}
			return "/usr/bin/" + file, nil
		},
	}
	opts := Options{Mode: "docker", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "nginx", Image: "download", Dir: "/srv/supermetry", Yes: true}
	if err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys); err != nil {
		t.Fatal(err)
	}
	readyAt := slices.Index(events, "ready")
	writeAt := slices.Index(events, "write /etc/nginx/conf.d/supermetry.conf")
	reloadAt := slices.Index(events, "run systemctl reload nginx")
	if readyAt < 0 || writeAt < readyAt || reloadAt < writeAt {
		t.Fatalf("unsafe proxy sequencing: %v", events)
	}
}

func TestPlannedProxyAppendsCaddySiteBlock(t *testing.T) {
	sys := System{ReadFile: func(string) ([]byte, error) { return []byte("existing.example {\n  respond ok\n}\n"), nil }}
	file, err := plannedProxy(Options{Proxy: "caddy", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787"}, sys)
	if err != nil {
		t.Fatal(err)
	}
	got := string(file.Data)
	if !strings.HasPrefix(got, "existing.example") || !strings.Contains(got, "analytics.supermetry.com") {
		t.Fatalf("Caddyfile was not appended:\n%s", got)
	}
}

func TestPlannedProxyDoesNotDuplicateCaddySiteBlock(t *testing.T) {
	block := RenderCaddy("analytics.supermetry.com", "127.0.0.1:8787")
	sys := System{ReadFile: func(string) ([]byte, error) { return block, nil }}
	file, err := plannedProxy(Options{Proxy: "caddy", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787"}, sys)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(file.Data), "analytics.supermetry.com"); got != 1 {
		t.Fatalf("domain occurrences = %d, want 1:\n%s", got, file.Data)
	}
}

func TestPlannedProxyUpdatesCaddySiteBlock(t *testing.T) {
	existing := []byte("analytics.supermetry.com {\n    reverse_proxy 127.0.0.1:8000\n}\n")
	sys := System{ReadFile: func(string) ([]byte, error) { return existing, nil }}
	file, err := plannedProxy(Options{Proxy: "caddy", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787"}, sys)
	if err != nil {
		t.Fatal(err)
	}
	got := string(file.Data)
	if strings.Count(got, "analytics.supermetry.com") != 1 || strings.Contains(got, "127.0.0.1:8000") || !strings.Contains(got, "127.0.0.1:8787") {
		t.Fatalf("Caddy site block was not updated safely:\n%s", got)
	}
}

func TestPlannedProxyPreservesCaddyfileOnReadError(t *testing.T) {
	wantErr := errors.New("permission denied")
	sys := System{ReadFile: func(string) ([]byte, error) { return nil, wantErr }}
	_, err := plannedProxy(Options{Proxy: "caddy", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787"}, sys)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want read error", err)
	}
}

func TestRunDockerReportsPortInUseBeforeWriting(t *testing.T) {
	writes := 0
	sys := System{
		GOOS: "darwin", Runner: &fakeRunner{},
		WriteFile: func(string, []byte, os.FileMode) error { writes++; return nil },
		CheckPort: func(string) error { return errors.New("bind: address already in use") },
		Stat:      func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	}
	opts := Options{Mode: "docker", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "none", Image: "download", Dir: "/srv/supermetry", Yes: true}
	err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys)
	if err == nil || !strings.Contains(err.Error(), "port 8787 is already in use") {
		t.Fatalf("error = %v", err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want zero", writes)
	}
}

func TestRunReportsPortInUseBeforeWriting(t *testing.T) {
	writes := 0
	sys := System{
		GOOS:      "darwin",
		WriteFile: func(string, []byte, os.FileMode) error { writes++; return nil },
		Check:     func(context.Context, string) (int, error) { return 180000, nil },
		CheckPort: func(string) error { return errors.New("bind: address already in use") },
		Stat:      func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none", Yes: true}
	err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys)
	if err == nil || !strings.Contains(err.Error(), "port 8787 is already in use") {
		t.Fatalf("error = %v", err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want zero", writes)
	}
}

func TestRunReportsMissingSystemdBeforeWriting(t *testing.T) {
	writes := 0
	sys := System{
		GOOS: "linux", IsRoot: true,
		WriteFile: func(string, []byte, os.FileMode) error { writes++; return nil },
		Check:     func(context.Context, string) (int, error) { return 180000, nil },
		LookPath: func(file string) (string, error) {
			if file == "systemctl" {
				return "", errors.New("missing")
			}
			return "/usr/bin/" + file, nil
		},
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none", Yes: true}
	err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys)
	if err == nil || !strings.Contains(err.Error(), "systemd is required") {
		t.Fatalf("error = %v", err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want zero", writes)
	}
}

func TestRunRetriesDBWithDefaults(t *testing.T) {
	var output bytes.Buffer
	input := strings.Join([]string{
		"",           // component fields, not a complete URL
		"db.example", // host
		"",           // default port
		"alice",      // user
		"p@ss:w/rd#% x",
		"analytics", // name
		"",          // default remote sslmode
		"",          // URL remains empty on retry
		"",          // keep host
		"",          // keep port
		"",          // keep user
		"",          // keep password
		"",          // keep name
		"",          // keep sslmode
	}, "\n") + "\n"
	p := NewPrompter(strings.NewReader(input), &output)

	var writes int
	var checked []string
	sys := System{
		Runner: &fakeRunner{},
		WriteFile: func(_ string, _ []byte, _ os.FileMode) error {
			writes++
			return nil
		},
		GOOS:   "darwin",
		IsRoot: false,
		Check: func(_ context.Context, conn string) (int, error) {
			if writes != 0 {
				t.Fatalf("WriteFile called before database check passed")
			}
			checked = append(checked, conn)
			if len(checked) == 1 {
				return 0, &CheckError{Reason: "password authentication failed for user alice", Field: "password"}
			}
			return 180000, nil
		},
		Stat: func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "none"}
	if err := Run(context.Background(), opts, p, sys); err != nil {
		t.Fatal(err)
	}
	if len(checked) != 2 {
		t.Fatalf("checks = %d, want 2", len(checked))
	}
	for i, conn := range checked {
		u, err := url.Parse(conn)
		if err != nil {
			t.Fatal(err)
		}
		password, _ := u.User.Password()
		if u.Hostname() != "db.example" || u.User.Username() != "alice" || password != "p@ss:w/rd#% x" || strings.TrimPrefix(u.Path, "/") != "analytics" {
			t.Errorf("check %d did not retain entered defaults: %s", i+1, conn)
		}
	}
	if writes != 1 {
		t.Fatalf("writes = %d, want one .env write", writes)
	}
	if got := output.String(); !strings.Contains(got, "password authentication failed for user alice") || !strings.Contains(got, "Database host [db.example]") {
		t.Fatalf("output did not show reason and editable defaults:\n%s", got)
	}
}

func TestRunYesMissingValueErrors(t *testing.T) {

	tests := []struct {
		name string
		opts Options
		want string
	}{
		{name: "mode", opts: Options{Yes: true}, want: "--mode"},
		{name: "base URL", opts: Options{Yes: true, Mode: "binary"}, want: "--base-url"},
		{name: "database", opts: Options{Yes: true, Mode: "binary", BaseURL: "https://analytics.supermetry.com", Listen: "127.0.0.1:8787", Proxy: "none"}, want: "--database-url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Run(context.Background(), tt.opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), System{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Run() error = %v, want mention of %s", err, tt.want)
			}
		})
	}
}

func TestRunYesFailedDBExits(t *testing.T) {

	writes := 0
	wantErr := &CheckError{Reason: "database check failed", Field: "password"}
	sys := System{
		WriteFile: func(string, []byte, os.FileMode) error { writes++; return nil },
		GOOS:      "linux",
		IsRoot:    true,
		Check:     func(context.Context, string) (int, error) { return 0, wantErr },
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none", Yes: true}
	err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want database check error", err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want zero", writes)
	}
}

func TestRunNoWritesBeforeConfirm(t *testing.T) {

	var output bytes.Buffer
	writes := 0
	runner := &fakeRunner{}
	sys := System{
		Runner:    runner,
		WriteFile: func(string, []byte, os.FileMode) error { writes++; return nil },
		GOOS:      "linux",
		IsRoot:    false,
		LookPath:  func(string) (string, error) { return "/usr/bin/sudo", nil },
		Check:     func(context.Context, string) (int, error) { return 180000, nil },
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none"}
	err := Run(context.Background(), opts, NewPrompter(strings.NewReader("n\n"), &output), sys)
	if err != nil {
		t.Fatal(err)
	}
	if writes != 0 || len(runner.calls) != 0 {
		t.Fatalf("before confirmation: writes = %d, commands = %v; want none", writes, runner.calls)
	}
	if !strings.Contains(output.String(), "/etc/supermetry/supermetry.env") || !strings.Contains(output.String(), "/opt/supermetry/versions/latest/supermetry") {
		t.Fatalf("plan output missing paths:\n%s", output.String())
	}
}

func TestRunExistingEnvDeclinedStopsInstallation(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".env", []byte("SUPERMETRY_SECRET_KEY=keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	writes := 0
	sys := System{
		WriteFile: func(string, []byte, os.FileMode) error { writes++; return nil },
		GOOS:      "darwin",
		Check:     func(context.Context, string) (int, error) { return 180000, nil },
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.supermetry.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none"}
	if err := Run(context.Background(), opts, NewPrompter(strings.NewReader("n\n"), &output), sys); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want zero", writes)
	}
	if strings.Contains(output.String(), "Open https://analytics.supermetry.com/app") {
		t.Fatalf("installer reported completion after replacement was declined:\n%s", output.String())
	}
}
