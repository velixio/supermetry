// Package heatmap queries the privacy-preserving click and scroll aggregates.
package heatmap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"supermetry/internal/api"
	"supermetry/internal/dict"
	analyticsquery "supermetry/internal/query"
	"supermetry/internal/sites"
)

const (
	maxCells     = 20_000
	queryTimeout = 10 * time.Second
)

// Service reads heatmap aggregates without exposing raw click coordinates.
type Service struct {
	pool     *pgxpool.Pool
	registry sites.Registry
}

// New constructs a heatmap query service.
func New(pool *pgxpool.Pool, registry sites.Registry) *Service {
	return &Service{pool: pool, registry: registry}
}

// Query returns click cells and the share of sampled pageviews reaching each
// ten-percent scroll band.
func (s *Service) Query(ctx context.Context, req api.HeatmapRequest) (api.HeatmapResponse, error) {
	response := emptyResponse()
	if s == nil || s.pool == nil || s.registry == nil {
		return response, errors.New("heatmap query service is not configured")
	}
	site, ok := s.registry.Site(req.SiteId)
	if !ok {
		return response, fmt.Errorf("unknown site %d", req.SiteId)
	}
	normalized, err := normalize(req, site)
	if err != nil {
		return response, err
	}

	err = analyticsquery.WithStatementTimeout(ctx, s.pool, queryTimeout, func(tx pgx.Tx) error {
		var pathID int32
		err := tx.QueryRow(ctx, `select id from dict where kind=$1 and value=$2`, dict.Path, normalized.path).Scan(&pathID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("resolve heatmap path: %w", err)
		}

		if err := queryCells(ctx, tx, normalized, pathID, &response); err != nil {
			return err
		}
		if err := queryScroll(ctx, tx, normalized, pathID, &response); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return emptyResponse(), err
	}
	return response, nil
}

type normalizedRequest struct {
	siteID         int32
	retentionClass int16
	from           time.Time
	to             time.Time
	fromTimestamp  time.Time
	toTimestamp    time.Time
	path           string
	device         int16
}

func normalize(req api.HeatmapRequest, site sites.Site) (normalizedRequest, error) {
	if req.DateRange.From.Time.IsZero() || req.DateRange.To.Time.IsZero() {
		return normalizedRequest{}, errors.New("heatmap date_range.from and date_range.to are required")
	}
	from := civilDate(req.DateRange.From.Time)
	to := civilDate(req.DateRange.To.Time)
	if from.After(to) {
		return normalizedRequest{}, errors.New("heatmap date_range.from must not be after date_range.to")
	}
	if req.Path == "" {
		return normalizedRequest{}, errors.New("heatmap path is required")
	}
	if len(req.Path) > 2048 {
		return normalizedRequest{}, errors.New("heatmap path must not exceed 2048 bytes")
	}
	device, ok := deviceCode(req.Device)
	if !ok {
		return normalizedRequest{}, fmt.Errorf("unknown heatmap device %q", req.Device)
	}
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	return normalizedRequest{
		siteID:         req.SiteId,
		retentionClass: int16(site.Retention),
		from:           from,
		to:             to,
		fromTimestamp:  time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, location),
		toTimestamp:    time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1),
		path:           req.Path,
		device:         device,
	}, nil
}

