package sitesvc

import (
	"bytes"
	"context"
	"testing"

	coresites "supermetry/internal/sites"
)

func TestRegistryLoadsAppHashSecret(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	app, err := service.Create(ctx, principal, CreateInput{Name: "App", Kind: coresites.KindApp, Timezone: "UTC", RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := registry.Site(app.ID)
	if loaded.App == nil || loaded.App.HashSecret != nil {
		t.Fatalf("a new app has no secret yet: %+v", loaded.App)
	}
	secret := bytes.Repeat([]byte{7}, 32)
	if _, err := pool.Exec(ctx, `insert into app_hash_secrets(site_id, secret) values ($1, $2)`, app.ID, secret); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	loaded, _ = registry.Site(app.ID)
	if loaded.App == nil || !bytes.Equal(loaded.App.HashSecret, secret) {
		t.Fatalf("secret not loaded: %+v", loaded.App)
	}
}
