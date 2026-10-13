package updaterdaemon

import (
	"context"
	"net"
	"testing"
	"time"

	"supermetry/internal/updatehelper"
)

func TestServerHandlesStrictStatusRequest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := updatehelper.NewFileStore(dir)
	if err := store.Save(updatehelper.Job{ID: "8", TargetVersion: "v1.2.3", State: updatehelper.StateComplete}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := Server{Listener: listener, Controller: NewController(Config{JobsDir: dir, BackendFactory: func(context.Context, updatehelper.Job) (updatehelper.Backend, error) {
		return &recordingBackend{}, nil
	}}), ConnectionTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	request := updatehelper.Request{Version: 1, Operation: updatehelper.OperationStatus, JobID: "8"}
	if err := updatehelper.WriteRequest(connection, request); err != nil {
		t.Fatal(err)
	}
	response, err := updatehelper.ReadResponse(connection)
	if err != nil {
		t.Fatal(err)
	}
	if response.State != updatehelper.StateComplete || response.JobID != "8" {
		t.Fatalf("response = %+v", response)
	}
	_ = connection.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
