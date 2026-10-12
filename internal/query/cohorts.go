package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/api"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
)

// Bounds that keep an on-demand cohort evaluation cheap enough to run inside
// the statement timeout on a busy site.
const (
	MaxCohortConditions = 10
	MaxCohortWindowDays = 90
	MaxCohortAgeDays    = 365
	MaxCohortMinCount   = 1000
	MaxCohortSample     = 50
	// CohortSizeCap stops counting once this many profiles match.
	CohortSizeCap = 100000

	maxCohortNameRunes  = 200
	maxCohortPathRunes  = 500
	maxCohortKeyRunes   = 100
	maxCohortValueRunes = 200

	maxPowerUserLimit = 100
	maxPowerUserMin   = 100000
)

// ErrCohortInput marks a cohort request the caller can correct.
var ErrCohortInput = api.ErrCohortInput

// ErrCohortNotFound means the cohort does not exist for the site.
var ErrCohortNotFound = api.ErrCohortNotFound

// ErrCohortsUnavailable means the site cannot evaluate person-level audiences:
// anonymous tracking, or profiles disabled.
var ErrCohortsUnavailable = api.ErrCohortsUnavailable

const cohortsUnavailableMessage = "cohorts and power users need identified tracking with profiles enabled; anonymous mode keeps no person-level history"

// Condition kinds, operators and relations accepted in a cohort definition.
const (
	cohortEvent         = "event"
	cohortPage          = "page"
	cohortEventProperty = "event_property"
	cohortActivity      = "activity"
	cohortFirstSeen     = "first_seen"
	cohortLastSeen      = "last_seen"
)

// NormalizeCohortDefinition validates a definition, trims text and fills
// defaults (min_count 1, operator is). The result is what gets stored.
func NormalizeCohortDefinition(def api.CohortDefinition) (api.CohortDefinition, error) {
	switch string(def.Match) {
	case "all", "any":
	default:
		return def, fmt.Errorf("%w: match must be all or any", ErrCohortInput)
	}
	if def.WindowDays < 1 || def.WindowDays > MaxCohortWindowDays {
		return def, fmt.Errorf("%w: window_days must be between 1 and %d", ErrCohortInput, MaxCohortWindowDays)
	}
	if len(def.Conditions) < 1 || len(def.Conditions) > MaxCohortConditions {
		return def, fmt.Errorf("%w: a cohort needs between 1 and %d conditions", ErrCohortInput, MaxCohortConditions)
	}
	out := api.CohortDefinition{Match: def.Match, WindowDays: def.WindowDays, Conditions: make([]api.CohortCondition, 0, len(def.Conditions))}
	for index, condition := range def.Conditions {
		normalized, err := normalizeCohortCondition(condition)
		if err != nil {
			return def, fmt.Errorf("%w: condition %d: %s", ErrCohortInput, index+1, err.Error())
		}
		out.Conditions = append(out.Conditions, normalized)
	}
	return out, nil
}

