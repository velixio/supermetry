package query

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
)

type retentionVisit struct {
	day       time.Time
	visitorID int64
	sessions  int64
}

type profileRetentionVisit struct {
	day       time.Time
	profileID int64
}

const maxRetentionOffsets = 10

// Retention reports repeat sessions within a daily visitor-ID identity
// window. It intentionally never links IDs across salt rotation boundaries.
func (e *Engine) Retention(ctx context.Context, req api.RetentionRequest) (api.RetentionResponse, error) {
	if e.trackingMode != privacy.Identified {
		return api.RetentionResponse{}, api.ErrAnonymousHistory
	}
	if e == nil || e.pool == nil || e.registry == nil {
		return api.RetentionResponse{}, errors.New("query engine is not configured")
	}
	site, ok := e.registry.Site(req.SiteId)
	if !ok {
		return api.RetentionResponse{}, fmt.Errorf("unknown site %d", req.SiteId)
	}
	if !req.Interval.Valid() {
		return api.RetentionResponse{}, fmt.Errorf("interval must be day, week, or month")
	}
	if err := validateRawReportRange(req.SiteId, req.DateRange, filtersFrom(req.Filters), site, e.reportNow()); err != nil {
		return api.RetentionResponse{}, err
	}
	querySQL, args, err := buildRetentionSQL(req, site.Retention, postgresLocation(ctx, e.pool, site.TZ))
	if err != nil {
		return api.RetentionResponse{}, err
	}
	response := api.RetentionResponse{
		IdentityWindow: api.DailyIdentityWindow,
		RetentionMode:  api.WithinDaySessionReturn,
		Cohorts:        []api.RetentionCohort{},
	}
	if req.Identity != nil && *req.Identity == api.ProfileRetentionIdentity {
		if !site.ProfilesEnabled {
			return api.RetentionResponse{}, errors.New("profiles are disabled for this site")
		}
		response.IdentityWindow = api.ProfileIdentityWindow
		response.RetentionMode = api.ClassicProfileReturn
	}
	if err := globalQuerySlots.Acquire(ctx, 1); err != nil {
		return api.RetentionResponse{}, fmt.Errorf("query retention: %w", err)
	}
	defer globalQuerySlots.Release(1)
	err = WithStatementTimeout(ctx, e.pool, queryTimeout, func(tx pgx.Tx) error {
		rows, queryErr := tx.Query(ctx, querySQL, args...)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var start time.Time
			var size, retained int64
			var offset int
			if scanErr := rows.Scan(&start, &size, &offset, &retained); scanErr != nil {
				return scanErr
			}
			if len(response.Cohorts) == 0 || !response.Cohorts[len(response.Cohorts)-1].Start.Time.Equal(start) {
				response.Cohorts = append(response.Cohorts, api.RetentionCohort{
					Start: openapi_types.Date{Time: civilDate(start)}, Size: size, Retained: make([]int64, 0, offset+1),
				})
			}
			cohort := &response.Cohorts[len(response.Cohorts)-1]
			for len(cohort.Retained) < offset {
				cohort.Retained = append(cohort.Retained, 0)
			}
			cohort.Retained = append(cohort.Retained, retained)
		}
		return rows.Err()
	})
	if err != nil {
		return api.RetentionResponse{}, fmt.Errorf("query retention: %w", err)
	}
	return response, nil
}

