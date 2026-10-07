package synth

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Pipeline:
//
//	units (site, local day) --gen workers--> sorted per-unit event arrays
//	--k-way merge by (ts, site) per 2h UTC window--> batches of refs
//	--format workers (optionally zstd per batch)--> ordered writer
//
// Units are generated up to lookahead ahead of the window being merged and
// released once the writer has passed their last window, which bounds memory
// to roughly 1.5 days of compact events.

const (
	windowMs    = 2 * hour
	lookaheadMs = 12 * hour
	batchSize   = 4096
)

// Stats summarizes a run.
type Stats struct {
	Events, Pageviews, Customs, Sessions, Visitors int64
	RawBytes, OutBytes                             int64
	Units                                          int
}

type ref struct {
	u *unit
	i int32
}

type result struct {
	buf []byte
	raw int64
}

type outItem struct {
	res     chan result
	release []*unit
}

type cursor struct {
	ts   int64
	site int
	u    *unit
	i    int32
	end  int32
}

func less(a, b *cursor) bool {
	if a.ts != b.ts {
		return a.ts < b.ts
	}
	return a.site < b.site
}

type cursorHeap []cursor

func (h cursorHeap) down(i int) {
	n := len(h)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		m := l
		if r := l + 1; r < n && less(&h[r], &h[l]) {
			m = r
		}
		if !less(&h[m], &h[i]) {
			return
		}
		h[i], h[m] = h[m], h[i]
		i = m
	}
}

