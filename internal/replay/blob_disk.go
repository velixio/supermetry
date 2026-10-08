package replay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	blobFileMode = 0o640
	blobDirMode  = 0o750
)

type DiskStore struct {
	dir string
}

func NewDiskStore(dir string) (*DiskStore, error) {
	if dir == "" {
		return nil, errors.New("replay disk directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve replay disk directory: %w", err)
	}
	if err := os.MkdirAll(abs, blobDirMode); err != nil {
		return nil, fmt.Errorf("create replay disk directory: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open replay disk directory: %w", err)
	}
	if err := root.Close(); err != nil {
		return nil, fmt.Errorf("close replay disk directory: %w", err)
	}
	return &DiskStore{dir: abs}, nil
}

func (s *DiskStore) Append(ctx context.Context, key string, r io.Reader) (int64, error) {
	name, err := validateBlobPath(key)
	if err != nil {
		return 0, err
	}
	root, err := s.openRoot()
	if err != nil {
		return 0, err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(name), blobDirMode); err != nil {
		return 0, fmt.Errorf("create replay blob parent: %w", err)
	}
	file, err := root.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, blobFileMode)
	if err != nil {
		return 0, fmt.Errorf("open replay blob for append: %w", err)
	}
	written, copyErr := io.Copy(file, contextReader{ctx: ctx, reader: r})
	closeErr := file.Close()
	if copyErr != nil {
		return written, fmt.Errorf("append replay blob: %w", copyErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("close replay blob: %w", closeErr)
	}
	return written, nil
}

func (s *DiskStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := validateBlobPath(key)
	if err != nil {
		return nil, err
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	file, err := root.Open(name)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("open replay blob: %w", err)
	}
	return &rootedFile{File: file, root: root}, nil
}

func (s *DiskStore) PutIfAbsent(ctx context.Context, key string, r io.Reader) (bool, error) {
	name, err := validateBlobPath(key)
	if err != nil {
		return false, err
	}
	root, err := s.openRoot()
	if err != nil {
		return false, err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(name), blobDirMode); err != nil {
		return false, fmt.Errorf("create replay blob parent: %w", err)
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, blobFileMode)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create replay blob: %w", err)
	}
	_, copyErr := io.Copy(file, contextReader{ctx: ctx, reader: r})
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = root.Remove(name)
		if copyErr != nil {
			return false, fmt.Errorf("write replay blob: %w", copyErr)
		}
		return false, fmt.Errorf("close replay blob: %w", closeErr)
	}
	return true, nil
}

func (s *DiskStore) DeletePrefix(ctx context.Context, prefix string) (int64, error) {
	name, err := validateBlobPath(prefix)
	if err != nil {
		return 0, err
	}
	root, err := s.openRoot()
	if err != nil {
		return 0, err
	}
	defer root.Close()
	var freed int64
	err = fs.WalkDir(root.FS(), filepath.ToSlash(name), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		freed += info.Size()
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("measure replay blob prefix: %w", err)
	}
	if err := root.RemoveAll(name); err != nil {
		return 0, fmt.Errorf("delete replay blob prefix: %w", err)
	}
	return freed, nil
}

func (s *DiskStore) Delete(ctx context.Context, key string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	name, err := validateBlobPath(key)
	if err != nil {
		return 0, err
	}
	root, err := s.openRoot()
	if err != nil {
		return 0, err
	}
	defer root.Close()
	info, err := root.Stat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat replay blob: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("replay blob is not a regular file")
	}
	if err := root.Remove(name); err != nil {
		return 0, fmt.Errorf("delete replay blob: %w", err)
	}
	return info.Size(), nil
}

func (s *DiskStore) openRoot() (*os.Root, error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, fmt.Errorf("open replay disk directory: %w", err)
	}
	return root, nil
}

func validateBlobPath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", errors.New("invalid replay blob path")
	}
	native := filepath.FromSlash(name)
	for _, component := range strings.FieldsFunc(native, func(r rune) bool { return r == '/' || r == '\\' }) {
		if component == ".." {
			return "", errors.New("invalid replay blob path")
		}
	}
	clean := filepath.Clean(native)
	if clean == "." || !filepath.IsLocal(clean) {
		return "", errors.New("invalid replay blob path")
	}
	return clean, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type rootedFile struct {
	*os.File
	root *os.Root
}

func (f *rootedFile) Close() error {
	return errors.Join(f.File.Close(), f.root.Close())
}
