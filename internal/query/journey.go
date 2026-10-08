package query

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"panel/internal/api"
	"panel/internal/db"
	"panel/internal/dict"
	"panel/internal/session"
	"panel/internal/sites"
)

const (
	journeyOther = "other"
	journeyExit  = "exit"
)

type journeyVisit struct {
	day       time.Time
	visitorID int64
	paths     []string
}

type journeyOptions struct {
	steps              int
	limit              int
	startPath, endPath *string
}

// Journey returns a raw-event path flow. Consecutive duplicate pageviews in
// each session are collapsed before steps and links are counted.
func (e *Engine) Journey(ctx context.Context, req api.JourneyRequest) (api.JourneyResponse, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return api.JourneyResponse{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(req.SiteId)
	if !ok {
		return api.JourneyResponse{}, fmt.Errorf("unknown site %d", req.SiteId)
	}
	if err := validateJourneyRequest(req, site, e.reportNow()); err != nil {
		return api.JourneyResponse{}, err
	}
	querySQL, args, err := buildJourneySQL(req, site.Retention, site.TZ, e.reportNow())
	if err != nil {
		return api.JourneyResponse{}, err
	}
	response := api.JourneyResponse{Nodes: []api.JourneyNode{}, Links: []api.JourneyLink{}}
	if err := globalQuerySlots.Acquire(ctx, 1); err != nil {
		return api.JourneyResponse{}, fmt.Errorf("query journeys: %w", err)
	}
	defer globalQuerySlots.Release(1)
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, queryErr := tx.Query(ctx, querySQL, args...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var kind, path string
			var step int
			var toPath *string
			var visitors int64
			if scanErr := rows.Scan(&kind, &step, &path, &toPath, &visitors); scanErr != nil {
				return scanErr
			}
			if kind == "node" {
				response.Nodes = append(response.Nodes, api.JourneyNode{Step: step, Path: path, Visitors: visitors})
			} else if toPath != nil {
				response.Links = append(response.Links, api.JourneyLink{FromStep: step, FromPath: path, ToPath: *toPath, Visitors: visitors})
			}
		}
		return rows.Err()
	})
	if err != nil {
		return api.JourneyResponse{}, fmt.Errorf("query journeys: %w", err)
	}
	return response, nil
}

func validateJourneyRequest(req api.JourneyRequest, site sites.Site, now time.Time) error {
	if req.Steps < 2 || req.Steps > 5 {
		return errors.New("steps must be between 2 and 5")
	}
	if req.Limit < 1 || req.Limit > 10 {
		return errors.New("limit must be between 1 and 10")
	}
	if req.StartPath != nil && req.EndPath != nil {
		return errors.New("provide start_path or end_path, not both")
	}
	return validateRawReportRange(req.SiteId, req.DateRange, filtersFrom(req.Filters), site, now)
}

