package api_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestOpenAPIEventsEndpoint(t *testing.T) {
	source, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var document struct {
		Paths map[string]struct {
			Post *struct {
				RequestBody struct {
					Content map[string]struct {
						Schema struct {
							Ref string `yaml:"$ref"`
						}
					} `yaml:"content"`
				} `yaml:"requestBody"`
				Responses map[string]any `yaml:"responses"`
			} `yaml:"post"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]struct {
				Required   []string             `yaml:"required"`
				Properties map[string]yaml.Node `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(source, &document); err != nil {
		t.Fatal(err)
	}

	operation := document.Paths["/api/public/v1/events"].Post
	if operation == nil {
		t.Fatal("POST /api/public/v1/events is missing")
	}
	for _, status := range []string{"202", "400", "429", "503"} {
		if _, ok := operation.Responses[status]; !ok {
			t.Errorf("response %s is missing", status)
		}
	}

	const schemaRef = "#/components/schemas/EventPayload"
	if got := operation.RequestBody.Content["application/json"].Schema.Ref; got != schemaRef {
		t.Fatalf("request schema = %q, want %q", got, schemaRef)
	}
	schema := document.Components.Schemas["EventPayload"]
	for _, field := range []string{"site", "name"} {
		if !slices.Contains(schema.Required, field) {
			t.Errorf("required field %q is missing", field)
		}
	}
	wantProperties := []string{
		"id", "ts", "v", "site", "name", "url", "referrer", "screen_width", "props",
		"engagement_ms", "scroll_pct", "revenue", "traits", "profile_id",
	}
	if len(schema.Properties) != len(wantProperties) {
		t.Fatalf("properties = %v, want exactly %v", mapKeys(schema.Properties), wantProperties)
	}
	for _, property := range wantProperties {
		if _, ok := schema.Properties[property]; !ok {
			t.Errorf("property %q is missing", property)
		}
	}
	type propertySchema struct {
		Type      string `yaml:"type"`
		Format    string `yaml:"format"`
		MinLength int    `yaml:"minLength"`
		MaxLength int    `yaml:"maxLength"`
		Pattern   string `yaml:"pattern"`
		Enum      []int  `yaml:"enum"`
	}
	decodeProperty := func(name string) propertySchema {
		t.Helper()
		var property propertySchema
		node := schema.Properties[name]
		if err := node.Decode(&property); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
		return property
	}
	if id := decodeProperty("id"); id.Type != "string" || id.MinLength != 1 || id.MaxLength != 64 || id.Pattern != "^[A-Za-z0-9_-]+$" {
		t.Errorf("id schema = %+v, want optional 1-64 character bounded identifier", id)
	}
	if ts := decodeProperty("ts"); ts.Type != "integer" || ts.Format != "int64" {
		t.Errorf("ts schema = %+v, want int64 integer", ts)
	}
	if version := decodeProperty("v"); version.Type != "integer" || !slices.Equal(version.Enum, []int{1}) {
		t.Errorf("v schema = %+v, want integer enum [1]", version)
	}
	for _, field := range []string{"id", "ts", "v"} {
		if slices.Contains(schema.Required, field) {
			t.Errorf("field %q must remain optional", field)
		}
	}
}

func TestOpenAPISessionsEndpoints(t *testing.T) {
	source, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var document struct {
		Paths map[string]struct {
			Get *struct {
				Description string         `yaml:"description"`
				Responses   map[string]any `yaml:"responses"`
			} `yaml:"get"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(source, &document); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/public/v1/sites/{site_id}/sessions",
		"/api/public/v1/sessions/{session_id}",
	} {
		operation := document.Paths[path].Get
		if operation == nil {
			t.Errorf("GET %s is missing", path)
			continue
		}
		for _, status := range []string{"200", "400", "401", "403", "404", "429"} {
			if _, ok := operation.Responses[status]; !ok {
				t.Errorf("GET %s response %s is missing", path, status)
			}
		}
	}
	if operation := document.Paths["/api/public/v1/sites/{site_id}/sessions"].Get; operation != nil &&
		!strings.Contains(operation.Description, "cannot link") {
		t.Error("sessions endpoint must document that anonymous IDs cannot link visitors across days")
	}
	for _, schema := range []string{"SessionSummary", "SessionList", "SessionTimelineItem", "SessionDetail"} {
		if _, ok := document.Components.Schemas[schema]; !ok {
			t.Errorf("schema %s is missing", schema)
		}
	}
}

func TestOpenAPIProfileErasureEndpoints(t *testing.T) {
	source, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var document struct {
		Paths map[string]struct {
			Get *struct {
				Responses map[string]any `yaml:"responses"`
			} `yaml:"get"`
			Post *struct {
				Responses map[string]any `yaml:"responses"`
			} `yaml:"post"`
			Delete *struct {
				Responses map[string]any `yaml:"responses"`
			} `yaml:"delete"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(source, &document); err != nil {
		t.Fatal(err)
	}

	deleteOperation := document.Paths["/api/private/profiles/{profile_id}"].Delete
	if deleteOperation == nil || deleteOperation.Responses["202"] == nil {
		t.Fatal("private profile deletion must document its 202 response")
	}
	for _, path := range []string{"/api/private/erasures/{erasure_id}", "/api/public/v1/erasures/{erasure_id}"} {
		if document.Paths[path].Get == nil {
			t.Errorf("GET %s is missing", path)
		}
	}
	if document.Paths["/api/private/erasures/{erasure_id}/retry"].Post == nil {
		t.Error("private erasure retry endpoint is missing")
	}
	for _, schema := range []string{"ErasureAccepted", "ErasureRequest"} {
		if _, ok := document.Components.Schemas[schema]; !ok {
			t.Errorf("schema %s is missing", schema)
		}
	}
}

func TestOpenAPIShareExpiryContract(t *testing.T) {
	source, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]struct {
			Get   *yaml.Node `yaml:"get"`
			Post  *yaml.Node `yaml:"post"`
			Patch *yaml.Node `yaml:"patch"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]yaml.Node `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(source, &document); err != nil {
		t.Fatal(err)
	}
	collection := document.Paths["/api/private/sites/{site_id}/shares"]
	if collection.Get == nil || collection.Post == nil {
		t.Fatal("share list/create operations are missing")
	}
	item := document.Paths["/api/private/sites/{site_id}/shares/{share_id}"]
	if item.Patch == nil {
		t.Fatal("share expiry PATCH operation is missing")
	}
	for _, name := range []string{"Share", "CreateShareRequest", "ChangeShareExpiryRequest"} {
		if _, ok := document.Components.Schemas[name]; !ok {
			t.Errorf("schema %s is missing", name)
		}
	}
	var expiryDays struct {
		Enum []int  `yaml:"enum"`
		Type string `yaml:"type"`
	}
	if node, ok := document.Components.Schemas["ShareExpiryDays"]; ok {
		if err := node.Decode(&expiryDays); err != nil {
			t.Fatal(err)
		}
	}
	if got := expiryDays.Enum; len(got) != 4 || expiryDays.Type != "integer" {
		t.Fatalf("expires_in_days enum = %v, want null/7/30/90/365", got)
	}
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
