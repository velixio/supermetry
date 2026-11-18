package search

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseDay(s string) (time.Time, error) { return time.Parse(time.DateOnly, s) }

// ErrNoUser reports assigned:me without a signed-in user.
var ErrNoUser = errors.New("assigned:me needs a signed-in user")

// Compile turns q into a SQL condition over app_issues as alias i, the
// per-range event and user counts as alias a (columns events, users), and the
// tag rollup app_issue_tags as alias t inside EXISTS subqueries. The first
// placeholder is $<base+1>. me is the signed-in user's id (0 when none) and
// lookupMember resolves a member email to a user id (nil: emails never match).
// The returned SQL contains only fixed fragments and placeholders: no input
// text. An empty query returns "true".
func Compile(q Query, now time.Time, me int64, lookupMember func(email string) (int64, bool), base int) (where string, args []any, err error) {
	var conds []string
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(base+len(args))
	}
	for _, t := range q.Terms {
		var c string
		switch t.Key {
		case "":
			p := arg(t.Value)
			c = "(strpos(lower(i.title), lower(" + p + ")) > 0 or strpos(lower(i.culprit), lower(" + p + ")) > 0)"
		case "is":
			c = isCond(t.Value)
		case "assigned":
			switch t.Value {
			case "none":
				c = "i.assignee_user_id is null"
			case "me":
				if me == 0 {
					return "", nil, ErrNoUser
				}
				c = "coalesce(i.assignee_user_id = " + arg(me) + ", false)"
			default:
				id, ok := int64(0), false
				if lookupMember != nil {
					id, ok = lookupMember(t.Value)
				}
				if !ok {
					c = "false"
				} else {
					c = "coalesce(i.assignee_user_id = " + arg(id) + ", false)"
				}
			}
		case "kind":
			// oom and watchdog are crash issues whose events carry an exit
			// reason; the issue queries expose it as a.term (see appquery).
			switch t.Value {
			case "oom":
				c = "coalesce(a.term = 'oom', false)"
			case "watchdog":
				c = "coalesce(a.term = 'watchdog_termination', false)"
			default:
				c = "i.kind = " + arg(t.Value)
			}
		case "priority":
			c = "i.priority = " + arg(t.Value)
		case "release", "environment", "platform", "process", "level":
			k, v := arg(t.Key), arg(t.Value)
			c = "exists (select 1 from app_issue_tags t where t.issue_id = i.id and t.key = " + k + " and t.value = " + v + ")"
		case "events", "users":
			col := "a.events"
			if t.Key == "users" {
				col = "a.users"
			}
			n, _ := strconv.ParseInt(t.Value, 10, 64)
			c = col + " " + sqlOp(t.Op) + " " + arg(n)
		case "first-seen", "last-seen":
			col := "i.first_seen"
			if t.Key == "last-seen" {
				col = "i.last_seen"
			}
			if t.Op == "=" {
				day, err := parseDay(t.Value)
				if err != nil {
					return "", nil, fmt.Errorf("search: bad date: %w", err)
				}
				c = "(" + col + " >= " + arg(day) + " and " + col + " < " + arg(day.AddDate(0, 0, 1)) + ")"
			} else {
				d, err := relative(t.Value)
				if err != nil {
					return "", nil, err
				}
				c = col + " " + sqlOp(t.Op) + " " + arg(now.Add(-d))
			}
		default:
			return "", nil, fmt.Errorf("search: unknown key %q", t.Key)
		}
		if t.Negate {
			c = "not (" + c + ")"
		}
		conds = append(conds, c)
	}
	if len(conds) == 0 {
		return "true", nil, nil
	}
	return strings.Join(conds, " and "), args, nil
}

// sqlOp maps a validated operator to its SQL text.
func sqlOp(op string) string {
	switch op {
	case ">", "<", ">=", "<=":
		return op
	}
	return "="
}

func isCond(v string) string {
	switch v {
	case "unresolved":
		return "i.status in ('open', 'regressed')"
	case "resolved":
		return "i.status = 'resolved'"
	case "ignored":
		return "i.status = 'ignored'"
	case "archived":
		return "i.status = 'archived'"
	case "snoozed":
		return "(i.status = 'ignored' and (i.snooze_until is not null or i.snooze_events is not null or i.snooze_users is not null))"
	case "assigned":
		return "i.assignee_user_id is not null"
	default: // unassigned
		return "i.assignee_user_id is null"
	}
}

// relative parses a validated -24h / +30d value to the distance back in time
// (a "-" value is a positive distance, a "+" value too: the sign only picks
// the comparison).
func relative(v string) (time.Duration, error) {
	n, err := strconv.ParseInt(v[1:len(v)-1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("search: bad duration: %w", err)
	}
	unit := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[v[len(v)-1]]
	return time.Duration(n) * unit, nil
}
