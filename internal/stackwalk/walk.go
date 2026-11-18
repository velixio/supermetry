// Package stackwalk unwinds the crashing thread of a minidump: Breakpad CFI
// first, then frame pointers, then a bounded scan of the stack for return
// addresses. It is written from the public Breakpad symbol-file documentation
// and the DWARF/ABI specifications.
package stackwalk

import (
	"encoding/binary"
	"maps"
	"time"

	"supermetry/internal/breakpad"
	"supermetry/internal/minidump"
)

// Limits on one walk. MaxFrames also caps the caller's max.
const (
	MaxFrames  = 256
	walkBudget = 2 * time.Second
)

// Symbols gives the walker what it needs from loaded symbol files. A module
// without symbols returns (nil, false) from CFI and true from Covers (nothing
// is known, so nothing is rejected).
type Symbols interface {
	CFI(m *minidump.Module) (*breakpad.CFITable, bool)
	// Covers reports whether rel (relative to the module base) lies inside a
	// known function; it validates candidates found by the stack scan.
	Covers(m *minidump.Module, rel uint64) bool
}

// Frame is one unwound frame. Rel is PC minus the module base; for caller
// frames PC is a return address, so symbolicate at Rel-1.
type Frame struct {
	PC     uint64
	Module *minidump.Module
	Rel    uint64
	Trust  string // "context", "cfi", "fp" or "scan"
}

type arch struct {
	name       string
	pc, sp, fp string
	lr         string
	lrLeaf     bool
}

type walker struct {
	d       *minidump.Dump
	t       *minidump.Thread
	syms    Symbols
	a       *arch
	scanned int
}

// read loads a little-endian 64-bit word from the thread stack or any other
// captured memory.
func (w *walker) read(addr uint64) (uint64, bool) {
	if s := w.t.Stack; len(s) >= 8 && addr >= w.t.StackStart {
		if off := addr - w.t.StackStart; off <= uint64(len(s))-8 {
			return binary.LittleEndian.Uint64(s[off:]), true
		}
	}
	b, ok := w.d.ReadMemory(addr, 8)
	if !ok {
		return 0, false
	}
	return binary.LittleEndian.Uint64(b), true
}

type step struct {
	pc, sp uint64
	regs   map[string]uint64
	trust  string
}

// Walk unwinds thread t of d and returns at most max frames (never more than
// MaxFrames), innermost first. Frame 0 comes from the context. The walk stops
// at a zero or out-of-module return address, when the stack pointer fails to
// rise, when no method finds a caller, or after a fixed time budget.
// Unsupported CPUs yield only the context frame.
func Walk(d *minidump.Dump, t *minidump.Thread, syms Symbols, max int) []Frame {
	if d == nil || t == nil || max <= 0 {
		return nil
	}
	if max > MaxFrames {
		max = MaxFrames
	}
	ctx := &t.Context
	if ctx.PC == 0 && ctx.SP == 0 {
		return nil
	}
	w := &walker{d: d, t: t, syms: syms}
	cpu := ctx.CPU
	if cpu == "" {
		cpu = d.CPU
	}
	switch cpu {
	case "amd64":
		w.a = amd64Arch
	case "arm64":
		w.a = arm64Arch
	}
	mk := func(pc uint64, trust string) Frame {
		f := Frame{PC: pc, Trust: trust}
		if m, ok := d.ModuleAt(pc); ok {
			f.Module, f.Rel = m, pc-m.Base
		}
		return f
	}
	frames := make([]Frame, 1, min(max, 32))
	frames[0] = mk(ctx.PC, "context")
	if w.a == nil {
		return frames
	}
	regs := make(map[string]uint64, len(ctx.Regs)+4)
	maps.Copy(regs, ctx.Regs)
	regs[w.a.pc], regs[w.a.sp], regs[w.a.fp] = ctx.PC, ctx.SP, ctx.FP
	if w.a.lr != "" {
		regs[w.a.lr] = ctx.LR
	}
	sp := ctx.SP
	deadline := time.Now().Add(walkBudget)

	for len(frames) < max && time.Now().Before(deadline) {
		cur := &frames[len(frames)-1]
		first := len(frames) == 1
		next, end := w.unwind(cur, first, sp, regs)
		if end || next == nil {
			break
		}
		f := mk(next.pc, next.trust)
		if f.Module == nil {
			break
		}
		frames = append(frames, f)
		sp, regs = next.sp, next.regs
	}
	return frames
}