func buildRetentionSQL(req api.RetentionRequest, retention db.RetentionClass, location *time.Location) (string, []any, error) {
	builder := &sqlBuilder{}
	filters := filtersFrom(req.Filters)
	pageFilters, remaining := splitPageFilters(filters)
	specialFilters, sessionFilters := splitSpecialFilters(remaining)
	conditions, err := filterConditions(builder, sessionFilters, reportSessionResolver("s"))
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
	siteArg, retentionArg := builder.arg(req.SiteId), builder.arg(retention)
	fromArg, toArg := builder.arg(dateParam(from)), builder.arg(dateParam(to))
	startArg, endArg := builder.arg(startTS), builder.arg(endTS)
	conditions = constrainReportEventTimes(conditions, startArg, endArg)
	where := "s.site_id=" + siteArg + " and s.retention_class=" + retentionArg +
		" and s.local_day between " + fromArg + "::date and " + toArg + "::date" +
		" and s.start >= " + startArg + " and s.start < " + endArg + andSQL(conditions)
	bucket := "local_day"
	switch req.Interval {
	case api.RetentionWeek:
		bucket = "date_trunc('week',local_day)::date"
	case api.RetentionMonth:
		bucket = "date_trunc('month',local_day)::date"
	}
	if req.Identity != nil && *req.Identity == api.ProfileRetentionIdentity {
		timezoneArg := builder.arg(location.String())
		profileBucket := "((p.first_seen_at at time zone " + timezoneArg + ")::date)"
		activityBucket := "e.local_day"
		offset := "(o.activity_bucket-o.first_bucket)"
		switch req.Interval {
		case api.RetentionWeek:
			profileBucket = "date_trunc('week'," + profileBucket + ")::date"
			activityBucket = "date_trunc('week',e.local_day)::date"
			offset = "((o.activity_bucket-o.first_bucket)/7)"
		case api.RetentionMonth:
			profileBucket = "date_trunc('month'," + profileBucket + ")::date"
			activityBucket = "date_trunc('month',e.local_day)::date"
			offset = "((extract(year from o.activity_bucket)::int-extract(year from o.first_bucket)::int)*12+extract(month from o.activity_bucket)::int-extract(month from o.first_bucket)::int)"
		}
		return `with matched_sessions as (
  select s.site_id,s.retention_class,s.local_day,s.session_id
  from sessions s
  where ` + where + `
), profile_cohorts as (
  select p.id as profile_id,` + profileBucket + ` as first_bucket
  from profiles p
  where p.site_id=` + siteArg + ` and p.first_seen_at>=` + startArg + ` and p.first_seen_at<` + endArg + `
), classic_activity as (
  select distinct e.profile_id,` + activityBucket + ` as activity_bucket
  from events e
  join matched_sessions s on s.site_id=e.site_id and s.retention_class=e.retention_class
    and s.local_day=e.local_day and s.session_id=e.session_id
  where e.profile_id is not null
), observed as (
  select c.profile_id,c.first_bucket,a.activity_bucket
  from profile_cohorts c join classic_activity a using(profile_id)
  where a.activity_bucket>=c.first_bucket
), cohort_sizes as (
  select first_bucket,count(distinct profile_id)::int8 as size from profile_cohorts group by first_bucket
), max_offset as (
  select least(coalesce(max(` + offset + `),0),` + fmt.Sprint(maxRetentionOffsets-1) + `)::int as value from observed o
), offsets as (
  select generate_series(0,(select value from max_offset)) as offset
)
select c.first_bucket,c.size,x.offset,
       count(distinct o.profile_id) filter (where ` + offset + `=x.offset)::int8 as retained
from cohort_sizes c cross join offsets x
left join observed o on o.first_bucket=c.first_bucket
group by c.first_bucket,c.size,x.offset
order by c.first_bucket,x.offset`, builder.args, nil
	}
	return `with daily_identities as (
  select s.local_day, s.visitor_id, count(distinct s.session_id)::int8 as sessions
  from sessions s
  where ` + where + `
  group by s.local_day, s.visitor_id
), bucketed as (
  select ` + bucket + ` as cohort_start, sessions from daily_identities
), cohort_sizes as (
  select cohort_start, count(*)::int8 as size from bucketed group by cohort_start
), offsets as (
  select generate_series(0,least(coalesce((select max(sessions) from bucketed),0)::int,` + fmt.Sprint(maxRetentionOffsets) + `)-1) as offset
)
select c.cohort_start,c.size,o.offset,
       count(*) filter (where b.sessions>o.offset)::int8 as retained
from cohort_sizes c
join bucketed b using(cohort_start)
cross join offsets o
group by c.cohort_start,c.size,o.offset
order by c.cohort_start,o.offset`, builder.args, nil
}

