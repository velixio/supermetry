package closer

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

const maxDayBlockLevel uint8 = 9
const rollupRefreshLockID int64 = 7469002

type dayBlockKey struct {
	site  int32
	start time.Time
}

func refreshDayBlocks(ctx context.Context, tx pgx.Tx, siteIDs []int32, day time.Time) error {
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, rollupRefreshLockID); err != nil {
		return err
	}
	affected := make([]map[dayBlockKey]struct{}, int(maxDayBlockLevel)+1)
	for level := range affected {
		affected[level] = make(map[dayBlockKey]struct{}, len(siteIDs))
		start := containingDayBlock(day, uint8(level))
		for _, siteID := range siteIDs {
			affected[level][dayBlockKey{site: siteID, start: start}] = struct{}{}
		}
	}

	for level, set := range affected {
		keys := make([]dayBlockKey, 0, len(set))
		for key := range set {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].site != keys[j].site {
				return keys[i].site < keys[j].site
			}
			return keys[i].start.Before(keys[j].start)
		})
		sites := make([]int32, len(keys))
		starts := make([]time.Time, len(keys))
		for i, key := range keys {
			sites[i], starts[i] = key.site, key.start
		}

		if level > 0 {
			if _, err := tx.Exec(ctx, deleteBlockDimensionsSQL, sites, starts, int16(level)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, deleteBlockDoneSQL, sites, starts, int16(level)); err != nil {
			return err
		}
		if level == 0 {
			if _, err := tx.Exec(ctx, insertDayBlockDoneSQL, sites, starts); err != nil {
				return err
			}
			continue
		}

		half := int32(1) << (level - 1)
		if level == 1 {
			if _, err := tx.Exec(ctx, insertFirstBlockSQL, sites, starts); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, insertParentBlockSQL, sites, starts, int16(level), half); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, insertBlockDoneSQL, sites, starts, int16(level), half); err != nil {
			return err
		}
	}
	return nil
}

const deleteBlockDimensionsSQL = `
delete from rollup_block_dim block
using unnest($1::int[], $2::date[]) wanted(site_id, start_day)
where block.site_id = wanted.site_id and block.k = $3 and block.start_day = wanted.start_day`

const deleteBlockDoneSQL = `
delete from rollup_block_done block
using unnest($1::int[], $2::date[]) wanted(site_id, start_day)
where block.site_id = wanted.site_id and block.k = $3 and block.start_day = wanted.start_day`

const insertDayBlockDoneSQL = `
insert into rollup_block_done (site_id, k, start_day)
select site_id, 0, start_day
from unnest($1::int[], $2::date[]) wanted(site_id, start_day)`

const insertFirstBlockSQL = `
with wanted as (
  select site_id, start_day from unnest($1::int[], $2::date[]) values(site_id, start_day)
), ready as (
  select wanted.* from wanted
  join rollup_block_done left_day
    on left_day.site_id = wanted.site_id and left_day.k = 0 and left_day.start_day = wanted.start_day
  join rollup_block_done right_day
    on right_day.site_id = wanted.site_id and right_day.k = 0 and right_day.start_day = wanted.start_day + 1
)
insert into rollup_block_dim (
  site_id, k, start_day, dim, value_id, visitors, visits, pageviews, bounces,
  duration_ms, engagement_ms, engaged_n, scroll_sum, scroll_n
)
select ready.site_id, 1, ready.start_day, daily.dim, daily.value_id,
       sum(daily.visitors), sum(daily.visits), sum(daily.pageviews), sum(daily.bounces),
       sum(daily.duration_ms), sum(daily.engagement_ms), sum(daily.engaged_n),
       sum(daily.scroll_sum), sum(daily.scroll_n)
from ready
join rollup_day_dim daily
  on daily.site_id = ready.site_id
 and daily.day >= ready.start_day and daily.day < ready.start_day + 2
group by ready.site_id, daily.dim, ready.start_day, daily.value_id`

const insertParentBlockSQL = `
with wanted as (
  select site_id, start_day from unnest($1::int[], $2::date[]) values(site_id, start_day)
), ready as (
  select wanted.* from wanted
  join rollup_block_done left_block
    on left_block.site_id = wanted.site_id and left_block.k = $3 - 1
   and left_block.start_day = wanted.start_day
  join rollup_block_done right_block
    on right_block.site_id = wanted.site_id and right_block.k = $3 - 1
   and right_block.start_day = wanted.start_day + $4::int
)
insert into rollup_block_dim (
  site_id, k, start_day, dim, value_id, visitors, visits, pageviews, bounces,
  duration_ms, engagement_ms, engaged_n, scroll_sum, scroll_n
)
select ready.site_id, $3, ready.start_day, child.dim, child.value_id,
       sum(child.visitors), sum(child.visits), sum(child.pageviews), sum(child.bounces),
       sum(child.duration_ms), sum(child.engagement_ms), sum(child.engaged_n),
       sum(child.scroll_sum), sum(child.scroll_n)
from ready
join rollup_block_dim child
  on child.site_id = ready.site_id and child.k = $3 - 1
 and (child.start_day = ready.start_day or child.start_day = ready.start_day + $4::int)
group by ready.site_id, child.dim, ready.start_day, child.value_id`

const insertBlockDoneSQL = `
with wanted as (
  select site_id, start_day from unnest($1::int[], $2::date[]) values(site_id, start_day)
)
insert into rollup_block_done (site_id, k, start_day)
select wanted.site_id, $3, wanted.start_day from wanted
join rollup_block_done left_block
  on left_block.site_id = wanted.site_id and left_block.k = $3 - 1
 and left_block.start_day = wanted.start_day
join rollup_block_done right_block
  on right_block.site_id = wanted.site_id and right_block.k = $3 - 1
 and right_block.start_day = wanted.start_day + $4::int`

func containingDayBlock(day time.Time, level uint8) time.Time {
	if level > maxDayBlockLevel {
		level = maxDayBlockLevel
	}
	days := day.Unix() / int64((24 * time.Hour).Seconds())
	size := int64(1) << level
	remainder := days % size
	if remainder < 0 {
		remainder += size
	}
	return time.Unix((days-remainder)*int64((24*time.Hour).Seconds()), 0).UTC()
}
