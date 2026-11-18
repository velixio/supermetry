package sentryingest

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"supermetry/internal/appingest"
	"supermetry/internal/breakpad"
	"supermetry/internal/minidump"
	"supermetry/internal/stackwalk"
	"supermetry/internal/symcache"
)

// RemoteSymbols finds a symbol cache outside the project's own uploads, such
// as Electron's public symbols. The cache key it returns is read through
// Symbolicator.Cache. A nil RemoteSymbols is valid and means none.
type RemoteSymbols interface {
	CacheFor(ctx context.Context, siteID int32, m appingest.DebugImage) (cacheKey string, found bool, err error)
}

func (s *Symbolicator) remoteCache(ctx context.Context, siteID int32, im appingest.DebugImage) (string, bool) {
	if s.cfg.Remote == nil {
		return "", false
	}
	key, found, err := s.cfg.Remote.CacheFor(ctx, siteID, im)
	if err != nil || !found {
		return "", false
	}
	return key, true
}

// MinidumpResolver turns the minidump an event carries into an exception with
// a stack-walked crashing thread. It must run before NativeResolver, which
// symbolicates the frames it builds. The dump is processed in memory only.
func MinidumpResolver() Resolver { return minidumpResolver{} }

type minidumpResolver struct{}

func (minidumpResolver) Wants(b appingest.Body) bool { return len(b.Minidump) > 0 }

// dumpModuleSyms loads the .sym caches the stack walker needs, one per module.
type dumpModuleSyms struct {
	ctx    context.Context
	s      *Symbolicator
	site   int32
	img    map[uint64]appingest.DebugImage
	loaded map[uint64]*dumpSym
}

type dumpSym struct {
	file  *os.File
	cache *symcache.Cache
	cfi   *breakpad.CFITable
}

func (d *dumpModuleSyms) load(m *minidump.Module) *dumpSym {
	if e, ok := d.loaded[m.Base]; ok {
		return e
	}
	d.loaded[m.Base] = nil
	id := debugIDOf(d.img[m.Base])
	if id == "" {
		return nil
	}
	key, found := d.s.cacheFor(d.ctx, d.site, "breakpad", id)
	if !found {
		key, found = d.s.remoteCache(d.ctx, d.site, d.img[m.Base])
	}
	if !found {
		return nil
	}
	f, size, err := d.s.Cache(d.ctx, key)
	if err != nil {
		return nil
	}
	c, err := symcache.Open(f, size)
	if err != nil {
		f.Close()
		return nil
	}
	e := &dumpSym{file: f, cache: c}
	if t, err := breakpad.OpenCFI(c); err == nil {
		e.cfi = t
	}
	d.loaded[m.Base] = e
	return e
}

func (d *dumpModuleSyms) CFI(m *minidump.Module) (*breakpad.CFITable, bool) {
	if e := d.load(m); e != nil && e.cfi != nil {
		return e.cfi, true
	}
	return nil, false
}

// Covers says whether rel is inside a known function. A module without
// symbols covers everything, so the scan is not blinded by missing files.
func (d *dumpModuleSyms) Covers(m *minidump.Module, rel uint64) bool {
	e := d.load(m)
	if e == nil {
		return true
	}
	_, ok := e.cache.Lookup(rel)
	return ok
}

func (d *dumpModuleSyms) close() {
	for _, e := range d.loaded {
		if e != nil {
			e.file.Close()
		}
	}
}

func dumpImageType(osName string) string {
	switch osName {
	case "mac":
		return "macho"
	case "windows":
		return "pe"
	}
	return "elf"
}

func dumpOSName(osName string) string {
	switch osName {
	case "mac":
		return "macOS"
	case "windows":
		return "Windows"
	case "android":
		return "Android"
	case "linux":
		return "Linux"
	}
	return ""
}

