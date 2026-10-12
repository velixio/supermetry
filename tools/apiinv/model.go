package main

type Route struct {
	Method     string   `json:"method"`
	Pattern    string   `json:"pattern"`
	Group      string   `json:"group"`
	Handler    string   `json:"handler"`
	Auth       string   `json:"auth"`
	InOpenAPI  bool     `json:"in_openapi"`
	HitByTests []string `json:"hit_by_tests"`
	canonical  string
}

type Operation struct {
	Method      string
	Pattern     string
	OperationID string
	canonical   string
}

type Inventory struct {
	Routes               []Route
	OpenAPIWithoutRoute  []Operation
	UnknownRegistrations []string
}

type hitSet map[string]map[string]struct{}
