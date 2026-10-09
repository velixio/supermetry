package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
)

type oneSiteRegistry struct{ site sites.Site }

func (r oneSiteRegistry) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (r oneSiteRegistry) Site(id int32) (sites.Site, bool) {
	return r.site, id == r.site.ID
}
func (r oneSiteRegistry) All() []sites.Site { return []sites.Site{r.site} }

// Journey and retention scan raw tables, so they must share the global query
// slots with funnels and closed-tier queries instead of bypassing them.
func TestRawReportsWaitForGlobalQuerySlots(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	engine := &Engine{trackingMode: privacy.Identified,
		pool:     pool,
		registry: oneSiteRegistry{site: sites.Site{ID: 1, TZ: time.UTC, Retention: db.RetForever}},
		now:      func() time.Time { return now },
	}
	dateRange := api.DateRange{From: types.Date{Time: dateForTest("2026-10-07")}, To: types.Date{Time: dateForTest("2026-10-08")}}

	if err := globalQuerySlots.Acquire(context.Background(), 16); err != nil {
		t.Fatal(err)
	}
	defer globalQuerySlots.Release(16)

	calls := map[string]func(context.Context) error{
		"journey": func(ctx context.Context) error {
			_, err := engine.Journey(ctx, api.JourneyRequest{SiteId: 1, DateRange: dateRange, Steps: 3, Limit: 5})
			return err
		},
		"retention": func(ctx context.Context) error {
			_, err := engine.Retention(ctx, api.RetentionRequest{SiteId: 1, DateRange: dateRange, Interval: api.RetentionDay})
			return err
		},
	}
	for name, call := range calls {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		err := call(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s with all slots taken: error = %v, want context deadline exceeded", name, err)
		}
	}
}