// lowerBound returns the first event index with off >= target.
func lowerBound(ev []eventRec, target int64) int {
	if target <= 0 {
		return 0
	}
	lo, hi := 0, len(ev)
	for lo < hi {
		m := (lo + hi) / 2
		if int64(ev[m].off) < target {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// Run writes the event stream to out and truth.json to truth (if non-nil).
func (g *Generator) Run(out io.Writer, truth io.Writer) (Stats, error) {
	var st Stats
	nSites := len(g.sites)
	var units []*unit
	for d := 0; d < g.cfg.Days; d++ {
		for si, s := range g.sites {
			units = append(units, g.newUnit(s, si, d))
		}
	}
	slices.SortStableFunc(units, func(a, b *unit) int {
		if a.start != b.start {
			if a.start < b.start {
				return -1
			}
			return 1
		}
		return a.site.id - b.site.id
	})
	st.Units = len(units)
	minStart, maxEnd := units[0].start, units[0].end
	for _, u := range units {
		minStart, maxEnd = min(minStart, u.start), max(maxEnd, u.end)
	}

	// Truth writer (rows in (date, site) order as they complete).
	var tw *bufio.Writer
	var rows [][]byte
	nextRow := 0
	var truthErr error
	writeTruth := func(s string) {
		if truthErr == nil {
			_, truthErr = tw.WriteString(s)
		}
	}
	if truth != nil {
		tw = bufio.NewWriterSize(truth, 1<<20)
		rows = make([][]byte, len(units))
		writeTruth(fmt.Sprintf(`{"generator":"panel/bench/gen","tier":%q,"seed":%d,"start":%q,"days":%d,"events_per_day":%d,"sites":%d,`+
			`"dims":["path","country","source","browser","os","device","entry","exit"],"top_n":%d,"funnel_f_steps":["/","/pricing","signup"],"rows":[`+"\n",
			g.cfg.Tier, g.seed, g.cfg.Start.Format("2006-01-02"), g.cfg.Days, g.eventsPerDay, nSites, topN))
	}

	// Generation workers.
	tasks := make(chan *unit, len(units))
	var genWG sync.WaitGroup
	for w := 0; w < g.workers; w++ {
		genWG.Add(1)
		go func() {
			defer genWG.Done()
			for u := range tasks {
				g.generate(u, truth != nil)
				close(u.done)
			}
		}()
	}

	// Format workers and the ordered writer.
	var enc *zstd.Encoder
	if g.cfg.Zstd {
		var err error
		enc, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(g.workers))
		if err != nil {
			return st, err
		}
	}
	bufPool := sync.Pool{New: func() any { b := make([]byte, 0, batchSize*600); return &b }}
	refPool := sync.Pool{New: func() any { r := make([]ref, 0, batchSize); return &r }}
	type job struct {
		refs *[]ref
		res  chan result
	}
	jobs := make(chan job, 2*g.workers)
	order := make(chan outItem, 4*g.workers)
	var fmtWG sync.WaitGroup
	for w := 0; w < g.workers; w++ {
		fmtWG.Add(1)
		go func() {
			defer fmtWG.Done()
			f := formatter{g: g}
			for j := range jobs {
				bp := bufPool.Get().(*[]byte)
				b := (*bp)[:0]
				for _, r := range *j.refs {
					b = f.appendEvent(b, r.u, &r.u.events[r.i])
				}
				raw := int64(len(b))
				*j.refs = (*j.refs)[:0]
				refPool.Put(j.refs)
				if enc != nil {
					zp := bufPool.Get().(*[]byte)
					z := enc.EncodeAll(b, (*zp)[:0])
					*bp = b[:0]
					bufPool.Put(bp)
					b = z
				}
				j.res <- result{buf: b, raw: raw}
			}
		}()
	}
	var writeErr error
	writerDone := make(chan struct{})
	bw := bufio.NewWriterSize(out, 4<<20)
	go func() {
		defer close(writerDone)
		for it := range order {
			if it.res != nil {
				r := <-it.res
				if writeErr == nil {
					_, writeErr = bw.Write(r.buf)
				}
				st.RawBytes += r.raw
				st.OutBytes += int64(len(r.buf))
				b := r.buf[:0]
				bufPool.Put(&b)
			}
			for _, u := range it.release {
				u.visitors, u.sessions, u.events = nil, nil, nil
			}
		}
		if writeErr == nil {
			writeErr = bw.Flush()
		}
	}()

	cur := refPool.Get().(*[]ref)
	flushBatch := func() {
		if len(*cur) == 0 {
			return
		}
		res := make(chan result, 1)
		order <- outItem{res: res}
		jobs <- job{refs: cur, res: res}
		cur = refPool.Get().(*[]ref)
	}

	next, waited := 0, 0
	var active []*unit
	var merged int64
	heap := make(cursorHeap, 0, 2*nSites)
	w0 := minStart / windowMs * windowMs
	for wLo := w0; wLo < maxEnd; wLo += windowMs {
		wHi := wLo + windowMs
		for next < len(units) && units[next].start < wHi+lookaheadMs {
			tasks <- units[next]
			next++
		}
		for waited < len(units) && units[waited].start < wHi {
			u := units[waited]
			<-u.done
			st.Events += u.counts.events
			st.Pageviews += u.counts.pageviews
			st.Customs += u.counts.customs
			st.Sessions += u.counts.sessions
			st.Visitors += u.counts.visitors
			if rows != nil {
				rows[u.dateIdx*nSites+u.siteIdx] = u.row
				u.row = nil
				for nextRow < len(rows) && rows[nextRow] != nil {
					if nextRow > 0 {
						writeTruth(",\n")
					}
					if truthErr == nil {
						_, truthErr = tw.Write(rows[nextRow])
					}
					rows[nextRow] = []byte{} // written; keep non-nil
					nextRow++
				}
			}
			active = append(active, u)
			waited++
		}
		// Merge this window.
		heap = heap[:0]
		for _, u := range active {
			lo := lowerBound(u.events, wLo-u.start)
			hi := lowerBound(u.events, wHi-u.start)
			if lo < hi {
				heap = append(heap, cursor{ts: u.start + int64(u.events[lo].off), site: u.site.id, u: u, i: int32(lo), end: int32(hi)})
			}
		}
		for i := len(heap)/2 - 1; i >= 0; i-- {
			heap.down(i)
		}
		for len(heap) > 0 {
			c := &heap[0]
			*cur = append(*cur, ref{u: c.u, i: c.i})
			if len(*cur) == batchSize {
				flushBatch()
			}
			merged++
			c.i++
			if c.i < c.end {
				c.ts = c.u.start + int64(c.u.events[c.i].off)
			} else {
				heap[0] = heap[len(heap)-1]
				heap = heap[:len(heap)-1]
			}
			heap.down(0)
		}
		// Release units that end within this window.
		var keep, rel []*unit
		for _, u := range active {
			if u.end <= wHi {
				rel = append(rel, u)
			} else {
				keep = append(keep, u)
			}
		}
		active = keep
		if len(rel) > 0 {
			flushBatch()
			order <- outItem{release: rel}
		}
	}
	flushBatch()
	close(jobs)
	fmtWG.Wait()
	close(order)
	<-writerDone
	close(tasks)
	genWG.Wait()
	if enc != nil {
		enc.Close()
	}
	if merged != st.Events {
		return st, fmt.Errorf("internal: merged %d events, generated %d", merged, st.Events)
	}
	if writeErr != nil {
		return st, writeErr
	}
	if tw != nil {
		if nextRow != len(rows) {
			return st, fmt.Errorf("internal: wrote %d of %d truth rows", nextRow, len(rows))
		}
		writeTruth("\n],\"events\":" + strconv.FormatInt(st.Events, 10) + "}\n")
		if truthErr == nil {
			truthErr = tw.Flush()
		}
		if truthErr != nil {
			return st, truthErr
		}
	}
	return st, nil
}
