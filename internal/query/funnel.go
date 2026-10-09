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

	"supermetry/internal/api"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
)

// Funnel returns exact session counts for ordered page and custom-event steps.
// A later step must occur after the preceding step in the same session.
func (e *Engine) Funnel(ctx context.Context, req api.FunnelRequest) (api.FunnelResponse, error) {
	if e.trackingMode != privacy.Identified {
		return api.FunnelResponse{}, api.ErrAnonymousHistory
	}
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
	countBy := api.FunnelCountBySessions
	if req.CountBy != nil {
		if !req.CountBy.Valid() {
			return api.FunnelResponse{}, fmt.Errorf("invalid funnel count_by %q", *req.CountBy)
		}
		countBy = *req.CountBy
	}
	window := api.FunnelWindowSession
	if req.Window != nil {
		if !req.Window.Valid() {
			return api.FunnelResponse{}, fmt.Errorf("invalid funnel window %q", *req.Window)
		}
		window = *req.Window
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
	ranges := splitDateRange(from, to, 8)
	if countBy == api.FunnelCountByVisitors {
		ranges = [][2]time.Time{{from, to}}
	}
	for _, dateRange := range ranges {
		dateRange := dateRange
		group.Go(func() error {
			querySQL, args := buildFunnelSQL(req.SiteId, dateRange[0], dateRange[1], closedThrough, matchers, countBy, window)
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
	response := api.FunnelResponse{CountBy: api.FunnelResponseCountBy(countBy), Steps: make([]api.FunnelStepResult, len(counts))}
	for i, count := range counts {
		conversion := 0.0
		if counts[0] > 0 {
			conversion = float64(count) / float64(counts[0]) * 100
		}
		response.Steps[i] = api.FunnelStepResult{Step: i + 1, Count: count, Visitors: count, Conversion: conversion}
	}
	return response, nil
}

type funnelMatcher struct {
	page      bool
	ids       []int32
	propKey   string
	propValue string
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
	if (step.PropKey == nil) != (step.PropValue == nil) {
		return funnelMatcher{}, errors.New("prop_key and prop_value must be provided together")
	}
	if step.PropKey != nil && step.Type != api.Event {
		return funnelMatcher{}, errors.New("property conditions require an event step")
	}
	builder := &sqlBuilder{}
	condition, err := dictionaryMatchPredicate(builder, string(step.Match), step.Value)
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
	matcher := funnelMatcher{page: step.Type == api.Page, ids: ids}
	if step.PropKey != nil {
		matcher.propKey, matcher.propValue = *step.PropKey, *step.PropValue
	}
	return matcher, nil
}

func buildFunnelSQL(siteID int32, from, to, closedThrough time.Time, matchers []funnelMatcher, countBy api.FunnelRequestCountBy, window api.FunnelRequestWindow) (string, []any) {
	builder := &sqlBuilder{}
	siteArg := builder.arg(siteID)
	propertyCTEs, propertyNames := funnelPropertyCTEs(builder, siteArg, from, to, matchers)
	if window == api.FunnelWindowSession {
		return buildSessionFunnelSQL(builder, siteArg, from, to, closedThrough, matchers, countBy, propertyCTEs, propertyNames)
	}
	return buildTimedFunnelSQL(builder, siteArg, from, to, closedThrough, matchers, countBy, window, propertyCTEs, propertyNames)
}

func funnelPropertyCTEs(builder *sqlBuilder, siteArg string, from, to time.Time, matchers []funnelMatcher) ([]string, map[int]string) {
	ctes := make([]string, 0)
	names := make(map[int]string)
	for i, matcher := range matchers {
		if matcher.propKey == "" {
			continue
		}
		name := "property_match_" + strconv.Itoa(i+1)
		names[i] = name
		idsArg := builder.arg(matcher.ids)
		keyArg, valueArg := builder.arg(matcher.propKey), builder.arg(matcher.propValue)
		fromArg, toArg := builder.arg(dateParam(from)), builder.arg(dateParam(to))
		// Legacy rows lack session IDs, so min(session_id) can misattribute a match under session-scoped filters.
		ctes = append(ctes, name+` as materialized (
  with exact_matches as (
    select distinct c.day,c.visitor_id,c.session_id
    from custom_closed c
    where c.site_id=`+siteArg+` and c.day between `+fromArg+`::date and `+toArg+`::date
      and c.session_id is not null and c.name_id=any(`+idsArg+`::int[]) and c.props ->> `+keyArg+`=`+valueArg+`
  ), legacy_days as (
    select distinct c.day,c.visitor_id
    from custom_closed c
    where c.site_id=`+siteArg+` and c.day between `+fromArg+`::date and `+toArg+`::date
      and c.session_id is null and c.name_id=any(`+idsArg+`::int[]) and c.props ->> `+keyArg+`=`+valueArg+`
  ), legacy_matches as (
    select d.day,d.visitor_id,min(s.session_id) as session_id
    from legacy_days d join sessions_closed s using(day,visitor_id)
    where s.site_id=`+siteArg+` and exists (
      select 1 from unnest(s.seq) u(code) where u.code<0 and -u.code=any(`+idsArg+`::int[])
    ) group by d.day,d.visitor_id
  )
  select * from exact_matches union select * from legacy_matches
)`)
	}
	return ctes, names
}

func funnelEncodedCodeMatch(builder *sqlBuilder, alias string, matcher funnelMatcher) string {
	idsArg := builder.arg(matcher.ids)
	if matcher.page {
		return alias + ".code>0 and (" + alias + ".code>>2)=any(" + idsArg + "::int[])"
	}
	return alias + ".code<0 and -" + alias + ".code=any(" + idsArg + "::int[])"
}

func funnelRawCodeMatch(builder *sqlBuilder, alias string, matcher funnelMatcher) string {
	idsArg := builder.arg(matcher.ids)
	if matcher.page {
		return alias + ".kind=0 and " + alias + ".path_id=any(" + idsArg + "::int[])"
	}
	return alias + ".kind=1 and " + alias + ".name_id=any(" + idsArg + "::int[])"
}

func funnelCodeUnion(builder *sqlBuilder, alias string, matchers []funnelMatcher, encoded bool) string {
	conditions := make([]string, len(matchers))
	for i, matcher := range matchers {
		if encoded {
			conditions[i] = "(" + funnelEncodedCodeMatch(builder, alias, matcher) + ")"
		} else {
			conditions[i] = "(" + funnelRawCodeMatch(builder, alias, matcher) + ")"
		}
	}
	return "(" + strings.Join(conditions, " or ") + ")"
}

func funnelPropertyJoins(from string, columns []string, propertyNames map[int]string) (string, []string) {
	maxIndex := -1
	for i := range propertyNames {
		maxIndex = max(maxIndex, i)
	}
	for i := 0; i <= maxIndex; i++ {
		name := propertyNames[i]
		if name == "" {
			continue
		}
		alias := "pm" + strconv.Itoa(i+1)
		from += " left join " + name + " " + alias + " using(day,visitor_id,session_id)"
		columns = append(columns, alias+".session_id is not null as "+name)
	}
	return from, columns
}

func funnelEventMatch(builder *sqlBuilder, alias string, index int, matcher funnelMatcher, propertyNames map[int]string) string {
	condition := funnelEncodedCodeMatch(builder, alias, matcher)
	if name := propertyNames[index]; name != "" {
		keyArg, valueArg := builder.arg(matcher.propKey), builder.arg(matcher.propValue)
		condition += " and ((" + alias + ".exact_props and " + alias + ".props ->> " + keyArg + "=" + valueArg + ") or (not " + alias + ".exact_props and " + alias + "." + name + "))"
	}
	return "(" + condition + ")"
}

func funnelIdentity() string {
	return "case when profile_id is not null then 'p:'||profile_id::text else 'a:'||day::text||':'||visitor_id::text end"
}

func buildSessionFunnelSQL(builder *sqlBuilder, siteArg string, from, to, closedThrough time.Time, matchers []funnelMatcher, countBy api.FunnelRequestCountBy, propertyCTEs []string, propertyNames map[int]string) (string, []any) {
	var sequences []string
	closedTo := minTime(to, closedThrough)
	if !closedTo.Before(from) {
		sequences = append(sequences, `select day,visitor_id,session_id,profile_id,seq,
       array_fill(null::jsonb,array[cardinality(seq)]) as props,false as exact_props
from sessions_closed
where site_id=`+siteArg+` and day between `+builder.arg(dateParam(from))+`::date and `+builder.arg(dateParam(closedTo))+`::date`)
	}
	hotFrom := maxTime(from, closedThrough.AddDate(0, 0, 1))
	if !hotFrom.After(to) {
		eventStartArg, eventEndArg := hotEventRangeArgs(builder, Part{From: hotFrom, To: to})
		codeFilter := funnelCodeUnion(builder, "e", matchers, false)
		sequences = append(sequences, `select s.local_day as day,s.visitor_id,s.session_id,max(e.profile_id) as profile_id,
       array_agg(case when e.kind=0 then e.path_id << 2 else -e.name_id end
         order by e.ts,(case when e.kind=0 then e.path_id << 2 else -e.name_id end) desc) as seq,
       array_agg(e.props order by e.ts,(case when e.kind=0 then e.path_id << 2 else -e.name_id end) desc) as props,
       true as exact_props
from sessions s join events e on e.site_id=s.site_id and e.session_id=s.session_id
where s.site_id=`+siteArg+` and s.local_day between `+builder.arg(dateParam(hotFrom))+`::date and `+builder.arg(dateParam(to))+`::date
  and e.site_id=`+siteArg+` and e.ts>=`+eventStartArg+` and e.ts<`+eventEndArg+` and `+codeFilter+`
group by s.local_day,s.visitor_id,s.session_id`)
	}
	if len(sequences) == 0 {
		sequences = append(sequences, "select null::date day,0::int8 visitor_id,0::int8 session_id,null::int8 profile_id,'{}'::int[] seq,'{}'::jsonb[] props,false exact_props where false")
	}

	ctes := append([]string{}, propertyCTEs...)
	ctes = append(ctes, "sequences_base as ("+strings.Join(sequences, " union all ")+")")
	fromSQL, columns := funnelPropertyJoins("sequences_base s", []string{"s.*"}, propertyNames)
	ctes = append(ctes, "sequences as (select "+strings.Join(columns, ",")+" from "+fromSQL+")")
	ctes = append(ctes, "step0 as (select *,"+funnelIdentity()+" as identity_key,0::int8 as p0 from sequences)")
	for i, matcher := range matchers {
		previous, current := "step"+strconv.Itoa(i), "step"+strconv.Itoa(i+1)
		previousPosition, currentPosition := "p"+strconv.Itoa(i), "p"+strconv.Itoa(i+1)
		condition := funnelEncodedCodeMatch(builder, "u", matcher)
		if name := propertyNames[i]; name != "" {
			keyArg, valueArg := builder.arg(matcher.propKey), builder.arg(matcher.propValue)
			condition += " and ((s.exact_props and u.props ->> " + keyArg + "=" + valueArg + ") or (not s.exact_props and s." + name + "))"
		}
		ctes = append(ctes, current+` as (
  select s.*,(select min(u.ord)::int8 from unnest(s.seq,s.props) with ordinality u(code,props,ord)
              where u.ord>s.`+previousPosition+` and `+condition+`) as `+currentPosition+`
  from `+previous+` s
)`)
	}
	entity := "session_id"
	if countBy == api.FunnelCountByVisitors {
		entity = "identity_key"
	}
	counts := make([]string, len(matchers))
	for i := range matchers {
		counts[i] = "count(distinct " + entity + ") filter (where p" + strconv.Itoa(i+1) + " is not null)::int8"
	}
	return "with " + strings.Join(ctes, ",\n") + " select " + strings.Join(counts, ", ") + " from step" + strconv.Itoa(len(matchers)), builder.args
}

func buildTimedFunnelSQL(builder *sqlBuilder, siteArg string, from, to, closedThrough time.Time, matchers []funnelMatcher, countBy api.FunnelRequestCountBy, window api.FunnelRequestWindow, propertyCTEs []string, propertyNames map[int]string) (string, []any) {
	var events []string
	closedTo := minTime(to, closedThrough)
	if !closedTo.Before(from) {
		codeFilter := funnelCodeUnion(builder, "u", matchers, true)
		events = append(events, `select s.day,s.visitor_id,s.session_id,s.profile_id,
       coalesce(u.event_at,s.started_at + (u.ord-1) * interval '1 microsecond') as occurred_at,
       u.ord::int8 as local_order,u.code,null::jsonb as props,false as exact_props
from sessions_closed s
cross join lateral unnest(s.seq,s.seq_at) with ordinality u(code,event_at,ord)
where s.site_id=`+siteArg+` and s.day between `+builder.arg(dateParam(from))+`::date and `+builder.arg(dateParam(closedTo))+`::date
  and `+codeFilter)
	}
	hotFrom := maxTime(from, closedThrough.AddDate(0, 0, 1))
	if !hotFrom.After(to) {
		eventStartArg, eventEndArg := hotEventRangeArgs(builder, Part{From: hotFrom, To: to})
		codeFilter := funnelCodeUnion(builder, "e", matchers, false)
		events = append(events, `select s.local_day as day,s.visitor_id,s.session_id,
       max(e.profile_id) over (partition by e.site_id,e.session_id) as profile_id,e.ts as occurred_at,
       row_number() over (partition by e.site_id,e.session_id order by e.ts,
         (case when e.kind=0 then e.path_id << 2 else -e.name_id end) desc)::int8 as local_order,
       case when e.kind=0 then e.path_id << 2 else -e.name_id end as code,e.props,true as exact_props
from sessions s join events e on e.site_id=s.site_id and e.session_id=s.session_id
where s.site_id=`+siteArg+` and s.local_day between `+builder.arg(dateParam(hotFrom))+`::date and `+builder.arg(dateParam(to))+`::date
  and e.site_id=`+siteArg+` and e.ts>=`+eventStartArg+` and e.ts<`+eventEndArg+` and `+codeFilter)
	}
	if len(events) == 0 {
		events = append(events, "select null::date day,0::int8 visitor_id,0::int8 session_id,null::int8 profile_id,null::timestamptz occurred_at,0::int8 local_order,0::int4 code,null::jsonb props,false exact_props where false")
	}

	ctes := append([]string{}, propertyCTEs...)
	ctes = append(ctes, "funnel_events as ("+strings.Join(events, " union all ")+")")
	matchedFrom, matchedColumns := funnelPropertyJoins("funnel_events e", []string{"e.*"}, propertyNames)
	matchAny := make([]string, len(matchers))
	for i, matcher := range matchers {
		matchAny[i] = funnelEventMatch(builder, "e", i, matcher, propertyNames)
	}
	ctes = append(ctes, "matched_events as (select "+strings.Join(matchedColumns, ",")+" from "+matchedFrom+" where "+strings.Join(matchAny, " or ")+")")
	ctes = append(ctes, "identified_events as (select *,"+funnelIdentity()+" as identity_key from matched_events)")
	unit := "session_id::text"
	if countBy == api.FunnelCountByVisitors {
		unit = "identity_key"
	}
	ctes = append(ctes, `ordered_events as materialized (
  select *,`+unit+` as unit_key,
         row_number() over (partition by `+unit+` order by occurred_at,session_id,local_order)::int8 as event_order
  from identified_events
)`)
	ctes = append(ctes, "step1 as (select e.*,case when "+funnelEventMatch(builder, "e", 0, matchers[0], propertyNames)+" then e.occurred_at end as anchor_1 from ordered_events e)")
	windowLimit := "interval '7 days'"
	switch window {
	case api.FunnelWindow1h:
		windowLimit = "interval '1 hour'"
	case api.FunnelWindow24h:
		windowLimit = "interval '24 hours'"
	}
	for i := 1; i < len(matchers); i++ {
		stepNumber := i + 1
		previous := "step" + strconv.Itoa(i)
		prior := "step" + strconv.Itoa(stepNumber) + "_prior"
		current := "step" + strconv.Itoa(stepNumber)
		previousAnchor := "anchor_" + strconv.Itoa(i)
		priorAnchor := "prior_anchor_" + strconv.Itoa(stepNumber)
		currentAnchor := "anchor_" + strconv.Itoa(stepNumber)
		ctes = append(ctes, prior+` as (
  select s.*,max(s.`+previousAnchor+`) over (
    partition by s.unit_key order by s.event_order rows between unbounded preceding and 1 preceding
  ) as `+priorAnchor+`
  from `+previous+` s
)`)
		ctes = append(ctes, current+` as (
  select p.*,case when p.`+priorAnchor+` is not null
    and p.occurred_at<=p.`+priorAnchor+`+`+windowLimit+`
    and `+funnelEventMatch(builder, "p", i, matchers[i], propertyNames)+`
    then p.`+priorAnchor+` end as `+currentAnchor+`
  from `+prior+` p
)`)
	}
	counts := make([]string, len(matchers))
	entity := "session_id"
	if countBy == api.FunnelCountByVisitors {
		entity = "identity_key"
	}
	for i := range matchers {
		counts[i] = "(select count(distinct " + entity + ") filter (where anchor_" + strconv.Itoa(i+1) + " is not null)::int8 from step" + strconv.Itoa(i+1) + ")"
	}
	querySQL := "with " + strings.Join(ctes, ",\n") + " select " + strings.Join(counts, ", ")
	return querySQL, builder.args
}
