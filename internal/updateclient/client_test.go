package updateclient

import (
	"context"
	"net"
	"testing"
	"time"

	"supermetry/internal/updatehelper"
)

func TestClientStartUsesNarrowFramedProtocol(t *testing.T) {
	server, application := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = application.Close() })
	want := updatehelper.Request{Version: 1, JobID: "42", TargetVersion: "v1.2.0", Nonce: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"}
	dialed := false
	client := New(func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return application, nil
	})
	done := make(chan error, 1)
	go func() {
		request, err := updatehelper.ReadRequest(server)
		if err == nil && request != want {
			t.Errorf("request = %+v, want %+v", request, want)
		}
		if err == nil {
			err = updatehelper.WriteResponse(server, updatehelper.Response{Version: 1, JobID: "42", State: updatehelper.StateRequested})
		}
		done <- err
	}()
	response, err := client.Start(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !dialed || response.JobID != "42" || response.State != updatehelper.StateRequested {
		t.Fatalf("response = %+v, dialed=%v", response, dialed)
	}
}

func TestClientStartHonorsContextAndRejectsMismatchedJob(t *testing.T) {
	request := updatehelper.Request{Version: 1, JobID: "7", TargetVersion: "v1.2.0", Nonce: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"}
	t.Run("cancelled dial", func(t *testing.T) {
		client := New(func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := client.Start(ctx, request); err == nil {
			t.Fatal("cancelled request succeeded")
		}
	})
	t.Run("mismatched response", func(t *testing.T) {
		server, application := net.Pipe()
		defer server.Close()
		defer application.Close()
		client := New(func(context.Context, string, string) (net.Conn, error) { return application, nil })
		go func() {
			_, _ = updatehelper.ReadRequest(server)
			_ = updatehelper.WriteResponse(server, updatehelper.Response{Version: 1, JobID: "8", State: updatehelper.StateRequested})
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := client.Start(ctx, request); err == nil {
			t.Fatal("mismatched job response succeeded")
		}
	})
}
