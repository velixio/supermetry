package query

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"panel/internal/api"
	"panel/internal/privacy"
	"panel/internal/sites"
)

// The verified view uses only page events, never whole-session counters. A
// visitor/visit means a daily visitor/session containing at least one verified
// pageview under the current immutable policy. Imports are never unioned here.
// A single statement observes day_close and both data tiers atomically so a
// concurrent close cannot cause missing or doubled pageviews.
const verifiedPathsSQL = `
with hot as (
 select e.local_day as day, e.path_id, e.visitor_id, e.session_id
 from events e
 where e.site_id=$1 and e.local_day >= $2 and e.local_day <= $3
   and e.ts >= $2::date - interval '1 day' and e.ts < $3::date + interval '2 days'
   and e.kind=0 and e.path_id>0 and e.path_sanitizer_version=$4 and e.path_policy_id=$5
   and not exists(select 1 from day_close dc where dc.site_id=e.site_id and dc.day=e.local_day and dc.status='done')
), hot_counts as (
 select day, coalesce(path_id,0) as path_id, count(distinct visitor_id)::bigint as visitors,
   count(distinct session_id)::bigint as visits, count(*)::bigint as pageviews
 from hot group by grouping sets ((day,path_id),(day))
), combined as (
 select path_id,visitors,visits,pageviews from hot_counts
 union all
 select v.path_id,v.visitors,v.visits,v.pageviews from verified_page_day v
 join day_close dc on dc.site_id=v.site_id and dc.day=v.day and dc.status='done'
 where v.site_id=$1 and v.day >= $2 and v.day <= $3
   and v.path_sanitizer_version=$4 and v.path_policy_id=$5
)
select c.path_id,coalesce(d.value,''),sum(c.visitors)::bigint,sum(c.visits)::bigint,sum(c.pageviews)::bigint
from combined c left join dict d on d.kind=0 and d.id=c.path_id
group by c.path_id,d.value`

func (e *Engine) queryVerifiedPaths(ctx context.Context, request api.QueryRequest, site sites.Site, req Normalized, now time.Time, metadata closeMetadata) (api.QueryResponse, error) {
	siteLocation := site.TZ
	if siteLocation == nil {
		siteLocation = time.UTC
	}
	if req.Location.String() != siteLocation.String() {
		return api.QueryResponse{}, errors.New("verified path queries require the site time zone")
	}
	if err := globalQuerySlots.Acquire(ctx, 1); err != nil {
		return api.QueryResponse{}, err
	}
	var result []resultRow
	totals := Sums{}
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, verifiedPathsSQL, req.SiteID, req.From, req.To, privacy.PathSanitizerVersion, e.pathPolicy.Fingerprint())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int32
			var label string
			var sums Sums
			if err := rows.Scan(&id, &label, &sums.Visitors, &sums.Visits, &sums.Pageviews); err != nil {
				return err
			}
			if id == 0 {
				totals = sums
				continue
			}
			// A bad dictionary/provenance pairing is corruption, not permission to
			// disclose a label or combine incompatible per-path distinct counts.
			if e.pathPolicy.PublicPath(label) != label {
				return api.ErrAnonymousHistory
			}
			result = append(result, resultRow{dimensions: []string{label}, sums: sums})
		}
		return rows.Err()
	})
	globalQuerySlots.Release(1)
	if err != nil {
		return api.QueryResponse{}, fmt.Errorf("verified paths: %w", err)
	}
	sortResultRows(result, req)
	offset, err := cursorOffset(req.Cursor)
	if err != nil {
		return api.QueryResponse{}, err
	}
	offset = min(offset, len(result))
	result = result[offset:]
	var cursor *string
	if len(result) > req.Limit {
		v := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + req.Limit)))
		cursor = &v
		result = result[:req.Limit]
	}
	response := api.QueryResponse{
		Columns: append(append([]string(nil), req.Dimensions...), metricNames(req.Metrics)...),
		Rows:    make([]map[string]any, 0, len(result)), Totals: finalMetrics(req.Metrics, totals, Sums{}, nil), NextCursor: cursor,
		Meta: api.QueryMeta{SourcesUsed: []string{"verified_hot", "verified_day"}, Privacy: &api.QueryPrivacy{
			View: "verified_paths", Coverage: "verified_only", PathSanitizerVersion: int(privacy.PathSanitizerVersion), PolicyId: e.pathPolicy.Fingerprint(), ImportsExcluded: true, UnverifiedExcluded: true,
		}},
	}
	for _, row := range result {
		item := map[string]any{req.Dimensions[0]: row.dimensions[0]}
		for k, v := range finalMetrics(req.Metrics, row.sums, Sums{}, req.Dimensions) {
			item[k] = v
		}
		response.Rows = append(response.Rows, item)
	}
	if req.Compare != nil {
		from, to := comparisonRange(req.From, req.To, *req.Compare)
		comparisonRequest := request
		comparisonRequest.Period = nil
		comparisonRequest.Compare = nil
		comparisonRequest.DateRange = &api.DateRange{From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: to}}
		normalized, problem := Normalize(comparisonRequest, site, now)
		if problem != nil {
			return api.QueryResponse{}, errors.New(problemDetail(problem))
		}
		comparison, err := e.queryNormalized(ctx, comparisonRequest, site, normalized, now, metadata)
		if err != nil {
			return api.QueryResponse{}, err
		}
		response.Comparison = &api.ComparisonResult{DateRange: *comparisonRequest.DateRange, Rows: comparison.Rows, Totals: comparison.Totals}
	}
	return response, nil
}
