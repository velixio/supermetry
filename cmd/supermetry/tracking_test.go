package main

import (
	"context"
	"strings"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestTrackingStartupRequiresExplicitUpgradeChoice(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := validateTrackingStartup(ctx, pool, config.TrackingMode{}, nil); err != nil {
		t.Fatalf("fresh install: %v", err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Synthetic','synthetic') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,settings) values ($1,'privacy00001','Synthetic','UTC',30,'{"profiles_enabled":true}')`, orgID); err != nil {
		t.Fatal(err)
	}
	err := validateTrackingStartup(ctx, pool, config.TrackingMode{}, nil)
	if err == nil || !strings.Contains(err.Error(), "SUPERMETRY_TRACKING_MODE=identified") {
		t.Fatalf("upgrade error = %v", err)
	}
	for _, mode := range []privacy.Mode{privacy.Anonymous, privacy.Identified} {
		if err := validateTrackingStartup(ctx, pool, config.TrackingMode{Mode: mode, Explicit: true}, nil); err != nil {
			t.Fatalf("explicit choice: %v", err)
		}
	}
	var enabled bool
	if err := pool.QueryRow(ctx, `select settings->'profiles_enabled'='true'::jsonb from sites`).Scan(&enabled); err != nil || !enabled {
		t.Fatalf("startup changed configured data: enabled=%t err=%v", enabled, err)
	}
}

func TestTrackingStartupChecksCompatibilitySites(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := validateTrackingStartup(ctx, pool, config.TrackingMode{}, []sites.Site{{ProfilesEnabled: true}}); err == nil {
		t.Fatal("compatibility site bypassed upgrade check")
	}
}