// unwind finds the caller of cur. end reports a clean end of stack (a zero
// return address); a nil step with end false means no method worked.
func (w *walker) unwind(cur *Frame, first bool, sp uint64, regs map[string]uint64) (*step, bool) {
	valid := func(s *step, leaf bool) bool {
		if s.pc == 0 {
			return false
		}
		if _, ok := w.d.ModuleAt(s.pc); !ok {
			return false
		}
		return s.sp > sp || leaf && s.sp == sp
	}

	if s, end, ok := w.byCFI(cur, first, regs); ok {
		if end {
			return nil, true
		}
		if valid(s, false) {
			return s, false
		}
	}
	if s, end, ok := w.byFP(first, sp, regs); ok {
		if end {
			return nil, true
		}
		if valid(s, first && w.a.lrLeaf) {
			return s, false
		}
	}
	if npc, nsp, ok := w.scan(sp); ok {
		nr := maps.Clone(regs)
		nr[w.a.pc], nr[w.a.sp] = npc, nsp
		return &step{pc: npc, sp: nsp, regs: nr, trust: "scan"}, false
	}
	return nil, false
}

// byCFI applies the module's STACK CFI rules at the frame's address. Caller
// frames look up pc-1, since the return address may already belong to the
// next function.
func (w *walker) byCFI(cur *Frame, first bool, regs map[string]uint64) (s *step, end, ok bool) {
	if w.syms == nil || cur.Module == nil {
		return nil, false, false
	}
	tab, found := w.syms.CFI(cur.Module)
	if !found || tab == nil {
		return nil, false, false
	}
	rel := cur.Rel
	if !first && rel > 0 {
		rel--
	}
	rules, found := tab.RulesAt(rel)
	if !found {
		return nil, false, false
	}
	out, err := EvalPostfix(rules, regs, w.read)
	if err != nil {
		return nil, false, false
	}
	cfa, ok1 := out[".cfa"]
	ra, ok2 := out[".ra"]
	if !ok1 || !ok2 {
		return nil, false, false
	}
	if ra == 0 {
		return nil, true, true
	}
	nr := maps.Clone(regs)
	for k, v := range out {
		if k != ".cfa" && k != ".ra" {
			nr[k] = v
		}
	}
	nr[w.a.sp], nr[w.a.pc] = cfa, ra
	return &step{pc: ra, sp: cfa, regs: nr, trust: "cfi"}, false, true
}

// byFP follows the frame-pointer chain. On arm64, the first frame without a
// usable frame record takes its caller from the link register.
func (w *walker) byFP(first bool, sp uint64, regs map[string]uint64) (s *step, end, ok bool) {
	fp := regs[w.a.fp]
	if fp != 0 && fp >= sp && fp&7 == 0 {
		saved, ok1 := w.read(fp)
		ra, ok2 := w.read(fp + 8)
		if ok1 && ok2 {
			if ra == 0 {
				return nil, true, true
			}
			nr := maps.Clone(regs)
			if saved <= fp { // the chain must rise; stop following it
				saved = 0
			}
			nr[w.a.fp], nr[w.a.sp], nr[w.a.pc] = saved, fp+16, ra
			return &step{pc: ra, sp: fp + 16, regs: nr, trust: "fp"}, false, true
		}
	}
	if first && w.a.lrLeaf {
		if lr := regs[w.a.lr]; lr != 0 {
			nr := maps.Clone(regs)
			nr[w.a.pc] = lr
			nr[w.a.lr] = 0
			return &step{pc: lr, sp: sp, regs: nr, trust: "fp"}, false, true
		}
	}
	return nil, false, false
}
