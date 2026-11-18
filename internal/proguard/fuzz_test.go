package proguard

import (
	"bytes"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func FuzzParse(f *testing.F) {
	if b, err := os.ReadFile(fixtureMapping); err == nil {
		f.Add(b)
	}
	f.Add([]byte("a.A -> b:\n    1:2:void m():3:4 -> c\n"))
	f.Add([]byte("a -> b:\n    1:1:void x.y.z():1 -> c\n    -> \n:::\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		var out bytes.Buffer
		if err := BuildIndexIn(bytes.NewReader(data), &out, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		ix, err := OpenIndex(bytes.NewReader(out.Bytes()), int64(out.Len()))
		if err != nil {
			t.Fatal(err)
		}
		ix.Remap("b", "c", 3)
		ix.Remap("a.b.c.e", "a", 17)
	})
}

func FuzzOpenIndex(f *testing.F) {
	var out bytes.Buffer
	_ = BuildIndexIn(strings.NewReader("a.A -> b:\n    1:2:void m():3:4 -> c\n"), &out, f.TempDir())
	f.Add(out.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		ix, err := OpenIndex(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		ix.Remap("b", "c", 1)
		ix.Class("b")
	})
}

// genReader yields a synthetic mapping of n member lines without holding it.
type genReader struct {
	n, i    int
	pending []byte
}

func (g *genReader) Read(p []byte) (int, error) {
	for len(g.pending) == 0 {
		if g.i >= g.n {
			return 0, io.EOF
		}
		var sb strings.Builder
		for k := 0; k < 64 && g.i < g.n; k++ {
			if g.i%10 == 0 {
				c := strconv.Itoa(g.i / 10)
				sb.WriteString("com.example.pkg.Class" + c + " -> a.b" + c + ":\n")
			}
			m := strconv.Itoa(g.i % 10)
			sb.WriteString("    " + strconv.Itoa(g.i%97+1) + ":" + strconv.Itoa(g.i%97+3) + ":void method" + m + "(int):" + strconv.Itoa(g.i%500+10) + " -> m" + m + "\n")
			g.i++
		}
		g.pending = []byte(sb.String())
	}
	n := copy(p, g.pending)
	g.pending = g.pending[n:]
	return n, nil
}

func TestBuildIndexHugeMappingBoundedHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("10M-line mapping; skipped with -short")
	}
	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > peak.Load() {
				peak.Store(ms.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()
	runtime.GC()
	out, err := os.CreateTemp(t.TempDir(), "idx")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	start := time.Now()
	if err := BuildIndexIn(&genReader{n: 10_000_000}, out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	close(stop)
	<-done
	st, _ := out.Stat()
	t.Logf("built %d byte index from 10M lines in %v, peak HeapInuse %d MiB", st.Size(), time.Since(start), peak.Load()>>20)
	if peak.Load() > 128<<20 {
		t.Fatalf("peak heap %d MiB exceeds 128 MiB", peak.Load()>>20)
	}
	ix, err := OpenIndex(out, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if fr := ix.Remap("a.b777", "m3", 40); len(fr) != 1 || fr[0].Class != "com.example.pkg.Class777" || fr[0].Method != "method3" {
		t.Fatalf("lookup in the big index: %+v", fr)
	}
}
