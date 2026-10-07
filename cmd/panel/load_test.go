package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"panel/internal/db"
	"panel/internal/sites"
	"panel/internal/testpg"
)

func TestLoadReplaysNDJSON(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	sitesPath := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(sitesPath, []byte(`[{"id":1,"public_id":"site1.test","timezone":"UTC","retention_days":365,"allowed_hosts":["site1.test"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := sites.LoadFile(sitesPath)
	if err != nil {
		t.Fatal(err)
	}

	var input bytes.Buffer
	for i := range 100 {
		record := map[string]any{
			"ts":           time.Date(2026, 9, 1, 0, 0, i, 0, time.UTC),
			"site":         1,
			"host":         "site1.test",
			"path":         fmt.Sprintf("/page/%d", i%5),
			"ref":          "",
			"utm_source":   "",
			"utm_medium":   "",
			"utm_campaign": "",
			"name":         "pageview",
			"props":        map[string]string{},
			"ip":           fmt.Sprintf("192.0.2.%d", i+1),
			"ua":           "fixture-browser",
			"screen_w":     1280,
			"country":      "US",
			"browser":      "Fixture",
			"os":           "FixtureOS",
			"device":       "desktop",
		}
		if err := json.NewEncoder(&input).Encode(record); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := loadEvents(ctx, pool, registry, &input)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Events != 100 {
		t.Fatalf("loaded events = %d, want 100", stats.Events)
	}

	var count int
	if err := pool.QueryRow(ctx, `select count(*) from events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("stored events = %d, want 100", count)
	}
}
