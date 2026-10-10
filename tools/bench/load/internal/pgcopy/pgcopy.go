// Package pgcopy streams text-format COPY data to Postgres over several
// connections. Throwaway benchmark code.
package pgcopy

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

// Buf is a batch of COPY lines.
type Buf struct {
	bytes.Buffer
	Rows int
}

// Begin starts a row.
func (b *Buf) Begin() *Buf { b.Rows++; return b }

func (b *Buf) sep(first bool) {
	if !first {
		b.WriteByte('\t')
	}
}

// Str appends an escaped text field (first=true for the first column).
func (b *Buf) Str(first bool, s string) *Buf {
	b.sep(first)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteByte(c)
		}
	}
	return b
}

// StrN appends s, or NULL when s is empty.
func (b *Buf) StrN(first bool, s string) *Buf {
	if s == "" {
		return b.Null(first)
	}
	return b.Str(first, s)
}

// Null appends a NULL field.
func (b *Buf) Null(first bool) *Buf { b.sep(first); b.WriteString(`\N`); return b }

// Int appends an integer field.
func (b *Buf) Int(first bool, v int64) *Buf {
	b.sep(first)
	var tmp [24]byte
	b.Write(strconv.AppendInt(tmp[:0], v, 10))
	return b
}

// End finishes a row.
func (b *Buf) End() { b.WriteByte('\n') }

// Writer fans batches out to N COPY connections for one table.
type Writer struct {
	table, cols string
	ch          chan *Buf
	wg          sync.WaitGroup
	errMu       sync.Mutex
	err         error
	Rows        atomic.Int64
	pool        sync.Pool
}

// NewWriter opens n connections and starts COPY loops. Each batch is one COPY
// statement (its own transaction).
func NewWriter(ctx context.Context, dsn, table, cols string, n int) (*Writer, error) {
	w := &Writer{table: table, cols: cols, ch: make(chan *Buf, n*2)}
	w.pool.New = func() any { return &Buf{} }
	for range n {
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			return nil, err
		}
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			defer conn.Close(context.Background())
			sql := fmt.Sprintf("COPY %s (%s) FROM STDIN", table, cols)
			for b := range w.ch {
				if w.Err() == nil {
					if _, err := conn.PgConn().CopyFrom(ctx, bytes.NewReader(b.Bytes()), sql); err != nil {
						w.setErr(fmt.Errorf("copy %s: %w", table, err))
					} else {
						w.Rows.Add(int64(b.Rows))
					}
				}
				b.Reset()
				b.Rows = 0
				w.pool.Put(b)
			}
		}()
	}
	return w, nil
}

// Get returns an empty batch buffer.
func (w *Writer) Get() *Buf { return w.pool.Get().(*Buf) }

// Send queues a batch (blocks when all connections are busy: backpressure).
func (w *Writer) Send(b *Buf) {
	if b.Rows == 0 {
		w.pool.Put(b)
		return
	}
	w.ch <- b
}

func (w *Writer) setErr(err error) {
	w.errMu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.errMu.Unlock()
}

// Err returns the first COPY error.
func (w *Writer) Err() error { w.errMu.Lock(); defer w.errMu.Unlock(); return w.err }

// Close flushes and waits.
func (w *Writer) Close() error { close(w.ch); w.wg.Wait(); return w.Err() }
