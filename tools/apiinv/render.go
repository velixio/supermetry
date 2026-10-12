package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeInventory(outDir string, inventory Inventory) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(inventory.Routes, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(outDir, "inventory.json"), encoded, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "inventory.md"), []byte(renderMarkdown(inventory)), 0o644)
}

func renderMarkdown(inventory Inventory) string {
	documented, hit := 0, 0
	for _, route := range inventory.Routes {
		if route.InOpenAPI {
			documented++
		}
		if len(route.HitByTests) > 0 {
			hit++
		}
	}
	var report strings.Builder
	report.WriteString("# API route inventory\n\n")
	report.WriteString("| Measure | Total |\n|---|---:|\n")
	fmt.Fprintf(&report, "| Registered routes | %d |\n", len(inventory.Routes))
	fmt.Fprintf(&report, "| Documented routes | %d |\n", documented)
	fmt.Fprintf(&report, "| Routes hit by tests | %d |\n", hit)

	writeRouteSection(&report, "Routes not hit by tests", inventory.Routes, func(route Route) bool { return len(route.HitByTests) == 0 })
	writeRouteSection(&report, "Routes missing from OpenAPI", inventory.Routes, func(route Route) bool { return !route.InOpenAPI })

	report.WriteString("\n## OpenAPI operations with no route\n\n")
	if len(inventory.OpenAPIWithoutRoute) == 0 {
		report.WriteString("None.\n")
	} else {
		report.WriteString("| Method | Pattern | Operation |\n|---|---|---|\n")
		for _, operation := range inventory.OpenAPIWithoutRoute {
			fmt.Fprintf(&report, "| %s | `%s` | `%s` |\n", operation.Method, escapeCell(operation.Pattern), escapeCell(operation.OperationID))
		}
	}

	writeRouteSection(&report, "Unknown auth derivation", inventory.Routes, func(route Route) bool { return route.Auth == "unknown" })
	if len(inventory.UnknownRegistrations) > 0 {
		report.WriteString("\n## Registration expressions requiring runtime configuration\n\n")
		for _, registration := range inventory.UnknownRegistrations {
			fmt.Fprintf(&report, "- `%s`\n", escapeCell(registration))
		}
	}
	return report.String()
}

func writeRouteSection(report *strings.Builder, title string, routes []Route, include func(Route) bool) {
	report.WriteString("\n## " + title + "\n")
	groups := make(map[string][]Route)
	for _, route := range routes {
		if include(route) {
			groups[route.Group] = append(groups[route.Group], route)
		}
	}
	if len(groups) == 0 {
		report.WriteString("\nNone.\n")
		return
	}
	for _, group := range sortedRouteGroups(groups) {
		fmt.Fprintf(report, "\n### `%s`\n\n", escapeCell(group))
		report.WriteString("| Method | Pattern | Handler | Auth |\n|---|---|---|---|\n")
		for _, route := range groups[group] {
			method := route.Method
			if method == "" {
				method = "*"
			}
			fmt.Fprintf(report, "| %s | `%s` | `%s` | %s |\n", method, escapeCell(route.Pattern), escapeCell(route.Handler), route.Auth)
		}
	}
}

func sortedRouteGroups(groups map[string][]Route) []string {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func escapeCell(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}
