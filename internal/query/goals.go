package query

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"supermetry/internal/api"
	"supermetry/internal/dict"
	platformgoals "supermetry/internal/platform/goals"
)

// GoalLookup supplies the current per-site goal definitions.
type GoalLookup interface {
	Goals(int32) []platformgoals.Goal
}

type tierKind int

const goalConversionTotalRow = "__distinct_converting_sessions__"

const (
	tierHot tierKind = iota
	tierClosed
)

func (e *Engine) resolveGoalFilters(req *Normalized) error {
	for _, filter := range req.Filters {
		if filter.Dimension != "goal_id" {
			continue
		}
		if e.goals == nil {
			return errors.New(`invalid filter "goal_id"`)
		}
		id, err := strconv.ParseInt(filter.Values[0], 10, 64)
		if err != nil || id <= 0 {
			return errors.New(`invalid filter "goal_id"`)
		}
		var found platformgoals.Goal
		for _, goal := range e.goals.Goals(req.SiteID) {
			if goal.ID == id && goal.SiteID == req.SiteID {
				found = goal
				break
			}
		}
		if found.ID == 0 {
			return errors.New(`invalid filter "goal_id"`)
		}
		if hasRevenueMetric(req.Metrics) && found.Kind != "event" {
			return errors.New(`invalid filter "goal_id": revenue metrics require an event goal`)
		}
		if hasRevenueMetric(req.Metrics) && found.Currency != "" && found.Currency != req.RevenueCurrency {
			return fmt.Errorf("invalid filter \"goal_id\": legacy goal currency %s does not match project currency %s", found.Currency, req.RevenueCurrency)
		}
		if req.GoalDefinitions == nil {
			req.GoalDefinitions = make(map[int64]platformgoals.Goal)
		}
		req.GoalDefinitions[id] = found
	}
	return nil
}

func (e *Engine) resolveGoalBreakdown(req *Normalized) error {
	if e.goals == nil {
		return errors.New("goal breakdown is unavailable")
	}
	if req.GoalDefinitions == nil {
		req.GoalDefinitions = make(map[int64]platformgoals.Goal)
	}
	for _, goal := range e.goals.Goals(req.SiteID) {
		if goal.ID > 0 && goal.SiteID == req.SiteID {
			req.GoalDefinitions[goal.ID] = goal
		}
	}
	return nil
}

// SetGoalLookup attaches the shared goal snapshot after the query engine has
// been constructed. Startup uses this to resolve goal_id filters without a
// database read on every analytics query.
func (e *Engine) SetGoalLookup(definitions GoalLookup) {
	e.goals = definitions
}

func goalCondition(b *sqlBuilder, tier tierKind, goal platformgoals.Goal, parts ...Part) (string, error) {
	switch goal.Kind {
	case "event":
		return eventGoalCondition(b, tier, goal, parts...)
	case "page":
		return pageGoalCondition(b, tier, goal)
	case "scroll":
		if goal.ScrollThreshold < 1 || goal.ScrollThreshold > 100 {
			return "", fmt.Errorf("invalid scroll threshold %d", goal.ScrollThreshold)
		}
		return scrollGoalCondition(b, tier, goal)
	default:
		return "", fmt.Errorf("unknown goal kind %q", goal.Kind)
	}
}

func hotGoalEventPredicate(b *sqlBuilder, goal platformgoals.Goal, alias string) (string, error) {
	prefix := alias + "."
	switch goal.Kind {
	case "event":
		ids, err := goalEventIDs(b, goal.Match, goal.Value)
		if err != nil {
			return "", err
		}
		condition := prefix + "kind=1 and " + prefix + "name_id in (" + ids + ")"
		if goal.PropKey != "" {
			condition += " and " + prefix + "props ->> " + b.arg(goal.PropKey) + " = " + b.arg(goal.PropValue)
		}
		return condition, nil
	case "page":
		paths, err := goalPathIDs(b, goal.Match, goal.Value)
		if err != nil {
			return "", err
		}
		return prefix + "kind=0 and " + prefix + "path_id in (" + paths + ")", nil
	case "scroll":
		if goal.ScrollThreshold < 1 || goal.ScrollThreshold > 100 {
			return "", fmt.Errorf("invalid scroll threshold %d", goal.ScrollThreshold)
		}
		paths, err := goalPathIDs(b, goal.Match, goal.Value)
		if err != nil {
			return "", err
		}
		return prefix + "kind=2 and " + prefix + "path_id in (" + paths + ") and " + prefix + "scroll_pct >= " + b.arg(goal.ScrollThreshold), nil
	default:
		return "", fmt.Errorf("unknown goal kind %q", goal.Kind)
	}
}

