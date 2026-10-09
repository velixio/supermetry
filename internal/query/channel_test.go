package query_test

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/api"
	"supermetry/internal/closer"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/query"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

func TestChannelGroupingAndFiltersMatchAcrossTiers(t *testing.T) {
	_, engine := setupChannels(t)
	const d = "2026-10-06"
	for _, route := range []query.Route{query.RouteClosed, query.RouteHot} {
		t.Run(route.String(), func(t *testing.T) {
			e := engine.WithForcedRoute(route)
			run := func(dims []string, filters ...api.Filter) api.QueryResponse {
				return mustQuery(t, e, queryRequestRange(d, d, dims, filters))
			}
			check := func(name string, got, want map[string]float64) {
				t.Helper()
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: got %v, want %v", name, got, want)
				}
			}
			check("group", visitorsBy(run([]string{"channel"}), "channel"), map[string]float64{
				"AI Assistants": 5, "Other": 3, "Referral": 2, "Organic Search": 1, "Paid Search": 1,
				"Paid Social": 1, "Organic Social": 1, "Email": 1, "Direct": 1,
			})
			check("is", visitorsBy(run([]string{"source"}, filter("channel", api.Is, "AI Assistants")), "source"),
				map[string]float64{"ChatGPT": 1, "Claude": 1, "Gemini": 1, "Copilot": 1, "Perplexity": 1})
			check("page", visitorsBy(run([]string{"page"}, filter("channel", api.Is, "Referral")), "page"), map[string]float64{"/": 2})
			notOther := run([]string{}, filter("channel", api.IsNot, "Other")).Totals["visitors"]
			paid := run([]string{}, filter("channel", api.Contains, "Paid")).Totals["visitors"]
			if notOther != 13 || paid != 2 {
				t.Errorf("is_not Other = %v (want 13), contains Paid = %v (want 2)", notOther, paid)
			}
		})
	}
}

func setupChannels(t *testing.T) (*pgxpool.Pool, *query.Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, day("2026-10-05"), day("2026-10-10")); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
insert into dict(kind, id, value) values (0,1,'/'),
  (2,10,'Google'),(2,11,'Facebook'),(2,12,'ChatGPT'),(2,13,'Claude'),(2,14,'Gemini'),(2,15,'Copilot'),(2,16,'Perplexity'),
  (2,17,'newsletter'),(2,18,'example.com'),(2,19,'GitHub'),(2,20,'Foo'),(2,21,'Direct'),
  (3,30,'organic'),(3,31,'social'),(3,32,'paid'),(3,33,'email'),(3,34,'referral'),(3,35,'banner');
insert into sessions (site_id, retention_class, session_id, visitor_id, start, last, local_day, entry_path, exit_path,
  source_id, medium_id, country, region_id, city_id, browser_id, os_id, device, pageviews, events, engagement_ms, bounce)
select 1, 30, 100+n, 1000+n, '2026-10-06 10:00Z'::timestamptz + n * interval '1 minute',
  '2026-10-06 10:00Z'::timestamptz + n * interval '1 minute', '2026-10-06', 1, 1, src, med, 'US', 0, 0, 0, 0, 0, 1, 0, 0, true
from (values (1,10,30),(2,10,32),(3,11,32),(4,11,31),(5,12,34),(6,13,34),(7,14,34),(8,15,34),
  (9,16,34),(10,17,33),(11,18,34),(12,19,34),(13,0,0),(14,20,32),(15,20,35),(16,21,0)) v(n, src, med);
insert into events (site_id, retention_class, local_day, ts, visitor_id, session_id, kind, name_id, path_id,
  source_id, medium_id, country, region_id, city_id, browser_id, os_id, device, engagement_ms, scroll_pct, props)
select site_id, retention_class, local_day, start, visitor_id, session_id, 0, 0, 1,
  source_id, medium_id, country, 0, 0, 0, 0, 0, 0, 0, null from sessions where site_id = 1;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closer.CloseDay(ctx, pool, []int32{1}, day("2026-10-06")); err != nil {
		t.Fatal(err)
	}
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	reg := registry{site: sites.Site{ID: 1, PublicID: "channels", TZ: time.UTC, Retention: db.Ret30}}
	return pool, identifiedEngine(pool, dictionary, reg)
}

func visitorsBy(response api.QueryResponse, dimension string) map[string]float64 {
	result := make(map[string]float64, len(response.Rows))
	for _, row := range response.Rows {
		result[fmt.Sprint(row[dimension])], _ = strconv.ParseFloat(fmt.Sprint(row["visitors"]), 64)
	}
	return result
}
