package sentryingest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"supermetry/internal/appingest"
)

const maxCSPReports = 20

type cspFields struct {
	Directive   string
	BlockedURI  string
	DocumentURI string
}

// cspReportEvents turns a CSP report body into stackless events. Both shapes
// are accepted: the legacy report-uri object {"csp-report": {...}} and the
// report-to array of {"type":"csp-violation","body":{...}}.
func cspReportEvents(body []byte, now time.Time) []appingest.Event {
	var reports []cspFields
	var legacy struct {
		Report map[string]any `json:"csp-report"`
	}
	if json.Unmarshal(body, &legacy) == nil && legacy.Report != nil {
		reports = append(reports, cspFromMap(legacy.Report))
	} else {
		var list []struct {
			Type string         `json:"type"`
			Body map[string]any `json:"body"`
		}
		if json.Unmarshal(body, &list) == nil {
			for _, item := range list {
				if (item.Type == "" || item.Type == "csp-violation") && item.Body != nil {
					reports = append(reports, cspFromMap(item.Body))
				}
			}
		}
	}
	if len(reports) > maxCSPReports {
		reports = reports[:maxCSPReports]
	}
	events := make([]appingest.Event, 0, len(reports))
	for _, rep := range reports {
		if rep.Directive == "" && rep.BlockedURI == "" {
			continue
		}
		events = append(events, cspEvent(rep, now))
	}
	return events
}

func cspFromMap(m map[string]any) cspFields {
	get := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	return cspFields{
		Directive:   get("effective-directive", "effectiveDirective", "violated-directive", "violatedDirective"),
		BlockedURI:  get("blocked-uri", "blockedURL", "blockedURI", "blocked-url"),
		DocumentURI: get("document-uri", "documentURL", "documentURI"),
	}
}

// blockedHost reduces a blocked URI to its host, or a CSP keyword such as
// "inline" or "eval". Paths, queries and fragments never leave this function.
func blockedHost(blocked string) string {
	if blocked == "" {
		return "unknown"
	}
	if u, err := url.Parse(blocked); err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname())
	}
	if u, err := url.Parse(blocked); err == nil && u.Scheme != "" && (u.Opaque != "" || u.Path != "") {
		return u.Scheme // data:, blob:, about: and similar
	}
	if len(blocked) > 32 || strings.ContainsAny(blocked, "/?#:") {
		return "unknown"
	}
	return strings.ToLower(blocked)
}

func cspEvent(rep cspFields, now time.Time) appingest.Event {
	directive := rep.Directive
	if f, _, ok := strings.Cut(directive, " "); ok {
		directive = f // violated-directive carries the policy text after the name
	}
	if len(directive) > 40 {
		directive = directive[:40]
	}
	host := blockedHost(rep.BlockedURI)
	frames := directive + " " + host
	var id [16]byte
	_, _ = rand.Read(id[:])
	ev := appingest.Event{
		ID: hex.EncodeToString(id[:]), TS: now, Kind: "error", Level: "error", Handled: true, Platform: "web",
		ErrorType: "csp", Frames: frames, Occurrences: 1, Tags: map[string]string{},
	}
	ev.Fingerprint = appingest.Fingerprint(ev.Kind, ev.ErrorType, ev.Frames, "")
	ev.Culprit = directive
	return ev
}
