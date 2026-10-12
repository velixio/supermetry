package platformhttp

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"supermetry/internal/apiroutes"
)

const publicAPIGroup = "/api/public/v1"

func TestOpenAPIDrift(t *testing.T) {
	t.Parallel()

	repo := findRepositoryRoot(t)
	routes, unknown, err := apiroutes.Collect(
		filepath.Join(repo, "internal/api"),
		filepath.Join(repo, "internal/platform/http"),
	)
	if err != nil {
		t.Fatalf("collect registered routes: %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("route registrations could not be resolved: %s", strings.Join(unknown, ", "))
	}
	operations, err := apiroutes.ReadOpenAPI(filepath.Join(repo, "internal/api/openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI operations: %v", err)
	}
	inventory := apiroutes.MergeInventory(routes, operations, nil)
	allowlist := readOpenAPIPrivateAllowlist(t, filepath.Join(repo, "internal/platform/http/testdata/openapi_private_allowlist.txt"))

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "public routes are documented",
			run: func(t *testing.T) {
				var missing []string
				for _, route := range inventory.Routes {
					// Methodless ServeMux subtree registrations are dispatch
					// fallbacks, not HTTP operations representable in OpenAPI.
					if route.Method != "" && route.Group == publicAPIGroup && !route.InOpenAPI {
						missing = append(missing, routeKey(route.Method, route.Pattern))
					}
				}
				if len(missing) != 0 {
					t.Errorf("public routes missing from OpenAPI:\n%s", strings.Join(missing, "\n"))
				}
			},
		},
		{
			name: "OpenAPI operations have routes",
			run: func(t *testing.T) {
				var missing []string
				for _, operation := range inventory.OpenAPIWithoutRoute {
					missing = append(missing, fmt.Sprintf("%s (operationId %q)", routeKey(operation.Method, operation.Pattern), operation.OperationID))
				}
				if len(missing) != 0 {
					t.Errorf("OpenAPI operations without registered routes:\n%s", strings.Join(missing, "\n"))
				}
			},
		},
		{
			name: "other undocumented routes are allowlisted",
			run: func(t *testing.T) {
				var missing []string
				for _, route := range inventory.Routes {
					if route.Method == "" || route.InOpenAPI || route.Group == publicAPIGroup {
						continue
					}
					key := routeKey(route.Method, route.Pattern)
					if _, ok := allowlist[key]; !ok {
						missing = append(missing, key)
					}
				}
				if len(missing) != 0 {
					t.Errorf("undocumented routes missing from private allowlist:\n%s", strings.Join(missing, "\n"))
				}
			},
		},
		{
			name: "allowlist entries match routes",
			run: func(t *testing.T) {
				registered := make(map[string]struct{}, len(inventory.Routes))
				for _, route := range inventory.Routes {
					if route.Method != "" {
						registered[routeKey(route.Method, route.Pattern)] = struct{}{}
					}
				}
				var stale []string
				for key := range allowlist {
					_, pattern, _ := strings.Cut(key, " ")
					if pattern == publicAPIGroup || strings.HasPrefix(pattern, publicAPIGroup+"/") {
						t.Errorf("public API route must not be allowlisted: %s", key)
					}
					if _, ok := registered[key]; !ok {
						stale = append(stale, key)
					}
				}
				sort.Strings(stale)
				if len(stale) != 0 {
					t.Errorf("allowlist entries without registered routes:\n%s", strings.Join(stale, "\n"))
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestOpenAPIMCPContract(t *testing.T) {
	t.Parallel()

	filename := filepath.Join(findRepositoryRoot(t), "internal/api/openapi.yaml")
	source, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read OpenAPI document: %v", err)
	}
	type schemaRef struct {
		Ref string `yaml:"$ref"`
	}
	var document struct {
		Paths map[string]struct {
			Post struct {
				RequestBody struct {
					Content map[string]struct {
						Schema schemaRef `yaml:"schema"`
					} `yaml:"content"`
				} `yaml:"requestBody"`
				Responses map[string]any `yaml:"responses"`
			} `yaml:"post"`
			Options struct {
				Responses map[string]any `yaml:"responses"`
			} `yaml:"options"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]struct {
				OneOf []schemaRef `yaml:"oneOf"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(source, &document); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}

	mcp := document.Paths["/api/public/v1/mcp"].Post
	if got := mcp.RequestBody.Content["application/json"].Schema.Ref; got != "#/components/schemas/MCPMessage" {
		t.Errorf("MCP request schema = %q, want MCPMessage", got)
	}
	for _, status := range []string{"200", "202", "400", "403", "429", "503"} {
		if _, ok := mcp.Responses[status]; !ok {
			t.Errorf("MCP response %s is missing", status)
		}
	}
	if _, ok := mcp.Responses["401"]; ok {
		t.Error("MCP response 401 is documented, but authentication failures use an HTTP 200 JSON-RPC error")
	}
	wantVariants := []string{"#/components/schemas/MCPRequest", "#/components/schemas/MCPResponse"}
	variants := document.Components.Schemas["MCPMessage"].OneOf
	if len(variants) != len(wantVariants) {
		t.Fatalf("MCPMessage variants = %#v, want %v", variants, wantVariants)
	}
	for i, want := range wantVariants {
		if variants[i].Ref != want {
			t.Errorf("MCPMessage variant %d = %q, want %q", i, variants[i].Ref, want)
		}
	}
	if _, ok := document.Paths["/api/public/v1/events"].Options.Responses["204"]; !ok {
		t.Error("event preflight response 204 is missing")
	}
}

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root containing go.mod not found")
		}
		dir = parent
	}
}

func readOpenAPIPrivateAllowlist(t *testing.T, filename string) map[string]string {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatalf("open private-route allowlist: %v", err)
	}
	defer file.Close()

	entries := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		before, reason, ok := strings.Cut(line, "  # ")
		fields := strings.Fields(before)
		if !ok || len(fields) != 2 || !strings.HasPrefix(fields[1], "/") || strings.TrimSpace(reason) == "" {
			t.Errorf("%s:%d: want METHOD /pattern  # reason", filename, lineNumber)
			continue
		}
		key := routeKey(strings.ToUpper(fields[0]), fields[1])
		if _, duplicate := entries[key]; duplicate {
			t.Errorf("%s:%d: duplicate allowlist entry %s", filename, lineNumber, key)
			continue
		}
		entries[key] = strings.TrimSpace(reason)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read private-route allowlist: %v", err)
	}
	return entries
}

func routeKey(method, pattern string) string {
	return method + " " + pattern
}
