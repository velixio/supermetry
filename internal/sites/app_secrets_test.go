package sites_test

import (
	"context"
	"testing"

	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestEnsureAppHashSecrets(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	for _, q := range []string{
		`insert into orgs(name,slug) values('O','o')`,
		`insert into sites(org_id,public_id,name,timezone,retention_class,kind) select id,'app000000001','A1','UTC',30,'app' from orgs`,
		`insert into sites(org_id,public_id,name,timezone,retention_class,kind) select id,'app000000002','A2','UTC',30,'app' from orgs`,
		`insert into sites(org_id,public_id,name,timezone,retention_class,kind) select id,'web000000001','W','UTC',30,'web' from orgs`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	created, err := sites.EnsureAppHashSecrets(ctx, pool)
	if err != nil || created != 2 {
		t.Fatalf("created=%d err=%v", created, err)
	}
	created, err = sites.EnsureAppHashSecrets(ctx, pool)
	if err != nil || created != 0 {
		t.Fatalf("second call created=%d err=%v", created, err)
	}
	var webSecrets, length int
	if err := pool.QueryRow(ctx, `select count(*) filter (where s.kind='web'), min(octet_length(h.secret)) from app_hash_secrets h join sites s on s.id=h.site_id`).Scan(&webSecrets, &length); err != nil {
		t.Fatal(err)
	}
	if webSecrets != 0 || length != 32 {
		t.Fatalf("web secrets=%d length=%d", webSecrets, length)
	}
}
