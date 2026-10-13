package updatedocker

import (
	"fmt"
	"os"
	"path/filepath"
)

type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return os.ReadFile(path)
}

func (OSFileSystem) AtomicWrite(path string, data []byte, mode uint32) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".update-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(os.FileMode(mode)); err != nil {
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace file atomically: %w", err)
	}
	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open parent directory: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return fmt.Errorf("sync parent directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close parent directory: %w", closeErr)
	}
	return nil
}

var _ FileSystem = OSFileSystem{}
