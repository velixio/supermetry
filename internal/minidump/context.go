package minidump

import "fmt"

// Context is the register state of a thread. Regs holds every general purpose
// register by lower-case name (rax..r15 and rip; x0..x30, sp, pc, fp, lr;
// eax..eip; r0..r15).
type Context struct {
	CPU            string
	PC, SP, FP, LR uint64
	Regs           map[string]uint64
}

// CONTEXT offsets, from the Windows CONTEXT definitions (winnt.h) and the
// layouts Breakpad writes for arm64 and arm.
var amd64Regs = [...]struct {
	name string
	off  int
}{
	{"rax", 0x78}, {"rcx", 0x80}, {"rdx", 0x88}, {"rbx", 0x90},
	{"rsp", 0x98}, {"rbp", 0xa0}, {"rsi", 0xa8}, {"rdi", 0xb0},
	{"r8", 0xb8}, {"r9", 0xc0}, {"r10", 0xc8}, {"r11", 0xd0},
	{"r12", 0xd8}, {"r13", 0xe0}, {"r14", 0xe8}, {"r15", 0xf0},
	{"rip", 0xf8},
}

var x86Regs = [...]struct {
	name string
	off  int
}{
	{"edi", 0x9c}, {"esi", 0xa0}, {"ebx", 0xa4}, {"edx", 0xa8},
	{"ecx", 0xac}, {"eax", 0xb0}, {"ebp", 0xb4}, {"eip", 0xb8}, {"esp", 0xc4},
}

const (
	amd64ContextMin = 0xf8 + 8
	x86ContextMin   = 0xc4 + 4
	arm64ContextMin = 272 // flags+cpsr (8), x0..x30 (248), sp, pc
	armContextMin   = 4 + 16*4
)

// decodeContext reads the registers for cpu from raw. A context too short for
// its CPU, or for an unsupported CPU, yields an empty Context.
func decodeContext(cpu string, raw []byte) Context {
	switch cpu {
	case "amd64":
		if len(raw) < amd64ContextMin {
			return Context{}
		}
		c := Context{CPU: cpu, Regs: make(map[string]uint64, len(amd64Regs))}
		for _, r := range amd64Regs {
			c.Regs[r.name] = le.Uint64(raw[r.off:])
		}
		c.PC, c.SP, c.FP = c.Regs["rip"], c.Regs["rsp"], c.Regs["rbp"]
		return c
	case "x86":
		if len(raw) < x86ContextMin {
			return Context{}
		}
		c := Context{CPU: cpu, Regs: make(map[string]uint64, len(x86Regs))}
		for _, r := range x86Regs {
			c.Regs[r.name] = uint64(le.Uint32(raw[r.off:]))
		}
		c.PC, c.SP, c.FP = c.Regs["eip"], c.Regs["esp"], c.Regs["ebp"]
		return c
	case "arm64":
		// MD_CONTEXT_ARM64 (flags 0x00400000: u32 flags + u32 cpsr) and the
		// older MD_CONTEXT_ARM64_OLD (flags 0x80000000: u64 flags) both put
		// x0..x30 at offset 8, sp at 256 and pc at 264, so one reader serves both.
		if len(raw) < arm64ContextMin {
			return Context{}
		}
		c := Context{CPU: cpu, Regs: make(map[string]uint64, 36)}
		for i := 0; i < 31; i++ {
			c.Regs[fmt.Sprintf("x%d", i)] = le.Uint64(raw[8+i*8:])
		}
		c.SP, c.PC = le.Uint64(raw[256:]), le.Uint64(raw[264:])
		c.FP, c.LR = c.Regs["x29"], c.Regs["x30"]
		c.Regs["sp"], c.Regs["pc"], c.Regs["fp"], c.Regs["lr"] = c.SP, c.PC, c.FP, c.LR
		return c
	case "arm":
		if len(raw) < armContextMin {
			return Context{}
		}
		c := Context{CPU: cpu, Regs: make(map[string]uint64, 16)}
		for i := 0; i < 16; i++ {
			c.Regs[fmt.Sprintf("r%d", i)] = uint64(le.Uint32(raw[4+i*4:]))
		}
		c.FP, c.SP, c.LR, c.PC = c.Regs["r11"], c.Regs["r13"], c.Regs["r14"], c.Regs["r15"]
		return c
	}
	return Context{}
}
