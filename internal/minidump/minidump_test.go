package minidump_test

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"runtime"
	"testing"
	"time"

	"supermetry/internal/minidump"
	"supermetry/internal/minidump/minidumptest"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func linuxDump(t *testing.T) []byte {
	t.Helper()
	b := &minidumptest.Builder{OS: "linux", CPU: "amd64"}
	b.AddModule("app", 0x400000, 0x10000, mustHex(t, "109e3c7ab2558e4b8a532f0c8d4e9a01c0ffee00"))
	stack := make([]byte, 64)
	binary.LittleEndian.PutUint64(stack, 0x400250)
	b.AddThread(7, minidump.Context{PC: 0x400123, SP: 0x7ff000}, 0x7ff000, stack)
	b.SetException(7, 11, 1, 0)
	return b.Bytes()
}

func TestParseSynthetic(t *testing.T) {
	d, err := minidump.Parse(linuxDump(t))
	if err != nil {
		t.Fatal(err)
	}
	if d.OS != "linux" || d.CPU != "amd64" {
		t.Fatalf("os/cpu = %q/%q", d.OS, d.CPU)
	}
	if d.Crash == nil || d.Crash.Reason != "SIGSEGV / SEGV_MAPERR" || d.Crash.Address != 0 {
		t.Fatalf("crash = %+v", d.Crash)
	}
	if d.CrashThread == nil || d.CrashThread.ID != 7 {
		t.Fatalf("crash thread = %+v", d.CrashThread)
	}
	if c := d.CrashThread.Context; c.PC != 0x400123 || c.SP != 0x7ff000 || c.Regs["rip"] != 0x400123 {
		t.Fatalf("context = %+v", c)
	}
	if len(d.Modules) != 1 {
		t.Fatalf("modules = %d", len(d.Modules))
	}
	m := d.Modules[0]
	if m.DebugID != "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01" || m.CodeID != "109e3c7ab2558e4b8a532f0c8d4e9a01c0ffee00" || m.CodeFile != "app" || m.Base != 0x400000 || m.Size != 0x10000 {
		t.Fatalf("module = %+v", m)
	}
	if _, ok := d.ModuleAt(0x40ffff); !ok {
		t.Error("ModuleAt(0x40ffff) missed")
	}
	if _, ok := d.ModuleAt(0x410000); ok {
		t.Error("ModuleAt(0x410000) hit")
	}
	if _, ok := d.ModuleAt(0x3fffff); ok {
		t.Error("ModuleAt(0x3fffff) hit")
	}
	w, ok := d.ReadMemory(0x7ff000, 8)
	if !ok || binary.LittleEndian.Uint64(w) != 0x400250 {
		t.Fatalf("ReadMemory = %x, %v", w, ok)
	}
	if _, ok := d.ReadMemory(0x7ff000+60, 8); ok {
		t.Error("read across the end of a range succeeded")
	}
	if _, ok := d.ReadMemory(0x1000, 8); ok {
		t.Error("read of unmapped memory succeeded")
	}
}

