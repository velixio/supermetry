package writer

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supermetry/internal/db"
	"supermetry/internal/session"
	"supermetry/internal/testpg"
)

func TestEventPreflightCapacityAndFinalEnqueue(t *testing.T) {
	for _, capacity := range []int{0, 2} {
		t.Run(map[int]string{0: "zero", 2: "positive"}[capacity], func(t *testing.T) {
			w := New(nil, nil, capacity, time.Hour, time.Hour)
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
	idle := New(nil, nil, 1, time.Hour, time.Hour)
	idle.stopAccepting()
	if !idle.RejectEventIfUnavailable() {
		t.Fatal("empty stopped writer accepted preflight")
	}
	w := New(nil, nil, 1, time.Hour, time.Hour)
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

func TestEventReservationExcludesCompetitorsAndReleasesExactlyOnce(t *testing.T) {
	w := New(nil, nil, 1, time.Hour, time.Hour)
	before := Dropped.Load()

	reservation, ok := w.ReserveEvent()
	if !ok {
		t.Fatal("empty writer rejected reservation")
	}
	if w.Enqueue(Event{}) {
		t.Fatal("competing enqueue took reserved capacity")
	}
	if Dropped.Load() != before+1 {
		t.Fatal("competing rejection was not counted")
	}

	reservation.Cancel()
	reservation.Cancel()
	if !w.Enqueue(Event{SessionID: 7}) {
		t.Fatal("cancel did not restore capacity")
	}
	if event := <-w.ch; event.SessionID != 7 {
		t.Fatalf("queued session=%d, want 7", event.SessionID)
	}
	<-w.slots

	reservation, ok = w.ReserveEvent()
	if !ok || !reservation.Commit(Event{SessionID: 9}) {
		t.Fatal("reservation commit failed")
	}
	if reservation.Commit(Event{}) {
		t.Fatal("reservation committed twice")
	}
	reservation.Cancel()
	if event := <-w.ch; event.SessionID != 9 {
		t.Fatalf("committed session=%d, want 9", event.SessionID)
	}
	<-w.slots
}

func TestEventReservationDoesNotHoldStateLock(t *testing.T) {
	w := New(nil, nil, 1, time.Hour, time.Hour)
	reservation, ok := w.ReserveEvent()
	if !ok {
		t.Fatal("reserve")
	}
	defer reservation.Cancel()
	if !w.stateMu.TryLock() {
		t.Fatal("reservation held state lock during enrichment")
	}
	w.stateMu.Unlock()
}

func TestShutdownWaitsForSlowReservationAndFlushesCommit(t *testing.T) {
	pool, _ := testpg.New(t)
	dbCtx := context.Background()
	if err := db.Migrate(dbCtx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(dbCtx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	store := &observedSessionStore{
		Memory: session.NewMemory(1, wallClock{}),
		dirty:  make(chan struct{}),
	}
	w := New(pool, store, 2, time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- w.Run(ctx) }()

	slow, ok := w.ReserveEvent()
	if !ok {
		t.Fatal("reserve slow enrichment")
	}
	secondReady := make(chan *EventReservation, 1)
	go func() {
		reservation, _ := w.ReserveEvent()
		secondReady <- reservation
	}()
	var second *EventReservation
	select {
	case second = <-secondReady:
		if second == nil {
			t.Fatal("concurrent reservation rejected despite available capacity")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent reservation serialized behind slow enrichment")
	}
	second.Cancel()

	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		w.stateMu.RLock()
		accepting := w.accepting
		w.stateMu.RUnlock()
		if !accepting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not stop admission while reservation was unresolved")
		}
		runtime.Gosched()
	}
	select {
	case err := <-stopped:
		t.Fatalf("shutdown passed unresolved reservation: %v", err)
	default:
	}
	select {
	case <-store.dirty:
		t.Fatal("session shutdown flushed before slow reservation resolved")
	default:
	}
	hit := session.Hit{
		SiteID: 1, Retention: db.Ret30, TS: day.Add(12 * time.Hour), LocalDay: day,
		VisitorID: 7, Kind: 0, PathID: 1,
	}
	result := store.Touch(hit)
	if !slow.Commit(Event{Hit: hit, SessionID: result.SessionID}) {
		t.Fatal("commit slow reservation")
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not stop")
	}
	var events, sessions int
	if err := pool.QueryRow(dbCtx, `select (select count(*) from events), (select count(*) from sessions)`).Scan(&events, &sessions); err != nil {
		t.Fatal(err)
	}
	if events != 1 || sessions != 1 {
		t.Fatalf("shutdown flushed events/sessions = %d/%d, want 1/1", events, sessions)
	}
}

type observedSessionStore struct {
	*session.Memory
	dirtyOnce sync.Once
	dirty     chan struct{}
}

func (s *observedSessionStore) Dirty(max int) []session.Snapshot {
	s.dirtyOnce.Do(func() { close(s.dirty) })
	return s.Memory.Dirty(max)
}

func TestOutOfOrderHitAcrossFlushKeepsOneSessionRow(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, day, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}

	sessions := session.NewMemory(1, wallClock{})
	w := New(pool, sessions, 1, time.Hour, time.Hour)
	createdAt := day.Add(12*time.Hour + 50*time.Millisecond)
	first := sessions.Touch(session.Hit{
		SiteID: 1, Retention: db.Ret30, TS: createdAt, LocalDay: day,
		VisitorID: 7, Kind: 0, PathID: 1,
	})
	if err := w.upsertSessions(ctx, sessions.Dirty(1)); err != nil {
		t.Fatal(err)
	}
	sessions.Touch(session.Hit{
		SiteID: 1, Retention: db.Ret30, TS: day.Add(12 * time.Hour), LocalDay: day,
		VisitorID: 7, Kind: 0, PathID: 2,
	})
	if err := w.upsertSessions(ctx, sessions.Dirty(1)); err != nil {
		t.Fatal(err)
	}

	var rows, visits, pageviews int
	var start time.Time
	if err := pool.QueryRow(ctx, `
		select count(*), count(distinct session_id), sum(pageviews), min(start)
		from sessions where site_id=$1 and session_id=$2`, 1, first.SessionID).
		Scan(&rows, &visits, &pageviews, &start); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || visits != 1 || pageviews != 2 || !start.Equal(createdAt) {
		t.Fatalf("rows=%d visits=%d pageviews=%d start=%s, want 1/1/2/%s", rows, visits, pageviews, start, createdAt)
	}
}

func TestEventPreflightTracksInFlightRetryAndRecovery(t *testing.T) {
	w := New(nil, nil, 1, time.Millisecond, time.Hour)
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
	w := New(nil, nil, 64, time.Hour, time.Hour)
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
