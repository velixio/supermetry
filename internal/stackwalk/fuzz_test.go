package stackwalk

import (
	"testing"
)

// FuzzWalk feeds arbitrary registers and stack bytes (and optionally a CFI
// table) through the walker: it must terminate within the frame cap and never
// panic.
func FuzzWalk(f *testing.F) {
	f.Add(uint64(0x400123), uint64(0x7ff000), uint64(0), words(0x400250, 0, 0), true, false)
	f.Add(uint64(0x400123), uint64(0x7ff000), uint64(0x7ff008), words(0x7ff010, 0x400250, 0x7ff008, 0x400260), false, false)
	f.Add(uint64(0x400100), uint64(0x7ff000), uint64(0x7ff000), words(0x7ff000, 0x400240), false, true)
	f.Fuzz(func(t *testing.T, pc, sp, fp uint64, stack []byte, cfi, arm bool) {
		if len(stack) > 1<<16 {
			stack = stack[:1<<16]
		}
		cpu, ctx := "amd64", amd64Ctx(pc, sp, fp)
		if arm {
			cpu = "arm64"
			ctx.CPU = cpu
			ctx.Regs = map[string]uint64{"pc": pc, "sp": sp, "x29": fp, "lr": pc ^ 0x40, "x30": pc ^ 0x40}
			ctx.LR = pc ^ 0x40
		}
		d, th := dumpFor(t, cpu, ctx, sp, stack)
		var s Symbols
		if cfi {
			s = &fakeSyms{tab: appCFI(t)}
		}
		if got := Walk(d, th, s, 256); len(got) > 256 {
			t.Fatalf("%d frames", len(got))
		}
	})
}
