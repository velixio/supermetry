package minidump

import "fmt"

// CrashReason names a crash from the exception record: the signal and si_code
// on Linux and Android (code = signal, flags = si_code), the exception code on
// Windows (info[0] refines access violations), and the Mach exception type and
// kernel code on macOS and iOS (code = type, flags = first code).
func CrashReason(os string, code, flags uint32, info []uint64) string {
	switch os {
	case "linux", "android":
		return signalReason(code, flags)
	case "windows":
		return windowsReason(code, info)
	case "mac", "ios":
		return machReason(code, flags)
	}
	return fmt.Sprintf("0x%x", code)
}

var signals = map[uint32]string{
	1: "SIGHUP", 2: "SIGINT", 3: "SIGQUIT", 4: "SIGILL", 5: "SIGTRAP", 6: "SIGABRT",
	7: "SIGBUS", 8: "SIGFPE", 9: "SIGKILL", 10: "SIGUSR1", 11: "SIGSEGV", 12: "SIGUSR2",
	13: "SIGPIPE", 14: "SIGALRM", 15: "SIGTERM", 31: "SIGSYS",
}

var sigCodes = map[uint32]map[uint32]string{
	4:  {1: "ILL_ILLOPC", 2: "ILL_ILLOPN", 3: "ILL_ILLADR", 4: "ILL_ILLTRP", 5: "ILL_PRVOPC", 6: "ILL_PRVREG", 7: "ILL_COPROC", 8: "ILL_BADSTK"},
	5:  {1: "TRAP_BRKPT", 2: "TRAP_TRACE"},
	7:  {1: "BUS_ADRALN", 2: "BUS_ADRERR", 3: "BUS_OBJERR"},
	8:  {1: "FPE_INTDIV", 2: "FPE_INTOVF", 3: "FPE_FLTDIV", 4: "FPE_FLTOVF", 5: "FPE_FLTUND", 6: "FPE_FLTRES", 7: "FPE_FLTINV", 8: "FPE_FLTSUB"},
	11: {1: "SEGV_MAPERR", 2: "SEGV_ACCERR"},
}

func signalReason(sig, siCode uint32) string {
	name, ok := signals[sig]
	if !ok {
		return fmt.Sprintf("0x%x", sig)
	}
	if s, ok := sigCodes[sig][siCode]; ok {
		return name + " / " + s
	}
	return name
}

var winCodes = map[uint32]string{
	0x80000002: "EXCEPTION_DATATYPE_MISALIGNMENT",
	0x80000003: "EXCEPTION_BREAKPOINT",
	0x80000004: "EXCEPTION_SINGLE_STEP",
	0xC0000006: "EXCEPTION_IN_PAGE_ERROR",
	0xC000001D: "EXCEPTION_ILLEGAL_INSTRUCTION",
	0xC0000025: "EXCEPTION_NONCONTINUABLE_EXCEPTION",
	0xC000008C: "EXCEPTION_ARRAY_BOUNDS_EXCEEDED",
	0xC000008D: "EXCEPTION_FLT_DENORMAL_OPERAND",
	0xC000008E: "EXCEPTION_FLT_DIVIDE_BY_ZERO",
	0xC000008F: "EXCEPTION_FLT_INEXACT_RESULT",
	0xC0000090: "EXCEPTION_FLT_INVALID_OPERATION",
	0xC0000091: "EXCEPTION_FLT_OVERFLOW",
	0xC0000092: "EXCEPTION_FLT_STACK_CHECK",
	0xC0000093: "EXCEPTION_FLT_UNDERFLOW",
	0xC0000094: "EXCEPTION_INT_DIVIDE_BY_ZERO",
	0xC0000095: "EXCEPTION_INT_OVERFLOW",
	0xC0000096: "EXCEPTION_PRIV_INSTRUCTION",
	0xC00000FD: "EXCEPTION_STACK_OVERFLOW",
	0xC0000374: "STATUS_HEAP_CORRUPTION",
	0xC0000409: "STATUS_STACK_BUFFER_OVERRUN",
}

func windowsReason(code uint32, info []uint64) string {
	if code == 0xC0000005 {
		if len(info) > 0 {
			switch info[0] {
			case 0:
				return "EXCEPTION_ACCESS_VIOLATION_READ"
			case 1:
				return "EXCEPTION_ACCESS_VIOLATION_WRITE"
			case 8:
				return "EXCEPTION_ACCESS_VIOLATION_EXEC"
			}
		}
		return "EXCEPTION_ACCESS_VIOLATION"
	}
	if n, ok := winCodes[code]; ok {
		return n
	}
	return fmt.Sprintf("0x%x", code)
}

var machTypes = map[uint32]string{
	1: "EXC_BAD_ACCESS", 2: "EXC_BAD_INSTRUCTION", 3: "EXC_ARITHMETIC", 4: "EXC_EMULATION",
	5: "EXC_SOFTWARE", 6: "EXC_BREAKPOINT", 7: "EXC_SYSCALL", 8: "EXC_MACH_SYSCALL",
	9: "EXC_RPC_ALERT", 10: "EXC_CRASH", 11: "EXC_RESOURCE", 12: "EXC_GUARD", 13: "EXC_CORPSE_NOTIFY",
}

var machAccess = map[uint32]string{
	1: "KERN_INVALID_ADDRESS", 2: "KERN_PROTECTION_FAILURE", 3: "KERN_NO_SPACE",
	4: "KERN_INVALID_ARGUMENT", 5: "KERN_FAILURE",
}

func machReason(typ, code uint32) string {
	name, ok := machTypes[typ]
	if !ok {
		return fmt.Sprintf("0x%x", typ)
	}
	if typ == 1 {
		if s, ok := machAccess[code]; ok {
			return name + " / " + s
		}
	}
	return name
}
