package query

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"panel/internal/api"
	"panel/internal/dict"
	platformgoals "panel/internal/platform/goals"
)

// GoalLookup supplies the current per-site goal definitions.
type GoalLookup interface {
	Goals(int32) []platformgoals.Goal
}

type tierKind int

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
		if req.GoalDefinitions == nil {
			req.GoalDefinitions = make(map[int64]platformgoals.Goal)
		}
		req.GoalDefinitions[id] = found
	}
	return nil
}

// SetGoalLookup attaches the shared goal snapshot after the query engine has
// been constructed. Startup uses this to resolve goal_id filters without a
// database read on every analytics query.
func (e *Engine) SetGoalLookup(definitions GoalLookup) {
	e.goals = definitions
}

func goalCondition(b *sqlBuilder, tier tierKind, goal platformgoals.Goal) (string, error) {
	switch goal.Kind {
	case "event":
		return eventGoalCondition(b, tier, goal), nil
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

func eventGoalCondition(b *sqlBuilder, tier tierKind, goal platformgoals.Goal) string {
	kind := strconv.Itoa(int(dict.EventName))
	if tier == tierHot {
		condition := "ce.site_id=s.site_id and ce.session_id=s.session_id and ce.kind=1 and ce.name_id in (select id from dict where kind=" + kind + " and value=" + b.arg(goal.Value) + ")"
		if goal.PropKey != "" {
			condition += " and ce.props ->> " + b.arg(goal.PropKey) + " = " + b.arg(goal.PropValue)
		}
		return "exists (select 1 from events ce where " + condition + ")"
	}
	if goal.PropKey != "" {
		return "exists (select 1 from custom_closed ce where ce.site_id=s.site_id and ce.day=s.day and ce.visitor_id=s.visitor_id and ce.name_id in (select id from dict where kind=" + kind + " and value=" + b.arg(goal.Value) + ") and ce.props ->> " + b.arg(goal.PropKey) + " = " + b.arg(goal.PropValue) + ")"
	}
	return "exists (select 1 from unnest(s.seq) u(code) where u.code < 0 and -u.code in (select id from dict where kind=" + kind + " and value=" + b.arg(goal.Value) + "))"
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
	var predicate string
	switch match {
	case "exact":
		predicate = "value=" + b.arg(value)
	case "prefix":
		predicate = "value like " + b.arg(EscapeLike(value)+"%") + ` escape '\'`
	case "wildcard":
		parts := strings.Split(value, "*")
		for i := range parts {
			parts[i] = EscapeLike(parts[i])
		}
		predicate = "value like " + b.arg(strings.Join(parts, "%")) + ` escape '\'`
	default:
		return "", fmt.Errorf("unknown goal match %q", match)
	}
	return "select id from dict where kind=" + strconv.Itoa(int(dict.Path)) + " and " + predicate, nil
}

func goalFilterCondition(builder *sqlBuilder, filter api.Filter, tier tierKind, definitions map[int64]platformgoals.Goal) (string, error) {
	id, err := strconv.ParseInt(filter.Values[0], 10, 64)
	if err != nil {
		return "", errors.New(`invalid filter "goal_id"`)
	}
	goal, ok := definitions[id]
	if !ok {
		return "", errors.New(`invalid filter "goal_id"`)
	}
	return goalCondition(builder, tier, goal)
}
