// Package appattach stores and serves the attachments of app events.
//
// Attachments are attacker-controlled bytes. Prepare decides what may be kept:
// only PNG, JPEG and WebP images (verified by content, not by the declared
// type) and text, which is scrubbed first. Everything else keeps its metadata
// only. Stored content types come from a closed set, so nothing active (HTML,
// SVG, scripts) is ever stored under a type a browser would render.
package appattach

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

// Limits. Every attachment is already inside the envelope size cap; these
// bound what is stored.
const (
	MaxFile     = 1 << 20
	MaxEvent    = 5 << 20
	MaxPerEvent = 32
	MaxNameLen  = 128

	// maxScrubLine is the longest run handed to the scrubber at once.
	maxScrubLine = 32 << 10
)

// Stored content types.
const (
	TypeText = "text/plain"
	TypeJSON = "application/json"
	TypePNG  = "image/png"
	TypeJPEG = "image/jpeg"
	TypeWebP = "image/webp"
)

// Meta describes one attachment of an event; it is part of the event body.
// Its position in the list is the attachment index used to download it.
type Meta struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // the SDK's attachment_type
	ContentType string `json:"content_type,omitempty"`
	Size        int    `json:"size"`
	Stored      bool   `json:"stored"`
}

// File is attachment content ready to store.
type File struct {
	Idx         int16
	Name        string
	ContentType string
	Data        []byte
}

// Input is one attachment item of an envelope.
type Input struct {
	Name           string
	ContentType    string
	AttachmentType string
	Payload        []byte
}

// Result is what Prepare decided.
type Result struct {
	Metas       []Meta
	Files       []File
	TooLarge    int // not stored for size: over the file or the event cap
	Unsupported int // not stored for type or content
	Minidump    int // minidumps are never stored, whatever their size
}

// isMinidump reports a native crash dump, which is processed separately and
// never stored raw.
func isMinidump(a Input) bool {
	return a.AttachmentType == "event.minidump" || strings.HasSuffix(strings.ToLower(strings.TrimSpace(a.Name)), ".dmp")
}

// Prepare classifies, scrubs and caps the attachments of one event. scrub
// removes personal data from text.
func Prepare(in []Input, scrub func(string) string) Result {
	var res Result
	var stored int
	for i, a := range in {
		if i >= MaxPerEvent {
			break
		}
		m := Meta{Name: cleanName(a.Name, scrub), Type: clip(a.AttachmentType, 64), Size: len(a.Payload)}
		ct, ok := classify(a)
		switch {
		case isMinidump(a):
			res.Minidump++
		case len(a.Payload) > MaxFile || stored+len(a.Payload) > MaxEvent:
			res.TooLarge++
		case !ok:
			res.Unsupported++
		default:
			data, ct, ok := finish(a.Payload, ct, scrub)
			if !ok {
				res.Unsupported++
				break
			}
			m.ContentType, m.Stored = ct, true
			stored += len(data)
			res.Files = append(res.Files, File{Idx: int16(i), Name: m.Name, ContentType: ct, Data: data})
		}
		res.Metas = append(res.Metas, m)
	}
	return res
}

// Inline reports whether a stored content type may be shown in the page.
func Inline(ct string) bool {
	return ct == TypePNG || ct == TypeJPEG || ct == TypeWebP
}

// Allowed reports whether ct is one of the stored content types.
func Allowed(ct string) bool {
	switch ct {
	case TypeText, TypeJSON, TypePNG, TypeJPEG, TypeWebP:
		return true
	}
	return false
}

// classify maps an attachment to a stored type, or reports it unsupported.
// The declared type only chooses between text and image; images must also
// look like what they claim.
func classify(a Input) (string, bool) {
	if a.AttachmentType == "event.minidump" {
		return "", false
	}
	if a.AttachmentType == "event.view_hierarchy" {
		return TypeJSON, true
	}
	declared := strings.ToLower(strings.TrimSpace(strings.SplitN(a.ContentType, ";", 2)[0]))
	switch {
	case declared == "application/json":
		return TypeJSON, true
	case strings.HasPrefix(declared, "text/"):
		return TypeText, true
	case strings.HasPrefix(declared, "image/") || declared == "" || declared == "application/octet-stream":
		generic := declared == "" || declared == "application/octet-stream"
		if sniffed := http.DetectContentType(head(a.Payload)); Inline(sniffed) && (generic || declared == sniffed) {
			return sniffed, true
		}
		if generic {
			switch strings.ToLower(path.Ext(a.Name)) {
			case ".txt", ".log":
				return TypeText, true
			case ".json":
				return TypeJSON, true
			}
		}
	}
	return "", false
}

func head(b []byte) []byte {
	if len(b) > 512 {
		return b[:512]
	}
	return b
}

// finish scrubs text and returns the bytes to store. Images pass untouched.
func finish(data []byte, ct string, scrub func(string) string) ([]byte, string, bool) {
	if Inline(ct) {
		return data, ct, true
	}
	if !utf8.Valid(data) {
		return nil, "", false
	}
	clean := scrubLines(string(data), scrub)
	if ct == TypeJSON && !json.Valid([]byte(clean)) {
		ct = TypeText
	}
	return []byte(clean), ct, true
}

// scrubLines scrubs line by line, so the scrubber's input cap cannot silently
// drop the tail of a large file; a longer line is cut into pieces.
func scrubLines(s string, scrub func(string) string) string {
	var b strings.Builder
	b.Grow(len(s))
	first := true
	for line := range strings.SplitSeq(s, "\n") {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		for len(line) > maxScrubLine {
			cut := maxScrubLine
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			b.WriteString(scrub(line[:cut]))
			line = line[cut:]
		}
		b.WriteString(scrub(line))
	}
	return b.String()
}

// cleanName keeps a file name safe to show and to put in a header: base name
// only, no control characters or quotes, scrubbed, bounded.
func cleanName(name string, scrub func(string) string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = clip(scrub(name), MaxNameLen)
	if name == "" {
		return "attachment"
	}
	return name
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