func buildJourneySQL(req api.JourneyRequest, retention db.RetentionClass, location *time.Location, now time.Time) (string, []any, error) {
	builder := &sqlBuilder{}
	filters := filtersFrom(req.Filters)
	pageFilters, remaining := splitPageFilters(filters)
	specialFilters, sessionFilters := splitSpecialFilters(remaining)
	resolver := reportSessionResolver("s")
	conditions, err := filterConditions(builder, sessionFilters, resolver)
	if err != nil {
		return "", nil, err
	}
	for _, filter := range pageFilters {
		matches, matchErr := filterConditions(builder, []api.Filter{filter}, func(string) (string, *dict.Kind, error) {
			return "pe.path_id", dictionaryKind(dict.Path), nil
		})
		if matchErr != nil {
			return "", nil, matchErr
		}
		conditions = append(conditions, "exists (select 1 from events pe where pe.site_id=s.site_id and pe.session_id=s.session_id and pe.retention_class=s.retention_class and pe.local_day=s.local_day and pe.kind=0 and "+matches[0]+")")
	}
	specialConditions, err := hotSpecialConditions(builder, specialFilters)
	if err != nil {
		return "", nil, err
	}
	conditions = append(conditions, constrainReportEventConditions(specialConditions)...)
	from, to := civilDate(req.DateRange.From.Time), civilDate(req.DateRange.To.Time)
	if location == nil {
		location = time.UTC
	}
	startTS := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, location)
	endTS := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1)
	siteArg := builder.arg(req.SiteId)
	retentionArg := builder.arg(retention)
	fromArg, toArg := builder.arg(from), builder.arg(to)
	startArg, endArg := builder.arg(startTS), builder.arg(endTS)
	endedBeforeArg := builder.arg(now.Add(-session.IdleTimeout))
	stepsArg, limitArg := builder.arg(req.Steps), builder.arg(req.Limit)
	conditions = constrainReportEventTimes(conditions, startArg, endArg)
	where := "s.site_id=" + siteArg + " and s.retention_class=" + retentionArg +
		" and s.local_day between " + fromArg + "::date and " + toArg + "::date" +
		" and s.start >= " + startArg + " and s.start < " + endArg + andSQL(conditions)
	anchorIndex := "1"
	anchorPredicate := "true"
	displayPaths := "paths[1:" + stepsArg + "]"
	appendExit := true
	if req.StartPath != nil {
		anchor := builder.arg(*req.StartPath)
		anchorIndex = "array_position(paths," + anchor + ")"
		anchorPredicate = anchorIndex + " is not null"
		displayPaths = "paths[anchor_index:anchor_index+" + stepsArg + "-1]"
	} else if req.EndPath != nil {
		anchor := builder.arg(*req.EndPath)
		anchorIndex = "(select max(ord)::int from unnest(paths) with ordinality p(path,ord) where path=" + anchor + ")"
		anchorPredicate = anchorIndex + " is not null"
		displayPaths = "array_fill(null::text, array[greatest(0," + stepsArg + "-least(" + stepsArg + ",anchor_index))]) || paths[greatest(1,anchor_index-" + stepsArg + "+1):anchor_index]"
		appendExit = false
	}
	if appendExit {
		displayPaths = "case when ended and cardinality(" + displayPaths + ") < " + stepsArg + " then " + displayPaths + " || 'exit'::text else " + displayPaths + " end"
	}
	querySQL := `with selected_sessions as (
  select s.site_id, s.local_day, s.visitor_id, s.session_id,
         (s.last < ` + endArg + ` and s.last < ` + endedBeforeArg + `) as ended
  from sessions s
  where ` + where + `
), numbered as (
  select e.site_id, ss.local_day, ss.visitor_id, e.session_id, ss.ended, e.path_id, e.ts,
         row_number() over (partition by e.site_id, e.session_id order by e.ts, e.path_id) as event_order
  from selected_sessions ss
  join events e on e.site_id=ss.site_id and e.session_id=ss.session_id
  where e.site_id=` + siteArg + ` and e.retention_class=` + retentionArg + `
    and e.local_day between ` + fromArg + `::date and ` + toArg + `::date
    and e.ts >= ` + startArg + ` and e.ts < ` + endArg + ` and e.kind=0
), compared as (
  select *, lag(path_id) over (partition by site_id, session_id order by event_order) as previous_path
  from numbered
), sequences as (
  select c.local_day, c.visitor_id, c.session_id, c.ended, array_agg(d.value order by c.event_order) as paths
  from compared c
  join dict d on d.kind=` + fmt.Sprint(dict.Path) + ` and d.id=c.path_id
  where c.previous_path is distinct from c.path_id
  group by c.local_day, c.visitor_id, c.session_id, c.ended
), indexed as (
  select *, ` + anchorIndex + ` as anchor_index from sequences
), displayed as (
  select local_day, visitor_id, session_id, ended, ` + displayPaths + ` as paths
  from indexed where ` + anchorPredicate + `
), positions as (
  select local_day, visitor_id, session_id, ord::int as step, path
  from displayed cross join lateral unnest(paths) with ordinality p(path,ord)
  where path is not null
), raw_nodes as (
  select step, path, count(distinct (local_day,visitor_id))::int8 as visitors
  from positions group by step,path
), page_ranks as (
  select step,path,row_number() over (partition by step order by visitors desc,path) as rank
  from raw_nodes where path<>'exit'
), mapped as (
  select p.local_day,p.visitor_id,p.session_id,p.step,
         case when p.path='exit' then p.path when r.rank>` + limitArg + ` then 'other' else p.path end as path
  from positions p left join page_ranks r using(step,path)
), nodes as (
  select step,path,count(distinct (local_day,visitor_id))::int8 as visitors
  from mapped group by step,path
), links as (
  select a.step,a.path as from_path,b.path as to_path,
         count(distinct (a.local_day,a.visitor_id))::int8 as visitors
  from mapped a join mapped b on b.local_day=a.local_day and b.session_id=a.session_id and b.step=a.step+1
  group by a.step,a.path,b.path
), result as (
  select 'node'::text as kind,step,path,null::text as to_path,visitors from nodes
  union all
  select 'link',step,from_path,to_path,visitors from links
)
select kind,step,path,to_path,visitors from result
order by case kind when 'node' then 0 else 1 end,step,visitors desc,path,to_path nulls first`
	return querySQL, builder.args, nil
}

