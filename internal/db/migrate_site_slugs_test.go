package db

import (
	"context"
	"testing"

	"supermetry/internal/testpg"
)

func TestSiteSlugMigrationBackfillsAndKeepsSlugsUniquePerOrganization(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var slugMigration *migration
	for index := range migrations {
		if migrations[index].version < 140 {
			if err := applyMigration(ctx, conn, migrations[index]); err != nil {
				conn.Release()
				t.Fatal(err)
			}
		} else if migrations[index].version == 140 {
			slugMigration = &migrations[index]
		}
	}
	conn.Release()
	if slugMigration == nil {
		t.Fatal("migration 140 is missing")
	}
	if _, err := pool.Exec(ctx, `insert into orgs(name,slug) values('One','one'),('Two','two')`); err != nil {
		t.Fatal(err)
	}
	legacy := []struct {
		org    int
		id     string
		name   string
		hosts  string
		want   string
		delete bool
	}{
		{1, "legacy000001", "Main", "array['www.supermetry.com']", "example-com", false},
		{1, "legacy000002", "Copy", "array['supermetry.com']", "example-com-2", false},
		{2, "legacy000003", "Other org", "array['supermetry.com']", "example-com", false},
		{1, "legacy000004", "Wildcard", "array['*.shop.supermetry.com']", "shop-example-com", false},
		{1, "legacy000005", "Twelve", "array['abcdefghijkl']", "abcdefghijkl-site", false},
		{1, "legacy000006", "Docs", "'{}'::text[]", "docs", false},
		{1, "legacy000007", "Reserved", "array['settings']", "settings-site", false},
	}
	for _, site := range legacy {
		if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class) values($1,$2,$3,`+site.hosts+`,'UTC',30)`, site.org, site.id, site.name); err != nil {
			t.Fatalf("%s: %v", site.id, err)
		}
	}
	conn, err = pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, conn, *slugMigration); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	conn.Release()

	for _, site := range legacy {
		var got string
		if err := pool.QueryRow(ctx, `select slug from sites where public_id=$1`, site.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != site.want {
			t.Errorf("%s slug=%q, want %q", site.id, got, site.want)
		}
	}

	// New sites get a slug without the insert naming one.
	var created, second string
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class) values(2,'created00001','New',array['blog.example.com'],'UTC',30) returning slug`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class) values(2,'created00002','New 2',array['blog.example.com'],'UTC',30) returning slug`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if created != "blog-example-org" || second != "blog-example-org-2" {
		t.Fatalf("new slugs=%q,%q", created, second)
	}

	// The slug rules are enforced by the table itself.
	for _, bad := range []string{"settings", "Upper", "has space", "abcdefghijkl", "-lead", "a--b", ""} {
		if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,slug) values(2,'badslug00001','Bad','{}','UTC',30,$1)`, bad); err == nil {
			t.Errorf("slug %q was accepted", bad)
		}
	}
	if _, err := pool.Exec(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class,slug) values(2,'dupslug00001','Dup','{}','UTC',30,'blog-example-org')`); err == nil {
		t.Error("a duplicate slug in one organization was accepted")
	}

	// A deleted site does not hold its slug, and restoring it gives way to a live one.
	if _, err := pool.Exec(ctx, `update sites set deleted_at=now() where public_id='created00001'`); err != nil {
		t.Fatal(err)
	}
	var reused string
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,allowed_hosts,timezone,retention_class) values(2,'created00003','Again',array['blog.example.com'],'UTC',30) returning slug`).Scan(&reused); err != nil {
		t.Fatal(err)
	}
	if reused != "blog-example-org" {
		t.Fatalf("slug of a deleted site was not freed: got %q", reused)
	}
	var restored string
	if err := pool.QueryRow(ctx, `update sites set deleted_at=null where public_id='created00001' returning slug`).Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if restored == "blog-example-org" || restored == "" {
		t.Fatalf("restored site kept a slug a live site holds: %q", restored)
	}
}
