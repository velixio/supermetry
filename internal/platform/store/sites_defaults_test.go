package store

import (
	"context"
	"testing"
)

func TestCreateSiteEnablesProfilesByDefault(t *testing.T) {
	pool, orgID := newSiteLifecycleStore(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	created, err := CreateSite(ctx, tx, Site{OrgID: orgID, PublicID: "defaults0001", Name: "Defaults", AllowedHosts: []string{}, Timezone: "UTC", RetentionClass: 30, RevenueCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	var on bool
	if err := tx.QueryRow(ctx, `select coalesce(settings->'profiles_enabled' = 'true'::jsonb, false) from sites where id=$1`, created.ID).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Fatal("new project has profiles disabled")
	}
}
