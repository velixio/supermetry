package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/closer"
)

// closableDays lists the civil days in [first, last] that the day-close job
// would already have closed: every day before today. The current day stays
// open, as in production.
func closableDays(first, last, today time.Time) []time.Time {
	var days []time.Time
	for day := first; !day.After(last) && day.Before(today); day = day.AddDate(0, 0, 1) {
		days = append(days, day)
	}
	return days
}

// civilToday returns now's calendar date in the named zone as a UTC midnight,
// the representation the closer uses for civil days.
func civilToday(now time.Time, zone string) (time.Time, error) {
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load time zone %q: %w", zone, err)
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC), nil
}

// closeDatasetDays closes every complete loaded day of every site, which builds
// the closed-session tier and the rollups. A freshly loaded database holds only
// hot raw rows, so without this step the benchmark would measure raw scans that
// production serves from closed days and rollups.
func closeDatasetDays(ctx context.Context, pool *pgxpool.Pool, sites []siteInfo, now time.Time) error {
	started := time.Now()
	closed := 0
	for _, site := range sites {
		var first, last *time.Time
		if err := pool.QueryRow(ctx, `select min(local_day), max(local_day) from events where site_id=$1`, site.ID).Scan(&first, &last); err != nil {
			return fmt.Errorf("find loaded days of site %d: %w", site.ID, err)
		}
		if first == nil || last == nil {
			continue
		}
		today, err := civilToday(now, site.Timezone)
		if err != nil {
			return err
		}
		for _, day := range closableDays(*first, *last, today) {
			if _, err := closer.CloseDay(ctx, pool, []int32{site.ID}, day); err != nil {
				return fmt.Errorf("close site %d day %s: %w", site.ID, day.Format(time.DateOnly), err)
			}
			closed++
		}
	}
	slog.Info("closed benchmark days", "site_days", closed, "elapsed", time.Since(started).Round(time.Second).String())
	return nil
}
