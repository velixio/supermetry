package sentryingest

import (
	"context"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/debugfile"
	"supermetry/internal/symcache"
)

// maxNativeFrames bounds the frames symbolicated for one event.
const maxNativeFrames = 256

// NativeResolver resolves native (ELF and Mach-O) frames through the symbol
// caches built from uploaded debug files: C, C++, Go, Swift, Objective-C and
// Rust code on Android NDK, iOS, macOS and Linux.
func NativeResolver() Resolver { return nativeResolver{} }

type nativeResolver struct{}

func nativeKind(t string) string {
	switch strings.ToLower(t) {
	case "macho", "apple":
		return "macho"
	case "elf":
		return "elf"
	}
	return ""
}

func (nativeResolver) Wants(b appingest.Body) bool {
	native := b.Platform == "native" || b.Platform == "cocoa"
	for _, im := range b.DebugImages {
		if nativeKind(im.Type) != "" {
			native = true
			break
		}
	}
	if !native {
		return false
	}
	for _, ex := range b.Exceptions {
		for _, f := range ex.Frames {
			if f.InstructionAddr != "" {
				return true
			}
		}
	}
	return false
}

// nativeImage is one debug image of the event and, once looked up, its cache.
type nativeImage struct {
	im    appingest.DebugImage
	kind  string
	id    string
	kinds []string // cache kinds to try, in order
	miss  string   // kind reported to the missing-symbols recorder
	tried bool
	cache *symcache.Cache
	file  *os.File
}

func debugIDOf(im appingest.DebugImage) string {
	if im.DebugID != "" {
		return debugfile.NormalizeDebugID(im.DebugID)
	}
	if b, err := hex.DecodeString(im.CodeID); err == nil && len(b) > 0 {
		return debugfile.DebugIDFromBuildID(b)
	}
	return ""
}

func parseFrameAddr(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(strings.ToLower(s), "0x"); ok {
		v, err := strconv.ParseUint(rest, 16, 64)
		return v, err == nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	return v, err == nil
}

// nativeInApp says whether an image belongs to the app rather than the system.
func nativeInApp(codeFile string) bool {
	for _, m := range []string{"/Bundle/Application/", ".app/", "/data/app/"} {
		if strings.Contains(codeFile, m) {
			return true
		}
	}
	return false
}

func (nativeResolver) Resolve(ctx context.Context, ev appingest.Event, body *appingest.Body, s *Symbolicator) (attempted, resolved int) {
	var imgs []*nativeImage
	for _, im := range body.DebugImages {
		if im.Minidump {
			// Minidump images carry Breakpad ids: .sym caches first, then the
			// native debug files of the same id.
			ni := &nativeImage{im: im, kind: nativeKind(im.Type), id: debugIDOf(im), kinds: []string{"breakpad"}, miss: "breakpad"}
			if ni.kind != "" {
				ni.kinds = append(ni.kinds, ni.kind)
			}
			imgs = append(imgs, ni)
		} else if k := nativeKind(im.Type); k != "" {
			imgs = append(imgs, &nativeImage{im: im, kind: k, id: debugIDOf(im), kinds: []string{k}, miss: k})
		}
	}
	defer func() {
		for _, ni := range imgs {
			if ni.file != nil {
				ni.file.Close()
			}
		}
	}()
	findImage := func(addr uint64, pkg string) *nativeImage {
		for _, ni := range imgs {
			if ni.im.ImageSize > 0 && addr >= ni.im.ImageAddr && addr-ni.im.ImageAddr < ni.im.ImageSize {
				return ni
			}
		}
		if pkg != "" {
			for _, ni := range imgs {
				if ni.im.CodeFile == pkg {
					return ni
				}
			}
		}
		return nil
	}
	cacheOf := func(ni *nativeImage) *symcache.Cache {
		if ni.tried {
			return ni.cache
		}
		ni.tried = true
		if ni.id == "" {
			return nil
		}
		var key string
		var found bool
		for _, k := range ni.kinds {
			if key, found = s.cacheFor(ctx, body.SiteID, k, ni.id); found {
				break
			}
		}
		if !found && ni.im.Minidump {
			key, found = s.remoteCache(ctx, body.SiteID, ni.im)
		}
		if !found {
			if s.cfg.Misses != nil {
				s.cfg.Misses.Missing(body.SiteID, ni.miss, ni.im, time.Now())
			}
			return nil
		}
		f, size, err := s.Cache(ctx, key)
		if err != nil {
			return nil
		}
		c, err := symcache.Open(f, size)
		if err != nil {
			f.Close()
			return nil
		}
		ni.file, ni.cache = f, c
		return c
	}

	budget := maxNativeFrames
	for i := range body.Exceptions {
		ex := &body.Exceptions[i]
		crashed := !(ex.Mechanism != nil && ex.Mechanism.Handled != nil && *ex.Mechanism.Handled)
		out := make([]appingest.Frame, 0, len(ex.Frames))
		for k, f := range ex.Frames {
			addr, ok := parseFrameAddr(f.InstructionAddr)
			if !ok || budget == 0 {
				out = append(out, f)
				continue
			}
			ni := findImage(addr, f.Package)
			if ni == nil {
				out = append(out, f)
				continue
			}
			budget--
			attempted++
			c := cacheOf(ni)
			if c == nil || addr < ni.im.ImageAddr {
				out = append(out, f)
				continue
			}
			rel := addr - ni.im.ImageAddr
			// A frame that is not the crashing instruction holds a return
			// address, the first byte after the call: look up the call itself.
			if !(crashed && k == len(ex.Frames)-1) && rel > 0 {
				rel--
			}
			locs, ok := c.Lookup(rel)
			if !ok {
				out = append(out, f)
				continue
			}
			resolved++
			inApp := f.InApp || nativeInApp(ni.im.CodeFile)
			// Frames run oldest call first, so the outermost function leads.
			for j := len(locs) - 1; j >= 0; j-- {
				nf := f
				nf.Function = clip(locs[j].Function, 200)
				nf.Filename = scrubFilename(locs[j].File)
				nf.Lineno = int(locs[j].Line)
				nf.InApp = inApp
				out = append(out, nf)
			}
		}
		ex.Frames = out
	}
	return attempted, resolved
}
