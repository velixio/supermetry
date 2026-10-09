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

	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
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

func TestLoadPublicPathsRemainUnverified(t *testing.T) {
	for _, mode := range []privacy.Mode{privacy.Anonymous, privacy.Identified} {
		t.Run(mode.String(), func(t *testing.T) {
			pool, _ := testpg.New(t)
			ctx := context.Background()
			if err := db.Migrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			sitesPath := filepath.Join(t.TempDir(), "sites.json")
			if err := os.WriteFile(sitesPath, []byte(`[{"id":1,"public_id":"public.test","timezone":"UTC","retention_days":365,"allowed_hosts":["public.test"]}]`), 0o600); err != nil {
				t.Fatal(err)
			}
			registry, err := sites.LoadFile(sitesPath)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := privacy.NewPathPolicy([]string{"/pricing"})
			if err != nil {
				t.Fatal(err)
			}
			input := bytes.NewBufferString(`{"ts":"2026-09-01T00:00:00Z","site":1,"host":"public.test","path":"/pricing","name":"pageview","ip":"192.0.2.1","ua":"fixture-browser","country":"US","browser":"Fixture","os":"FixtureOS","device":"desktop"}` + "\n")
			stats, err := loadEventsWithPolicy(ctx, pool, registry, input, 256, time.Millisecond, time.Millisecond, mode, policy)
			if err != nil {
				t.Fatal(err)
			}
			if stats.Events != 1 {
				t.Fatalf("events=%d", stats.Events)
			}
			var version int16
			var policyID string
			var pathID int32
			if err := pool.QueryRow(ctx, `select path_sanitizer_version,path_policy_id,path_id from events`).Scan(&version, &policyID, &pathID); err != nil {
				t.Fatal(err)
			}
			if version != 0 || policyID != "" {
				t.Fatalf("historical load promoted: version=%d policy=%q", version, policyID)
			}
			if mode == privacy.Anonymous && pathID != 0 {
				t.Fatalf("unverified anonymous path=%d", pathID)
			}
		})
	}
}
