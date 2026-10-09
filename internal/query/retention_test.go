package query

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/testpg"
)

func TestBuildRetentionCountsWithinDayReturningSessions(t *testing.T) {
	t.Parallel()
	rows := []retentionVisit{
		{day: dateForTest("2026-09-28"), visitorID: 1, sessions: 3},
		{day: dateForTest("2026-09-28"), visitorID: 2, sessions: 1},
		{day: dateForTest("2026-09-29"), visitorID: 1, sessions: 2}, // same ID next day is a different identity
		{day: dateForTest("2026-10-05"), visitorID: 3, sessions: 2},
	}

	got := buildRetention(rows, api.RetentionWeek)
	want := api.RetentionResponse{
		IdentityWindow: api.DailyIdentityWindow,
		RetentionMode:  api.WithinDaySessionReturn,
		Cohorts: []api.RetentionCohort{
			{Start: apiDate("2026-09-28"), Size: 3, Retained: []int64{3, 2, 1}},
			{Start: apiDate("2026-10-05"), Size: 1, Retained: []int64{1, 1, 0}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retention = %#v, want %#v", got, want)
	}
}

func TestBuildRetentionBucketsMonthsWithoutLinkingDays(t *testing.T) {
	t.Parallel()
	rows := []retentionVisit{
		{day: dateForTest("2026-09-30"), visitorID: 7, sessions: 2},
		{day: dateForTest("2026-10-01"), visitorID: 7, sessions: 1},
	}

	got := buildRetention(rows, api.RetentionMonth)
	want := []api.RetentionCohort{
		{Start: apiDate("2026-09-01"), Size: 1, Retained: []int64{1, 1}},
		{Start: apiDate("2026-10-01"), Size: 1, Retained: []int64{1, 0}},
	}
	if !reflect.DeepEqual(got.Cohorts, want) {
		t.Fatalf("cohorts = %#v, want %#v", got.Cohorts, want)
	}
}

func TestBuildRetentionSQLGroupsDailyIdentitySessions(t *testing.T) {
	t.Parallel()
	querySQL, _, err := buildRetentionSQL(api.RetentionRequest{
		SiteId: 1,
		DateRange: api.DateRange{
			From: apiDate("2026-10-01"),
			To:   apiDate("2026-10-08"),
		},
		Interval: api.RetentionDay,
	}, 30, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(querySQL)
	for _, fragment := range []string{"from sessions s", "group by s.local_day, s.visitor_id", "count(distinct s.session_id)", "daily_identities", "cohort_sizes", "generate_series", "least(", "count(*) filter", "s.retention_class"} {
		if !strings.Contains(lower, fragment) {
			t.Errorf("SQL missing %q:\n%s", fragment, querySQL)
		}
	}
}

func TestBuildProfileRetentionLinksIdentifiedUsersAcrossDays(t *testing.T) {
	t.Parallel()
	activity := []profileRetentionVisit{
		{day: dateForTest("2026-10-01"), profileID: 1},
		{day: dateForTest("2026-10-08"), profileID: 1},
		{day: dateForTest("2026-10-15"), profileID: 1},
		{day: dateForTest("2026-10-02"), profileID: 2},
		{day: dateForTest("2026-10-09"), profileID: 2},
		{day: dateForTest("2026-10-09"), profileID: 3},
	}

	got := buildProfileRetention(activity, api.RetentionWeek)
	want := api.RetentionResponse{
		IdentityWindow: api.ProfileIdentityWindow,
		RetentionMode:  api.ClassicProfileReturn,
		Cohorts: []api.RetentionCohort{
			{Start: apiDate("2026-09-28"), Size: 2, Retained: []int64{2, 2, 1}},
			{Start: apiDate("2026-10-05"), Size: 1, Retained: []int64{1, 0, 0}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profile retention = %#v, want %#v", got, want)
	}
}

func TestBuildProfileRetentionSQLUsesProfileID(t *testing.T) {
	t.Parallel()
	identity := api.ProfileRetentionIdentity
	querySQL, _, err := buildRetentionSQL(api.RetentionRequest{
		SiteId:    1,
		DateRange: api.DateRange{From: apiDate("2026-10-01"), To: apiDate("2026-10-31")},
		Interval:  api.RetentionWeek,
		Identity:  &identity,
	}, 30, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(querySQL)
	for _, fragment := range []string{"e.profile_id is not null", "first_bucket", "count(distinct profile_id)", "classic"} {
		if !strings.Contains(lower, fragment) {
			t.Errorf("profile retention SQL missing %q:\n%s", fragment, querySQL)
		}
	}
}

func TestBuildRetentionSQLPlans(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	request := api.RetentionRequest{
		SiteId:    1,
		DateRange: api.DateRange{From: apiDate("2026-10-01"), To: apiDate("2026-10-08")},
		Interval:  api.RetentionWeek,
	}
	filters := []api.Filter{{Dimension: "page", Operator: api.Contains, Values: []string{"docs"}}, {Dimension: "event", Operator: api.Is, Values: []string{"signup"}}}
	request.Filters = &filters
	for _, identity := range []struct {
		name  string
		value *api.RetentionRequestIdentity
	}{
		{name: "anonymous"},
		{name: "profile", value: func() *api.RetentionRequestIdentity { value := api.ProfileRetentionIdentity; return &value }()},
	} {
		t.Run(identity.name, func(t *testing.T) {
			request.Identity = identity.value
			querySQL, args, err := buildRetentionSQL(request, db.Ret30, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), "explain "+querySQL, args...); err != nil {
				t.Fatalf("plan retention SQL: %v\n%s", err, querySQL)
			}
		})
	}
}

func apiDate(value string) types.Date { return types.Date{Time: dateForTest(value)} }

func dateForTest(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return parsed
}
