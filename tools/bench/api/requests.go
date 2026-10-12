package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

type InventoryRoute struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
	Auth    string `json:"auth"`
}

type RequestSet struct {
	Name    string            `yaml:"name" json:"name"`
	Method  string            `yaml:"method" json:"method"`
	Route   string            `yaml:"route" json:"route"`
	Params  map[string]any    `yaml:"params" json:"params,omitempty"`
	Body    any               `yaml:"body" json:"body,omitempty"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
	Auth    string            `yaml:"-" json:"-"`
}

type requestFile struct {
	Defaults RequestSet   `yaml:"defaults"`
	Routes   []RequestSet `yaml:"routes"`
}

func loadInventory(r io.Reader) ([]InventoryRoute, error) {
	var routes []InventoryRoute
	if err := json.NewDecoder(r).Decode(&routes); err != nil {
		return nil, fmt.Errorf("decode inventory: %w", err)
	}
	return routes, nil
}

func loadRequestSets(r io.Reader, inventory []InventoryRoute) ([]RequestSet, error) {
	var file requestFile
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode request sets: %w", err)
	}
	registered := make(map[string]InventoryRoute, len(inventory))
	sets := make([]RequestSet, 0, len(inventory)+len(file.Routes))
	for _, route := range inventory {
		if route.Method == "" {
			continue
		}
		key := route.Method + " " + route.Pattern
		registered[key] = route
		base := cloneRequestSet(file.Defaults)
		base.Name = "default"
		base.Method = route.Method
		base.Route = route.Pattern
		base.Auth = route.Auth
		sets = append(sets, base)
	}
	for _, set := range file.Routes {
		set.Method = strings.ToUpper(strings.TrimSpace(set.Method))
		set.Route = strings.TrimSpace(set.Route)
		key := set.Method + " " + set.Route
		if _, ok := registered[key]; !ok {
			return nil, fmt.Errorf("unknown route %s", key)
		}
		merged := cloneRequestSet(file.Defaults)
		mergeRequestSet(&merged, set)
		merged.Auth = registered[key].Auth
		sets = append(sets, merged)
	}
	return sets, nil
}

func cloneRequestSet(in RequestSet) RequestSet {
	out := in
	out.Params = cloneMap(in.Params)
	out.Headers = make(map[string]string, len(in.Headers))
	for key, value := range in.Headers {
		out.Headers[key] = value
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeRequestSet(dst *RequestSet, src RequestSet) {
	if src.Name != "" {
		dst.Name = src.Name
	}
	dst.Method, dst.Route = src.Method, src.Route
	for key, value := range src.Params {
		dst.Params[key] = value
	}
	if src.Body != nil {
		dst.Body = src.Body
	}
	for key, value := range src.Headers {
		dst.Headers[key] = value
	}
}