func normalizeCohortCondition(c api.CohortCondition) (api.CohortCondition, error) {
	text := func(field *string, label string, maxRunes int, required bool) (*string, error) {
		if field == nil {
			if required {
				return nil, fmt.Errorf("%s is required", label)
			}
			return nil, nil
		}
		value := strings.TrimSpace(*field)
		if value == "" {
			if required {
				return nil, fmt.Errorf("%s is required", label)
			}
			return nil, nil
		}
		if utf8.RuneCountInString(value) > maxRunes {
			return nil, fmt.Errorf("%s must not exceed %d characters", label, maxRunes)
		}
		if strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("%s contains an invalid character", label)
		}
		return &value, nil
	}
	count := func() (*int, error) {
		value := 1
		if c.MinCount != nil {
			value = *c.MinCount
		}
		if value < 1 || value > MaxCohortMinCount {
			return nil, fmt.Errorf("min_count must be between 1 and %d", MaxCohortMinCount)
		}
		return &value, nil
	}
	reject := func(label string, present bool) error {
		if present {
			return fmt.Errorf("%s does not apply to a %s condition", label, c.Type)
		}
		return nil
	}
	out := api.CohortCondition{Type: c.Type}
	if c.Negate != nil && *c.Negate {
		negate := true
		out.Negate = &negate
	}
	var err error
	switch string(c.Type) {
	case cohortEvent, cohortEventProperty:
		if out.Name, err = text(c.Name, "name", maxCohortNameRunes, true); err != nil {
			return c, err
		}
		if string(c.Type) == cohortEventProperty {
			if out.Key, err = text(c.Key, "key", maxCohortKeyRunes, true); err != nil {
				return c, err
			}
			if out.Value, err = text(c.Value, "value", maxCohortValueRunes, true); err != nil {
				return c, err
			}
		} else if err = errors.Join(reject("key", c.Key != nil), reject("value", c.Value != nil)); err != nil {
			return c, err
		}
		if out.MinCount, err = count(); err != nil {
			return c, err
		}
		err = errors.Join(reject("path", c.Path != nil), reject("operator", c.Operator != nil), reject("metric", c.Metric != nil), reject("relation", c.Relation != nil), reject("days", c.Days != nil))
	case cohortPage:
		if out.Path, err = text(c.Path, "path", maxCohortPathRunes, true); err != nil {
			return c, err
		}
		operator := "is"
		if c.Operator != nil {
			operator = *c.Operator
		}
		if operator != "is" && operator != "starts_with" {
			return c, errors.New("operator must be is or starts_with")
		}
		out.Operator = &operator
		if out.MinCount, err = count(); err != nil {
			return c, err
		}
		err = errors.Join(reject("name", c.Name != nil), reject("key", c.Key != nil), reject("value", c.Value != nil), reject("metric", c.Metric != nil), reject("relation", c.Relation != nil), reject("days", c.Days != nil))
	case cohortActivity:
		if c.Metric == nil || (*c.Metric != "sessions" && *c.Metric != "events") {
			return c, errors.New("metric must be sessions or events")
		}
		metric := *c.Metric
		out.Metric = &metric
		if out.MinCount, err = count(); err != nil {
			return c, err
		}
		err = errors.Join(reject("name", c.Name != nil), reject("path", c.Path != nil), reject("key", c.Key != nil), reject("value", c.Value != nil), reject("operator", c.Operator != nil), reject("relation", c.Relation != nil), reject("days", c.Days != nil))
	case cohortFirstSeen, cohortLastSeen:
		if c.Relation == nil || (*c.Relation != "within" && *c.Relation != "before") {
			return c, errors.New("relation must be within or before")
		}
		relation := *c.Relation
		out.Relation = &relation
		if c.Days == nil || *c.Days < 1 || *c.Days > MaxCohortAgeDays {
			return c, fmt.Errorf("days must be between 1 and %d", MaxCohortAgeDays)
		}
		days := *c.Days
		out.Days = &days
		err = errors.Join(reject("name", c.Name != nil), reject("path", c.Path != nil), reject("key", c.Key != nil), reject("value", c.Value != nil), reject("operator", c.Operator != nil), reject("metric", c.Metric != nil), reject("min_count", c.MinCount != nil))
	default:
		return c, fmt.Errorf("unknown condition type %q", c.Type)
	}
	if err != nil {
		return c, err
	}
	return out, nil
}

// positiveWindowed reports whether the condition can only hold for a profile
// with at least one event inside the window.
func positiveWindowed(c api.CohortCondition) bool {
	if c.Negate != nil && *c.Negate {
		return false
	}
	switch string(c.Type) {
	case cohortEvent, cohortPage, cohortEventProperty, cohortActivity:
		return true
	}
	return false
}

// cohortWindow is the evaluation window of a definition: events with
// from <= ts <= to count, so both edges are inclusive.
func cohortWindow(def api.CohortDefinition, asOf time.Time) (time.Time, time.Time) {
	return asOf.Add(-time.Duration(def.WindowDays) * 24 * time.Hour), asOf
}

