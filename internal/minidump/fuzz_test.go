package minidump_test

import (
	"encoding/binary"
	"runtime"
	"testing"

	"supermetry/internal/minidump"
	"supermetry/internal/minidump/minidumptest"
)

func FuzzParse(f *testing.F) {
	for _, cpu := range []string{"amd64", "arm64", "x86", "arm"} {
		for _, os := range []string{"linux", "mac", "windows"} {
			b := &minidumptest.Builder{OS: os, CPU: cpu}
			b.AddModule("app", 0x400000, 0x10000, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20})
			b.AddModulePE(`C:\x\dbghelp.dll`, 0x10000000, 0x1000, 7, "9C2A902B-6FDF-40AD-8308-588A41D572A0", 1, "dbghelp.pdb")
			b.AddThread(7, minidump.Context{PC: 0x400123, SP: 0x7ff000, FP: 0x7ff100}, 0x7ff000, make([]byte, 128))
			b.AddThread(8, minidump.Context{PC: 1}, 0, nil)
			b.AddMemory(0x9000, []byte{1, 2, 3})
			b.AddMemory64(0xa000, []byte{4, 5, 6})
			b.SetException(7, 11, 1, 0x10)
			b.SetExceptionInfo(1, 2)
			b.SetExceptionContext(minidump.Context{PC: 5, SP: 6})
			f.Add(b.Bytes())
		}
	}
	// The 32-byte header of a real dump, with nothing behind it.
	hdr := make([]byte, 32)
	copy(hdr, "MDMP")
	binary.LittleEndian.PutUint32(hdr[4:], 0xa793)
	f.Add(hdr)

	f.Fuzz(func(t *testing.T, in []byte) {
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		d, err := minidump.Parse(in)
		runtime.ReadMemStats(&m1)
		// Bounded allocation: a small constant plus a multiple of the input
		// (many threads and modules legitimately take a few hundred KiB).
		if got := m1.TotalAlloc - m0.TotalAlloc; got > uint64(len(in))*4+(1<<20) {
			t.Fatalf("allocated %d bytes for %d input bytes", got, len(in))
		}
		if err != nil {
			return
		}
		for _, m := range d.Modules {
			d.ModuleAt(m.Base)
		}
		for _, th := range d.Threads {
			d.ReadMemory(th.StackStart, len(th.Stack))
			d.ReadMemory(th.StackStart, 8)
		}
		d.ReadMemory(^uint64(0)-3, 8)
		d.ReadMemory(0, -1)
		d.ModuleAt(^uint64(0))
	})
}
