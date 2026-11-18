package minidump

import "sort"

// Stream types used (MINIDUMP_STREAM_TYPE).
const (
	streamThreadList   = 3
	streamModuleList   = 4
	streamMemoryList   = 5
	streamException    = 6
	streamSystemInfo   = 7
	streamMemory64List = 9
)

// Processor architectures (MINIDUMP_SYSTEM_INFO.ProcessorArchitecture), with
// the legacy Breakpad value for 64-bit ARM.
const (
	archX86      = 0
	archARM      = 5
	archAMD64    = 9
	archARM64    = 12
	archARM64Old = 0x8003
)

// Platform ids (MINIDUMP_SYSTEM_INFO.PlatformId): VER_PLATFORM_WIN32_NT plus
// the Breakpad extensions.
const (
	platWindowsNT = 2
	platMac       = 0x8101
	platIOS       = 0x8102
	platLinux     = 0x8201
	platAndroid   = 0x8203
)

func (d *Dump) parseSystemInfo(s []byte) error {
	if len(s) < 28 {
		return bad("short system info")
	}
	switch le.Uint16(s) {
	case archX86:
		d.CPU = "x86"
	case archARM:
		d.CPU = "arm"
	case archAMD64:
		d.CPU = "amd64"
	case archARM64, archARM64Old:
		d.CPU = "arm64"
	}
	switch le.Uint32(s[20:]) { // PlatformId follows the 4-byte build number
	case platWindowsNT:
		d.OS = "windows"
	case platMac:
		d.OS = "mac"
	case platIOS:
		d.OS = "ios"
	case platLinux:
		d.OS = "linux"
	case platAndroid:
		d.OS = "android"
	}
	return nil
}

// ctxCache shares decoded contexts that several threads point at, so a hostile
// directory cannot multiply one context into thousands of maps.
type ctxCache struct {
	m map[[2]uint32]Context
}

func (c *ctxCache) get(d *Dump, b []byte, size, rva uint32) (Context, error) {
	if size == 0 {
		return Context{}, nil
	}
	key := [2]uint32{size, rva}
	if ctx, ok := c.m[key]; ok {
		return ctx, nil
	}
	raw, ok := sub(b, uint64(rva), uint64(size))
	if !ok {
		return Context{}, bad("context outside file")
	}
	ctx := decodeContext(d.CPU, raw)
	if c.m == nil {
		c.m = map[[2]uint32]Context{}
	}
	c.m[key] = ctx
	return ctx, nil
}

const threadSize = 48

func (d *Dump) parseThreads(b, s []byte, cache *ctxCache) error {
	if len(s) < 4 {
		return bad("short thread list")
	}
	n := le.Uint32(s)
	if n > maxThreads {
		return bad("%d threads", n)
	}
	if uint64(len(s)-4) < uint64(n)*threadSize {
		return bad("thread list truncated")
	}
	d.Threads = make([]Thread, n)
	for i := range d.Threads {
		e := s[4+i*threadSize:]
		t := &d.Threads[i]
		t.ID = le.Uint32(e)
		t.StackStart = le.Uint64(e[24:])
		stackSize := le.Uint32(e[32:])
		stackRVA := le.Uint32(e[36:])
		if stackSize > 0 {
			st, ok := sub(b, uint64(stackRVA), uint64(stackSize))
			if !ok {
				return bad("thread stack outside file")
			}
			t.Stack = st
		}
		ctx, err := cache.get(d, b, le.Uint32(e[40:]), le.Uint32(e[44:]))
		if err != nil {
			return err
		}
		t.Context = ctx
	}
	return nil
}

const moduleSize = 108

