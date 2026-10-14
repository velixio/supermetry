package pixels

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func newDB(t *testing.T) (*pgxpool.Pool, int32) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values ('Pixels','pixels') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var siteID int32
	if err := pool.QueryRow(ctx, `insert into sites(org_id,public_id,name,timezone,retention_class,kind) values ($1,'pixsite00001','P','Asia/Kathmandu',365,'pixel') returning id`, orgID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	return pool, siteID
}

func TestNewPublicID(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id, err := NewPublicID()
		if err != nil || len(id) != 12 || id[0] != 'p' || seen[id] {
			t.Fatalf("id = %q, %v", id, err)
		}
		seen[id] = true
	}
}

func TestStoreCRUDLimitAndTotals(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	created, err := Create(ctx, pool, Pixel{SiteID: siteID, Name: "GitHub profile", Enabled: true})
	if err != nil || created.ID == 0 || created.BadgeLabel != "views" || len(created.PublicID) != 12 {
		t.Fatalf("created = %+v, %v", created, err)
	}
	created.Name, created.BadgeLabel, created.Enabled = "Profile", "hits", false
	updated, err := Update(ctx, pool, created)
	if err != nil || updated.BadgeLabel != "hits" || updated.Enabled {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	if _, err := pool.Exec(ctx, `insert into pixel_views_daily values ($1,'2026-10-13','','proxy',5),($1,'2026-10-14','NP','direct',2)`, created.ID); err != nil {
		t.Fatal(err)
	}
	totals, err := Totals(ctx, pool)
	if err != nil || totals[created.ID] != 7 {
		t.Fatalf("totals = %v, %v", totals, err)
	}
	for i := 1; i < MaxPerSite; i++ {
		if _, err := Create(ctx, pool, Pixel{SiteID: siteID, Name: fmt.Sprintf("p%d", i), Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Create(ctx, pool, Pixel{SiteID: siteID, Name: "one too many", Enabled: true}); !errors.Is(err, ErrLimit) {
		t.Fatalf("101st err = %v", err)
	}
	if err := Delete(ctx, pool, siteID+1, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-site delete err = %v", err)
	}
	if err := Delete(ctx, pool, siteID, created.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := List(ctx, pool, siteID)
	if len(list) != MaxPerSite-1 {
		t.Fatalf("list len = %d", len(list))
	}
}

func TestPoolWriterUpsertsAndSkipsDeletedPixels(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	p, err := Create(ctx, pool, Pixel{SiteID: siteID, Name: "a", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	write := PoolWriter(pool)
	rows := map[ViewKey]int64{{PixelID: p.ID, Day: "2026-10-14", Via: "proxy"}: 2, {PixelID: 999999, Day: "2026-10-14", Via: "proxy"}: 1}
	for range 2 {
		if err := write(ctx, rows); err != nil {
			t.Fatal(err)
		}
	}
	var views int64
	_ = pool.QueryRow(ctx, `select coalesce(sum(views),0) from pixel_views_daily`).Scan(&views)
	if views != 4 {
		t.Fatalf("views = %d, want 4", views)
	}
}