func buildJourney(visits []journeyVisit, options journeyOptions) api.JourneyResponse {
	type identity struct {
		day       int64
		visitorID int64
	}
	type positioned struct {
		identity identity
		paths    []string
	}
	positionedVisits := make([]positioned, 0, len(visits))
	pageCounts := make([]map[string]map[identity]struct{}, options.steps)
	for step := range pageCounts {
		pageCounts[step] = make(map[string]map[identity]struct{})
	}
	for _, visit := range visits {
		collapsed := collapseJourneyPaths(visit.paths)
		paths := positionJourney(collapsed, options)
		if paths == nil {
			continue
		}
		key := identity{day: civilDate(visit.day).Unix(), visitorID: visit.visitorID}
		positionedVisits = append(positionedVisits, positioned{identity: key, paths: paths})
		for step, path := range paths {
			if path == "" || path == journeyExit {
				continue
			}
			addJourneyIdentity(pageCounts[step], path, key)
		}
	}
	top := make([]map[string]bool, options.steps)
	for step := range top {
		top[step] = topJourneyPaths(pageCounts[step], options.limit)
	}
	nodes := make([]map[string]map[identity]struct{}, options.steps)
	links := make([]map[string]map[identity]struct{}, options.steps-1)
	nodeOrder := make([][]string, options.steps)
	linkOrder := make([][]string, options.steps-1)
	for step := range nodes {
		nodes[step] = make(map[string]map[identity]struct{})
		if step < len(links) {
			links[step] = make(map[string]map[identity]struct{})
		}
	}
	for _, visit := range positionedVisits {
		mapped := make([]string, len(visit.paths))
		for step, path := range visit.paths {
			if path != "" && path != journeyExit && !top[step][path] {
				path = journeyOther
			}
			mapped[step] = path
			if path == "" {
				continue
			}
			if _, exists := nodes[step][path]; !exists {
				nodeOrder[step] = append(nodeOrder[step], path)
			}
			addJourneyIdentity(nodes[step], path, visit.identity)
		}
		for step := 0; step+1 < len(mapped); step++ {
			if mapped[step] == "" || mapped[step+1] == "" {
				continue
			}
			key := mapped[step] + "\x00" + mapped[step+1]
			if _, exists := links[step][key]; !exists {
				linkOrder[step] = append(linkOrder[step], key)
			}
			addJourneyIdentity(links[step], key, visit.identity)
		}
	}
	response := api.JourneyResponse{Nodes: make([]api.JourneyNode, 0), Links: make([]api.JourneyLink, 0)}
	for step, order := range nodeOrder {
		for _, path := range order {
			response.Nodes = append(response.Nodes, api.JourneyNode{Step: step + 1, Path: path, Visitors: int64(len(nodes[step][path]))})
		}
	}
	for step, order := range linkOrder {
		for _, key := range order {
			parts := strings.SplitN(key, "\x00", 2)
			response.Links = append(response.Links, api.JourneyLink{FromStep: step + 1, FromPath: parts[0], ToPath: parts[1], Visitors: int64(len(links[step][key]))})
		}
	}
	return response
}

func collapseJourneyPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if len(result) == 0 || result[len(result)-1] != path {
			result = append(result, path)
		}
	}
	return result
}

func positionJourney(paths []string, options journeyOptions) []string {
	if len(paths) == 0 {
		return nil
	}
	if options.startPath != nil {
		index := indexPath(paths, *options.startPath, false)
		if index < 0 {
			return nil
		}
		paths = paths[index:]
	} else if options.endPath != nil {
		index := indexPath(paths, *options.endPath, true)
		if index < 0 {
			return nil
		}
		paths = paths[:index+1]
	}
	positioned := make([]string, options.steps)
	if options.endPath != nil {
		if len(paths) > options.steps {
			paths = paths[len(paths)-options.steps:]
		}
		copy(positioned[options.steps-len(paths):], paths)
		return positioned
	}
	count := copy(positioned, paths)
	if count < options.steps {
		positioned[count] = journeyExit
	}
	return positioned
}