func eventGoalCondition(b *sqlBuilder, tier tierKind, goal platformgoals.Goal, parts ...Part) (string, error) {
	ids, err := goalEventIDs(b, goal.Match, goal.Value)
	if err != nil {
		return "", err
	}
	if tier == tierHot {
		condition := "ce.site_id=s.site_id and ce.session_id=s.session_id and ce.kind=1 and ce.name_id in (" + ids + ")"
		if goal.PropKey != "" {
			condition += " and ce.props ->> " + b.arg(goal.PropKey) + " = " + b.arg(goal.PropValue)
		}
		return "exists (select 1 from events ce where " + condition + ")", nil
	}
	if goal.PropKey != "" {
		keyArg, valueArg := b.arg(goal.PropKey), b.arg(goal.PropValue)
		siteArg := b.arg(goal.SiteID)
		dayBound := ""
		if len(parts) > 0 {
			dayBound = " and ce.day between " + b.arg(dateParam(parts[0].From)) + "::date and " + b.arg(dateParam(parts[0].To)) + "::date"
		}
		condition := "exists (select 1 from unnest(s.seq) u(code) where u.code < 0 and -u.code in (" + ids + "))"
		// Legacy rows lack session IDs, so min(session_id) can misattribute a match under session-scoped filters.
		matches := `(with exact_matches as materialized (
  select distinct ce.site_id,ce.day,ce.visitor_id,ce.session_id
  from custom_closed ce
  where ce.site_id=` + siteArg + dayBound + ` and ce.session_id is not null
    and ce.name_id in (` + ids + `) and ce.props ->> ` + keyArg + ` = ` + valueArg + `
), legacy_days as materialized (
  select distinct ce.site_id,ce.day,ce.visitor_id
  from custom_closed ce
  where ce.site_id=` + siteArg + dayBound + ` and ce.session_id is null
    and ce.name_id in (` + ids + `) and ce.props ->> ` + keyArg + ` = ` + valueArg + `
), legacy_matches as materialized (
  select ld.site_id,ld.day,ld.visitor_id,min(gs.session_id) as session_id
  from legacy_days ld
  join sessions_closed gs using(site_id,day,visitor_id)
  where exists (select 1 from unnest(gs.seq) gu(code) where gu.code<0 and -gu.code in (` + ids + `))
  group by ld.site_id,ld.day,ld.visitor_id
), custom_goal_matches as (
  select * from exact_matches union select * from legacy_matches
)
select 1 from custom_goal_matches gm
where (gm.site_id,gm.day,gm.visitor_id,gm.session_id)=(s.site_id,s.day,s.visitor_id,s.session_id))`
		return "(" + condition + " and exists " + matches + ")", nil
	}
	return "exists (select 1 from unnest(s.seq) u(code) where u.code < 0 and -u.code in (" + ids + "))", nil
}

func pageGoalCondition(b *sqlBuilder, tier tierKind, goal platformgoals.Goal) (string, error) {
	paths, err := goalPathIDs(b, goal.Match, goal.Value)
	if err != nil {
		return "", err
	}
	if tier == tierHot {
		return "exists (select 1 from events ge where ge.site_id=s.site_id and ge.session_id=s.session_id and ge.kind=0 and ge.path_id in (" + paths + "))", nil
	}
	return "exists (select 1 from unnest(s.seq) u(code) where u.code > 0 and (u.code >> 2) in (" + paths + "))", nil
}

func scrollGoalCondition(b *sqlBuilder, tier tierKind, goal platformgoals.Goal) (string, error) {
	paths, err := goalPathIDs(b, goal.Match, goal.Value)
	if err != nil {
		return "", err
	}
	threshold := b.arg(goal.ScrollThreshold)
	if tier == tierHot {
		return "exists (select 1 from events ge where ge.site_id=s.site_id and ge.session_id=s.session_id and ge.kind=2 and ge.path_id in (" + paths + ") and ge.scroll_pct >= " + threshold + ")", nil
	}
	return "exists (select 1 from unnest(s.seq, s.seq_scroll) u(code, scroll) where u.code > 0 and (u.code >> 2) in (" + paths + ") and u.scroll >= " + threshold + ")", nil
}

func goalPathIDs(b *sqlBuilder, match, value string) (string, error) {
	predicate, err := dictionaryMatchPredicate(b, match, value)
	if err != nil {
		return "", fmt.Errorf("goal path: %w", err)
	}
	return "select id from dict where kind=" + strconv.Itoa(int(dict.Path)) + " and " + predicate, nil
}

