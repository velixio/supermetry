package updaterdaemon

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"supermetry/internal/updatehelper"
)

func TestControllerStartIsDurableIdempotentAndRejectsNonceReplay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	backend := &blockingBackend{entered: make(chan struct{}), release: make(chan struct{})}
	controller := NewController(Config{
		JobsDir: dir,
		BackendFactory: func(context.Context, updatehelper.Job) (updatehelper.Backend, error) {
			return backend, nil
		},
	})
	nonce := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request := updatehelper.Request{Version: 1, Operation: updatehelper.OperationStart, JobID: "42", TargetVersion: "v1.2.3", Nonce: nonce}

	first := controller.Handle(context.Background(), request)
	if first.ErrorCode != "" || first.State != updatehelper.StateRequested {
		t.Fatalf("first response = %+v", first)
	}
	select {
	case <-backend.entered:
	case <-time.After(time.Second):
		t.Fatal("update did not start")
	}
	second := controller.Handle(context.Background(), request)
	if second.ErrorCode != "" || second.JobID != "42" {
		t.Fatalf("idempotent response = %+v", second)
	}

	replayed := request
	replayed.JobID = "43"
	replayed.TargetVersion = "v1.2.4"
	if got := controller.Handle(context.Background(), replayed); got.ErrorCode != "nonce_reused" {
		t.Fatalf("replayed nonce error = %q", got.ErrorCode)
	}
	close(backend.release)
	controller.Wait()
}

func TestControllerEnforcesOneActiveJobAndReturnsStatus(t *testing.T) {
	t.Parallel()
	backend := &blockingBackend{entered: make(chan struct{}), release: make(chan struct{})}
	controller := NewController(Config{
		JobsDir: t.TempDir(),
		BackendFactory: func(context.Context, updatehelper.Job) (updatehelper.Backend, error) {
			return backend, nil
		},
	})
	first := startRequest("10", "v1.2.3", 1)
	controller.Handle(context.Background(), first)
	select {
	case <-backend.entered:
	case <-time.After(time.Second):
		t.Fatal("update did not start")
	}
	if got := controller.Handle(context.Background(), startRequest("11", "v1.2.4", 2)); got.ErrorCode != "active_update" {
		t.Fatalf("second start = %+v", got)
	}
	status := controller.Handle(context.Background(), updatehelper.Request{Version: 1, Operation: updatehelper.OperationStatus, JobID: "10"})
	if status.JobID != "10" || status.ErrorCode != "" {
		t.Fatalf("status = %+v", status)
	}
	missing := controller.Handle(context.Background(), updatehelper.Request{Version: 1, Operation: updatehelper.OperationStatus, JobID: "999"})
	if missing.ErrorCode != "job_not_found" {
		t.Fatalf("missing status = %+v", missing)
	}
	close(backend.release)
	controller.Wait()
}

func TestControllerRecoversPersistedJobAfterRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := updatehelper.NewFileStore(dir)
	job := updatehelper.Job{ID: "7", TargetVersion: "v2.0.0", State: updatehelper.StateRequested}
	if err := store.Save(job); err != nil {
		t.Fatal(err)
	}
	backend := &recordingBackend{}
	controller := NewController(Config{
		JobsDir: dir,
		BackendFactory: func(context.Context, updatehelper.Job) (updatehelper.Backend, error) {
			return backend, nil
		},
	})
	if err := controller.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	controller.Wait()
	got, err := store.Load("7")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != updatehelper.StateComplete {
		t.Fatalf("state = %q", got.State)
	}
}

func TestControllerRequiresOperatorWhenBackendCannotRecoverStoppedJob(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := updatehelper.NewFileStore(dir)
	job := updatehelper.Job{ID: "9", TargetVersion: "v2.0.0", State: updatehelper.StateStopping}
	if err := store.Save(job); err != nil {
		t.Fatal(err)
	}
	controller := NewController(Config{
		JobsDir: dir,
		BackendFactory: func(context.Context, updatehelper.Job) (updatehelper.Backend, error) {
			return nil, errors.New("manifest unavailable")
		},
	})
	if err := controller.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	controller.Wait()
	got, err := store.Load("9")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != updatehelper.StateRecoveryRequired || got.ErrorCode != "backend_initialization_failed" {
		t.Fatalf("job = %+v", got)
	}
}

func startRequest(jobID, target string, fill byte) updatehelper.Request {
	return updatehelper.Request{
		Version: 1, Operation: updatehelper.OperationStart, JobID: jobID, TargetVersion: target,
		Nonce: base64.RawURLEncoding.EncodeToString(bytesOf(fill, 32)),
	}
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

type blockingBackend struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingBackend) RunStep(ctx context.Context, step updatehelper.State, _ updatehelper.Job) error {
	if step != updatehelper.StatePreflight {
		return nil
	}
	select {
	case <-b.entered:
	default:
		close(b.entered)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
		return nil
	}
}

func (*blockingBackend) Recover(context.Context, updatehelper.RecoveryAction, updatehelper.Job) error {
	return nil
}

type recordingBackend struct{}

func (*recordingBackend) RunStep(context.Context, updatehelper.State, updatehelper.Job) error {
	return nil
}
func (*recordingBackend) Recover(context.Context, updatehelper.RecoveryAction, updatehelper.Job) error {
	return errors.New("unexpected recovery")
}
