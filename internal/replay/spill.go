package replay

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const spillSuffix = ".spill"

var errSpillClosed = errors.New("replay spill store is closed")

type spillSet struct {
	dir string

	mu     sync.Mutex
	files  map[string]*spillFile
	closed bool
}

type spillFile struct {
	key    string
	path   string
	mu     sync.Mutex
	sealed bool
}

func newSpillSet(dir string) (*spillSet, error) {
	if dir == "" {
		return nil, errors.New("replay spill directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve replay spill directory: %w", err)
	}
	if err := os.MkdirAll(abs, blobDirMode); err != nil {
		return nil, fmt.Errorf("create replay spill directory: %w", err)
	}

	set := &spillSet{dir: abs, files: make(map[string]*spillFile)}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("scan replay spill directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), spillSuffix) {
			continue
		}
		encoded := strings.TrimSuffix(entry.Name(), spillSuffix)
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		key := string(decoded)
		if _, err := validateBlobPath(key); err != nil {
			continue
		}
		set.files[key] = &spillFile{key: key, path: filepath.Join(abs, entry.Name())}
	}
	return set, nil
}

func (s *spillSet) append(ctx context.Context, key string, r io.Reader) (int64, error) {
	if _, err := validateBlobPath(key); err != nil {
		return 0, err
	}
	file, err := s.getOrCreate(key)
	if err != nil {
		return 0, err
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.sealed {
		return 0, errors.New("replay spill is closing")
	}

	handle, err := os.OpenFile(file.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, blobFileMode)
	if err != nil {
		return 0, fmt.Errorf("open replay spill: %w", err)
	}
	written, copyErr := io.Copy(handle, contextReader{ctx: ctx, reader: r})
	closeErr := handle.Close()
	if copyErr != nil {
		return written, fmt.Errorf("append replay spill: %w", copyErr)
	}
	if closeErr != nil {
		return written, fmt.Errorf("close replay spill: %w", closeErr)
	}
	return written, nil
}

func (s *spillSet) open(key string) (*os.File, bool, error) {
	s.mu.Lock()
	file, ok := s.files[key]
	s.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	handle, err := os.Open(file.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open replay spill: %w", err)
	}
	return handle, true, nil
}

func (s *spillSet) get(key string) (*spillFile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, ok := s.files[key]
	return file, ok
}

func (s *spillSet) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.files))
	for key := range s.files {
		keys = append(keys, key)
	}
	return keys
}

func (s *spillSet) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *spillSet) remove(file *spillFile) error {
	if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove replay spill: %w", err)
	}
	s.mu.Lock()
	if s.files[file.key] == file {
		delete(s.files, file.key)
	}
	s.mu.Unlock()
	return nil
}

func (s *spillSet) removePrefix(prefix string) (int64, error) {
	var freed int64
	for _, key := range s.keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		file, ok := s.get(key)
		if !ok {
			continue
		}
		file.mu.Lock()
		file.sealed = true
		info, err := os.Stat(file.path)
		if err == nil {
			freed += info.Size()
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			file.mu.Unlock()
			return freed, fmt.Errorf("stat replay spill: %w", err)
		}
		err = s.remove(file)
		file.mu.Unlock()
		if err != nil {
			return freed, err
		}
	}
	return freed, nil
}

func (s *spillSet) removeKey(key string) (int64, error) {
	file, ok := s.get(key)
	if !ok {
		return 0, nil
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	file.sealed = true
	var size int64
	info, err := os.Stat(file.path)
	if err == nil {
		size = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("stat replay spill: %w", err)
	}
	if err := s.remove(file); err != nil {
		return 0, err
	}
	return size, nil
}

func (s *spillSet) getOrCreate(key string) (*spillFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errSpillClosed
	}
	if file := s.files[key]; file != nil {
		return file, nil
	}
	name := base64.RawURLEncoding.EncodeToString([]byte(key)) + spillSuffix
	file := &spillFile{key: key, path: filepath.Join(s.dir, name)}
	s.files[key] = file
	return file, nil
}
