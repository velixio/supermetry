package sitesvc

import (
	"context"
	"testing"
)

func TestRegistryLoadsAndClonesReplaySettings(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	site, err := service.Create(context.Background(), principal, "Replay", []string{"replay.example.com"}, "UTC", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		update sites set settings = settings || '{
			"replay":{"enabled":true,"mask":true,"sample_rate":25,"triggers":{"url_patterns":["/checkout/*"]}}
		}'::jsonb where id = $1`, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	loaded, ok := registry.Site(site.ID)
	if !ok || !loaded.Replay.Enabled || loaded.Replay.Mask == nil || !*loaded.Replay.Mask || loaded.Replay.SampleRate == nil || *loaded.Replay.SampleRate != 25 {
		t.Fatalf("registry replay = (%+v, %t)", loaded.Replay, ok)
	}
	*loaded.Replay.Mask = false
	loaded.Replay.Triggers.URLPatterns[0] = "/mutated"
	again, ok := registry.Site(site.ID)
	if !ok || again.Replay.Mask == nil || !*again.Replay.Mask || again.Replay.Triggers.URLPatterns[0] != "/checkout/*" {
		t.Fatalf("registry replay aliased caller mutation: %+v", again.Replay)
	}
}