func goalEventIDs(b *sqlBuilder, match, value string) (string, error) {
	predicate, err := dictionaryMatchPredicate(b, match, value)
	if err != nil {
		return "", fmt.Errorf("goal event: %w", err)
	}
	return "select id from dict where kind=" + strconv.Itoa(int(dict.EventName)) + " and " + predicate, nil
}

func goalFilterCondition(builder *sqlBuilder, filter api.Filter, tier tierKind, definitions map[int64]platformgoals.Goal, parts ...Part) (string, error) {
	id, err := strconv.ParseInt(filter.Values[0], 10, 64)
	if err != nil {
		return "", errors.New(`invalid filter "goal_id"`)
	}
	goal, ok := definitions[id]
	if !ok {
		return "", errors.New(`invalid filter "goal_id"`)
	}
	return goalCondition(builder, tier, goal, parts...)
}

// customFilterConditions applies goal predicates to the revenue-bearing event
// row itself. A session-level EXISTS would count unrelated events in that session.
func customFilterConditions(builder *sqlBuilder, req Normalized, resolver dimensionResolver, alias string) ([]string, error) {
	conditions := make([]string, 0, len(req.Filters))
	for _, filter := range req.Filters {
		if filter.Dimension != "goal_id" {
			ordinary, err := filterConditions(builder, []api.Filter{filter}, resolver)
			if err != nil {
				return nil, err
			}
			conditions = append(conditions, ordinary...)
			continue
		}
		id, err := strconv.ParseInt(filter.Values[0], 10, 64)
		goal, ok := req.GoalDefinitions[id]
		if err != nil || !ok || goal.SiteID != req.SiteID {
			return nil, errors.New(`invalid filter "goal_id"`)
		}
		if goal.Kind != "event" {
			return nil, errors.New(`invalid filter "goal_id": custom event queries require an event goal`)
		}
		ids, matchErr := goalEventIDs(builder, goal.Match, goal.Value)
		if matchErr != nil {
			return nil, matchErr
		}
		condition := alias + ".name_id in (" + ids + ")"
		if goal.PropKey != "" {
			condition += " and " + alias + ".props ->> " + builder.arg(goal.PropKey) + " = " + builder.arg(goal.PropValue)
		}
		conditions = append(conditions, condition)
	}
	return conditions, nil
}

func buildGoalBreakdownSQL(req Normalized, part Part, tier tierKind) (string, []any, error) {
	builder := &sqlBuilder{}
	ids := make([]int64, 0, len(req.GoalDefinitions))
	for id := range req.GoalDefinitions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		return "select ''::text, 0::int8,0::int8,0::int8,0::int8,0::int8,0::int8,0::int8,0::int8,0::int8,0::int8,0::int8,0::int8,0::int8,0::int8 where false", nil, nil
	}
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		goal := req.GoalDefinitions[id]
		condition, err := goalCondition(builder, tier, goal, part)
		if err != nil {
			return "", nil, err
		}
		values = append(values, "("+builder.arg(strconv.FormatInt(id, 10))+"::text,"+condition+")")
	}
	goalIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		goalIDs = append(goalIDs, "("+builder.arg(strconv.FormatInt(id, 10))+"::text)")
	}
	dayColumn, table := "s.local_day", "sessions s"
	pageviews, custom, duration := "s.pageviews", "s.events", "round(extract(epoch from (s.last-s.start))*1000)"
	bounce := "s.bounce"
	if tier == tierClosed {
		dayColumn, table = "s.day", "sessions_closed s"
		custom, duration, bounce = "s.custom", "s.duration_ms", "s.pageviews=1 and s.custom=0"
	}
	conditions, err := goalBreakdownFilterConditions(builder, req, tier, part)
	if err != nil {
		return "", nil, err
	}
	where := "s.site_id=" + builder.arg(req.SiteID) + " and " + dayColumn + " between " + builder.arg(dateParam(part.From)) + "::date and " + builder.arg(dateParam(part.To)) + "::date" + andSQL(conditions) + " and g.matched"
	sums := []string{
		"count(distinct (" + dayColumn + ",s.visitor_id))::int8", "count(*)::int8", "coalesce(sum(" + pageviews + "),0)::int8",
		"count(*) filter (where " + bounce + ")::int8", "coalesce(sum(" + duration + "),0)::int8", "coalesce(sum(s.engagement_ms),0)::int8",
		"count(*) filter (where s.pageviews>0)::int8", "0::int8", "0::int8", "0::int8", "coalesce(sum(" + custom + "),0)::int8",
		"count(*)::int8", "0::int8", "0::int8",
	}
	aggregates := make([]string, len(sums))
	outputs := make([]string, len(sums))
	for i, sum := range sums {
		column := "v" + strconv.Itoa(i)
		aggregates[i] = sum + " as " + column
		outputs[i] = "coalesce(a." + column + ",0)::int8"
	}
	totalOutputs := make([]string, len(sums))
	for i := range totalOutputs {
		totalOutputs[i] = "0::int8"
	}
	totalOutputs[11] = "count(distinct s.session_id)::int8"
	querySQL := "with goal_ids(goal_id) as (values " + strings.Join(goalIDs, ",") + `),
matched as materialized (
  select g.goal_id,s.* from ` + table + " cross join lateral (values " + strings.Join(values, ",") + ") g(goal_id,matched) where " + where + `
), aggregates as (
  select s.goal_id, ` + strings.Join(aggregates, ", ") + ` from matched s group by s.goal_id
)
select d.goal_id, ` + strings.Join(outputs, ", ") + " from goal_ids d left join aggregates a using(goal_id)" +
		" union all select " + builder.arg(goalConversionTotalRow) + "::text, " + strings.Join(totalOutputs, ", ") + " from matched s"
	return querySQL, builder.args, nil
}