// cohortPredicate returns a boolean SQL expression over a profiles row aliased
// p; siteArg is the placeholder holding the site id. def must already be normalized. Event, page and activity conditions look
// at events inside the window; first_seen and last_seen compare the profile's
// own timestamps with now minus the condition's days.
func cohortPredicate(b *sqlBuilder, siteArg string, def api.CohortDefinition, asOf time.Time) (string, error) {
	from, to := cohortWindow(def, asOf)
	// Arguments are bound on first use: an unreferenced parameter has no
	// inferable type and PostgreSQL rejects the statement.
	var fromArg, toArg string
	parts := make([]string, 0, len(def.Conditions))
	anyPositive, allPositive := false, true
	for _, c := range def.Conditions {
		var sql string
		switch string(c.Type) {
		case cohortFirstSeen, cohortLastSeen:
			column := "p.first_seen_at"
			if string(c.Type) == cohortLastSeen {
				column = "p.last_seen_at"
			}
			cutoff := b.arg(asOf.Add(-time.Duration(*c.Days) * 24 * time.Hour))
			if *c.Relation == "within" {
				sql = column + ">=" + cutoff
			} else {
				sql = column + "<" + cutoff
			}
		default:
			if fromArg == "" {
				fromArg, toArg = b.arg(from), b.arg(to)
			}
			sql = windowedCondition(b, c, siteArg, fromArg, toArg)
		}
		if c.Negate != nil && *c.Negate {
			sql = "not (" + sql + ")"
		}
		parts = append(parts, "("+sql+")")
		if positiveWindowed(c) {
			anyPositive = true
		} else {
			allPositive = false
		}
	}
	joiner := " and "
	if def.Match == "any" {
		joiner = " or "
	}
	predicate := "(" + strings.Join(parts, joiner) + ")"
	// A profile with an event in the window has last_seen_at inside it, so
	// skipping older profiles cannot change the result when every matching
	// profile must have such an event: some positive condition under "all", or
	// only positive conditions under "any".
	if (def.Match == "all" && anyPositive) || (def.Match == "any" && allPositive) {
		predicate = "p.last_seen_at>=" + fromArg + " and " + predicate
	}
	return predicate, nil
}

func windowedCondition(b *sqlBuilder, c api.CohortCondition, siteArg, fromArg, toArg string) string {
	base := "e.site_id=" + siteArg + " and e.profile_id is not null and e.ts>=" + fromArg + " and e.ts<=" + toArg
	minCount := 1
	if c.MinCount != nil {
		minCount = *c.MinCount
	}
	// Each condition becomes one uncorrelated subquery that PostgreSQL hashes
	// once, so the window's events are scanned once however many profiles the
	// site has, instead of probing every partition for every profile.
	members := func(filter, count string) string {
		sql := "p.id in (select e.profile_id from events e where " + base + " and " + filter
		if minCount > 1 || count != "count(*)" {
			sql += " group by e.profile_id having " + count + ">=" + b.arg(minCount)
		}
		return sql + ")"
	}
	eventName := func() string {
		return "e.kind=1 and e.name_id in (select d.id from dict d where d.kind=" + strconv.Itoa(int(dict.EventName)) + " and d.value=" + b.arg(*c.Name) + ")"
	}
	switch string(c.Type) {
	case cohortEvent:
		return members(eventName(), "count(*)")
	case cohortEventProperty:
		return members(eventName()+" and e.props ->> "+b.arg(*c.Key)+" = "+b.arg(*c.Value), "count(*)")
	case cohortPage:
		var match string
		if c.Operator != nil && *c.Operator == "starts_with" {
			match = "d.value like " + b.arg(escapeLike(*c.Path)+"%") + ` escape '\'`
		} else {
			match = "d.value=" + b.arg(*c.Path)
		}
		return members("e.kind=0 and e.path_id in (select d.id from dict d where d.kind="+strconv.Itoa(int(dict.Path))+" and "+match+")", "count(*)")
	default: // activity
		if *c.Metric == "sessions" {
			return members("e.kind in (0,1)", "count(distinct e.session_id)")
		}
		return members("e.kind in (0,1)", "count(*)")
	}
}

