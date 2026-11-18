package imports

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

var (
	ErrQueueFull      = errors.New("import queue is full")
	ErrManagerRunning = errors.New("import manager is already running")
	ErrManagerStopped = errors.New("import manager is stopped")
)

type Job struct {
	ImportID int64
	Source   Source
	Path     string
	// Pull marks a pull import: the worker runs the PullRunner instead of
	// parsing Path, and an interrupted run stays resumable.
	Pull bool
}

// PullRunner executes or resumes a pull import. It returns the context error
// untouched on shutdown so the import can resume at the next start.
type PullRunner interface {
	RunPull(ctx context.Context, importID int64) error
}

type LifecycleStore interface {
	SetRunning(context.Context, int64) error
	SetFailed(context.Context, int64, error) error
	Complete(context.Context, int64, Aggregate, ParseStats) error
	ImportLocation(context.Context, int64) (*time.Location, error)
}

// Manager owns a bounded queue consumed synchronously by Run. Starting exactly
// one Run goroutine gives each application instance one import worker.
type Manager struct {
	store   LifecycleStore
	jobs    chan Job
	process func(context.Context, Source, string, *time.Location) (Aggregate, ParseStats, error)
	pull    PullRunner

	mu      sync.Mutex
	running bool
	stopped bool
}

func NewManager(store LifecycleStore, queueSize int) *Manager {
	if queueSize < 1 {
		queueSize = 1
	}
	return &Manager{store: store, jobs: make(chan Job, queueSize), process: ProcessFileInLocation}
}

// SetPullRunner enables pull imports; call it before Run.
func (m *Manager) SetPullRunner(runner PullRunner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pull = runner
}

// Queue schedules a job without blocking. The caller retains ownership of the
// temporary file when Queue returns an error; Manager removes accepted files.
func (m *Manager) Queue(job Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrManagerStopped
	}
	select {
	case m.jobs <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

func (m *Manager) Run(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return ErrManagerRunning
	}
	if m.stopped {
		m.mu.Unlock()
		return ErrManagerStopped
	}
	m.running = true
	m.mu.Unlock()
	defer m.stopAndRemoveQueuedFiles()

	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case job := <-m.jobs:
			if ctx.Err() != nil {
				m.interrupt(ctx, job)
				return context.Cause(ctx)
			}
			m.runJob(ctx, job)
		}
	}
}

// failTimeout bounds the status write that records a failure. It uses a
// context detached from shutdown so interrupted imports never stay "running".
const failTimeout = 5 * time.Second

func (m *Manager) markFailed(ctx context.Context, id int64, cause error) {
	failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failTimeout)
	defer cancel()
	if err := m.store.SetFailed(failCtx, id, cause); err != nil {
		slog.Warn("mark import failed", "import_id", id, "error", err)
	}
}

// interrupt drops a job that cannot run because of shutdown. File imports
// fail (their upload is gone); pull imports stay pending and resume at the
// next start from their stored cursor.
func (m *Manager) interrupt(ctx context.Context, job Job) {
	if job.Pull {
		return
	}
	_ = os.Remove(job.Path)
	m.markFailed(ctx, job.ImportID, errInterrupted)
}

func (m *Manager) runPull(ctx context.Context, job Job) {
	m.mu.Lock()
	runner := m.pull
	m.mu.Unlock()
	if runner == nil {
		m.markFailed(ctx, job.ImportID, ErrPullNotEnabled)
		return
	}
	err := runner.RunPull(ctx, job.ImportID)
	if err == nil {
		return
	}
	if ctx.Err() != nil {
		// Shutdown: leave the import running; Recover resets it to pending.
		slog.Info("pull import paused by shutdown", "import_id", job.ImportID)
		return
	}
	m.markFailed(ctx, job.ImportID, err)
}

func (m *Manager) runJob(ctx context.Context, job Job) {
	if job.Path != "" {
		defer os.Remove(job.Path)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("import panicked", "import_id", job.ImportID, "panic", recovered, "stack", string(debug.Stack()))
			m.markFailed(ctx, job.ImportID, fmt.Errorf("import failed unexpectedly"))
		}
	}()
	if err := m.store.SetRunning(ctx, job.ImportID); err != nil {
		if !errors.Is(err, ErrNotFound) {
			m.markFailed(ctx, job.ImportID, err)
		}
		return
	}
	if job.Pull {
		m.runPull(ctx, job)
		return
	}
	location, err := m.store.ImportLocation(ctx, job.ImportID)
	if err != nil {
		m.markFailed(ctx, job.ImportID, err)
		return
	}
	aggregate, stats, err := m.process(ctx, job.Source, job.Path, location)
	if err == nil {
		err = m.store.Complete(ctx, job.ImportID, aggregate, stats)
	}
	if err != nil {
		if ctx.Err() != nil {
			err = errInterrupted
		}
		m.markFailed(ctx, job.ImportID, err)
	}
}

var errInterrupted = errors.New("import interrupted by server shutdown")

func (m *Manager) stopAndRemoveQueuedFiles() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = false
	m.stopped = true
	for {
		select {
		case job := <-m.jobs:
			m.interrupt(context.Background(), job)
		default:
			return
		}
	}
}

// ProcessFile parses an uploaded file; reads fail once ctx is done.
func ProcessFile(ctx context.Context, source Source, path string) (Aggregate, ParseStats, error) {
	return ProcessFileInLocation(ctx, source, path, time.UTC)
}

// ProcessFileInLocation parses an upload using the site's timezone for source
// formats whose rows contain timestamps rather than civil dates.
func ProcessFileInLocation(ctx context.Context, source Source, path string, location *time.Location) (Aggregate, ParseStats, error) {
	switch source {
	case SourcePlausible:
		return parsePlausible(ctx, path)
	case SourceUmami:
		file, err := os.Open(path)
		if err != nil {
			return newAggregate(), ParseStats{}, err
		}
		defer file.Close()
		return ParseUmamiInLocation(contextReader{ctx, file}, location)
	case SourceGA4:
		return parseGA4File(ctx, path)
	case SourceMixpanel:
		return parseEventFile(ctx, path, location, providerMixpanel)
	case SourceAmplitude:
		return parseEventFile(ctx, path, location, providerAmplitude)
	default:
		return newAggregate(), ParseStats{}, fmt.Errorf("unsupported import source %q", source)
	}
}
