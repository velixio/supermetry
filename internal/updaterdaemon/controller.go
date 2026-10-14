// Package updaterdaemon implements the root-owned half of the manual update
// protocol. It accepts only validated requests from the narrow Unix socket and
// persists every decision before doing work.
package updaterdaemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"supermetry/internal/updatehelper"
)

type BackendFactory func(context.Context, updatehelper.Job) (updatehelper.Backend, error)

type Config struct {
	JobsDir        string
	BackendFactory BackendFactory
	Now            func() time.Time
}

type Controller struct {
	jobsDir string
	store   *updatehelper.FileStore
	factory BackendFactory
	now     func() time.Time

	mu     sync.Mutex
	active string
	wg     sync.WaitGroup
}

type receipt struct {
	JobID         string `json:"job_id"`
	TargetVersion string `json:"target_version"`
	NonceSHA256   string `json:"nonce_sha256"`
}

func NewController(config Config) *Controller {
	if config.JobsDir == "" {
		config.JobsDir = updatehelper.DefaultJobStoreDir
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Controller{
		jobsDir: config.JobsDir,
		store:   updatehelper.NewFileStore(config.JobsDir),
		factory: config.BackendFactory,
		now:     config.Now,
	}
}

func (c *Controller) Handle(ctx context.Context, request updatehelper.Request) updatehelper.Response {
	if err := request.Validate(); err != nil {
		return response(request.JobID, updatehelper.StateFailed, "invalid_request")
	}
	switch request.Operation {
	case updatehelper.OperationStatus:
		job, err := c.store.Load(request.JobID)
		if err != nil {
			return response(request.JobID, updatehelper.StateFailed, "job_not_found")
		}
		return response(job.ID, job.State, job.ErrorCode)
	case updatehelper.OperationStart:
		return c.start(ctx, request)
	default:
		return response(request.JobID, updatehelper.StateFailed, "invalid_request")
	}
}

func (c *Controller) start(ctx context.Context, request updatehelper.Request) updatehelper.Response {
	c.mu.Lock()
	defer c.mu.Unlock()

	receipts, err := c.loadReceipts()
	if err != nil {
		return response(request.JobID, updatehelper.StateFailed, "state_unavailable")
	}
	digest := nonceDigest(request.Nonce)
	for _, saved := range receipts {
		if saved.NonceSHA256 != digest {
			if saved.JobID == request.JobID {
				return response(request.JobID, updatehelper.StateRequested, "job_conflict")
			}
			continue
		}
		if saved.JobID != request.JobID || saved.TargetVersion != request.TargetVersion {
			return response(request.JobID, updatehelper.StateRequested, "nonce_reused")
		}
		job, loadErr := c.store.Load(request.JobID)
		if loadErr == nil {
			return response(job.ID, job.State, job.ErrorCode)
		}
		if !errors.Is(loadErr, os.ErrNotExist) {
			return response(request.JobID, updatehelper.StateFailed, "state_unavailable")
		}
		// A crash may occur after the receipt is durable but before the job is.
		// The same authenticated retry is allowed to finish that transaction.
	}
	if existing, loadErr := c.store.Load(request.JobID); loadErr == nil {
		return response(existing.ID, existing.State, "job_conflict")
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return response(request.JobID, updatehelper.StateFailed, "state_unavailable")
	}
	active, err := c.activeJobs()
	if err != nil {
		return response(request.JobID, updatehelper.StateFailed, "state_unavailable")
	}
	if len(active) > 0 || c.active != "" {
		return response(request.JobID, updatehelper.StateRequested, "active_update")
	}
	saved := receipt{JobID: request.JobID, TargetVersion: request.TargetVersion, NonceSHA256: digest}
	if err := c.saveReceipt(saved); err != nil {
		return response(request.JobID, updatehelper.StateFailed, "state_unavailable")
	}
	now := c.now().UTC()
	job := updatehelper.Job{ID: request.JobID, TargetVersion: request.TargetVersion, State: updatehelper.StateRequested, CreatedAt: now, UpdatedAt: now}
	if err := c.store.Save(job); err != nil {
		return response(request.JobID, updatehelper.StateFailed, "state_unavailable")
	}
	c.active = job.ID
	c.launch(ctx, job, false)
	return response(job.ID, job.State, "")
}

// Recover resumes the sole durable non-terminal job. More than one active job
// indicates state corruption and causes the helper to fail closed.
func (c *Controller) Recover(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	jobs, err := c.activeJobs()
	if err != nil {
		return err
	}
	if len(jobs) > 1 {
		return errors.New("multiple active updater jobs found")
	}
	if len(jobs) == 0 {
		return nil
	}
	c.active = jobs[0].ID
	c.launch(ctx, jobs[0], true)
	return nil
}

func (c *Controller) launch(ctx context.Context, job updatehelper.Job, recover bool) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() {
			c.mu.Lock()
			if c.active == job.ID {
				c.active = ""
			}
			c.mu.Unlock()
		}()
		if c.factory == nil {
			c.failInitialization(job)
			return
		}
		backend, err := c.factory(ctx, job)
		if err != nil {
			c.failInitialization(job)
			return
		}
		engine := updatehelper.Engine{Store: c.store, Backend: backend, Now: c.now}
		if recover {
			_, _ = engine.Recover(ctx, job.ID)
			return
		}
		_, _ = engine.Run(ctx, job)
	}()
}

