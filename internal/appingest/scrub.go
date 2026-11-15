// Package appingest accepts app health reports: crashes, errors and install
// failures from desktop, mobile and backend apps. It validates and scrubs each
// event, groups it into an issue and stores it without ever persisting raw
// install or session ids.
package appingest

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxFramesBytes     = 4096
	fingerprintFrames  = 5
	maxFingerprintHex  = 64
	maxTitleFrameBytes = 120
)

var (
	fileURLDrive = regexp.MustCompile(`(?i)file:///?([A-Za-z]:[\\/])`)
	fileURL      = regexp.MustCompile(`(?i)file://`)
	// absolutePath finds an absolute Unix or Windows path that starts a token.
	// A path stops at whitespace, a colon (the ":line:col" suffix), brackets and quotes.
	absolutePath = regexp.MustCompile(`(^|[\s("'\[=])((?:[A-Za-z]:[\\/]|/)[^\s:()"'\[\]]*)`)
	homePath     = regexp.MustCompile(`(?i)^(?:[a-z]:/users|/users|/home)/[^/]+/(.*)$`)
	lineNumbers  = regexp.MustCompile(`(?::\d+)+`)
	hexString    = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// ScrubFrames removes personal data from a stack-frame block, one line at a
// time: home folders become "~/", any other absolute path keeps only its last
// three segments, file:// prefixes go, and the result is cut to 4 KB on a line
// boundary.
func ScrubFrames(frames string) string {
	var lines []string
	size := 0
	for _, line := range strings.Split(frames, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = scrubLine(line)
		add := len(line)
		if len(lines) > 0 {
			add++
		}
		if size+add > maxFramesBytes {
			if len(lines) == 0 {
				return cutBytes(line, maxFramesBytes)
			}
			break
		}
		size += add
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func scrubLine(line string) string {
	line = fileURLDrive.ReplaceAllString(line, "$1")
	line = fileURL.ReplaceAllString(line, "")
	return absolutePath.ReplaceAllStringFunc(line, func(match string) string {
		sub := absolutePath.FindStringSubmatch(match)
		return sub[1] + scrubPath(sub[2])
	})
}

func scrubPath(path string) string {
	slashed := strings.ReplaceAll(path, `\`, "/")
	if m := homePath.FindStringSubmatch(slashed); m != nil {
		return "~/" + m[1]
	}
	var segments []string
	for _, segment := range strings.Split(slashed, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return path
	}
	if len(segments) > 3 {
		segments = segments[len(segments)-3:]
	}
	return strings.Join(segments, "/")
}

func cutBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// normalisedFrames returns the top frames with line numbers stripped.
func normalisedFrames(frames string, limit int) []string {
	var out []string
	for _, line := range strings.Split(frames, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, lineNumbers.ReplaceAllString(line, ""))
		if len(out) == limit {
			break
		}
	}
	return out
}

// Fingerprint groups reports into an issue. A client value is used when it is
// a hex string of at most 64 characters; otherwise it is the sha256 of the
// kind, the error type and the top five frames without line numbers. Pass
// scrubbed frames.
func Fingerprint(kind, errorType, scrubbedFrames, client string) string {
	if client != "" && len(client) <= maxFingerprintHex && hexString.MatchString(client) {
		return strings.ToLower(client)
	}
	input := kind + "|" + errorType + "|" + strings.Join(normalisedFrames(scrubbedFrames, fingerprintFrames), "\n")
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

// IssueTitle derives a title from the error type and top frame, never from a
// free-text message.
func IssueTitle(kind, errorType, scrubbedFrames string) string {
	subject := errorType
	if subject == "" {
		subject = kind
	}
	top := normalisedFrames(scrubbedFrames, 1)
	if len(top) == 0 {
		return subject
	}
	frame := cutBytes(top[0], maxTitleFrameBytes)
	return subject + " in " + frame
}
