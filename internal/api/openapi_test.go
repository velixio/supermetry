package api_test

import (
	"os"
	"slices"
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
				Required   []string       `yaml:"required"`
				Properties map[string]any `yaml:"properties"`
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
		"site", "name", "url", "referrer", "screen_width", "props",
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
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
