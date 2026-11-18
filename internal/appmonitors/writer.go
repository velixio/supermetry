package appmonitors

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"supermetry/internal/health"
)

const (
	defaultQueue    = 4096
	defaultRetry    = time.Second
	shutdownTimeout = 15 * time.Second
)

// Stats counts named events (sentryingest.Stats satisfies it).
type Stats interface {
	Add(category string, n uint64)
}

// Recorder stores one check-in; Store implements it, tests replace it.
type Recorder interface {
	Record(ctx context.Context, ci CheckIn) error
}

// Writer takes check-ins from the ingest handler and records them on one
// goroutine, in order. Enqueue never blocks and never touches the database: a
// full queue drops the check-in and counts checkin_queue_full.
type Writer struct {
	rec   Recorder
	stats Stats
	ch    chan CheckIn
	retry time.Duration

	closeMu sync.RWMutex
	closed  bool
	stop    chan struct{}
	done    chan struct{}
}

// NewWriter starts a writer with a queue of queueSize (4,096 when <= 0).
func NewWriter(rec Recorder, queueSize int, stats Stats) *Writer {
	if queueSize <= 0 {
		queueSize = defaultQueue
	}
	w := &Writer{rec: rec, stats: stats, ch: make(chan CheckIn, queueSize), retry: defaultRetry,
		stop: make(chan struct{}), done: make(chan struct{})}
	go w.loop()
	return w
}

func (w *Writer) count(category string) {
	if w.stats != nil {
		w.stats.Add(category, 1)
	}
}

// Enqueue queues a check-in; false means it was dropped.
func (w *Writer) Enqueue(ci CheckIn) bool {
	w.closeMu.RLock()
	defer w.closeMu.RUnlock()
	if w.closed {
		return false
	}
	select {
	case w.ch <- ci:
		return true
	default:
		w.count("checkin_queue_full")
		return false
	}
}

// Close stops accepting check-ins, records what is queued and waits. It is
// safe to call more than once.
func (w *Writer) Close() {
	w.closeMu.Lock()
	if w.closed {
		w.closeMu.Unlock()
		<-w.done
		return
	}
	w.closed = true
	w.closeMu.Unlock()
	close(w.stop)
	<-w.done
}

func (w *Writer) loop() {
	defer close(w.done)
	for {
		select {
		case ci := <-w.ch:
			w.record(context.Background(), ci)
		case <-w.stop:
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			for {
				select {
				case ci := <-w.ch:
					w.record(ctx, ci)
					continue
				default:
				}
				return
			}
		}
	}
}

// record writes one check-in, retrying once after a database failure.
// Replaying is safe: a repeated check_in_id is ignored or completes its row.
func (w *Writer) record(ctx context.Context, ci CheckIn) {
	err := w.rec.Record(ctx, ci)
	if isPermanent(err) {
		w.permanent(err)
		return
	}
	if err != nil {
		slog.Warn("check-in write failed, retrying once", "error", err)
		timer := time.NewTimer(w.retry)
		select {
		case <-timer.C:
			err = w.rec.Record(ctx, ci)
		case <-ctx.Done():
			timer.Stop()
		}
		if isPermanent(err) {
			w.permanent(err)
			return
		}
	}
	if err != nil {
		health.AppIngestWriteFailed.Add(1)
		w.count("checkin_write_failed")
		slog.Error("check-in write failed", "error", err)
	}
}

func isPermanent(err error) bool {
	return errors.Is(err, ErrUnknownMonitor) || errors.Is(err, ErrInvalidConfig) || errors.Is(err, ErrMonitorLimit)
}

func (w *Writer) permanent(err error) {
	switch {
	case errors.Is(err, ErrUnknownMonitor):
		w.count("checkin_unknown_monitor")
	case errors.Is(err, ErrMonitorLimit):
		w.count("checkin_monitor_limit")
	default:
		w.count("checkin_invalid_config")
	}
}