func (d *Dump) parseModules(b, s []byte) error {
	if len(s) < 4 {
		return bad("short module list")
	}
	n := le.Uint32(s)
	if n > maxModules {
		return bad("%d modules", n)
	}
	if uint64(len(s)-4) < uint64(n)*moduleSize {
		return bad("module list truncated")
	}
	d.Modules = make([]Module, 0, n)
	names := map[uint32]string{}
	for i := 0; i < int(n); i++ {
		e := s[4+i*moduleSize:]
		m := Module{Base: le.Uint64(e), Size: uint64(le.Uint32(e[8:]))}
		timestamp := le.Uint32(e[16:])
		nameRVA := le.Uint32(e[20:])
		name, seen := names[nameRVA]
		if !seen {
			var ok bool
			name, ok = utf16String(b, nameRVA)
			if !ok {
				return bad("module name outside file")
			}
			names[nameRVA] = name
		}
		m.CodeFile = name
		m.DebugFile = baseName(name)
		if cv, ok := sub(b, uint64(le.Uint32(e[80:])), uint64(le.Uint32(e[76:]))); ok {
			applyCodeView(&m, cv, timestamp)
		}
		d.Modules = append(d.Modules, m)
	}
	sort.SliceStable(d.Modules, func(i, j int) bool { return d.Modules[i].Base < d.Modules[j].Base })
	return nil
}

func (d *Dump) parseMemoryList(b, s []byte) error {
	if len(s) < 4 {
		return bad("short memory list")
	}
	n := uint64(le.Uint32(s))
	if n*16 > uint64(len(s)-4) {
		return bad("memory list truncated")
	}
	if n > maxMemory {
		n = maxMemory
	}
	for i := uint64(0); i < n; i++ {
		e := s[4+i*16:]
		size := le.Uint32(e[8:])
		if size == 0 {
			continue
		}
		data, ok := sub(b, uint64(le.Uint32(e[12:])), uint64(size))
		if !ok {
			return bad("memory range outside file")
		}
		d.mem = append(d.mem, memRange{le.Uint64(e), data})
	}
	return nil
}

func (d *Dump) parseMemory64List(b, s []byte) error {
	if len(s) < 16 {
		return bad("short memory64 list")
	}
	n := le.Uint64(s)
	cursor := le.Uint64(s[8:])
	if n > uint64(len(s)-16)/16 {
		return bad("memory64 list truncated")
	}
	if n > maxMemory {
		n = maxMemory
	}
	for i := uint64(0); i < n; i++ {
		e := s[16+i*16:]
		size := le.Uint64(e[8:])
		data, ok := sub(b, cursor, size)
		if !ok {
			return bad("memory64 range outside file")
		}
		cursor += size
		if size > 0 {
			d.mem = append(d.mem, memRange{le.Uint64(e), data})
		}
	}
	return nil
}

// MINIDUMP_EXCEPTION_STREAM: ThreadId, alignment, MINIDUMP_EXCEPTION (152
// bytes), then the thread context location.
const exceptionStreamSize = 8 + 152 + 8

func (d *Dump) parseException(b, s []byte, cache *ctxCache) error {
	if len(s) < exceptionStreamSize {
		return bad("short exception stream")
	}
	ex := &Exception{ThreadID: le.Uint32(s)}
	rec := s[8:]
	ex.Code = le.Uint32(rec)
	ex.Flags = le.Uint32(rec[4:])
	ex.Address = le.Uint64(rec[16:])
	np := le.Uint32(rec[24:])
	if np > 15 {
		np = 15
	}
	info := make([]uint64, np)
	for i := range info {
		info[i] = le.Uint64(rec[32+i*8:])
	}
	ex.Reason = CrashReason(d.OS, ex.Code, ex.Flags, info)
	d.Crash = ex
	for i := range d.Threads {
		if d.Threads[i].ID == ex.ThreadID {
			d.CrashThread = &d.Threads[i]
			// The exception's own context is the state at the fault.
			if ctx, err := cache.get(d, b, le.Uint32(s[160:]), le.Uint32(s[164:])); err == nil && ctx.CPU != "" {
				d.Threads[i].Context = ctx
			}
			break
		}
	}
	return nil
}

func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
