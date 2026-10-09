package closer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// LockRollupRefresh serializes erasure with closing/rebuilding derived data.
// Acquire it before reading erasure candidates, in the same transaction as the
// deletes. It does not fence ingestion or replace durable erasure tombstones.
func LockRollupRefresh(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, rollupRefreshLockID)
	return err
}

// ErrVerifiedPageSourceIncomplete prevents replacing durable provenance with a
// partial/expired raw stream. RecomputeRollups remains available after expiry.
var ErrVerifiedPageSourceIncomplete = errors.New("verified page source is incomplete; rebuild rollups from retained closed data instead of reclosing expired raw events")

const verifiedPageRawGroupsSQL = `
select site_id, local_day as day, session_id, visitor_id, path_id,
       path_policy_id, path_sanitizer_version, count(*)::bigint as pageviews
from events
where local_day=$1 and ts >= $2 and ts < $3 and site_id=any($4)
  and kind=0 and path_sanitizer_version=1 and path_policy_id<>'' and path_id>0
group by site_id, local_day, session_id, visitor_id, path_id, path_policy_id, path_sanitizer_version`

// refreshVerifiedPages executes before the existing close deletes any rows and
// shares its transaction and advisory lock. Checking each group (rather than a
// global count) catches partial raw expiry as well as complete expiry.
func refreshVerifiedPages(ctx context.Context, tx pgx.Tx, day, from, to time.Time, siteIDs []int32) error {
	var incomplete bool
	err := tx.QueryRow(ctx, `with raw as (`+verifiedPageRawGroupsSQL+`)
select exists (
 select 1 from verified_page_sessions closed
 left join raw using (site_id,day,session_id,visitor_id,path_id,path_policy_id,path_sanitizer_version)
 where closed.day=$1 and closed.site_id=any($4)
   and coalesce(raw.pageviews,0)<closed.pageviews
)`, day, from, to, siteIDs).Scan(&incomplete)
	if err != nil {
		return fmt.Errorf("check verified page source: %w", err)
	}
	if incomplete {
		return ErrVerifiedPageSourceIncomplete
	}
	if _, err := tx.Exec(ctx, `delete from verified_page_sessions where day=$1 and site_id=any($2)`, day, siteIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into verified_page_sessions
 (site_id,day,session_id,visitor_id,path_id,path_policy_id,path_sanitizer_version,pageviews)
 `+verifiedPageRawGroupsSQL, day, from, to, siteIDs); err != nil {
		return fmt.Errorf("close verified pages: %w", err)
	}
	return rebuildVerifiedPageDay(ctx, tx, day, siteIDs)
}

func rebuildVerifiedPageDay(ctx context.Context, tx pgx.Tx, day time.Time, siteIDs []int32) error {
	if _, err := tx.Exec(ctx, `delete from verified_page_day where day=$1 and site_id=any($2)`, day, siteIDs); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `insert into verified_page_day
 (site_id,day,path_sanitizer_version,path_policy_id,path_id,visitors,visits,pageviews)
 select site_id,day,path_sanitizer_version,path_policy_id,coalesce(path_id,0),
        count(distinct visitor_id),count(distinct session_id),sum(pageviews)
 from verified_page_sessions
 where day=$1 and site_id=any($2)
 group by grouping sets (
  (site_id,day,path_sanitizer_version,path_policy_id,path_id),
  (site_id,day,path_sanitizer_version,path_policy_id)
 )`, day, siteIDs)
	if err != nil {
		return fmt.Errorf("rebuild verified page totals: %w", err)
	}
	return nil
}
