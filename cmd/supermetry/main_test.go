package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandNameDefaultsToServe(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"supermetry"}, "serve"},
		{[]string{"supermetry", "serve"}, "serve"},
		{[]string{"supermetry", "migrate"}, "migrate"},
		{[]string{"supermetry", "--help"}, "help"},
		{[]string{"supermetry", "-h"}, "help"},
		{[]string{"supermetry", "help"}, "help"},
	} {
		if got := commandName(tc.args); got != tc.want {
			t.Errorf("commandName(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestInstallHelpDocumentsFlagsAndExamples(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := installCommand(context.Background(), []string{"--help"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--dry-run", "--mode", "--base-url", "--database-url", "--db-host", "--db-port", "--db-user", "--db-password", "--db-name", "--db-sslmode", "--listen", "--proxy", "--image", "--dir", "--yes", "--version", "Examples:", "supermetry install --dry-run"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help missing %q:\n%s", want, output.String())
		}
	}
}

func TestStartupEnvFile(t *testing.T) {
	for _, tc := range []struct {
		name     string
		explicit string
		present  []string
		want     string
	}{
		{"explicit file wins", "/srv/custom.env", []string{".env", systemEnvFile}, "/srv/custom.env"},
		{"local .env first", "", []string{".env", systemEnvFile}, ".env"},
		{"installed settings when no local .env", "", []string{systemEnvFile}, systemEnvFile},
		{"local .env path when nothing exists", "", nil, ".env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key == "SUPERMETRY_ENV_FILE" {
					return tc.explicit
				}
				return ""
			}
			exists := func(path string) bool {
				for _, p := range tc.present {
					if p == path {
						return true
					}
				}
				return false
			}
			if got := startupEnvFile(getenv, exists); got != tc.want {
				t.Fatalf("startupEnvFile() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUserCommandReadsEnvFile runs main in a child process: `supermetry user`
// must load ./.env like every other command before it reads its settings.
func TestUserCommandReadsEnvFile(t *testing.T) {
	if os.Getenv("SUPERMETRY_TEST_RUN_MAIN") == "1" {
		os.Args = []string{"supermetry", "user", "make-admin", "--email", "admin@example.com"}
		main()
		return
	}
	dir := t.TempDir()
	settings := "SUPERMETRY_DATABASE_URL=postgres://u:p@127.0.0.1:1/none?sslmode=disable&connect_timeout=2\n" +
		"SUPERMETRY_BASE_URL=https://analytics.supermetry.com\n" +
		"SUPERMETRY_SECRET_KEY=" + strings.Repeat("ab", 32) + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestUserCommandReadsEnvFile$")
	cmd.Dir = dir
	cmd.Env = []string{"SUPERMETRY_TEST_RUN_MAIN=1", "PATH=" + os.Getenv("PATH")}
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "is required") || strings.Contains(string(out), "usage:") {
		t.Fatalf("user command ignored ./.env:\n%s", out)
	}
}
