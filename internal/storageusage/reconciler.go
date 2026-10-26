package storageusage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"
)

const ReconcileInterval = 24 * time.Hour

type Scanner interface {
	Fingerprint() string
	WorkUnits(context.Context, int64) ([]WorkUnit, error)
	Scan(context.Context, int64, WorkUnit) ([]Measurement, error)
}

type StepResult struct {
	RunID        int64
	Started      bool
	ProcessedKey string
	Published    bool
	Idle         bool
	Pending      int
}

type Reconciler struct {
	store    *Store
	version  int
	scanners []Scanner
}

func NewReconciler(store *Store, version int, scanners ...Scanner) *Reconciler {
	return &Reconciler{store: store, version: version, scanners: append([]Scanner(nil), scanners...)}
}

func (r *Reconciler) Status(ctx context.Context) (Status, error) {
	return r.store.Status(ctx)
}

func (r *Reconciler) Step(ctx context.Context, now time.Time) (StepResult, error) {
	fingerprint, scanners, err := r.scannerSet()
	if err != nil {
		return StepResult{}, err
	}
	active, err := r.store.ActiveRun(ctx)
	if err != nil {
		return StepResult{}, err
	}
	started := false
	if active == nil {
		lastCompleted, err := r.store.LastCompletedAt(ctx)
		if err != nil {
			return StepResult{}, err
		}
		if lastCompleted != nil && now.Before(lastCompleted.Add(ReconcileInterval)) {
			return StepResult{Idle: true}, nil
		}
		started = true
	}
	run, err := r.store.beginOrResume(ctx, r.version, fingerprint, now, false)
	if err != nil {
		return StepResult{}, err
	}
	// BeginOrResume may abandon an incompatible active run and start a clean
	// generation, which is a new run even when one was active on entry.
	if active != nil && active.ID != run.ID {
		started = true
	}

	units, ready, err := r.store.LoadManifest(ctx, run.ID)
	if err != nil {
		return StepResult{RunID: run.ID, Started: started}, err
	}
	if !ready {
		seen := make(map[string]struct{})
		for scannerID, scanner := range scanners {
			discovered, err := scanner.WorkUnits(ctx, run.ID)
			if err != nil {
				_ = r.store.RecordError(context.WithoutCancel(ctx), run.ID, err.Error(), now)
				return StepResult{RunID: run.ID, Started: started}, fmt.Errorf("discover storage work: %w", err)
			}
			for _, unit := range discovered {
				if unit.Key == "" {
					return StepResult{}, fmt.Errorf("invalid empty storage work key")
				}
				if _, duplicate := seen[unit.Key]; duplicate {
					return StepResult{}, fmt.Errorf("duplicate storage work unit %q", unit.Key)
				}
				seen[unit.Key] = struct{}{}
				unit.Scanner = scannerID
				units = append(units, unit)
			}
		}
		sort.Slice(units, func(i, j int) bool { return units[i].Key < units[j].Key })
		if err := r.store.SaveManifest(ctx, run.ID, units); err != nil {
			return StepResult{}, err
		}
	}

	next := -1
	for i := range units {
		if units[i].Key > run.Cursor {
			next = i
			break
		}
	}
	if next >= 0 {
		unit := units[next]
		scanner := scanners[unit.Scanner]
		if scanner == nil {
			return StepResult{}, fmt.Errorf("storage scanner %q is unavailable", unit.Scanner)
		}
		measurements, err := scanner.Scan(ctx, run.ID, unit)
		if err != nil {
			_ = r.store.RecordError(context.WithoutCancel(ctx), run.ID, err.Error(), now)
			return StepResult{RunID: run.ID, Started: started, Pending: len(units) - next}, fmt.Errorf("scan storage work %s: %w", unit.Key, err)
		}
		if err := r.store.ReplaceMeasurements(ctx, run.ID, unit.Key, measurements); err != nil {
			return StepResult{RunID: run.ID, Started: started}, err
		}
		if err := r.store.AdvanceCursor(ctx, run.ID, unit.Key, now); err != nil {
			return StepResult{RunID: run.ID, Started: started}, err
		}
		return StepResult{
			RunID: run.ID, Started: started, ProcessedKey: unit.Key,
			Pending: len(units) - next - 1,
		}, nil
	}

	if err := r.store.Publish(ctx, run.ID, now); err != nil {
		_ = r.store.RecordError(context.WithoutCancel(ctx), run.ID, err.Error(), now)
		return StepResult{RunID: run.ID, Started: started}, err
	}
	return StepResult{RunID: run.ID, Started: started, Published: true}, nil
}

func (r *Reconciler) scannerSet() (string, map[string]Scanner, error) {
	ids := make([]string, 0, len(r.scanners))
	byID := make(map[string]Scanner, len(r.scanners))
	for _, scanner := range r.scanners {
		id := scanner.Fingerprint()
		if id == "" {
			return "", nil, fmt.Errorf("storage scanner fingerprint is empty")
		}
		if _, duplicate := byID[id]; duplicate {
			return "", nil, fmt.Errorf("duplicate storage scanner fingerprint %q", id)
		}
		byID[id] = scanner
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	return fmt.Sprintf("%x", sum[:]), byID, nil
}
