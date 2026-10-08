package heatmap_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/heatmap"
	"panel/internal/sites"
	"panel/internal/testpg"
)

type registry struct{ site sites.Site }

func (r registry) ByPublicID(id string) (sites.Site, bool) { return r.site, id == r.site.PublicID }
func (r registry) Site(id int32) (sites.Site, bool)        { return r.site, id == r.site.ID }
func (r registry) All() []sites.Site                       { return []sites.Site{r.site} }

func TestService_QueryAggregatesCellsAndScrollBands(t *testing.T) {
	pool, service := setup(t)
	seed(t, pool)

	response, err := service.Query(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Cells) != 2 || response.Cells[0] != (api.HeatmapCell{Gx: 10, Gy: 20, Clicks: 7}) || response.Cells[1] != (api.HeatmapCell{Gx: 99, Gy: 5000, Clicks: 2}) {
		t.Fatalf("cells = %+v", response.Cells)
	}
	if response.Totals.Clicks != 9 || response.Totals.PageviewsSampled != 4 {
		t.Fatalf("totals = %+v", response.Totals)
	}
	if len(response.ScrollBands) != 10 {
		t.Fatalf("scroll bands = %+v", response.ScrollBands)
	}
	wants := []float64{3.0 / 4, 3.0 / 4, 2.0 / 4, 2.0 / 4, 2.0 / 4, 2.0 / 4, 1.0 / 4, 1.0 / 4, 1.0 / 4, 1.0 / 4}
	for i, band := range response.ScrollBands {
		if band.Band != int32((i+1)*10) || math.Abs(band.Share-wants[i]) > 1e-9 {
			t.Errorf("band %d = %+v, want share %v", i, band, wants[i])
		}
	}
}

func TestService_QueryCapsCellsAtTwentyThousand(t *testing.T) {
	pool, service := setup(t)
	_, err := pool.Exec(context.Background(), `
insert into dict(kind,id,value) values (0,1,'/docs');
insert into heatmap_cells(site_id,day,path_id,device,gx,gy,clicks)
select 1,'2026-10-07',1,0,gx,gy,1
from generate_series(0,99) gx cross join generate_series(0,200) gy`)
	if err != nil {
		t.Fatal(err)
	}

	response, err := service.Query(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Cells) != 20_000 {
		t.Fatalf("cells = %d, want 20000", len(response.Cells))
	}
	if response.Totals.Clicks != 20_100 {
		t.Fatalf("click total = %d, want 20100", response.Totals.Clicks)
	}
}

func TestService_QueryRejectsInvalidRequests(t *testing.T) {
	_, service := setup(t)
	tests := []struct {
		name string
		edit func(*api.HeatmapRequest)
		want string
	}{
		{name: "foreign site", edit: func(r *api.HeatmapRequest) { r.SiteId = 2 }, want: "unknown site 2"},
		{name: "empty path", edit: func(r *api.HeatmapRequest) { r.Path = "" }, want: "heatmap path is required"},
		{name: "unknown device", edit: func(r *api.HeatmapRequest) { r.Device = "watch" }, want: `unknown heatmap device "watch"`},
		{name: "backwards range", edit: func(r *api.HeatmapRequest) { r.DateRange.From, r.DateRange.To = r.DateRange.To, r.DateRange.From }, want: "heatmap date_range.from must not be after date_range.to"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := request()
			tt.edit(&req)
			_, err := service.Query(context.Background(), req)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Query() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func setup(t *testing.T) (*pgxpool.Pool, *heatmap.Service) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-01"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	site := sites.Site{ID: 1, PublicID: "heatmaps", TZ: time.UTC, Retention: db.Ret30}
	return pool, heatmap.New(pool, registry{site: site})
}

func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
insert into dict(kind,id,value) values (0,1,'/docs'),(0,2,'/other');
insert into heatmap_cells(site_id,day,path_id,device,gx,gy,clicks) values
  (1,'2026-10-06',1,0,10,20,3),
  (1,'2026-10-07',1,0,10,20,4),
  (1,'2026-10-07',1,0,99,5000,2),
  (1,'2026-10-07',1,1,10,20,100),
  (2,'2026-10-07',1,0,10,20,100),
  (1,'2026-10-07',2,0,10,20,100);
insert into day_close(site_id,day,status) values (1,'2026-10-06','done');
insert into sessions_closed
  (site_id,day,visitor_id,session_id,pageviews,custom,duration_ms,engagement_ms,vd_n,flags,seq,seq_engage,seq_scroll)
values (1,'2026-10-06',1,1,1,0,1000,500,1,0,array[4],array[500],array[100]::smallint[]);
insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,path_id,device,scroll_pct) values
  (1,30,'2026-10-07','2026-10-07 09:00Z',2,2,0,1,0,0),
  (1,30,'2026-10-07','2026-10-07 09:10Z',2,2,2,1,0,60),
  (1,30,'2026-10-07','2026-10-07 09:20Z',2,2,2,1,0,50),
  (1,30,'2026-10-07','2026-10-07 10:30Z',2,2,0,1,0,0),
  (1,30,'2026-10-07','2026-10-07 11:00Z',2,2,2,1,0,20),
  (1,30,'2026-10-07','2026-10-07 11:30Z',3,3,0,1,0,0),
  (1,30,'2026-10-07','2026-10-07 12:00Z',4,4,2,1,1,100),
  (1,30,'2026-10-07','2026-10-07 13:00Z',5,5,2,2,0,100)`)
	if err != nil {
		t.Fatal(err)
	}
}

func request() api.HeatmapRequest {
	return api.HeatmapRequest{
		SiteId: 1,
		DateRange: api.DateRange{
			From: openapi_types.Date{Time: day("2026-10-06")},
			To:   openapi_types.Date{Time: day("2026-10-07")},
		},
		Path:   "/docs",
		Device: api.HeatmapDeviceDesktop,
	}
}

func day(value string) time.Time {
	parsed, _ := time.Parse(time.DateOnly, value)
	return parsed
}
