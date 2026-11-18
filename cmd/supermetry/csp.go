package main

import "strings"

// applyCSPExtra merges operator-supplied sources (SUPERMETRY_CSP_EXTRA, e.g.
// "img-src https://cdn.example.com; connect-src https://api.example.com") into
// policy. Sources are appended to an existing directive or add a new one.
// Directives that would weaken the baseline (script-src, object-src,
// frame-ancestors, base-uri) are ignored: relax those with SUPERMETRY_CSP=off.
func applyCSPExtra(policy, extra string) string {
	if policy == "" || strings.TrimSpace(extra) == "" {
		return policy
	}
	parts := strings.Split(policy, "; ")
	for _, item := range strings.Split(extra, ";") {
		fields := strings.Fields(item)
		if len(fields) < 2 {
			continue
		}
		name := strings.ToLower(fields[0])
		switch name {
		case "script-src", "script-src-elem", "script-src-attr", "object-src", "frame-ancestors", "base-uri", "default-src":
			continue
		}
		sources := strings.Join(fields[1:], " ")
		if strings.ContainsAny(sources, ",\r\n") {
			continue
		}
		found := false
		for i, part := range parts {
			if strings.HasPrefix(part, name+" ") {
				parts[i] = part + " " + sources
				found = true
				break
			}
		}
		if !found {
			parts = append(parts, name+" "+sources)
		}
	}
	return strings.Join(parts, "; ")
}
