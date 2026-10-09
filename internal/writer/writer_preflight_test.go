package writer

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventPreflightCapacityAndFinalEnqueue(t *testing.T) {
	for _, capacity := range []int{0, 2} {
		t.Run(map[int]string{0: "zero", 2: "positive"}[capacity], func(t *testing.T) {
			w := New(nil, nil, capacity, time.Hour, time.Hour, nil)
			before := Dropped.Load()
			for range capacity {
				if w.RejectEventIfUnavailable() || w.RejectEventIfUnavailable() {
					t.Fatal("available capacity rejected")
				}
				if !w.Enqueue(Event{}) {
					t.Fatal("preflight reserved capacity")
				}
			}
			if !w.RejectEventIfUnavailable() || Dropped.Load() != before+1 {
				t.Fatal("full preflight must count one drop")
			}
			if len(w.ch) != capacity || len(w.slots) != capacity {
				t.Fatal("preflight changed queued capacity")
			}
		})
	}
	idle := New(nil, nil, 1, time.Hour, time.Hour, nil)
	idle.stopAccepting()
	if !idle.RejectEventIfUnavailable() {
		t.Fatal("empty stopped writer accepted preflight")
	}
	w := New(nil, nil, 1, time.Hour, time.Hour, nil)
	before := Dropped.Load()
	if w.RejectEventIfUnavailable() {
		t.Fatal("empty queue rejected")
	}
	// Another producer fills the queue between the hint and final enqueue.
	if !w.Enqueue(Event{}) || w.Enqueue(Event{}) {
		t.Fatal("final enqueue did not enforce capacity")
	}
	if Dropped.Load() != before+1 {
		t.Fatal("final rejection must count one drop")
	}
	w.stopAccepting()
	if !w.RejectEventIfUnavailable() || Dropped.Load() != before+2 {
		t.Fatal("shutdown must count one early drop")
	}
}

func TestEventPreflightTracksInFlightRetryAndRecovery(t *testing.T) {
	w := New(nil, nil, 1, time.Millisecond, time.Hour, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, retry := make(chan struct{}), make(chan struct{})
	fail, commit := make(chan struct{}), make(chan struct{})
	var attempts atomic.Int32
	done := make(chan error, 1)
	beforeDropped, beforeWritten := Dropped.Load(), Written.Load()
	if !w.Enqueue(Event{}) {
		t.Fatal("initial enqueue")
	}
	go func() {
		done <- w.runEvents(ctx, func(context.Context, []Event) error {
			if attempts.Add(1) == 1 {
				close(first)
				select {
				case <-fail:
					return errors.New("synthetic retry")
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			close(retry)
			select {
			case <-commit:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	awaitSignal(t, first)
	if len(w.ch) != 0 || len(w.slots) != 1 || !w.RejectEventIfUnavailable() {
		t.Fatal("in-flight event capacity lost")
	}
	close(fail)
	awaitSignal(t, retry)
	if len(w.ch) != 0 || !w.RejectEventIfUnavailable() {
		t.Fatal("retrying event capacity lost")
	}
	close(commit)
	deadline := time.Now().Add(3 * time.Second)
	for len(w.slots) != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(w.slots) != 0 || w.RejectEventIfUnavailable() {
		t.Fatal("committed capacity did not recover")
	}
	if Dropped.Load() != beforeDropped+2 || Written.Load() != beforeWritten+1 {
		t.Fatal("retry/recovery counters differ")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not stop")
	}
	if !w.RejectEventIfUnavailable() || Dropped.Load() != beforeDropped+3 {
		t.Fatal("stopped writer accepted preflight")
	}
}

func TestEventPreflightConcurrentEnqueueAndShutdown(t *testing.T) {
	w := New(nil, nil, 64, time.Hour, time.Hour, nil)
	before := Dropped.Load()
	var accepted, rejected atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 1000 {
				if w.RejectEventIfUnavailable() {
					rejected.Add(1)
					continue
				}
				if w.Enqueue(Event{}) {
					accepted.Add(1)
				} else {
					rejected.Add(1)
				}
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); <-start; w.stopAccepting() }()
	close(start)
	wg.Wait()
	if accepted.Load()+rejected.Load() != 16000 || accepted.Load() != int64(len(w.ch)) || len(w.slots) != len(w.ch) {
		t.Fatal("concurrent capacity accounting differs")
	}
	if Dropped.Load()-before != rejected.Load() {
		t.Fatalf("drops=%d rejected=%d", Dropped.Load()-before, rejected.Load())
	}
	if !w.RejectEventIfUnavailable() || w.Enqueue(Event{}) {
		t.Fatal("post-shutdown request accepted")
	}
	if Dropped.Load()-before != rejected.Load()+2 {
		t.Fatal("post-shutdown drops differ")
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("writer callback did not run")
	}
}
