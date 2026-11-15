package appingest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// This file is the only place that knows the legacy "diagnostic_report" event
// of an existing desktop client: its category names and prop names. It turns
// that event into the universal app event so nothing else in the core needs
// client-specific code.

// DiagnosticEvent is the legacy event name that maps to an app report.
const DiagnosticEvent = "diagnostic_report"

// DiagnosticInput is a diagnostic_report as the legacy intake parsed it.
type DiagnosticInput struct {
	ID          string
	TimestampMS *int64
	ProfileID   string // becomes install_id
	Props       map[string]string
}

var diagnosticKinds = map[string]string{
	"app_crash": "crash", "backend_crash": "crash", "renderer_crash": "crash",
	"install_failed": "install_failed", "install_stalled": "install_stalled", "incompatibility": "incompatibility",
	"generation_failed": "operation_failed", "model_load_failed": "operation_failed", "engine_failed": "operation_failed",
	"download_failed": "operation_failed", "operation_failed": "operation_failed",
}

// diagnosticFields maps a prop to its wire field; every other prop is a tag.
var diagnosticFields = map[string]string{
	"category": "", "app_version": "release", "build_commit": "build", "platform": "platform", "arch": "arch",
	"device_family": "device_class", "error_type": "error_type", "reason": "exit_reason", "signal": "signal",
	"exit_code": "exit_code", "frames": "frames", "fingerprint": "fingerprint", "occurrences": "occurrences",
}

// priorityTags survive the 20-tag cap ahead of the rest.
var priorityTags = []string{"category", "error_code"}

// MapDiagnostic converts a diagnostic_report into an app event. The result is
// validated like any event sent to the app endpoint, so an over-long field
// makes it invalid.
func MapDiagnostic(in DiagnosticInput) (Event, error) {
	props := in.Props
	category := props["category"]
	kind, known := diagnosticKinds[category]
	if !known {
		kind = "error"
	}
	id := in.ID
	if id == "" {
		random := make([]byte, 8)
		_, _ = rand.Read(random)
		id = "gen-" + hex.EncodeToString(random)
	}
	raw := map[string]any{"id": id, "kind": kind, "handled": kind != "crash"}
	if kind == "crash" {
		raw["level"] = "fatal"
	}
	if in.TimestampMS != nil {
		raw["ts"] = json.Number(strconv.FormatInt(*in.TimestampMS, 10))
	}
	if in.ProfileID != "" {
		raw["install_id"] = in.ProfileID
	}
	for prop, field := range diagnosticFields {
		value, ok := props[prop]
		if !ok || field == "" {
			continue
		}
		switch field {
		case "platform":
			value = normalisePlatform(value)
		case "arch":
			value = normaliseArch(value)
		case "exit_code", "occurrences":
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
			if err != nil || (field == "occurrences" && n < 1) {
				continue // a malformed counter is dropped, not fatal to the report
			}
			raw[field] = json.Number(strconv.FormatInt(n, 10))
			continue
		}
		if value != "" {
			raw[field] = value
		}
	}
	raw["tags"] = diagnosticTags(props)
	return ParseEvent(raw)
}

func diagnosticTags(props map[string]string) map[string]any {
	var rest []string
	for key := range props {
		if _, consumed := diagnosticFields[key]; consumed && key != "category" {
			continue
		}
		if len(key) > maxTagKeyBytes || contains(priorityTags, key) {
			continue
		}
		rest = append(rest, key)
	}
	sort.Strings(rest)
	ordered := make([]string, 0, maxTags)
	for _, key := range priorityTags {
		if value, ok := props[key]; ok && value != "" {
			ordered = append(ordered, key)
		}
	}
	ordered = append(ordered, rest...)
	tags := make(map[string]any, maxTags)
	for _, key := range ordered {
		if len(tags) == maxTags {
			break
		}
		if value := props[key]; value != "" {
			tags[key] = cutBytes(value, maxTagValueBytes)
		}
	}
	return tags
}

func normalisePlatform(v string) string {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case "":
		return ""
	case "darwin", "mac", "macos", "osx":
		return "macos"
	case "win32", "win", "windows":
		return "windows"
	case "linux", "ios", "android", "web":
		return s
	default:
		return "other"
	}
}

func normaliseArch(v string) string {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case "":
		return ""
	case "x64", "x86_64", "amd64":
		return "x64"
	case "arm64", "aarch64":
		return "arm64"
	case "x86", "ia32", "i386", "i686":
		return "x86"
	case "arm", "arm32", "armv7", "armv7l":
		return "arm"
	default:
		return "other"
	}
}