func indexPath(paths []string, wanted string, last bool) int {
	if last {
		for index := len(paths) - 1; index >= 0; index-- {
			if paths[index] == wanted {
				return index
			}
		}
		return -1
	}
	for index, path := range paths {
		if path == wanted {
			return index
		}
	}
	return -1
}

func addJourneyIdentity[T comparable](counts map[string]map[T]struct{}, path string, identity T) {
	if counts[path] == nil {
		counts[path] = make(map[T]struct{})
	}
	counts[path][identity] = struct{}{}
}

func topJourneyPaths[T comparable](counts map[string]map[T]struct{}, limit int) map[string]bool {
	paths := make([]string, 0, len(counts))
	for path := range counts {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		if len(counts[paths[i]]) != len(counts[paths[j]]) {
			return len(counts[paths[i]]) > len(counts[paths[j]])
		}
		return paths[i] < paths[j]
	})
	if len(paths) > limit {
		paths = paths[:limit]
	}
	result := make(map[string]bool, len(paths))
	for _, path := range paths {
		result[path] = true
	}
	return result
}

func reportSessionResolver(alias string) dimensionResolver {
	prefix := alias + "."
	return func(dimension string) (string, *dict.Kind, error) {
		switch dimension {
		case "source", "utm_source":
			return prefix + "source_id", dictionaryKind(dict.Source), nil
		case "utm_medium":
			return prefix + "medium_id", dictionaryKind(dict.Medium), nil
		case "utm_campaign":
			return prefix + "campaign_id", dictionaryKind(dict.Campaign), nil
		case "browser":
			return prefix + "browser_id", dictionaryKind(dict.Browser), nil
		case "os":
			return prefix + "os_id", dictionaryKind(dict.OS), nil
		case "entry":
			return prefix + "entry_path", dictionaryKind(dict.Path), nil
		case "exit":
			return prefix + "exit_path", dictionaryKind(dict.Path), nil
		case "country":
			return prefix + "country", nil, nil
		case "region":
			return prefix + "region_id", dictionaryKind(dict.Region), nil
		case "city":
			return prefix + "city_id", dictionaryKind(dict.City), nil
		case "device":
			return prefix + "device", nil, nil
		case "time":
			return prefix + "local_day", nil, nil
		default:
			return "", nil, fmt.Errorf("filter dimension %q is not available for this report", dimension)
		}
	}
}

func filtersFrom(filters *[]api.Filter) []api.Filter {
	if filters == nil {
		return nil
	}
	return *filters
}

func constrainReportEventConditions(conditions []string) []string {
	for index := range conditions {
		conditions[index] = strings.ReplaceAll(conditions[index], "ce.session_id=s.session_id and", "ce.session_id=s.session_id and ce.retention_class=s.retention_class and ce.local_day=s.local_day and")
	}
	return conditions
}

func constrainReportEventTimes(conditions []string, startArg, endArg string) []string {
	for index := range conditions {
		conditions[index] = strings.ReplaceAll(conditions[index], "pe.local_day=s.local_day and", "pe.local_day=s.local_day and pe.ts >= "+startArg+" and pe.ts < "+endArg+" and")
		conditions[index] = strings.ReplaceAll(conditions[index], "ce.local_day=s.local_day and", "ce.local_day=s.local_day and ce.ts >= "+startArg+" and ce.ts < "+endArg+" and")
	}
	return conditions
}

func (e *Engine) reportNow() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

func validateRawReportRange(siteID int32, dateRange api.DateRange, filters []api.Filter, site sites.Site, now time.Time) error {
	from, to := civilDate(dateRange.From.Time), civilDate(dateRange.To.Time)
	if siteID != site.ID {
		return errors.New("site_id does not match the requested site")
	}
	if from.IsZero() || to.IsZero() || from.After(to) {
		return errors.New("invalid date range")
	}
	if daysInclusive(from, to) > maxRangeDays {
		return errors.New("date range must not exceed 366 days")
	}
	if len(filters) > maxFilters {
		return errors.New("at most 10 filters are allowed")
	}
	for _, filter := range filters {
		if !knownDimension(filter.Dimension) || !filter.Operator.Valid() || len(filter.Values) == 0 {
			return fmt.Errorf("invalid filter %q", filter.Dimension)
		}
	}
	if site.Retention != db.RetForever {
		location := site.TZ
		if location == nil {
			location = time.UTC
		}
		today := civilDate(now.In(location))
		earliest := today.AddDate(0, 0, -int(site.Retention)+1)
		if from.Before(earliest) {
			return errors.New("date range must be within the raw retention window")
		}
	}
	return nil
}
