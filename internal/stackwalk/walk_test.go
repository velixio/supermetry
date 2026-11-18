package stackwalk

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"supermetry/internal/breakpad"
	"supermetry/internal/minidump"
	"supermetry/internal/minidump/minidumptest"
	"supermetry/internal/symcache"
)

const modBase = 0x400000

func words(w ...uint64) []byte {
	b := make([]byte, 8*len(w))
	for i, v := range w {
		binary.LittleEndian.PutUint64(b[i*8:], v)
	}
	return b
}

// dumpFor builds a one-thread dump with module app at 0x400000.
func dumpFor(t testing.TB, cpu string, ctx minidump.Context, stackStart uint64, stack []byte) (*minidump.Dump, *minidump.Thread) {
	t.Helper()
	b := &minidumptest.Builder{OS: "linux", CPU: cpu}
	b.AddModule("app", modBase, 0x10000, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	b.AddThread(7, ctx, stackStart, stack)
	b.SetException(7, 11, 1, 0)
	d, err := minidump.Parse(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return d, &d.Threads[0]
}

func amd64Ctx(rip, rsp, rbp uint64) minidump.Context {
	return minidump.Context{CPU: "amd64", PC: rip, SP: rsp, FP: rbp,
		Regs: map[string]uint64{"rip": rip, "rsp": rsp, "rbp": rbp}}
}

type fakeSyms struct {
	tab    *breakpad.CFITable
	reject map[uint64]bool
}

func (f *fakeSyms) CFI(*minidump.Module) (*breakpad.CFITable, bool) { return f.tab, f.tab != nil }
func (f *fakeSyms) Covers(_ *minidump.Module, rel uint64) bool       { return !f.reject[rel] }

func appCFI(t testing.TB) *breakpad.CFITable {
	t.Helper()
	data, err := os.ReadFile("../breakpad/testdata/app.sym")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := breakpad.Build(bytes.NewReader(data), &out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	c, err := symcache.Open(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	tab, err := breakpad.OpenCFI(c)
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

type want struct {
	rel   uint64
	trust string
}

func check(t *testing.T, got []Frame, exp ...want) {
	t.Helper()
	if len(got) != len(exp) {
		t.Fatalf("got %d frames %+v, want %d", len(got), got, len(exp))
	}
	for i, e := range exp {
		if got[i].Rel != e.rel || got[i].Trust != e.trust || got[i].Module == nil || got[i].Module.CodeFile != "app" {
			t.Errorf("frame %d = %+v, want rel %#x trust %s", i, got[i], e.rel, e.trust)
		}
	}
}

func TestWalkCFI(t *testing.T) {
	d, th := dumpFor(t, "amd64", amd64Ctx(0x400123, 0x7ff000, 0), 0x7ff000, words(0x400250, 0, 0))
	got := Walk(d, th, &fakeSyms{tab: appCFI(t)}, 256)
	check(t, got, want{0x123, "context"}, want{0x250, "cfi"})
}

func TestWalkFramePointer(t *testing.T) {
	stack := make([]byte, 0x300)
	put := func(addr, v uint64) { binary.LittleEndian.PutUint64(stack[addr-0x7ff000:], v) }
	put(0x7ff100, 0x7ff200)
	put(0x7ff108, 0x400250)
	put(0x7ff200, 0)
	put(0x7ff208, 0x400260)
	d, th := dumpFor(t, "amd64", amd64Ctx(0x400123, 0x7ff000, 0x7ff100), 0x7ff000, stack)
	got := Walk(d, th, nil, 256)
	check(t, got, want{0x123, "context"}, want{0x250, "fp"}, want{0x260, "fp"})
}

func TestWalkScan(t *testing.T) {
	d, th := dumpFor(t, "amd64", amd64Ctx(0x400123, 0x7ff000, 0), 0x7ff000, words(0x1234, 0x400250, 0xdead))
	check(t, Walk(d, th, nil, 256), want{0x123, "context"}, want{0x250, "scan"})
	got := Walk(d, th, &fakeSyms{reject: map[uint64]bool{0x24f: true}}, 256)
	check(t, got, want{0x123, "context"})
}

func TestWalkArm64LR(t *testing.T) {
	ctx := minidump.Context{CPU: "arm64", PC: 0x400100, SP: 0x7ff000, LR: 0x400240,
		Regs: map[string]uint64{"pc": 0x400100, "sp": 0x7ff000, "x30": 0x400240, "lr": 0x400240}}
	d, th := dumpFor(t, "arm64", ctx, 0x7ff000, words(0, 0))
	check(t, Walk(d, th, nil, 256), want{0x100, "context"}, want{0x240, "fp"})
}

func TestWalkArm64FramePointer(t *testing.T) {
	ctx := minidump.Context{CPU: "arm64", PC: 0x400100, SP: 0x7ff000, FP: 0x7ff010,
		Regs: map[string]uint64{"pc": 0x400100, "sp": 0x7ff000, "x29": 0x7ff010}}
	d, th := dumpFor(t, "arm64", ctx, 0x7ff000, words(0, 0, 0x7ff030, 0x400240, 0, 0, 0, 0x400300))
	got := Walk(d, th, nil, 256)
	// x29 chain: [0x7ff010]=0x7ff030 / [0x7ff018]=0x400240; [0x7ff030]=0 / [0x7ff038]=0x400300.
	check(t, got, want{0x100, "context"}, want{0x240, "fp"}, want{0x300, "fp"})
}

func TestWalkTerminates(t *testing.T) {
	// Self-referencing frame pointer: one repeat at most.
	stack := make([]byte, 0x200)
	binary.LittleEndian.PutUint64(stack[0x100:], 0x7ff100)
	binary.LittleEndian.PutUint64(stack[0x108:], 0x400250)
	d, th := dumpFor(t, "amd64", amd64Ctx(0x400123, 0x7ff000, 0x7ff100), 0x7ff000, stack)
	if got := Walk(d, th, nil, 256); len(got) > 3 {
		t.Errorf("self-referencing chain gave %d frames", len(got))
	}

	// A 10,000-frame chain stops at max.
	const n = 10000
	long := make([]byte, 16*n+16)
	for i := 0; i < n; i++ {
		next := uint64(0x10000000 + 16*(i+1))
		binary.LittleEndian.PutUint64(long[16*i:], next)
		binary.LittleEndian.PutUint64(long[16*i+8:], 0x400250)
	}
	d, th = dumpFor(t, "amd64", amd64Ctx(0x400123, 0x10000000, 0x10000000), 0x10000000, long)
	if got := Walk(d, th, nil, 256); len(got) != 256 {
		t.Errorf("long chain gave %d frames, want 256", len(got))
	}
	if got := Walk(d, th, nil, 10_000); len(got) != MaxFrames {
		t.Errorf("max above MaxFrames gave %d frames", len(got))
	}
	if got := Walk(d, th, nil, 5); len(got) != 5 {
		t.Errorf("max 5 gave %d frames", len(got))
	}
}

func TestWalkUnsupportedCPU(t *testing.T) {
	d, th := dumpFor(t, "amd64", amd64Ctx(0x400123, 0x7ff000, 0), 0x7ff000, words(0x400250))
	th.Context.CPU = "x86"
	d.CPU = "x86"
	got := Walk(d, th, nil, 10)
	if len(got) != 1 || got[0].Trust != "context" {
		t.Errorf("got %+v", got)
	}
}

func TestWalkScanBounded(t *testing.T) {
	// A stack full of valid-looking addresses: scanned frames stop at the cap.
	n := 4096
	w := make([]uint64, n)
	for i := range w {
		w[i] = 0x400250
	}
	d, th := dumpFor(t, "amd64", amd64Ctx(0x400123, 0x7ff000, 0), 0x7ff000, words(w...))
	got := Walk(d, th, nil, 256)
	if len(got) != 1+maxScanFrames {
		t.Errorf("got %d frames, want %d", len(got), 1+maxScanFrames)
	}
}

func BenchmarkWalkCFI(b *testing.B) {
	d, th := dumpFor(b, "amd64", amd64Ctx(0x400123, 0x7ff000, 0), 0x7ff000, words(0x400250, 0, 0))
	s := &fakeSyms{tab: appCFI(b)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Walk(d, th, s, 256)
	}
}
