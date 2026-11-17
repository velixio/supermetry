package sentryapi

import (
	"context"
	"log/slog"
	"sync"
)

// Start launches the assembly workers and re-queues bundles that a restart
// interrupted. Close stops them.
func (h *Handler) Start(ctx context.Context) {
	h.ctx, h.stop = context.WithCancel(ctx)
	var wg sync.WaitGroup
	for i := 0; i < assembleWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-h.ctx.Done():
					return
				case id := <-h.queue:
					h.assembleBundle(h.ctx, id)
				}
			}
		}()
	}
	go func() { wg.Wait(); close(h.done) }()
	rows, err := h.deps.Pool.Query(h.ctx, `select id from app_artifact_bundles where state in ('created','assembling') order by id limit $1`, assembleQueue)
	if err != nil {
		slog.Error("sentryapi: recover bundles", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			select {
			case h.queue <- id:
			default:
			}
		}
	}
}

// Close stops the workers; a bundle being assembled stays in its state and is
// picked up again on the next start.
func (h *Handler) Close() {
	if h.stop == nil {
		return
	}
	h.stop()
	<-h.done
}
