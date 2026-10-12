package apiroutes

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

var httpMethods = map[string]struct{}{
	"get": {}, "post": {}, "put": {}, "patch": {}, "delete": {}, "options": {}, "head": {}, "trace": {},
}

// ReadOpenAPI reads the HTTP operations declared by an OpenAPI document.
func ReadOpenAPI(filename string) ([]Operation, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var document struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	var operations []Operation
	for pattern, item := range document.Paths {
		for method, node := range item {
			if _, ok := httpMethods[strings.ToLower(method)]; !ok {
				continue
			}
			var operation struct {
				OperationID string `yaml:"operationId"`
			}
			if err := node.Decode(&operation); err != nil {
				return nil, fmt.Errorf("decode %s %s: %w", method, pattern, err)
			}
			operations = append(operations, Operation{Method: strings.ToUpper(method), Pattern: pattern, OperationID: operation.OperationID, canonical: canonicalPattern(pattern)})
		}
	}
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].Pattern == operations[j].Pattern {
			return operations[i].Method < operations[j].Method
		}
		return operations[i].Pattern < operations[j].Pattern
	})
	return operations, nil
}

// ReadHits reads the optional route-hit recorder output.
func ReadHits(filename string) (HitSet, error) {
	hits := make(HitSet)
	if filename == "" {
		return hits, nil
	}
	file, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) {
		return hits, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		pattern, testName, _ := strings.Cut(scanner.Text(), "\t")
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if hits[pattern] == nil {
			hits[pattern] = make(map[string]struct{})
		}
		if testName == "" {
			testName = "(unnamed test)"
		}
		hits[pattern][testName] = struct{}{}
	}
	return hits, scanner.Err()
}

// MergeInventory annotates routes with OpenAPI and test-hit coverage.
func MergeInventory(routes []Route, operations []Operation, hits HitSet) Inventory {
	matchedOperations := make(map[int]bool)
	for i := range routes {
		route := &routes[i]
		if route.HitByTests == nil {
			route.HitByTests = []string{}
		}
		route.canonical = canonicalPattern(route.Pattern)
		for operationIndex, operation := range operations {
			if route.Method == operation.Method && routeMatchesOperation(*route, operation) {
				route.InOpenAPI = true
				matchedOperations[operationIndex] = true
			}
		}
		fullPattern := route.Method
		if fullPattern != "" {
			fullPattern += " "
		}
		fullPattern += route.Pattern
		for testName := range hits[fullPattern] {
			route.HitByTests = append(route.HitByTests, testName)
		}
		sort.Strings(route.HitByTests)
	}
	var missing []Operation
	for i, operation := range operations {
		if !matchedOperations[i] {
			missing = append(missing, operation)
		}
	}
	return Inventory{Routes: routes, OpenAPIWithoutRoute: missing}
}

func routeMatchesOperation(route Route, operation Operation) bool {
	operationCanonical := operation.canonical
	if operationCanonical == "" {
		operationCanonical = canonicalPattern(operation.Pattern)
	}
	if route.canonical == operationCanonical {
		return true
	}
	if !strings.Contains(route.Pattern, "...") {
		return false
	}
	prefix := route.Pattern[:strings.LastIndex(route.Pattern, "{")]
	return strings.HasPrefix(operationCanonical, canonicalPattern(prefix))
}

func canonicalPattern(pattern string) string {
	var result strings.Builder
	for i := 0; i < len(pattern); {
		if pattern[i] != '{' {
			result.WriteByte(pattern[i])
			i++
			continue
		}
		end := strings.IndexByte(pattern[i:], '}')
		if end < 0 {
			result.WriteString(pattern[i:])
			break
		}
		result.WriteString("{}")
		i += end + 1
	}
	return result.String()
}

func routeGroup(pattern string) string {
	parts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	if len(parts) == 1 && parts[0] == "" {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

func sortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Group != routes[j].Group {
			return routes[i].Group < routes[j].Group
		}
		if routes[i].Pattern != routes[j].Pattern {
			return routes[i].Pattern < routes[j].Pattern
		}
		return routes[i].Method < routes[j].Method
	})
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
