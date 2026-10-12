package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"supermetry/internal/apiroutes"
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
	routes, unknown, err := apiroutes.Collect("internal/api", "internal/platform/http")
	if err != nil {
		return err
	}
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
