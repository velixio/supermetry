package installer

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	return nil, nil
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
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.example.com", Listen: "127.0.0.1:8787", Proxy: "none"}
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
	t.Parallel()

	tests := []struct {
		name string
		opts Options
		want string
	}{
		{name: "mode", opts: Options{Yes: true}, want: "--mode"},
		{name: "base URL", opts: Options{Yes: true, Mode: "binary"}, want: "--base-url"},
		{name: "database", opts: Options{Yes: true, Mode: "binary", BaseURL: "https://analytics.example.com", Listen: "127.0.0.1:8787", Proxy: "none"}, want: "--database-url"},
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
	t.Parallel()

	writes := 0
	wantErr := &CheckError{Reason: "database check failed", Field: "password"}
	sys := System{
		WriteFile: func(string, []byte, os.FileMode) error { writes++; return nil },
		GOOS:      "linux",
		IsRoot:    true,
		Check:     func(context.Context, string) (int, error) { return 0, wantErr },
	}
	opts := Options{Mode: "binary", BaseURL: "https://analytics.example.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none", Yes: true}
	err := Run(context.Background(), opts, NewPrompter(strings.NewReader(""), &bytes.Buffer{}), sys)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want database check error", err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want zero", writes)
	}
}

func TestRunNoWritesBeforeConfirm(t *testing.T) {
	t.Parallel()

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
	opts := Options{Mode: "binary", BaseURL: "https://analytics.example.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none"}
	err := Run(context.Background(), opts, NewPrompter(strings.NewReader("n\n"), &output), sys)
	if err != nil {
		t.Fatal(err)
	}
	if writes != 0 || len(runner.calls) != 0 {
		t.Fatalf("before confirmation: writes = %d, commands = %v; want none", writes, runner.calls)
	}
	if !strings.Contains(output.String(), "/etc/supermetry/supermetry.env") || !strings.Contains(output.String(), "/usr/local/bin/supermetry") {
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
	opts := Options{Mode: "binary", BaseURL: "https://analytics.example.com", DB: DBConfig{URL: "postgres://example/db"}, Listen: "127.0.0.1:8787", Proxy: "none"}
	if err := Run(context.Background(), opts, NewPrompter(strings.NewReader("n\n"), &output), sys); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want zero", writes)
	}
	if strings.Contains(output.String(), "Open https://analytics.example.com/app") {
		t.Fatalf("installer reported completion after replacement was declined:\n%s", output.String())
	}
}
