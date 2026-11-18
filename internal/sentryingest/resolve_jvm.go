package sentryingest

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/debugfile"
	"supermetry/internal/proguard"
)

// maxJVMFrames bounds the frames of one event that are deobfuscated.
const maxJVMFrames = 256

// BuildProguardCache converts an uploaded ProGuard or R8 mapping into the
// index the JVM resolver reads. It is the cache builder for kind "proguard".
func BuildProguardCache(_ context.Context, _ debugfile.Object, raw io.ReaderAt, size int64, out io.Writer) ([]string, error) {
	if err := proguard.BuildIndex(io.NewSectionReader(raw, 0, size), out); err != nil {
		return nil, err
	}
	return []string{"mapping"}, nil
}

type jvmResolver struct{}

// JVMResolver deobfuscates Java and Kotlin frames and exception classes
// through an uploaded ProGuard or R8 mapping, found by the mapping UUID the
// SDK reports in its debug images.
func JVMResolver() Resolver { return jvmResolver{} }

func proguardImages(b appingest.Body) []appingest.DebugImage {
	var out []appingest.DebugImage
	for _, im := range b.DebugImages {
		if im.Type == "proguard" && im.UUID != "" {
			out = append(out, im)
		}
	}
	return out
}

func (jvmResolver) Wants(b appingest.Body) bool {
	if len(proguardImages(b)) == 0 {
		return false
	}
	for _, ex := range b.Exceptions {
		for _, f := range ex.Frames {
			if f.Module != "" {
				return true
			}
		}
	}
	return false
}

func (jvmResolver) Resolve(ctx context.Context, ev appingest.Event, b *appingest.Body, s *Symbolicator) (attempted, resolved int) {
	imgs := proguardImages(*b)
	var ix *proguard.Index
	for _, im := range imgs {
		key, found := s.cacheFor(ctx, b.SiteID, "proguard", im.UUID)
		if !found {
			continue
		}
		f, size, err := s.Cache(ctx, key)
		if err != nil {
			slog.Warn("sentryingest: open proguard cache", "err", err)
			s.cfg.Stats.Add("symbolication_error", 1)
			continue
		}
		defer f.Close()
		if ix, err = proguard.OpenIndex(f, size); err != nil {
			slog.Warn("sentryingest: read proguard cache", "err", err)
			s.cfg.Stats.Add("symbolication_error", 1)
			ix = nil
			continue
		}
		break
	}
	if ix == nil {
		// Every image lacks a mapping: tell the banner, leave frames as they are.
		if s.cfg.Misses != nil {
			at := b.TS
			if at.IsZero() {
				at = time.Now()
			}
			for _, im := range imgs {
				s.cfg.Misses.Missing(b.SiteID, "proguard", im, at)
			}
		}
		return missingJVMFrames(b), 0
	}

	budget := maxJVMFrames
	for i := range b.Exceptions {
		ex := &b.Exceptions[i]
		if full := joinClass(ex.Module, ex.Type); full != "" {
			if orig, ok := ix.Class(full); ok && orig != full {
				ex.Module, ex.Type = splitClass(orig)
			}
		}
		frames := make([]appingest.Frame, 0, len(ex.Frames))
		for _, f := range ex.Frames {
			if f.Module == "" || budget == 0 {
				frames = append(frames, f)
				continue
			}
			budget--
			out := ix.Remap(f.Module, f.Function, f.Lineno)
			if out == nil {
				frames = append(frames, f)
				continue
			}
			resolved++
			// Remap is innermost first, frames are stored oldest call first.
			for k := len(out) - 1; k >= 0; k-- {
				nf := f
				nf.Module, nf.Function = clip(out[k].Class, 200), clip(out[k].Method, 200)
				nf.Filename, nf.Lineno = scrubFilename(out[k].File), out[k].Line
				frames = append(frames, nf)
			}
		}
		ex.Frames = frames
	}
	// A mapping only knows the app's own classes; frames of the platform and
	// libraries are not obfuscated, so they never count against the result.
	return resolved, resolved
}

// missingJVMFrames counts the frames a mapping would have been needed for:
// the app's own, or all when no frame is marked in-app.
func missingJVMFrames(b *appingest.Body) int {
	all, inApp := 0, 0
	for _, ex := range b.Exceptions {
		for _, f := range ex.Frames {
			if f.Module == "" {
				continue
			}
			all++
			if f.InApp {
				inApp++
			}
		}
	}
	if inApp > 0 {
		return inApp
	}
	return all
}

func joinClass(module, typ string) string {
	switch {
	case typ == "":
		return ""
	case module == "":
		return typ
	}
	return module + "." + typ
}

func splitClass(full string) (module, typ string) {
	if i := strings.LastIndexByte(full, '.'); i >= 0 {
		return full[:i], full[i+1:]
	}
	return "", full
}
