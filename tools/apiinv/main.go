package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
)

func main() {
	hitsFile := flag.String("hits", "", "route-hit file produced by SUPERMETRY_ROUTE_HITS")
	outDir := flag.String("out", "docs/api", "directory for inventory.json and inventory.md")
	flag.Parse()
	if err := run(*hitsFile, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(hitsFile, outDir string) error {
	var routes []Route
	var unknown []string
	for _, dir := range []string{"internal/api", "internal/platform/http"} {
		found, unresolved, err := collectRoutes(dir)
		if err != nil {
			return fmt.Errorf("collect %s: %w", dir, err)
		}
		routes = append(routes, found...)
		unknown = append(unknown, unresolved...)
	}
	routes = deduplicateRoutes(routes)
	operations, err := readOpenAPI("internal/api/openapi.yaml")
	if err != nil {
		return fmt.Errorf("read OpenAPI: %w", err)
	}
	hits, err := readHits(hitsFile)
	if err != nil {
		return fmt.Errorf("read route hits: %w", err)
	}
	inventory := mergeInventory(routes, operations, hits)
	inventory.UnknownRegistrations = uniqueStrings(unknown)
	if err := writeInventory(outDir, inventory); err != nil {
		return fmt.Errorf("write inventory: %w", err)
	}
	return nil
}

func deduplicateRoutes(routes []Route) []Route {
	byPattern := make(map[string]Route)
	for _, route := range routes {
		key := route.Method + "\x00" + route.Pattern
		byPattern[key] = mergeRoute(byPattern[key], route)
	}
	result := make([]Route, 0, len(byPattern))
	for _, route := range byPattern {
		result = append(result, route)
	}
	sortRoutes(result)
	return result
}

func uniqueStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortStrings(values []string) { sort.Strings(values) }
