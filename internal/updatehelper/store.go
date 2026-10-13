package updatehelper

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type JobStore interface {
	Save(Job) error
	Load(jobID string) (Job, error)
}

const DefaultJobStoreDir = "/var/lib/supermetry-updater/jobs"

type PersistPoint string

const (
	AfterTempWrite     PersistPoint = "after_temp_write"
	AfterTempSync      PersistPoint = "after_temp_sync"
	AfterRename        PersistPoint = "after_rename"
	AfterDirectorySync PersistPoint = "after_directory_sync"
)

type FileStore struct {
	dir         string
	InjectFault func(PersistPoint) error
}

func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir}
}

func (s *FileStore) Save(job Job) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("validate job: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create job store: %w", err)
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("encode job: %w", err)
	}
	payload = append(payload, '\n')
	temp, err := os.CreateTemp(s.dir, ".job-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary job: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary job: %w", err)
	}
	if _, err := temp.Write(payload); err != nil {
		return fmt.Errorf("write temporary job: %w", err)
	}
	if err := s.fault(AfterTempWrite); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary job: %w", err)
	}
	if err := s.fault(AfterTempSync); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary job: %w", err)
	}
	finalName := filepath.Join(s.dir, job.ID+".json")
	if err := os.Rename(tempName, finalName); err != nil {
		return fmt.Errorf("replace job: %w", err)
	}
	if err := s.fault(AfterRename); err != nil {
		return err
	}
	directory, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("open job directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync job directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close job directory: %w", err)
	}
	if err := s.fault(AfterDirectorySync); err != nil {
		return err
	}
	return nil
}

func (s *FileStore) Load(jobID string) (Job, error) {
	if err := validateJobID(jobID); err != nil {
		return Job{}, err
	}
	path := filepath.Join(s.dir, jobID+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return Job{}, fmt.Errorf("inspect job: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Job{}, errors.New("job state is not a regular file")
	}
	if info.Size() > MaxFrameSize {
		return Job{}, errors.New("job state is too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Job{}, fmt.Errorf("read job: %w", err)
	}
	var job Job
	if err := decodeStrictObject(data, &job, "id", "target_version", "state", "error_code", "created_at", "updated_at"); err != nil {
		return Job{}, fmt.Errorf("decode job: %w", err)
	}
	if err := job.Validate(); err != nil {
		return Job{}, fmt.Errorf("validate job: %w", err)
	}
	if job.ID != jobID {
		return Job{}, errors.New("job state ID does not match filename")
	}
	return job, nil
}

func (s *FileStore) fault(point PersistPoint) error {
	if s.InjectFault == nil {
		return nil
	}
	if err := s.InjectFault(point); err != nil {
		return fmt.Errorf("persist job at %s: %w", point, err)
	}
	return nil
}
