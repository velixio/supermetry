package updatebinary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"supermetry/internal/appbackup"
	"supermetry/internal/updatehelper"
)

// OSFileSystem is the production filesystem implementation. Tests should use
// an isolated implementation because updater paths are intentionally absolute.
type OSFileSystem struct{}

func (OSFileSystem) MkdirAll(name string, mode os.FileMode) error { return os.MkdirAll(name, mode) }
func (OSFileSystem) Open(name string) (File, error)               { return os.Open(name) }
func (OSFileSystem) OpenFile(name string, flag int, mode os.FileMode) (File, error) {
	return os.OpenFile(name, flag, mode)
}
func (OSFileSystem) Rename(oldName, newName string) error      { return os.Rename(oldName, newName) }
func (OSFileSystem) Remove(name string) error                  { return os.Remove(name) }
func (OSFileSystem) RemoveAll(name string) error               { return os.RemoveAll(name) }
func (OSFileSystem) Readlink(name string) (string, error)      { return os.Readlink(name) }
func (OSFileSystem) Symlink(oldName, newName string) error     { return os.Symlink(oldName, newName) }
func (OSFileSystem) Stat(name string) (os.FileInfo, error)     { return os.Stat(name) }
func (OSFileSystem) Lstat(name string) (os.FileInfo, error)    { return os.Lstat(name) }
func (OSFileSystem) Chmod(name string, mode os.FileMode) error { return os.Chmod(name, mode) }
func (OSFileSystem) Chown(name string, uid, gid int) error     { return os.Chown(name, uid, gid) }
func (OSFileSystem) Lchown(name string, uid, gid int) error    { return os.Lchown(name, uid, gid) }
func (OSFileSystem) ReadFile(name string) ([]byte, error)      { return os.ReadFile(name) }
func (OSFileSystem) WriteFile(name string, data []byte, mode os.FileMode) error {
	return os.WriteFile(name, data, mode)
}
func (OSFileSystem) SyncDir(name string) error {
	directory, err := os.Open(name)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	return errors.Join(syncErr, directory.Close())
}

// BinaryBackupCreator runs the old installed binary, with the service's
// captured environment, then independently verifies the resulting appbackup
// archive before activation is allowed.
type BinaryBackupCreator struct {
	Runner updatehelper.Runner
	Verify func(context.Context, string) (appbackup.Manifest, error)
	Open   func(string) (io.ReadCloser, error)
	Stat   func(string) (os.FileInfo, error)
}

func NewBinaryBackupCreator(runner updatehelper.Runner) *BinaryBackupCreator {
	return &BinaryBackupCreator{
		Runner: runner,
		Verify: appbackup.Verify,
		Open:   func(name string) (io.ReadCloser, error) { return os.Open(name) },
		Stat:   os.Stat,
	}
}

func (b *BinaryBackupCreator) CreateAndVerify(ctx context.Context, request BackupRequest) (appbackup.Result, error) {
	if b == nil || b.Runner == nil || b.Verify == nil || b.Open == nil || b.Stat == nil {
		return appbackup.Result{}, errors.New("binary backup creator is not configured")
	}
	if request.Executable == "" || request.OutputPath == "" || !stableVersion(request.Version) {
		return appbackup.Result{}, errors.New("binary backup request is invalid")
	}
	result, err := b.Runner.Run(ctx, request.Executable, []string{"backup", "--out", request.OutputPath}, updatehelper.RunOptions{Env: append([]string(nil), request.Environment...)})
	if err != nil {
		return appbackup.Result{}, fmt.Errorf("run current binary backup: %w", err)
	}
	if result.ExitCode != 0 {
		return appbackup.Result{}, fmt.Errorf("current binary backup exited with status %d", result.ExitCode)
	}
	manifest, err := b.Verify(ctx, request.OutputPath)
	if err != nil {
		return appbackup.Result{}, fmt.Errorf("verify current binary backup: %w", err)
	}
	if manifest.SchemaVersion < 0 || "v"+strings.TrimPrefix(manifest.SupermetryVersion, "v") != request.Version {
		return appbackup.Result{}, errors.New("backup manifest does not match current binary")
	}
	createdAt, err := time.Parse(time.RFC3339, manifest.CreatedAt)
	if err != nil {
		return appbackup.Result{}, errors.New("backup manifest has invalid creation time")
	}
	info, err := b.Stat(request.OutputPath)
	if err != nil || !info.Mode().IsRegular() {
		return appbackup.Result{}, errors.New("verified backup is not a regular file")
	}
	file, err := b.Open(request.OutputPath)
	if err != nil {
		return appbackup.Result{}, fmt.Errorf("open verified backup: %w", err)
	}
	hasher := sha256.New()
	_, hashErr := io.Copy(hasher, &contextReader{ctx: ctx, reader: file})
	closeErr := file.Close()
	if hashErr != nil || closeErr != nil {
		return appbackup.Result{}, errors.Join(hashErr, closeErr)
	}
	return appbackup.Result{
		Path: request.OutputPath, SHA256: hex.EncodeToString(hasher.Sum(nil)), Size: info.Size(),
		SchemaVersion: manifest.SchemaVersion, CreatedAt: createdAt,
	}, nil
}

var _ FileSystem = OSFileSystem{}
var _ BackupCreator = (*BinaryBackupCreator)(nil)
