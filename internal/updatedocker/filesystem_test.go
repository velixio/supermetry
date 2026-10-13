package updatedocker_test

import (
	"os"
	"path/filepath"
	"testing"

	"supermetry/internal/updatedocker"
)

func TestOSFileSystemAtomicWriteReplacesAndSyncsRegularFile(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "nested", "state")
	filesystem := updatedocker.OSFileSystem{}
	if err := filesystem.AtomicWrite(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.AtomicWrite(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := filesystem.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Fatalf("contents = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestOSFileSystemRejectsSymlinkReads(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := (updatedocker.OSFileSystem{}).ReadFile(link); err == nil {
		t.Fatal("symlink read accepted")
	}
}
