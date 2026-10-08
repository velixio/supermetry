package main

import (
	"encoding/json"
	"testing"

	"panel/bench/load/internal/rec"
)

func TestPanelBuilder(t *testing.T) {
	t.Parallel()
	record := &rec.Record{
		Site: 3, Host: "three.test", Path: "/pricing", Name: "signup",
		Ref: "https://example.test/", UTMSource: "newsletter", ScreenW: 1440,
		Props: map[string]string{"plan": "pro"},
	}
	request, err := panelBuilder("http://127.0.0.1:18080", map[int]string{3: "three.test"})(record)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := request.URL.String(), "http://127.0.0.1:18080/api/public/v1/events"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got := request.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got, want := body["site"], "three.test"; got != want {
		t.Errorf("site = %v, want %v", got, want)
	}
	if got, want := body["url"], "https://three.test/pricing?utm_source=newsletter"; got != want {
		t.Errorf("url = %v, want %v", got, want)
	}
	if got, want := body["name"], "signup"; got != want {
		t.Errorf("name = %v, want %v", got, want)
	}
}
