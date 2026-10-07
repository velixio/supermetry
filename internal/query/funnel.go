package query

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"

	"panel/internal/api"
	"panel/internal/dict"
)

// Funnel returns exact session counts for ordered page and custom-event steps.
// A later step must occur after the preceding step in the same session.
func (e *Engine) Funnel(ctx context.Context, req api.FunnelRequest) (api.FunnelResponse, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return api.FunnelResponse{}, errors.New("query engine is not configured")
	}
	if _, ok := e.registry.Site(req.SiteId); !ok {
		return api.FunnelResponse{}, fmt.Errorf("unknown site %d", req.SiteId)
	}
	if len(req.Steps) < 2 {
		return api.FunnelResponse{}, errors.New("a funnel requires at least two steps")
	}
	from, to := civilDate(req.DateRange.From.Time), civilDate(req.DateRange.To.Time)
	if from.IsZero() || to.IsZero() || from.After(to) {
		return api.FunnelResponse{}, errors.New("invalid funnel date range")
	}
	if daysInclusive(from, to) > maxRangeDays {
		return api.FunnelResponse{}, errors.New("funnel date range must not exceed 366 days")
	}
	closedThrough, err := e.closedThrough(ctx, req.SiteId, from, to)
	if err != nil {
		return api.FunnelResponse{}, err
	}
	matchers := make([]funnelMatcher, len(req.Steps))
	for i, step := range req.Steps {
		matcher, matcherErr := e.resolveFunnelStep(ctx, step)
		if matcherErr != nil {
			return api.FunnelResponse{}, fmt.Errorf("funnel step %d: %w", i+1, matcherErr)
		}
		matchers[i] = matcher
	}
	counts := make([]int64, len(matchers))
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	for _, dateRange := range splitDateRange(from, to, 8) {
		dateRange := dateRange
		group.Go(func() error {
			querySQL, args := buildFunnelSQL(req.SiteId, dateRange[0], dateRange[1], closedThrough, matchers)
			local := make([]int64, len(matchers))
			if acquireErr := globalQuerySlots.Acquire(groupCtx, 1); acquireErr != nil {
				return acquireErr
			}
			defer globalQuerySlots.Release(1)
			queryErr := WithStatementTimeout(groupCtx, e.pool, queryTimeout, func(tx pgx.Tx) error {
				destinations := make([]any, len(local))
				for i := range local {
					destinations[i] = &local[i]
				}
				return tx.QueryRow(groupCtx, querySQL, args...).Scan(destinations...)
			})
			if queryErr != nil {
				return queryErr
			}
			mu.Lock()
			for i := range local {
				counts[i] += local[i]
			}
			mu.Unlock()
			return nil
		})
	}
	err = group.Wait()
	if err != nil {
		return api.FunnelResponse{}, fmt.Errorf("query funnel: %w", err)
	}
	response := api.FunnelResponse{Steps: make([]api.FunnelStepResult, len(counts))}
	for i, count := range counts {
		conversion := 0.0
		if counts[0] > 0 {
			conversion = float64(count) / float64(counts[0]) * 100
		}
		response.Steps[i] = api.FunnelStepResult{Step: i + 1, Visitors: count, Conversion: conversion}
	}
	return response, nil
}

type funnelMatcher struct {
	page bool
	ids  []int32
}

func (e *Engine) resolveFunnelStep(ctx context.Context, step api.FunnelStep) (funnelMatcher, error) {
	var kind dict.Kind
	switch step.Type {
	case api.Page:
		kind = dict.Path
	case api.Event:
		kind = dict.EventName
	default:
		return funnelMatcher{}, fmt.Errorf("unsupported type %q", step.Type)
	}
	builder := &sqlBuilder{}
	filter := api.Filter{Operator: api.FilterOperator(step.Match), Values: []string{step.Value}}
	if step.Match == "exact" {
		filter.Operator = api.Is
	}
	condition, err := textMatch(builder, "value", filter)
	if err != nil {
		return funnelMatcher{}, err
	}
	var ids []int32
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, queryErr := tx.Query(ctx, "select id from dict where kind="+builder.arg(kind)+" and "+condition, builder.args...)
		if queryErr != nil {
			return queryErr
		}
		ids, queryErr = pgx.CollectRows(rows, pgx.RowTo[int32])
		return queryErr
	})
	if err != nil {
		return funnelMatcher{}, err
	}
	return funnelMatcher{page: step.Type == api.Page, ids: ids}, nil
}

func buildFunnelSQL(siteID int32, from, to, closedThrough time.Time, matchers []funnelMatcher) (string, []any) {
	builder := &sqlBuilder{}
	siteArg := builder.arg(siteID)
	var sequences []string
	closedTo := minTime(to, closedThrough)
	if !closedTo.Before(from) {
		sequences = append(sequences, `select day, visitor_id, session_id, seq from sessions_closed
where site_id=`+siteArg+` and day between `+builder.arg(from)+`::date and `+builder.arg(closedTo)+`::date`)
	}
	hotFrom := maxTime(from, closedThrough.AddDate(0, 0, 1))
	if !hotFrom.After(to) {
		sequences = append(sequences, `select s.local_day as day, s.visitor_id, s.session_id,
	       array_agg(case when e.kind=0 then e.path_id << 2 else -e.name_id end
	                 order by e.ts, (case when e.kind=0 then e.path_id << 2 else -e.name_id end) desc) as seq
from sessions s join events e on e.site_id=s.site_id and e.session_id=s.session_id and e.kind in (0,1)
where s.site_id=`+siteArg+` and s.local_day between `+builder.arg(hotFrom)+`::date and `+builder.arg(to)+`::date
group by s.local_day, s.visitor_id, s.session_id`)
	}
	if len(sequences) == 0 {
		sequences = append(sequences, "select null::date day, 0::int8 visitor_id, 0::int8 session_id, '{}'::int[] seq where false")
	}

	ctes := []string{"sequences as (" + strings.Join(sequences, " union all ") + ")", "step0 as (select day, visitor_id, session_id, seq, 0::int8 as p0 from sequences)"}
	for i, matcher := range matchers {
		idsArg := builder.arg(matcher.ids)
		codeMatch := "u.code = any(" + idsArg + "::int[])"
		if matcher.page {
			codeMatch = "u.code > 0 and (u.code >> 2) = any(" + idsArg + "::int[])"
		} else {
			codeMatch = "u.code < 0 and -u.code = any(" + idsArg + "::int[])"
		}
		previous, current := "step"+strconv.Itoa(i), "step"+strconv.Itoa(i+1)
		previousPosition, currentPosition := "p"+strconv.Itoa(i), "p"+strconv.Itoa(i+1)
		ctes = append(ctes, current+` as (
  select s.*, (select min(u.ord)::int8 from unnest(s.seq) with ordinality u(code, ord)
               where u.ord > s.`+previousPosition+` and `+codeMatch+`) as `+currentPosition+`
  from `+previous+` s
)`)
	}
	counts := make([]string, len(matchers))
	for i := range matchers {
		counts[i] = "count(distinct (day, session_id)) filter (where p" + strconv.Itoa(i+1) + " is not null)::int8"
	}
	querySQL := "with " + strings.Join(ctes, ",\n") + " select " + strings.Join(counts, ", ") + " from step" + strconv.Itoa(len(matchers))
	return querySQL, builder.args
}