func TestParseExceptionContextWins(t *testing.T) {
	b := &minidumptest.Builder{}
	b.AddThread(1, minidump.Context{PC: 0x1111, SP: 0x2000}, 0, nil)
	b.SetException(1, 11, 2, 0x10)
	b.SetExceptionContext(minidump.Context{PC: 0x4242, SP: 0x3000})
	d, err := minidump.Parse(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if d.CrashThread.Context.PC != 0x4242 || d.Crash.Address != 0x10 || d.Crash.Reason != "SIGSEGV / SEGV_ACCERR" {
		t.Fatalf("got %+v / %+v", d.CrashThread.Context, d.Crash)
	}
}

func TestParseArm64Context(t *testing.T) {
	for _, old := range []bool{false, true} {
		b := &minidumptest.Builder{OS: "mac", CPU: "arm64", ARM64Old: old}
		b.AddThread(3, minidump.Context{PC: 0x400100, SP: 0x7ff000, FP: 0x7ff100, LR: 0x400240, Regs: map[string]uint64{"x0": 9}}, 0, nil)
		d, err := minidump.Parse(b.Bytes())
		if err != nil {
			t.Fatalf("old=%v: %v", old, err)
		}
		c := d.Threads[0].Context
		if c.CPU != "arm64" || c.PC != 0x400100 || c.SP != 0x7ff000 || c.FP != 0x7ff100 || c.LR != 0x400240 || c.Regs["x29"] != 0x7ff100 || c.Regs["x30"] != 0x400240 || c.Regs["x0"] != 9 {
			t.Errorf("old=%v: context = %+v", old, c)
		}
	}
}

func TestParseOtherCPUs(t *testing.T) {
	for _, cpu := range []string{"x86", "arm"} {
		b := &minidumptest.Builder{OS: "android", CPU: cpu}
		b.AddThread(1, minidump.Context{PC: 0x1000, SP: 0x2000, FP: 0x3000}, 0, nil)
		d, err := minidump.Parse(b.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		c := d.Threads[0].Context
		if d.CPU != cpu || d.OS != "android" || c.PC != 0x1000 || c.SP != 0x2000 || c.FP != 0x3000 {
			t.Errorf("%s: %q %q %+v", cpu, d.OS, d.CPU, c)
		}
	}
}

func TestParseWindowsRSDS(t *testing.T) {
	b := &minidumptest.Builder{OS: "windows", CPU: "amd64"}
	b.AddModulePE(`C:\Windows\System32\dbghelp.dll`, 0x7ff600000000, 0x1f000, 0x5a1b2c3d, "{9C2A902B-6FDF-40AD-8308-588A41D572A0}", 1, `C:\build\dbghelp.pdb`)
	b.AddModulePE(`C:\app\noage.dll`, 0x10000, 0x100, 1, "9C2A902B-6FDF-40AD-8308-588A41D572A0", 0, "noage.pdb")
	d, err := minidump.Parse(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by base: noage first.
	if d.Modules[0].DebugID != "9c2a902b-6fdf-40ad-8308-588a41d572a0" {
		t.Errorf("age 0 id = %q", d.Modules[0].DebugID)
	}
	m := d.Modules[1]
	if m.DebugID != "9c2a902b-6fdf-40ad-8308-588a41d572a0-1" || m.DebugFile != "dbghelp.pdb" || m.CodeFile != `C:\Windows\System32\dbghelp.dll` || m.CodeID != "5A1B2C3D1f000" {
		t.Errorf("module = %+v", m)
	}
}

func TestParseMemory64(t *testing.T) {
	b := &minidumptest.Builder{}
	b.AddMemory64(0x1000, []byte{1, 2, 3, 4})
	b.AddMemory64(0x9000, []byte{9, 8, 7, 6})
	b.AddMemory(0x5000, []byte{5, 5})
	d, err := minidump.Parse(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		addr uint64
		n    int
		want string
	}{{0x1001, 2, "0203"}, {0x9000, 4, "09080706"}, {0x5000, 2, "0505"}} {
		got, ok := d.ReadMemory(c.addr, c.n)
		if !ok || hex.EncodeToString(got) != c.want {
			t.Errorf("ReadMemory(%#x) = %x, %v; want %s", c.addr, got, ok, c.want)
		}
	}
}

func TestCrashReasonTable(t *testing.T) {
	for _, c := range []struct {
		os          string
		code, flags uint32
		info        []uint64
		want        string
	}{
		{"linux", 11, 1, nil, "SIGSEGV / SEGV_MAPERR"},
		{"linux", 11, 2, nil, "SIGSEGV / SEGV_ACCERR"},
		{"android", 6, 0, nil, "SIGABRT"},
		{"linux", 7, 2, nil, "SIGBUS / BUS_ADRERR"},
		{"linux", 99, 0, nil, "0x63"},
		{"windows", 0xC0000005, 0, []uint64{0, 0x10}, "EXCEPTION_ACCESS_VIOLATION_READ"},
		{"windows", 0xC0000005, 0, []uint64{1, 0x10}, "EXCEPTION_ACCESS_VIOLATION_WRITE"},
		{"windows", 0xC0000005, 0, []uint64{8, 0x10}, "EXCEPTION_ACCESS_VIOLATION_EXEC"},
		{"windows", 0xC00000FD, 0, nil, "EXCEPTION_STACK_OVERFLOW"},
		{"windows", 0xE06D7363, 0, nil, "0xe06d7363"},
		{"mac", 1, 1, nil, "EXC_BAD_ACCESS / KERN_INVALID_ADDRESS"},
		{"mac", 1, 2, nil, "EXC_BAD_ACCESS / KERN_PROTECTION_FAILURE"},
		{"mac", 6, 1, nil, "EXC_BREAKPOINT"},
		{"mac", 77, 0, nil, "0x4d"},
		{"", 5, 0, nil, "0x5"},
	} {
		if got := minidump.CrashReason(c.os, c.code, c.flags, c.info); got != c.want {
			t.Errorf("CrashReason(%q, %#x, %d, %v) = %q, want %q", c.os, c.code, c.flags, c.info, got, c.want)
		}
	}
}

// header builds a minimal dump with one directory entry of the given type.
func header(typ, size, rva uint32, total int) []byte {
	b := make([]byte, total)
	copy(b, "MDMP")
	binary.LittleEndian.PutUint32(b[8:], 1)
	binary.LittleEndian.PutUint32(b[12:], 32)
	binary.LittleEndian.PutUint32(b[32:], typ)
	binary.LittleEndian.PutUint32(b[36:], size)
	binary.LittleEndian.PutUint32(b[40:], rva)
	return b
}

func TestParseRejects(t *testing.T) {
	badMagic := linuxDump(t)
	badMagic[3] = 'Q'

	hugeThreads := header(3, 8, 44, 64)
	binary.LittleEndian.PutUint32(hugeThreads[44:], 1<<31)

	manyThreads := header(3, 8, 44, 64)
	binary.LittleEndian.PutUint32(manyThreads[44:], 4097)

	hugeModules := header(4, 8, 44, 64)
	binary.LittleEndian.PutUint32(hugeModules[44:], 1<<31)

	hugeMem := header(5, 24, 44, 80)
	binary.LittleEndian.PutUint32(hugeMem[44:], 1)
	binary.LittleEndian.PutUint32(hugeMem[44+4+8:], 1<<30) // descriptor size
	binary.LittleEndian.PutUint32(hugeMem[44+4+12:], 60)

	hugeMem64 := header(9, 24, 44, 80)
	binary.LittleEndian.PutUint64(hugeMem64[44:], 1<<60)

	manyStreams := header(3, 0, 0, 64)
	binary.LittleEndian.PutUint32(manyStreams[8:], 1<<31)

	for name, in := range map[string][]byte{
		"empty":            nil,
		"short":            []byte("MDMP"),
		"bad magic":        badMagic,
		"directory beyond": func() []byte { b := header(3, 8, 44, 64); binary.LittleEndian.PutUint32(b[12:], 1<<31); return b }(),
		"stream rva":       header(3, 8, 1<<31, 64),
		"stream size":      header(3, 1<<31, 44, 64),
		"2^31 threads":     hugeThreads,
		"4097 threads":     manyThreads,
		"2^31 modules":     hugeModules,
		"huge mem range":   hugeMem,
		"huge mem64 count": hugeMem64,
		"2^31 streams":     manyStreams,
	} {
		start := time.Now()
		d, err := minidump.Parse(in)
		if err == nil {
			t.Errorf("%s: no error (%+v)", name, d)
			continue
		}
		if !errors.Is(err, minidump.ErrInvalid) {
			t.Errorf("%s: error %v does not wrap ErrInvalid", name, err)
		}
		if el := time.Since(start); el > 10*time.Millisecond {
			t.Errorf("%s: took %v", name, el)
		}
	}
}

// TestParseBoundedAlloc checks that dumps whose directories repeat one context
// or one module name for every entry cannot amplify into much more memory than
// the file itself.
func TestParseBoundedAlloc(t *testing.T) {
	// 4096 threads that all point at the same context, 4096 modules sharing a name.
	in := make([]byte, 0, 1<<20)
	in = append(in, make([]byte, 32)...)
	copy(in, "MDMP")
	put := func(p []byte) uint32 {
		for len(in)%4 != 0 {
			in = append(in, 0)
		}
		o := uint32(len(in))
		in = append(in, p...)
		return o
	}
	si := make([]byte, 56)
	binary.LittleEndian.PutUint16(si, 9)
	binary.LittleEndian.PutUint32(si[24:], 0x8201)
	siRVA := put(si)
	ctxRVA := put(make([]byte, 1232))
	name := make([]byte, 4+2048)
	binary.LittleEndian.PutUint32(name, 2048)
	nameRVA := put(name)
	tl := make([]byte, 4+48*4096)
	binary.LittleEndian.PutUint32(tl, 4096)
	for i := 0; i < 4096; i++ {
		e := tl[4+48*i:]
		binary.LittleEndian.PutUint32(e[40:], 1232)
		binary.LittleEndian.PutUint32(e[44:], ctxRVA)
	}
	tlRVA := put(tl)
	ml := make([]byte, 4+108*4096)
	binary.LittleEndian.PutUint32(ml, 4096)
	for i := 0; i < 4096; i++ {
		e := ml[4+108*i:]
		binary.LittleEndian.PutUint64(e, uint64(i)*0x1000)
		binary.LittleEndian.PutUint32(e[20:], nameRVA)
	}
	mlRVA := put(ml)
	dir := make([]byte, 36)
	for i, s := range [][3]uint32{{7, 56, siRVA}, {3, uint32(len(tl)), tlRVA}, {4, uint32(len(ml)), mlRVA}} {
		binary.LittleEndian.PutUint32(dir[i*12:], s[0])
		binary.LittleEndian.PutUint32(dir[i*12+4:], s[1])
		binary.LittleEndian.PutUint32(dir[i*12+8:], s[2])
	}
	dirRVA := put(dir)
	binary.LittleEndian.PutUint32(in[8:], 3)
	binary.LittleEndian.PutUint32(in[12:], dirRVA)

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	d, err := minidump.Parse(in)
	runtime.ReadMemStats(&m1)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Threads) != 4096 || len(d.Modules) != 4096 {
		t.Fatalf("threads/modules = %d/%d", len(d.Threads), len(d.Modules))
	}
	if got := m1.TotalAlloc - m0.TotalAlloc; got > uint64(len(in))*4 {
		t.Errorf("allocated %d bytes for a %d byte dump", got, len(in))
	}
}

func BenchmarkParse(b *testing.B) {
	bd := &minidumptest.Builder{}
	bd.AddModule("app", 0x400000, 0x10000, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	bd.AddThread(7, minidump.Context{PC: 0x400123, SP: 0x7ff000}, 0x7ff000, make([]byte, 4096))
	bd.SetException(7, 11, 1, 0)
	in := bd.Bytes()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := minidump.Parse(in); err != nil {
			b.Fatal(err)
		}
	}
}