func (c *Controller) failInitialization(job updatehelper.Job) {
	switch job.State {
	case updatehelper.StateStopping, updatehelper.StateBackup, updatehelper.StateActivating, updatehelper.StateRestarting, updatehelper.StateHealthCheck:
		// Backend construction failed after a persisted operation may have
		// stopped or changed the deployment. Without the backend, automatic
		// recovery cannot be proven safe.
		job.State = updatehelper.StateRecoveryRequired
		job.ErrorCode = "backend_initialization_failed"
	default:
		_ = job.Advance(updatehelper.StateFailed)
		job.ErrorCode = "backend_initialization_failed"
	}
	job.UpdatedAt = c.now().UTC()
	_ = c.store.Save(job)
}

func (c *Controller) Wait() { c.wg.Wait() }

func (c *Controller) activeJobs() ([]updatehelper.Job, error) {
	entries, err := os.ReadDir(c.jobsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read updater jobs: %w", err)
	}
	var jobs []updatehelper.Job
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".receipt.json") || strings.Contains(name, ".backup.") || strings.Contains(name, ".release-manifest.") {
			continue
		}
		jobID := strings.TrimSuffix(name, ".json")
		job, loadErr := c.store.Load(jobID)
		if loadErr != nil {
			return nil, loadErr
		}
		if !job.State.Terminal() {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs, nil
}

func (c *Controller) loadReceipts() ([]receipt, error) {
	entries, err := os.ReadDir(c.jobsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]receipt, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".receipt.json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(c.jobsDir, entry.Name()))
		if readErr != nil {
			return nil, readErr
		}
		var saved receipt
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&saved)
		var trailing any
		trailingErr := decoder.Decode(&trailing)
		if decodeErr != nil || !errors.Is(trailingErr, io.EOF) || saved.JobID == "" || saved.TargetVersion == "" || len(saved.NonceSHA256) != 64 {
			return nil, errors.New("invalid updater receipt")
		}
		result = append(result, saved)
	}
	return result, nil
}

func (c *Controller) saveReceipt(saved receipt) error {
	if err := os.MkdirAll(c.jobsDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(c.jobsDir, ".receipt-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	finalName := filepath.Join(c.jobsDir, saved.JobID+".receipt.json")
	if err := os.Rename(temporaryName, finalName); err != nil {
		return err
	}
	directory, err := os.Open(c.jobsDir)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func nonceDigest(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

func response(jobID string, state updatehelper.State, code string) updatehelper.Response {
	return updatehelper.Response{Version: updatehelper.ProtocolVersion, JobID: jobID, State: state, ErrorCode: code}
}