// dumpCodeFile reduces a module path to the scrubbed form kept on images and
// frames; it goes through the same scrubbing as any other path.
func dumpCodeFile(p string) string { return scrubFilename(strings.ReplaceAll(p, `\`, "/")) }

func moduleBase(codeFile string) string {
	b := path.Base(strings.ReplaceAll(codeFile, `\`, "/"))
	if b == "." || b == "/" {
		return ""
	}
	return b
}

func windowsSystemPath(codeFile string) bool {
	return strings.Contains(strings.ToLower(strings.ReplaceAll(codeFile, `\`, "/")), "/windows/")
}

func (minidumpResolver) Resolve(ctx context.Context, ev appingest.Event, body *appingest.Body, s *Symbolicator) (attempted, resolved int) {
	d, err := minidump.Parse(body.Minidump)
	if err != nil {
		s.cfg.Stats.Add("minidump_invalid", 1)
		return 0, 0
	}
	thread := d.CrashThread
	if thread == nil && len(d.Threads) > 0 {
		thread = &d.Threads[0]
	}

	images := make([]appingest.DebugImage, 0, len(d.Modules))
	byBase := make(map[uint64]appingest.DebugImage, len(d.Modules))
	for _, m := range d.Modules {
		im := appingest.DebugImage{
			Type: dumpImageType(d.OS), CodeFile: dumpCodeFile(m.CodeFile), DebugID: m.DebugID, CodeID: m.CodeID,
			DebugFile: dumpCodeFile(m.DebugFile), Arch: d.CPU, ImageAddr: m.Base, ImageSize: m.Size, Minidump: true,
		}
		images = append(images, im)
		byBase[m.Base] = im
	}

	var frames []appingest.Frame
	if thread != nil {
		syms := &dumpModuleSyms{ctx: ctx, s: s, site: body.SiteID, img: byBase, loaded: map[uint64]*dumpSym{}}
		defer syms.close()
		walked := stackwalk.Walk(d, thread, syms, maxNativeFrames)
		frames = make([]appingest.Frame, 0, len(walked))
		for i := len(walked) - 1; i >= 0; i-- { // oldest call first
			w := walked[i]
			f := appingest.Frame{InstructionAddr: fmt.Sprintf("0x%x", w.PC), Trust: w.Trust}
			if w.Module != nil {
				im := byBase[w.Module.Base]
				f.Package = im.CodeFile
				f.Function = clip(fmt.Sprintf("%s+0x%x", moduleBase(im.CodeFile), w.Rel), 200)
				f.InApp = !systemImage(w.Module.CodeFile) && !windowsSystemPath(w.Module.CodeFile)
			} else {
				f.Function = clip(f.InstructionAddr, 200)
			}
			frames = append(frames, f)
		}
	}

	ex := appingest.Exception{Type: "Crash", Frames: frames}
	handled := false
	ex.Mechanism = &appingest.Mechanism{Type: "minidump", Handled: &handled}
	if d.Crash != nil {
		if d.Crash.Reason != "" {
			ex.Type = clip(d.Crash.Reason, 200)
		}
		ex.Value = fmt.Sprintf("Fatal error at 0x%x", d.Crash.Address)
		if sig, _, _ := strings.Cut(d.Crash.Reason, " /"); strings.HasPrefix(sig, "SIG") {
			ex.Mechanism.Signal = sig
		}
	}
	body.Exceptions = []appingest.Exception{ex}
	body.DebugImages = images
	body.Platform = "native"

	if body.Contexts == nil {
		body.Contexts = map[string]map[string]any{}
	}
	if name := dumpOSName(d.OS); name != "" {
		if _, ok := body.Contexts["os"]; !ok {
			body.Contexts["os"] = map[string]any{"name": name}
		}
	}
	if d.CPU != "" {
		dev := body.Contexts["device"]
		if dev == nil {
			dev = map[string]any{}
			body.Contexts["device"] = dev
		}
		if _, ok := dev["arch"]; !ok {
			dev["arch"] = d.CPU
		}
	}
	return 0, 0 // the native resolver that runs next counts the frames it resolves
}
