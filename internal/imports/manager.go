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
}

type LifecycleStore interface {
	SetRunning(context.Context, int64) error
	SetFailed(context.Context, int64, error) error
	Complete(context.Context, int64, Aggregate, ParseStats) error
}

// Manager owns a bounded queue consumed synchronously by Run. Starting exactly
// one Run goroutine gives each application instance one import worker.
type Manager struct {
	store   LifecycleStore
	jobs    chan Job
	process func(context.Context, Source, string) (Aggregate, ParseStats, error)

	mu      sync.Mutex
	running bool
	stopped bool
}

func NewManager(store LifecycleStore, queueSize int) *Manager {
	if queueSize < 1 {
		queueSize = 1
	}
	return &Manager{store: store, jobs: make(chan Job, queueSize), process: ProcessFile}
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
				_ = os.Remove(job.Path)
				m.markFailed(ctx, job.ImportID, errInterrupted)
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

func (m *Manager) runJob(ctx context.Context, job Job) {
	defer os.Remove(job.Path)
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
	aggregate, stats, err := m.process(ctx, job.Source, job.Path)
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
			_ = os.Remove(job.Path)
			m.markFailed(context.Background(), job.ImportID, errInterrupted)
		default:
			return
		}
	}
}

// ProcessFile parses an uploaded file; reads fail once ctx is done.
func ProcessFile(ctx context.Context, source Source, path string) (Aggregate, ParseStats, error) {
	switch source {
	case SourcePlausible:
		return parsePlausible(ctx, path)
	case SourceUmami:
		file, err := os.Open(path)
		if err != nil {
			return newAggregate(), ParseStats{}, err
		}
		defer file.Close()
		return ParseUmami(contextReader{ctx, file})
	default:
		return newAggregate(), ParseStats{}, fmt.Errorf("unsupported import source %q", source)
	}
}
