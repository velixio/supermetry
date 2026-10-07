package writer

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunEventsDoesNotReplayInFlightBatchAtShutdown(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	checkContext := make(chan struct{})
	finish := make(chan struct{})
	var writes atomic.Int32

	w := New(nil, nil, batchSize, time.Hour, time.Hour, nil)
	for range batchSize {
		if !w.Enqueue(Event{}) {
			t.Fatal("batch event rejected")
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- w.runEvents(runCtx, func(ctx context.Context, _ []Event) error {
			if writes.Add(1) == 1 {
				close(started)
				<-checkContext
				if err := ctx.Err(); err != nil {
					return err
				}
				<-finish
			}
			return nil
		})
	}()

	<-started
	cancel()
	close(checkContext)
	close(finish)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run events: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not stop")
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("writes = %d, want 1", got)
	}
}
