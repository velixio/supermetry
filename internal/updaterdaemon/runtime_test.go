package updaterdaemon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"supermetry/internal/updatehelper"
)

func TestExecRunnerDoesNotUseShellAndBoundsOutput(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses Unix printf")
	}
	result, err := (ExecRunner{}).Run(context.Background(), "/usr/bin/printf", []string{"%s", "$(touch /tmp/should-not-exist)"}, updatehelper.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Stdout) != "$(touch /tmp/should-not-exist)" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestLoadDescriptorRejectsWritableOrSymlinkedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	valid := []byte(`{"version":1,"mode":"binary","service_name":"supermetry.service","versions_dir":"/opt/supermetry/versions","current_link":"/opt/supermetry/current","compose_dir":"/opt/supermetry","compose_service":"supermetry","backup_dir":"/var/lib/supermetry-updater/backups","readiness_url":"http://127.0.0.1:8787/readyz","socket_group":"supermetry-updater"}`)
	path := filepath.Join(dir, "deployment.json")
	if err := os.WriteFile(path, valid, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDescriptor(path); err == nil {
		t.Fatal("writable descriptor accepted")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDescriptor(path); err != nil {
		t.Fatalf("valid descriptor: %v", err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDescriptor(link); err == nil {
		t.Fatal("symlink descriptor accepted")
	}
}

func TestParseVersionNormalizesReleaseOutput(t *testing.T) {
	for input, want := range map[string]string{
		"supermetry 1.2.3\n":  "v1.2.3",
		"supermetry v1.2.3\n": "v1.2.3",
		"supermetry dev\n":    "",
	} {
		if got := parseVersion([]byte(input)); got != want {
			t.Fatalf("parseVersion(%q)=%q, want %q", input, got, want)
		}
	}
}
