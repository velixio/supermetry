package appingest

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Grouping versions. Version 1 is Fingerprint (the app-events wire format);
// version 2 is GroupingKey (Sentry events). An event keeps the version it was
// grouped with, so changing v2 never regroups stored v1 events.
const (
	GroupingVersionV1       int16 = 1
	GroupingVersion         int16 = 2
	groupingInAppFrameLimit       = 10
	groupingChainLimit            = 5
)

var (
	digitRun    = regexp.MustCompile(`\d+`)
	spaceRun    = regexp.MustCompile(`\s+`)
	defaultPart = map[string]struct{}{"{{ default }}": {}, "{{default}}": {}}
)

// GroupingKey computes the version 2 issue fingerprint of a scrubbed event.
//
// Default grouping hashes, per exception in the chain, the type and the
// innermost in-app frames (module or file, and function; never line numbers).
// When no frame is in-app all frames count. An exception without frames, or
// an event without exceptions, groups by kind, type and a template of the
// scrubbed text (digits replaced), so "Order 12 failed" and "Order 9 failed"
// meet. An SDK fingerprint replaces the default; the element
// "{{ default }}" stands for it, so ["{{ default }}", "tenant-x"] splits by
// tenant. body must already be scrubbed (the normaliser does that).
func GroupingKey(ev Event, body Body, sdkFingerprint []string) (string, int16) {
	var parts []string
	if len(sdkFingerprint) == 0 {
		parts = []string{defaultGrouping(ev, body)}
	} else {
		for _, p := range sdkFingerprint {
			if _, ok := defaultPart[strings.TrimSpace(p)]; ok {
				p = defaultGrouping(ev, body)
			}
			parts = append(parts, "c:"+p)
		}
	}
	sum := sha256.Sum256([]byte("v2\x00" + strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:]), GroupingVersion
}

func defaultGrouping(ev Event, body Body) string {
	var b strings.Builder
	b.WriteString("d:" + ev.Kind)
	exceptions := body.Exceptions
	if len(exceptions) > groupingChainLimit {
		exceptions = exceptions[len(exceptions)-groupingChainLimit:]
	}
	for _, ex := range exceptions {
		b.WriteString("\x01" + ex.Type)
		frames := groupingFrames(ex.Frames)
		if len(frames) == 0 {
			b.WriteString("\x02" + template(ex.Value))
			continue
		}
		for _, f := range frames {
			b.WriteString("\x03" + frameKey(f))
		}
	}
	if len(exceptions) == 0 {
		b.WriteString("\x04" + template(body.Message))
	}
	return b.String()
}

// groupingFrames returns the innermost in-app frames (all frames when none is
// in-app), innermost first. SDKs send the oldest call first.
func groupingFrames(frames []Frame) []Frame {
	var pick []Frame
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].InApp {
			pick = append(pick, frames[i])
		}
	}
	if len(pick) == 0 {
		for i := len(frames) - 1; i >= 0; i-- {
			pick = append(pick, frames[i])
		}
	}
	if len(pick) > groupingInAppFrameLimit {
		pick = pick[:groupingInAppFrameLimit]
	}
	return pick
}

func frameKey(f Frame) string {
	where := f.Module
	if where == "" {
		where = f.Filename
	}
	if where == "" {
		where = f.Package
	}
	return where + "|" + f.Function
}

// template normalises scrubbed text for stackless grouping.
func template(s string) string {
	s = digitRun.ReplaceAllString(s, "#")
	return strings.TrimSpace(spaceRun.ReplaceAllString(s, " "))
}