func goalBreakdownFilterConditions(builder *sqlBuilder, req Normalized, tier tierKind, part Part) ([]string, error) {
	pageFilters, remaining := splitPageFilters(req.Filters)
	special, ordinary := splitSpecialFilters(remaining)
	resolver := func(dimension string) (string, *dict.Kind, error) {
		switch dimension {
		case "source":
			return "s.source_id", dictionaryKind(dict.Source), nil
		case "channel":
			return channelSQL("s.source_id", "s.medium_id"), nil, nil
		case "browser":
			return "s.browser_id", dictionaryKind(dict.Browser), nil
		case "browser_version":
			return "s.browser_ver_id", dictionaryKind(dict.BrowserVersion), nil
		case "os":
			return "s.os_id", dictionaryKind(dict.OS), nil
		case "os_version":
			return "s.os_ver_id", dictionaryKind(dict.OSVersion), nil
		case "screen_size":
			return "s.screen_bucket", nil, nil
		case "language":
			return "s.language_id", dictionaryKind(dict.Language), nil
		case "hostname":
			return "s.hostname_id", dictionaryKind(dict.Hostname), nil
		case "entry":
			return "s.entry_path", dictionaryKind(dict.Path), nil
		case "exit":
			return "s.exit_path", dictionaryKind(dict.Path), nil
		case "utm_source":
			return "s.utm_source_id", dictionaryKind(dict.Source), nil
		case "utm_medium":
			return "s.medium_id", dictionaryKind(dict.Medium), nil
		case "utm_campaign":
			return "s.campaign_id", dictionaryKind(dict.Campaign), nil
		case "country":
			return "s.country", nil, nil
		case "region":
			return "s.region_id", dictionaryKind(dict.Region), nil
		case "city":
			return "s.city_id", dictionaryKind(dict.City), nil
		case "device":
			return "s.device", nil, nil
		default:
			return "", nil, fmt.Errorf("dimension %q is not available from goal sessions", dimension)
		}
	}
	conditions, err := filterConditions(builder, ordinary, resolver)
	if err != nil {
		return nil, err
	}
	for _, filter := range pageFilters {
		alias, sequence := "ge.path_id", "exists (select 1 from events ge where ge.site_id=s.site_id and ge.session_id=s.session_id and ge.kind=0 and "
		if tier == tierClosed {
			alias, sequence = "u.code >> 2", "exists (select 1 from unnest(s.seq) u(code) where u.code>0 and "
		}
		matches, matchErr := filterConditions(builder, []api.Filter{filter}, func(string) (string, *dict.Kind, error) {
			return alias, dictionaryKind(dict.Path), nil
		})
		if matchErr != nil {
			return nil, matchErr
		}
		conditions = append(conditions, sequence+matches[0]+")")
	}
	var specialConditions []string
	if tier == tierHot {
		specialConditions, err = hotSpecialConditions(builder, special, req.GoalDefinitions)
	} else {
		specialConditions, err = closedSpecialConditions(builder, special, part, req.GoalDefinitions)
	}
	if err != nil {
		return nil, err
	}
	return append(conditions, specialConditions...), nil
}