// cohortFilterPayload is the resolved form of a cohort filter: the stored
// definition plus the instant the window ends. The engine builds it from the
// filter's cohort id; SQL builders only ever see this payload.
type cohortFilterPayload struct {
	SiteID     int32                `json:"site_id"`
	Definition api.CohortDefinition `json:"definition"`
	AsOf       time.Time            `json:"as_of"`
}

// cohortMembership returns "<column> in (profiles matching the cohort)" for a
// resolved cohort filter.
func cohortMembership(b *sqlBuilder, filter api.Filter, column string) (string, error) {
	if len(filter.Values) != 1 || filter.Operator != api.Is {
		return "", errors.New(`cohort filter needs operator "is" with one cohort id`)
	}
	var payload cohortFilterPayload
	if err := json.Unmarshal([]byte(filter.Values[0]), &payload); err != nil || payload.AsOf.IsZero() {
		return "", errors.New("cohort filter was not resolved")
	}
	siteArg := b.arg(payload.SiteID)
	predicate, err := cohortPredicate(b, siteArg, payload.Definition, payload.AsOf)
	if err != nil {
		return "", err
	}
	return column + " in (select p.id from profiles p where p.site_id=" + siteArg + " and " + predicate + ")", nil
}

func hasCohortFilter(filters []api.Filter) bool {
	for _, filter := range filters {
		if filter.Dimension == "cohort" {
			return true
		}
	}
	return false
}

// resolveCohortFilters replaces each {cohort is <id>} filter by its resolved
// payload so the SQL builders need no database access. Cohorts of other sites
// resolve as not found.
func (e *Engine) resolveCohortFilters(ctx context.Context, siteID int32, filters []api.Filter) ([]api.Filter, error) {
	if !hasCohortFilter(filters) {
		return filters, nil
	}
	site, ok := e.registry.Site(siteID)
	if !ok {
		return nil, fmt.Errorf("unknown site %d", siteID)
	}
	if e.trackingMode != privacy.Identified || !site.ProfilesEnabled {
		return nil, ErrCohortsUnavailable
	}
	asOf := e.reportNow().UTC().Truncate(time.Second)
	out := make([]api.Filter, len(filters))
	copy(out, filters)
	for index, filter := range out {
		if filter.Dimension != "cohort" {
			continue
		}
		if filter.Operator != api.Is || len(filter.Values) != 1 {
			return nil, fmt.Errorf("%w: a cohort filter needs operator is and one cohort id", ErrCohortInput)
		}
		id, err := strconv.ParseInt(filter.Values[0], 10, 64)
		if err != nil || id < 1 {
			return nil, fmt.Errorf("%w: cohort id must be a positive integer", ErrCohortInput)
		}
		def, err := e.loadCohortDefinition(ctx, siteID, id)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(cohortFilterPayload{SiteID: siteID, Definition: def, AsOf: asOf})
		if err != nil {
			return nil, err
		}
		out[index].Values = []string{string(payload)}
	}
	return out, nil
}

func (e *Engine) loadCohortDefinition(ctx context.Context, siteID int32, id int64) (api.CohortDefinition, error) {
	var raw []byte
	err := e.pool.QueryRow(ctx, `select definition from cohorts where id=$1 and site_id=$2`, id, siteID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.CohortDefinition{}, ErrCohortNotFound
	}
	if err != nil {
		return api.CohortDefinition{}, fmt.Errorf("load cohort: %w", err)
	}
	var def api.CohortDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return api.CohortDefinition{}, fmt.Errorf("decode cohort: %w", err)
	}
	return NormalizeCohortDefinition(def)
}

