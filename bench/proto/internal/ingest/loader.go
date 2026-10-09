package ingest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"

	"supermetry/bench/proto/internal/core"
)

// ReadEvents parses NDJSON from r with parallel workers and calls sink for
// every event in input order (sessionization needs ts order). Parsing,
// hashing and enrichment run in parallel; sink runs on the caller's
// goroutine.
func ReadEvents(ctx context.Context, r io.Reader, b *Builder, workers int, sink func(*core.Event) error) (int64, error) {
	if workers <= 0 {
		workers = max(1, runtime.GOMAXPROCS(0)-2)
	}
	const chunkLines = 2048
	type result struct {
		evs  []*core.Event
		line int64
		err  error
	}
	type chunk struct {
		buf   []byte
		ends  []int
		first int64
		res   chan result
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan *chunk, workers)
	order := make(chan chan result, workers*2)
	var readErr error
	var wg sync.WaitGroup

	wg.Go(func() { // reader: owns work and order
		defer close(work)
		defer close(order)
		br := bufio.NewReaderSize(r, 1<<20)
		var line int64
		for {
			c := &chunk{first: line + 1, res: make(chan result, 1)}
			for len(c.ends) < chunkLines {
				l, err := br.ReadSlice('\n')
				if errors.Is(err, bufio.ErrBufferFull) {
					readErr = fmt.Errorf("line %d too long", line+1)
					return
				}
				if len(l) > 0 {
					line++
					c.buf = append(c.buf, l...)
					c.ends = append(c.ends, len(c.buf))
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					readErr = err
					return
				}
			}
			if len(c.ends) == 0 {
				return
			}
			select {
			case work <- c:
			case <-ctx.Done():
				return
			}
			select {
			case order <- c.res:
			case <-ctx.Done():
				return
			}
			if len(c.ends) < chunkLines {
				return // EOF
			}
		}
	})
	for range workers {
		wg.Go(func() {
			for c := range work {
				res := result{evs: make([]*core.Event, 0, len(c.ends))}
				start := 0
				for i, end := range c.ends {
					l := c.buf[start:end]
					start = end
					if len(l) <= 1 {
						continue
					}
					var rec core.Record
					if err := json.Unmarshal(l, &rec); err != nil {
						res.err, res.line = err, c.first+int64(i)
						break
					}
					ev, err := b.FromRecord(&rec)
					if errors.Is(err, ErrBot) {
						continue
					}
					if err != nil {
						res.err, res.line = err, c.first+int64(i)
						break
					}
					res.evs = append(res.evs, ev)
				}
				c.res <- res
			}
		})
	}

	var n int64
	var err error
	for rc := range order {
		var res result
		select {
		case res = <-rc:
		case <-ctx.Done():
		}
		if err != nil || ctx.Err() != nil {
			continue // drain so the reader can exit
		}
		if res.err != nil {
			err = fmt.Errorf("line %d: %w", res.line, res.err)
			cancel()
			continue
		}
		for _, ev := range res.evs {
			if e := sink(ev); e != nil {
				err = e
				cancel()
				break
			}
			n++
		}
	}
	wg.Wait()
	if err == nil {
		err = readErr
	}
	if err == nil {
		err = ctx.Err()
	}
	return n, err
}
