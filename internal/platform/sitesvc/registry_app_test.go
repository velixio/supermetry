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

func TestRegistryByDSNKey(t *testing.T) {
	service, registry, principal, pool := newSites(t)
	ctx := context.Background()
	app, err := service.Create(ctx, principal, CreateInput{Name: "App", Kind: coresites.KindApp, Timezone: "UTC", RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	const live, revoked, expired = "0123456789abcdef0123456789abcdef", "11111111111111111111111111111111", "22222222222222222222222222222222"
	for _, key := range []string{live, revoked, expired} {
		if _, err := pool.Exec(ctx, `insert into app_ingest_keys(site_id,prefix,hash,kind,public_key,allowed_origins,expires_at,revoked_at)
			values ($1,$2,sha256(convert_to($3::text,'UTF8')),'dsn',$3,'{https://a.example.test}',
			case when $3=$4 then now()-interval '1 hour' end, case when $3=$5 then now() end)`,
			app.ID, key[:8], key, expired, revoked); err != nil {
			t.Fatal(err)
		}
	}
	// A native key must not be reachable as a DSN key.
	if _, err := pool.Exec(ctx, `insert into app_ingest_keys(site_id,prefix,hash) values ($1,'abcdefgh',sha256('x'))`, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Invalidate(); err != nil {
		t.Fatal(err)
	}
	site, key, ok := registry.ByDSNKey(live)
	if !ok || site.ID != app.ID || key.PublicKey != live || len(key.AllowedOrigins) != 1 || key.AllowedOrigins[0] != "https://a.example.test" {
		t.Fatalf("live key: ok=%v site=%d key=%+v", ok, site.ID, key)
	}
	if _, _, ok := registry.ByDSNKey(revoked); ok {
		t.Error("revoked key resolved")
	}
	if _, _, ok := registry.ByDSNKey(expired); ok {
		t.Error("expired key resolved")
	}
	if _, _, ok := registry.ByDSNKey("abcdefgh"); ok {
		t.Error("native key resolved as DSN key")
	}
	loaded, _ := registry.Site(app.ID)
	if len(loaded.App.Keys) != 1 {
		t.Fatalf("native keys = %d, want 1 (DSN keys must not load as native keys)", len(loaded.App.Keys))
	}
	if _, _, ok := registry.ByDSNKey("33333333333333333333333333333333"); ok {
		t.Error("unknown key resolved")
	}
}
