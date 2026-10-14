package pixels

import (
	"context"
	"testing"
)

func TestStatsAgainstHandComputedFixture(t *testing.T) {
	pool, siteID := newDB(t)
	ctx := context.Background()
	a, _ := Create(ctx, pool, Pixel{SiteID: siteID, Name: "a", Enabled: true})
	b, _ := Create(ctx, pool, Pixel{SiteID: siteID, Name: "b", Enabled: true})
	_, err := pool.Exec(ctx, `insert into pixel_views_daily values
		($1,'2026-10-01','','proxy',10), ($1,'2026-10-01','NP','direct',2), ($1,'2026-10-02','IN','direct',3),
		($2,'2026-10-02','','proxy',5),  ($2,'2026-09-30','NP','direct',100)`, a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := StatsFor(ctx, pool, siteID, "2026-10-01", "2026-10-02", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []DayViews{{"2026-10-01", 10, 2}, {"2026-10-02", 5, 3}}
	if len(s.Daily) != 2 || s.Daily[0] != want[0] || s.Daily[1] != want[1] {
		t.Fatalf("daily = %+v", s.Daily)
	}
	if len(s.Pixels) != 2 || s.Pixels[0] != (PixelViews{a.ID, 15}) || s.Pixels[1] != (PixelViews{b.ID, 5}) {
		t.Fatalf("pixels = %+v", s.Pixels)
	}
	if len(s.Countries) != 2 || s.Countries[0] != (CountryViews{"IN", 3}) || s.Countries[1] != (CountryViews{"NP", 2}) {
		t.Fatalf("countries = %+v", s.Countries)
	}
	only, _ := StatsFor(ctx, pool, siteID, "2026-10-01", "2026-10-02", b.ID)
	if len(only.Daily) != 1 || only.Daily[0] != (DayViews{"2026-10-02", 5, 0}) {
		t.Fatalf("filtered = %+v", only.Daily)
	}
	v30, _ := Views30(ctx, pool, siteID, "2026-10-02")
	if v30[a.ID] != 15 || v30[b.ID] != 105 {
		t.Fatalf("views30 = %v", v30)
	}
}