func buildProfileRetention(visits []profileRetentionVisit, interval api.RetentionRequestInterval) api.RetentionResponse {
	type profileActivity struct {
		first time.Time
		days  map[time.Time]struct{}
	}
	profiles := make(map[int64]*profileActivity)
	lastBucket := time.Time{}
	for _, visit := range visits {
		bucket := retentionBucket(visit.day, interval)
		activity := profiles[visit.profileID]
		if activity == nil {
			activity = &profileActivity{first: bucket, days: make(map[time.Time]struct{})}
			profiles[visit.profileID] = activity
		}
		if bucket.Before(activity.first) {
			activity.first = bucket
		}
		activity.days[bucket] = struct{}{}
		if bucket.After(lastBucket) {
			lastBucket = bucket
		}
	}
	maxOffset := 0
	for _, activity := range profiles {
		if offset := retentionOffset(activity.first, lastBucket, interval); offset > maxOffset {
			maxOffset = offset
		}
	}
	if maxOffset >= maxRetentionOffsets {
		maxOffset = maxRetentionOffsets - 1
	}
	type aggregate struct {
		start    time.Time
		size     int64
		retained []int64
	}
	groups := make(map[time.Time]*aggregate)
	for _, activity := range profiles {
		group := groups[activity.first]
		if group == nil {
			group = &aggregate{start: activity.first, retained: make([]int64, maxOffset+1)}
			groups[activity.first] = group
		}
		group.size++
		for day := range activity.days {
			offset := retentionOffset(activity.first, day, interval)
			if offset >= 0 && offset <= maxOffset {
				group.retained[offset]++
			}
		}
	}
	ordered := make([]*aggregate, 0, len(groups))
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].start.Before(ordered[j].start) })
	response := api.RetentionResponse{IdentityWindow: api.ProfileIdentityWindow, RetentionMode: api.ClassicProfileReturn, Cohorts: make([]api.RetentionCohort, 0, len(ordered))}
	for _, group := range ordered {
		response.Cohorts = append(response.Cohorts, api.RetentionCohort{Start: openapi_types.Date{Time: group.start}, Size: group.size, Retained: group.retained})
	}
	return response
}

func retentionOffset(from, to time.Time, interval api.RetentionRequestInterval) int {
	switch interval {
	case api.RetentionWeek:
		return int(to.Sub(from).Hours() / (24 * 7))
	case api.RetentionMonth:
		return (to.Year()-from.Year())*12 + int(to.Month()-from.Month())
	default:
		return int(to.Sub(from).Hours() / 24)
	}
}

func buildRetention(visits []retentionVisit, interval api.RetentionRequestInterval) api.RetentionResponse {
	type aggregate struct {
		start    time.Time
		size     int64
		retained []int64
	}
	maxSessions := int64(0)
	for _, visit := range visits {
		if visit.sessions > maxSessions {
			maxSessions = visit.sessions
		}
	}
	if maxSessions > maxRetentionOffsets {
		maxSessions = maxRetentionOffsets
	}
	groups := make(map[time.Time]*aggregate)
	for _, visit := range visits {
		start := retentionBucket(visit.day, interval)
		group := groups[start]
		if group == nil {
			group = &aggregate{start: start, retained: make([]int64, int(maxSessions))}
			groups[start] = group
		}
		group.size++ // each row is one (local_day, visitor_id), i.e. one daily identity
		for offset := int64(0); offset < visit.sessions && offset < maxRetentionOffsets; offset++ {
			group.retained[offset]++
		}
	}
	ordered := make([]*aggregate, 0, len(groups))
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].start.Before(ordered[j].start) })
	response := api.RetentionResponse{
		IdentityWindow: api.DailyIdentityWindow,
		RetentionMode:  api.WithinDaySessionReturn,
		Cohorts:        make([]api.RetentionCohort, 0, len(ordered)),
	}
	for _, group := range ordered {
		response.Cohorts = append(response.Cohorts, api.RetentionCohort{
			Start:    openapi_types.Date{Time: group.start},
			Size:     group.size,
			Retained: group.retained,
		})
	}
	return response
}

func retentionBucket(day time.Time, interval api.RetentionRequestInterval) time.Time {
	day = civilDate(day)
	switch interval {
	case api.RetentionWeek:
		daysSinceMonday := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -daysSinceMonday)
	case api.RetentionMonth:
		return time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return day
	}
}
