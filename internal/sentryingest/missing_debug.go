package sentryingest

import (
	"context"
	"strings"

	"supermetry/internal/appingest"
	"supermetry/internal/debugfile"
)

// maxMissingDebug caps the debug images one event names in its detail.
const maxMissingDebug = 5

// systemImagePrefixes are loader paths of OS libraries, which users never
// upload symbols for; the banner must not ask for them.
var systemImagePrefixes = []string{"/system/", "/usr/lib/", "/apex/", "/vendor/", "/lib/", "/lib64/", "/usr/lib64/"}

func systemImage(codeFile string) bool {
	f := strings.ToLower(strings.ReplaceAll(codeFile, `\`, "/"))
	for _, p := range systemImagePrefixes {
		if strings.HasPrefix(f, p) {
			return true
		}
	}
	base := f[strings.LastIndex(f, "/")+1:]
	return base == "libc.so" || strings.HasPrefix(base, "libsystem_")
}

// missingDebug lists the debug images the event's frames point into that no
// uploaded file covers, for the banner on the event detail: native images
// holding a frame's instruction address (or named by its package) and ProGuard
// mappings of events with obfuscated frames. Only called for events that came
// out missing or partial, so the lookups (served by the lookup TTL cache) cost
// nothing on the healthy path. Names and ids only, never paths: the code file
// is reduced to its scrubbed base name.
func (s *Symbolicator) missingDebug(ctx context.Context, b appingest.Body) []appingest.MissingDebug {
	var out []appingest.MissingDebug
	seen := map[string]bool{}
	add := func(kind, id, file, arch string) {
		if id == "" || seen[kind+"|"+id] || len(out) >= maxMissingDebug {
			return
		}
		seen[kind+"|"+id] = true
		out = append(out, appingest.MissingDebug{Kind: kind, DebugID: id, CodeFile: file, Arch: arch})
	}
	for _, im := range b.DebugImages {
		kind, raw := im.Type, im.DebugID
		switch kind {
		case "proguard":
			raw = im.UUID
		case "macho", "elf":
			if systemImage(im.CodeFile) || !referenced(b, im) {
				continue
			}
		default:
			continue
		}
		id := debugfile.NormalizeDebugID(raw)
		if id == "" {
			continue
		}
		if _, found := s.cacheFor(ctx, b.SiteID, kind, id); found {
			continue
		}
		add(kind, id, baseFile(im.CodeFile), im.Arch)
	}
	return out
}

// referenced reports whether any frame of the event lies in the image.
func referenced(b appingest.Body, im appingest.DebugImage) bool {
	for _, ex := range b.Exceptions {
		for _, f := range ex.Frames {
			if f.InstructionAddr != "" && im.ImageSize > 0 {
				if a := parseAddr(f.InstructionAddr); a >= im.ImageAddr && a-im.ImageAddr < im.ImageSize {
					return true
				}
			}
			if f.Package != "" && f.Package == im.CodeFile {
				return true
			}
		}
	}
	return false
}

func baseFile(codeFile string) string {
	s := scrubFilename(strings.ReplaceAll(codeFile, `\`, "/"))
	return s[strings.LastIndex(s, "/")+1:]
}
