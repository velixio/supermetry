// Package demo creates and seeds synthetic demo projects.
package demo

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/alerts"
	"supermetry/internal/dict"
	"supermetry/internal/platform/authz"
	"supermetry/internal/replay"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/synth"
	"supermetry/internal/writer"
)

var (
	ErrInvalidTimezone   = errors.New("invalid timezone")
	ErrDemoAlreadySeeded = errors.New("demo project is already seeded")
)

const DemoLifetime = 24 * time.Hour

// Deps contains the process-owned dependencies used by demo creation and seeding.
type Deps struct {
	Pool     *pgxpool.Pool
	Registry interface {
		sites.Registry
		Invalidate() error
	}
	Dict     *dict.Dict
	Authz    func(context.Context, authz.Principal, int64) error
	Audit    func(context.Context, pgx.Tx, authz.Principal, int64, int32) error
	Blobs    replay.BlobStore
	Index    *replay.IndexWriter
	Sessions session.Store
	Activity session.ActivityRecorder
	Writer   *writer.Writer
	Alerts   interface {
		CreateAlert(context.Context, authz.Principal, int32, alerts.AlertInput) (alerts.Alert, error)
	}
	Definitions interface{ Invalidate() error }
	Now         func() time.Time
	Seed        int64
	Visitors    float64
	Window      int

	// Test hooks. Production callers leave these nil.
	SeedStep       func(context.Context, int32) error
	SeedVisit      func(index, total int)
	RetryWait      func(context.Context, time.Duration) error
	SetLiveThrough func(context.Context, int32, time.Time) error
}

// Service owns demo seed goroutines and their cancellation boundary.
type Service struct {
	deps Deps

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running map[int32]*seedRun

	tickMu       sync.Mutex
	pending      map[int32][]pendingVisit
	busyPending  map[int32][]pendingVisit
	busyWatching map[int32]bool
	busySeq      map[int32]uint64
	cursors      map[int32]time.Time
	viewMu       sync.Mutex
	viewed       map[int32]time.Time
	watched      map[int32]time.Time
}

type seedRun struct {
	done chan struct{}
	err  error
}

// New constructs a demo service. Close releases its background seed context.
func New(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Visitors <= 0 {
		deps.Visitors = 150
	}
	if deps.Window <= 0 {
		deps.Window = synth.DemoWindowDays
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		deps: deps, ctx: ctx, cancel: cancel,
		running: make(map[int32]*seedRun), pending: make(map[int32][]pendingVisit), busyPending: make(map[int32][]pendingVisit),
		busyWatching: make(map[int32]bool), busySeq: make(map[int32]uint64), cursors: make(map[int32]time.Time),
		viewed: make(map[int32]time.Time), watched: make(map[int32]time.Time),
	}
}

func (s *Service) configurationError() error {
	if s == nil || s.deps.Pool == nil || s.deps.Registry == nil || s.deps.Dict == nil ||
		s.deps.Blobs == nil || s.deps.Index == nil || s.deps.Alerts == nil || s.deps.Definitions == nil {
		return errors.New("demo service is not configured")
	}
	return nil
}

// Wait waits for seed goroutines started by Create.
func (s *Service) Wait() {
	if s != nil {
		s.wg.Wait()
	}
}

// Close cancels background seeds and waits for them to stop.
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.cancel()
	s.Wait()
}