func queryCells(ctx context.Context, tx pgx.Tx, req normalizedRequest, pathID int32, response *api.HeatmapResponse) error {
	if err := tx.QueryRow(ctx, `
select coalesce(sum(clicks),0)::bigint
from heatmap_cells
where site_id=$1 and day between $2 and $3 and path_id=$4 and device=$5`,
		req.siteID, req.from, req.to, pathID, req.device).Scan(&response.Totals.Clicks); err != nil {
		return fmt.Errorf("query heatmap click total: %w", err)
	}
	rows, err := tx.Query(ctx, `
select gx,gy,sum(clicks)::bigint
from heatmap_cells
where site_id=$1 and day between $2 and $3 and path_id=$4 and device=$5
group by gx,gy
order by gx,gy
limit $6`, req.siteID, req.from, req.to, pathID, req.device, maxCells)
	if err != nil {
		return fmt.Errorf("query heatmap cells: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cell api.HeatmapCell
		if err := rows.Scan(&cell.Gx, &cell.Gy, &cell.Clicks); err != nil {
			return fmt.Errorf("scan heatmap cell: %w", err)
		}
		response.Cells = append(response.Cells, cell)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate heatmap cells: %w", err)
	}
	return nil
}

func queryScroll(ctx context.Context, tx pgx.Tx, req normalizedRequest, pathID int32, response *api.HeatmapResponse) error {
	rows, err := tx.Query(ctx, `
with closed_samples as (
  select sequence.scroll_pct
  from sessions_closed s
  cross join lateral unnest(s.seq,s.seq_scroll) as sequence(code,scroll_pct)
  where s.site_id=$1 and s.day between $5 and $6 and s.device=$8
    and sequence.code > 0 and sequence.code >> 2 = $7
), hot_pageviews as (
  select e.site_id,e.session_id,e.local_day,e.ts,e.path_id,e.device,
         lead(e.ts) over (partition by e.site_id,e.session_id order by e.ts,e.path_id) as next_ts
  from events e
  where e.site_id=$1 and e.retention_class=$2
    and e.ts >= $3 and e.ts < $4 and e.local_day between $5 and $6 and e.kind=0
    and not exists (
      select 1 from day_close closed
      where closed.site_id=e.site_id and closed.day=e.local_day and closed.status='done'
    )
), hot_samples as (
  select coalesce(max(engagement.scroll_pct),0)::smallint as scroll_pct
  from hot_pageviews pageview
  left join events engagement
    on engagement.site_id=pageview.site_id and engagement.session_id=pageview.session_id
   and engagement.retention_class=$2 and engagement.kind=2
   and engagement.ts >= pageview.ts
   and (pageview.next_ts is null or engagement.ts < pageview.next_ts)
  where pageview.path_id=$7 and pageview.device=$8
  group by pageview.site_id,pageview.session_id,pageview.ts
), sampled as (
  select scroll_pct from closed_samples
  union all
  select scroll_pct from hot_samples
), totals as (
  select count(*)::bigint as pageviews from sampled
)
select band::integer,
       case when totals.pageviews=0 then 0::float8
            else count(*) filter (where sampled.scroll_pct >= band)::float8 / totals.pageviews end,
       totals.pageviews
from generate_series(10,100,10) band
cross join totals
left join sampled on true
group by band,totals.pageviews
order by band`, req.siteID, req.retentionClass, req.fromTimestamp, req.toTimestamp, req.from, req.to, pathID, req.device)
	if err != nil {
		return fmt.Errorf("query heatmap scroll bands: %w", err)
	}
	defer rows.Close()
	response.ScrollBands = response.ScrollBands[:0]
	for rows.Next() {
		var band api.ScrollBand
		if err := rows.Scan(&band.Band, &band.Share, &response.Totals.PageviewsSampled); err != nil {
			return fmt.Errorf("scan heatmap scroll band: %w", err)
		}
		response.ScrollBands = append(response.ScrollBands, band)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate heatmap scroll bands: %w", err)
	}
	return nil
}

func emptyResponse() api.HeatmapResponse {
	bands := make([]api.ScrollBand, 10)
	for i := range bands {
		bands[i].Band = int32((i + 1) * 10)
	}
	return api.HeatmapResponse{Cells: []api.HeatmapCell{}, ScrollBands: bands}
}

func deviceCode(device api.HeatmapDevice) (int16, bool) {
	switch device {
	case api.HeatmapDeviceDesktop:
		return 0, true
	case api.HeatmapDeviceMobile:
		return 1, true
	case api.HeatmapDeviceTablet:
		return 2, true
	default:
		return 0, false
	}
}

func civilDate(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