// EvaluateCohort counts the profiles matching a cohort over retained events,
// bounded by CohortSizeCap, the statement timeout and the global query slots.
// Sample members (external ids) are returned only when includeSample is true.
func (e *Engine) EvaluateCohort(ctx context.Context, siteID int32, cohortID int64, definition *api.CohortDefinition, sampleLimit int, includeSample bool) (api.CohortEvaluation, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return api.CohortEvaluation{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(siteID)
	if !ok {
		return api.CohortEvaluation{}, fmt.Errorf("unknown site %d", siteID)
	}
	if e.trackingMode != privacy.Identified || !site.ProfilesEnabled {
		return api.CohortEvaluation{}, ErrCohortsUnavailable
	}
	if sampleLimit < 0 || sampleLimit > MaxCohortSample {
		return api.CohortEvaluation{}, fmt.Errorf("%w: sample_limit must be between 0 and %d", ErrCohortInput, MaxCohortSample)
	}
	if (cohortID > 0) == (definition != nil) {
		return api.CohortEvaluation{}, fmt.Errorf("%w: provide exactly one of cohort_id and definition", ErrCohortInput)
	}
	var def api.CohortDefinition
	var err error
	if cohortID > 0 {
		def, err = e.loadCohortDefinition(ctx, siteID, cohortID)
	} else {
		def, err = NormalizeCohortDefinition(*definition)
	}
	if err != nil {
		return api.CohortEvaluation{}, err
	}
	asOf := e.reportNow().UTC().Truncate(time.Second)
	from, to := cohortWindow(def, asOf)
	result := api.CohortEvaluation{WindowFrom: from, WindowTo: to, Sample: []api.CohortMember{}}

	b := &sqlBuilder{}
	siteArg := b.arg(siteID)
	predicate, err := cohortPredicate(b, siteArg, def, asOf)
	if err != nil {
		return api.CohortEvaluation{}, err
	}
	countSQL := `select count(*) from (select 1 from profiles p where p.site_id=` + siteArg + ` and ` + predicate + ` limit ` + b.arg(CohortSizeCap+1) + `) t`
	var sampleSQL string
	var sampleArgs []any
	wantSample := sampleLimit > 0
	if wantSample && !includeSample {
		result.SampleWithheld = true
		wantSample = false
	}
	if wantSample {
		sb := &sqlBuilder{}
		sSite := sb.arg(siteID)
		sPredicate, predicateErr := cohortPredicate(sb, sSite, def, asOf)
		if predicateErr != nil {
			return api.CohortEvaluation{}, predicateErr
		}
		sFrom, sTo := sb.arg(from), sb.arg(to)
		sampleSQL = `select p.id,p.external_id,p.first_seen_at,p.last_seen_at,
  (select count(distinct e.session_id) from events e where e.site_id=p.site_id and e.profile_id=p.id and e.ts>=` + sFrom + ` and e.ts<=` + sTo + ` and e.kind in (0,1)),
  (select count(*) from events e where e.site_id=p.site_id and e.profile_id=p.id and e.ts>=` + sFrom + ` and e.ts<=` + sTo + ` and e.kind in (0,1))
from profiles p where p.site_id=` + sSite + ` and ` + sPredicate + `
order by p.last_seen_at desc,p.id desc limit ` + sb.arg(sampleLimit)
		sampleArgs = sb.args
	}

	if err := globalQuerySlots.Acquire(ctx, 1); err != nil {
		return api.CohortEvaluation{}, fmt.Errorf("evaluate cohort: %w", err)
	}
	defer globalQuerySlots.Release(1)
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, countSQL, b.args...).Scan(&result.Size); err != nil {
			return err
		}
		if result.Size > CohortSizeCap {
			result.Size, result.SizeCapped = CohortSizeCap, true
		}
		if sampleSQL == "" {
			return nil
		}
		rows, err := tx.Query(ctx, sampleSQL, sampleArgs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var member api.CohortMember
			if err := rows.Scan(&member.Id, &member.ExternalId, &member.FirstSeenAt, &member.LastSeenAt, &member.Sessions, &member.Events); err != nil {
				return err
			}
			result.Sample = append(result.Sample, member)
		}
		return rows.Err()
	})
	if err != nil {
		return api.CohortEvaluation{}, fmt.Errorf("evaluate cohort: %w", err)
	}
	return result, nil
}

// PowerUsers lists the busiest identified profiles in an inclusive range of
// civil dates (site time zone). Sessions count distinct sessions with a
// pageview or custom event; events count those pageviews and custom events.
func (e *Engine) PowerUsers(ctx context.Context, siteID int32, from, to time.Time, metric string, minimum, limit int) (api.PowerUsers, error) {
	if e == nil || e.pool == nil || e.registry == nil {
		return api.PowerUsers{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(siteID)
	if !ok {
		return api.PowerUsers{}, fmt.Errorf("unknown site %d", siteID)
	}
	if e.trackingMode != privacy.Identified || !site.ProfilesEnabled {
		return api.PowerUsers{}, ErrCohortsUnavailable
	}
	if metric == "" {
		metric = "sessions"
	}
	if metric != "sessions" && metric != "events" {
		return api.PowerUsers{}, fmt.Errorf("%w: metric must be sessions or events", ErrCohortInput)
	}
	if minimum == 0 {
		minimum = 2
	}
	if minimum < 1 || minimum > maxPowerUserMin {
		return api.PowerUsers{}, fmt.Errorf("%w: min must be between 1 and %d", ErrCohortInput, maxPowerUserMin)
	}
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > maxPowerUserLimit {
		return api.PowerUsers{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrCohortInput, maxPowerUserLimit)
	}
	from, to = civilDate(from), civilDate(to)
	if from.IsZero() || to.IsZero() || from.After(to) || daysInclusive(from, to) > maxRangeDays {
		return api.PowerUsers{}, fmt.Errorf("%w: from and to must be valid dates, from not after to, within %d days", ErrCohortInput, maxRangeDays)
	}
	loc := site.TZ
	if loc == nil {
		loc = time.UTC
	}
	start, end := revenueWindow(from, to, loc)
	column := "count(distinct e.session_id)"
	if metric == "events" {
		column = "count(*)"
	}
	activity := `select e.profile_id, count(distinct e.session_id) as sessions, count(*) as events
from events e
where e.site_id=$1 and e.profile_id is not null and e.kind in (0,1) and e.ts>=$2 and e.ts<$3
group by e.profile_id
having ` + column + `>=$4`
	result := api.PowerUsers{Metric: metric, Min: int32(minimum), Data: []api.PowerUser{}}
	if err := globalQuerySlots.Acquire(ctx, 1); err != nil {
		return api.PowerUsers{}, fmt.Errorf("power users: %w", err)
	}
	defer globalQuerySlots.Release(1)
	err := WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `with act as (`+activity+`)
select p.id,p.external_id,p.first_seen_at,p.last_seen_at,a.sessions,a.events,(select count(*) from (select 1 from act limit $5) c)
from act a join profiles p on p.id=a.profile_id and p.site_id=$1
order by `+metricOrder(metric)+`
limit $6`, siteID, start, end, minimum, CohortSizeCap+1, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var user api.PowerUser
			var qualifying int64
			if err := rows.Scan(&user.Id, &user.ExternalId, &user.FirstSeenAt, &user.LastSeenAt, &user.Sessions, &user.Events, &qualifying); err != nil {
				return err
			}
			result.Qualifying = qualifying
			result.Data = append(result.Data, user)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result.Data) == 0 {
			return nil
		}
		if result.Qualifying > CohortSizeCap {
			result.Qualifying, result.QualifyingCapped = CohortSizeCap, true
		}
		return nil
	})
	if err != nil {
		return api.PowerUsers{}, fmt.Errorf("power users: %w", err)
	}
	return result, nil
}

func metricOrder(metric string) string {
	if metric == "events" {
		return "a.events desc,a.sessions desc,p.id"
	}
	return "a.sessions desc,a.events desc,p.id"
}
