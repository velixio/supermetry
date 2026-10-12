package query

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/privacy"
	"supermetry/internal/sites"
	"supermetry/internal/testpg"
)

// Cohort fixture. Evaluation time is 2026-10-10 12:00:00 UTC; a 7-day window
// therefore covers 2026-10-03 12:00:00 through 2026-10-10 12:00:00, both
// edges inclusive. Paths: /pricing, /docs/intro, /docs/api. Events: signup,
// purchase. Counts below are over pageviews (kind 0) and custom events
// (kind 1); engagement rows (kind 2) never count.
//
//	alice (1): first 09-01, last 10-10 11:00
//	  s11 10-04 10:00 /pricing, 10:05 signup{plan:pro}
//	  s12 10-06 09:00 /docs/intro, 09:01 /docs/api, 09:02 purchase{plan:pro}
//	  s13 10-10 11:00 /pricing, 11:00:30 engagement
//	  -> 3 sessions, 6 events in window; /pricing x2; /docs x2
//	bob (2): first 10-08, last 10-09 15:00
//	  s21 10-09 08:00 /docs/intro, 08:01 signup{plan:free}; s22 10-09 15:00 /pricing
//	  -> 2 sessions, 3 events
//	carol (3): first 09-15, last 10-03 12:00:00  s31 10-03 12:00:00 /pricing (lower edge, inside)
//	dave (4): first 09-15, last 10-03 11:59:59   s41 10-03 11:59:59 /pricing (one second outside)
//	erin (5): first 09-20, last 10-10 12:00:00   s51 10-10 12:00:00 signup{plan:pro} (upper edge, inside)
var cohortAsOf = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func setupCohortFixture(t *testing.T, mode privacy.Mode, profiles bool, tz *time.Location) (*pgxpool.Pool, *Engine) {
	t.Helper()
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, dateForTest("2026-09-15"), dateForTest("2026-10-12")); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`insert into orgs(id,name,slug) overriding system value values (1,'Org','org')`)
	exec(`insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values (1,1,'cohortsite01','Cohorts','UTC',30)`)
	exec(`insert into dict(kind,id,value) values (0,1,'/pricing'),(0,2,'/docs/intro'),(0,3,'/docs/api'),($1,1,'signup'),($1,2,'purchase')`, int(dict.EventName))
	exec(`insert into profiles(id,site_id,external_id,first_seen_at,last_seen_at) overriding system value values
		(1,1,'alice','2026-09-01 08:00+00','2026-10-10 11:00+00'),
		(2,1,'bob','2026-10-08 08:00+00','2026-10-09 15:00+00'),
		(3,1,'carol','2026-09-15 08:00+00','2026-10-03 12:00:00+00'),
		(4,1,'dave','2026-09-15 08:00+00','2026-10-03 11:59:59+00'),
		(5,1,'erin','2026-09-20 08:00+00','2026-10-10 12:00:00+00')`)
	exec(`insert into sessions(site_id,retention_class,session_id,visitor_id,start,last,local_day,pageviews) values
		(1,30,11,111,'2026-10-04 10:00+00','2026-10-04 10:05+00','2026-10-04',1),
		(1,30,12,112,'2026-10-06 09:00+00','2026-10-06 09:02+00','2026-10-06',2),
		(1,30,13,113,'2026-10-10 11:00+00','2026-10-10 11:01+00','2026-10-10',1),
		(1,30,21,121,'2026-10-09 08:00+00','2026-10-09 08:01+00','2026-10-09',1),
		(1,30,22,122,'2026-10-09 15:00+00','2026-10-09 15:01+00','2026-10-09',1),
		(1,30,31,131,'2026-10-03 12:00:00+00','2026-10-03 12:01+00','2026-10-03',1),
		(1,30,41,141,'2026-10-03 11:59:59+00','2026-10-03 12:00+00','2026-10-03',1),
		(1,30,51,151,'2026-10-10 12:00:00+00','2026-10-10 12:01+00','2026-10-10',0)`)
	// kind 0 pageview, 1 custom, 2 engagement. name_id 1 signup, 2 purchase.
	exec(`insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,profile_id,name_id,path_id,props) values
		(1,30,'2026-10-04','2026-10-04 10:00+00',111,11,0,1,0,1,null),
		(1,30,'2026-10-04','2026-10-04 10:05+00',111,11,1,1,1,0,'{"plan":"pro"}'),
		(1,30,'2026-10-06','2026-10-06 09:00+00',112,12,0,1,0,2,null),
		(1,30,'2026-10-06','2026-10-06 09:01+00',112,12,0,1,0,3,null),
		(1,30,'2026-10-06','2026-10-06 09:02+00',112,12,1,1,2,0,'{"plan":"pro"}'),
		(1,30,'2026-10-10','2026-10-10 11:00+00',113,13,0,1,0,1,null),
		(1,30,'2026-10-10','2026-10-10 11:00:30+00',113,13,2,1,0,1,null),
		(1,30,'2026-10-09','2026-10-09 08:00+00',121,21,0,2,0,2,null),
		(1,30,'2026-10-09','2026-10-09 08:01+00',121,21,1,2,1,0,'{"plan":"free"}'),
		(1,30,'2026-10-09','2026-10-09 15:00+00',122,22,0,2,0,1,null),
		(1,30,'2026-10-03','2026-10-03 12:00:00+00',131,31,0,3,0,1,null),
		(1,30,'2026-10-03','2026-10-03 11:59:59+00',141,41,0,4,0,1,null),
		(1,30,'2026-10-10','2026-10-10 12:00:00+00',151,51,1,5,1,0,'{"plan":"pro"}')`)
	dictionary := dict.New(pool)
	if err := dictionary.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if tz == nil {
		tz = time.UTC
	}
	site := sites.Site{ID: 1, PublicID: "cohortsite01", TZ: tz, Retention: db.Ret30, ProfilesEnabled: profiles}
	engine := NewEngine(pool, dictionary, vitalsRegistry{site: site})
	engine.SetTrackingMode(mode)
	engine.now = func() time.Time { return cohortAsOf }
	return pool, engine
}

func cs(value string) *string { return &value }
func cn(value int) *int       { return &value }

func condEvent(name string, min int) api.CohortCondition {
	return api.CohortCondition{Type: "event", Name: cs(name), MinCount: cn(min)}
}
func condPage(path, operator string, min int) api.CohortCondition {
	return api.CohortCondition{Type: "page", Path: cs(path), Operator: cs(operator), MinCount: cn(min)}
}
func condActivity(metric string, min int) api.CohortCondition {
	return api.CohortCondition{Type: "activity", Metric: cs(metric), MinCount: cn(min)}
}
func condAge(kind, relation string, days int) api.CohortCondition {
	return api.CohortCondition{Type: kind, Relation: cs(relation), Days: cn(days)}
}
func negated(c api.CohortCondition) api.CohortCondition {
	yes := true
	c.Negate = &yes
	return c
}
func definition(match string, window int, conditions ...api.CohortCondition) api.CohortDefinition {
	return api.CohortDefinition{Match: match, WindowDays: window, Conditions: conditions}
}

func sampleIDs(result api.CohortEvaluation) []string {
	ids := make([]string, 0, len(result.Sample))
	for _, member := range result.Sample {
		ids = append(ids, member.ExternalId)
	}
	return ids
}

func TestCohortEvaluationMatchesHandComputedFixtures(t *testing.T) {
	_, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	ctx := context.Background()
	tests := []struct {
		name string
		def  api.CohortDefinition
		want []string // members, newest last_seen first
	}{
		{"signed up", definition("all", 7, condEvent("signup", 1)), []string{"erin", "alice", "bob"}},
		{"signed up twice", definition("all", 7, condEvent("signup", 2)), []string{}},
		{"purchased", definition("all", 7, condEvent("purchase", 1)), []string{"alice"}},
		{"viewed pricing, lower edge in and one second out", definition("all", 7, condPage("/pricing", "is", 1)), []string{"alice", "bob", "carol"}},
		{"viewed pricing twice", definition("all", 7, condPage("/pricing", "is", 2)), []string{"alice"}},
		{"docs prefix", definition("all", 7, condPage("/docs", "starts_with", 1)), []string{"alice", "bob"}},
		{"docs prefix twice", definition("all", 7, condPage("/docs", "starts_with", 2)), []string{"alice"}},
		{"signup with plan pro", definition("all", 7, api.CohortCondition{Type: "event_property", Name: cs("signup"), Key: cs("plan"), Value: cs("pro"), MinCount: cn(1)}), []string{"erin", "alice"}},
		{"two sessions", definition("all", 7, condActivity("sessions", 2)), []string{"alice", "bob"}},
		{"three sessions", definition("all", 7, condActivity("sessions", 3)), []string{"alice"}},
		{"three events", definition("all", 7, condActivity("events", 3)), []string{"alice", "bob"}},
		{"four events", definition("all", 7, condActivity("events", 4)), []string{"alice"}},
		{"first seen within 10 days", definition("all", 7, condAge("first_seen", "within", 10)), []string{"bob"}},
		{"first seen before 30 days", definition("all", 7, condAge("first_seen", "before", 30)), []string{"alice"}},
		{"last seen within a day", definition("all", 7, condAge("last_seen", "within", 1)), []string{"erin", "alice", "bob"}},
		{"last seen before 7 days: exact edge is not before", definition("all", 7, condAge("last_seen", "before", 7)), []string{"dave"}},
		{"all: signup and pricing", definition("all", 7, condEvent("signup", 1), condPage("/pricing", "is", 1)), []string{"alice", "bob"}},
		{"any: signup or purchase", definition("any", 7, condEvent("signup", 1), condEvent("purchase", 1)), []string{"erin", "alice", "bob"}},
		{"all: signup and not pricing", definition("all", 7, condEvent("signup", 1), negated(condPage("/pricing", "is", 1))), []string{"erin"}},
		{"any with negation reaches profiles outside the window", definition("any", 7, condEvent("purchase", 1), negated(condEvent("signup", 1))), []string{"alice", "carol", "dave"}},
		{"one day window", definition("all", 1, condEvent("signup", 1)), []string{"erin"}},
		{"ninety day window keeps everyone with a pageview", definition("all", 90, condPage("/pricing", "is", 1)), []string{"alice", "bob", "carol", "dave"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := engine.EvaluateCohort(ctx, 1, 0, &tc.def, 10, true)
			if err != nil {
				t.Fatal(err)
			}
			got := sampleIDs(result)
			if !sameMembers(got, tc.want) || result.Size != int64(len(tc.want)) || result.SizeCapped {
				t.Fatalf("members = %v size %d, want %v", got, result.Size, tc.want)
			}
			if !result.WindowTo.Equal(cohortAsOf) || !result.WindowFrom.Equal(cohortAsOf.AddDate(0, 0, -tc.def.WindowDays)) {
				t.Fatalf("window = %v..%v", result.WindowFrom, result.WindowTo)
			}
		})
	}
}

func sameMembers(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			return false
		}
	}
	return true
}

func TestCohortSampleCarriesWindowCountsOrderedByRecency(t *testing.T) {
	_, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	def := definition("all", 7, condActivity("sessions", 2))
	result, err := engine.EvaluateCohort(context.Background(), 1, 0, &def, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Size != 2 || len(result.Sample) != 1 {
		t.Fatalf("size %d sample %v, want size 2 and a one member sample", result.Size, result.Sample)
	}
	member := result.Sample[0]
	if member.ExternalId != "alice" || member.Sessions != 3 || member.Events != 6 {
		t.Fatalf("member = %+v, want alice with 3 sessions and 6 events (engagement rows excluded)", member)
	}
}

func TestCohortBoundaryMovesWithEvaluationTime(t *testing.T) {
	_, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	def := definition("all", 7, condPage("/pricing", "is", 1))
	engine.now = func() time.Time { return cohortAsOf.Add(time.Second) }
	result, err := engine.EvaluateCohort(context.Background(), 1, 0, &def, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := sampleIDs(result); !sameMembers(got, []string{"alice", "bob"}) {
		t.Fatalf("one second later members = %v, want carol to fall out of the window", got)
	}
	engine.now = func() time.Time { return cohortAsOf.Add(-time.Second) }
	result, err = engine.EvaluateCohort(context.Background(), 1, 0, &def, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	// One second earlier the window starts at 11:59:59 on 10-03, so dave is in too.
	if got := sampleIDs(result); !sameMembers(got, []string{"alice", "bob", "carol", "dave"}) {
		t.Fatalf("one second earlier members = %v, want dave inside the window", got)
	}
}

func TestCohortSampleWithheldWithoutPersonLevelAccess(t *testing.T) {
	_, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	def := definition("all", 7, condEvent("signup", 1))
	result, err := engine.EvaluateCohort(context.Background(), 1, 0, &def, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Size != 3 || len(result.Sample) != 0 || !result.SampleWithheld {
		t.Fatalf("result = %+v, want size 3, no sample, sample withheld", result)
	}
	withoutRequest, err := engine.EvaluateCohort(context.Background(), 1, 0, &def, 0, false)
	if err != nil || withoutRequest.SampleWithheld {
		t.Fatalf("size-only request = %+v, %v; nothing was withheld", withoutRequest, err)
	}
}

func TestCohortRefusedWithoutIdentifiedProfiles(t *testing.T) {
	def := definition("all", 7, condEvent("signup", 1))
	for name, tc := range map[string]struct {
		mode     privacy.Mode
		profiles bool
	}{"anonymous": {privacy.Anonymous, true}, "profiles disabled": {privacy.Identified, false}} {
		t.Run(name, func(t *testing.T) {
			_, engine := setupCohortFixture(t, tc.mode, tc.profiles, nil)
			if _, err := engine.EvaluateCohort(context.Background(), 1, 0, &def, 0, true); !errors.Is(err, ErrCohortsUnavailable) {
				t.Fatalf("EvaluateCohort() error = %v, want ErrCohortsUnavailable", err)
			}
			if _, err := engine.PowerUsers(context.Background(), 1, dateForTest("2026-10-01"), dateForTest("2026-10-10"), "sessions", 1, 10); !errors.Is(err, ErrCohortsUnavailable) {
				t.Fatalf("PowerUsers() error = %v, want ErrCohortsUnavailable", err)
			}
		})
	}
}

func TestCohortEvaluationRejectsBadRequests(t *testing.T) {
	_, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	ctx := context.Background()
	good := definition("all", 7, condEvent("signup", 1))
	if _, err := engine.EvaluateCohort(ctx, 1, 0, nil, 0, true); !errors.Is(err, ErrCohortInput) {
		t.Fatalf("no cohort and no definition: %v", err)
	}
	if _, err := engine.EvaluateCohort(ctx, 1, 5, &good, 0, true); !errors.Is(err, ErrCohortInput) {
		t.Fatalf("both cohort and definition: %v", err)
	}
	if _, err := engine.EvaluateCohort(ctx, 1, 0, &good, MaxCohortSample+1, true); !errors.Is(err, ErrCohortInput) {
		t.Fatalf("oversized sample: %v", err)
	}
	if _, err := engine.EvaluateCohort(ctx, 1, 999, nil, 0, true); !errors.Is(err, ErrCohortNotFound) {
		t.Fatalf("missing cohort: %v", err)
	}
}

func TestNormalizeCohortDefinitionEnforcesLimits(t *testing.T) {
	t.Parallel()
	many := make([]api.CohortCondition, MaxCohortConditions+1)
	for i := range many {
		many[i] = condEvent("signup", 1)
	}
	long := strings.Repeat("x", 201)
	tests := map[string]api.CohortDefinition{
		"bad match":            definition("some", 7, condEvent("signup", 1)),
		"window zero":          definition("all", 0, condEvent("signup", 1)),
		"window too long":      definition("all", MaxCohortWindowDays+1, condEvent("signup", 1)),
		"no conditions":        definition("all", 7),
		"too many conditions":  definition("all", 7, many...),
		"event without name":   definition("all", 7, api.CohortCondition{Type: "event"}),
		"event name too long":  definition("all", 7, condEvent(long, 1)),
		"count zero":           definition("all", 7, condEvent("signup", 0)),
		"count too high":       definition("all", 7, condEvent("signup", MaxCohortMinCount+1)),
		"page operator":        definition("all", 7, condPage("/a", "contains", 1)),
		"property without key": definition("all", 7, api.CohortCondition{Type: "event_property", Name: cs("signup"), Value: cs("pro")}),
		"activity metric":      definition("all", 7, condActivity("visitors", 1)),
		"age relation":         definition("all", 7, condAge("first_seen", "after", 3)),
		"age days too many":    definition("all", 7, condAge("last_seen", "within", MaxCohortAgeDays+1)),
		"unknown type":         definition("all", 7, api.CohortCondition{Type: "geo"}),
		"stray field":          definition("all", 7, api.CohortCondition{Type: "event", Name: cs("a"), Path: cs("/x")}),
		"nul byte":             definition("all", 7, condEvent("a\x00b", 1)),
	}
	for name, def := range tests {
		if _, err := NormalizeCohortDefinition(def); !errors.Is(err, ErrCohortInput) {
			t.Errorf("%s: error = %v, want ErrCohortInput", name, err)
		}
	}
	normalized, err := NormalizeCohortDefinition(definition("any", 30, api.CohortCondition{Type: "page", Path: cs("  /pricing ")}))
	if err != nil {
		t.Fatal(err)
	}
	c := normalized.Conditions[0]
	if *c.Path != "/pricing" || *c.Operator != "is" || *c.MinCount != 1 || c.Negate != nil {
		t.Fatalf("defaults = %+v, want trimmed path, operator is, min_count 1", c)
	}
}

func TestCohortEvaluationHitsStatementTimeout(t *testing.T) {
	pool, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	engine.SetQueryTimeout(150 * time.Millisecond)
	t.Cleanup(func() { engine.SetQueryTimeout(10 * time.Second) })
	ctx := context.Background()
	// An exclusive lock on profiles stalls the evaluation until the statement
	// timeout cancels it, the same outcome as a definition too heavy to finish.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(ctx) })
	if _, err := blocker.Exec(ctx, "lock table profiles in access exclusive mode"); err != nil {
		t.Fatal(err)
	}
	def := definition("all", 7, condEvent("signup", 1))
	started := time.Now()
	_, err = engine.EvaluateCohort(ctx, 1, 0, &def, 5, true)
	if problem := QueryProblem(err); problem == nil || problem.Status != 503 {
		t.Fatalf("EvaluateCohort() error = %v, want a 503 query timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("evaluation took %v, want cancellation near the 150ms timeout", elapsed)
	}
	_, err = engine.PowerUsers(ctx, 1, dateForTest("2026-10-01"), dateForTest("2026-10-10"), "sessions", 1, 10)
	if problem := QueryProblem(err); problem == nil || problem.Status != 503 {
		t.Fatalf("PowerUsers() error = %v, want a 503 query timeout", err)
	}
}

func TestPowerUsersRankByThresholdAndRange(t *testing.T) {
	_, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	ctx := context.Background()
	ids := func(result api.PowerUsers) []string {
		out := make([]string, 0, len(result.Data))
		for _, user := range result.Data {
			out = append(out, user.ExternalId)
		}
		return out
	}
	from, to := dateForTest("2026-10-01"), dateForTest("2026-10-10")
	result, err := engine.PowerUsers(ctx, 1, from, to, "sessions", 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(result); !reflect.DeepEqual(got, []string{"alice", "bob"}) || result.Qualifying != 2 || result.Data[0].Sessions != 3 || result.Data[0].Events != 6 || result.Data[1].Sessions != 2 || result.Data[1].Events != 3 {
		t.Fatalf("sessions>=2 = %v qualifying %d data %+v", ids(result), result.Qualifying, result.Data)
	}
	result, _ = engine.PowerUsers(ctx, 1, from, to, "sessions", 3, 10)
	if got := ids(result); !reflect.DeepEqual(got, []string{"alice"}) || result.Qualifying != 1 {
		t.Fatalf("sessions>=3 = %v qualifying %d", got, result.Qualifying)
	}
	result, _ = engine.PowerUsers(ctx, 1, from, to, "events", 3, 10)
	if got := ids(result); !reflect.DeepEqual(got, []string{"alice", "bob"}) {
		t.Fatalf("events>=3 = %v", got)
	}
	result, _ = engine.PowerUsers(ctx, 1, from, to, "events", 1, 1)
	if got := ids(result); !reflect.DeepEqual(got, []string{"alice"}) || result.Qualifying != 5 {
		t.Fatalf("limit 1 = %v qualifying %d, want alice first and 5 qualifying", got, result.Qualifying)
	}
	// Single-day ranges: 10-10 holds alice's s13 and erin's s51 (ties break by profile id).
	result, _ = engine.PowerUsers(ctx, 1, dateForTest("2026-10-10"), dateForTest("2026-10-10"), "sessions", 1, 10)
	if got := ids(result); !reflect.DeepEqual(got, []string{"alice", "erin"}) || result.Qualifying != 2 {
		t.Fatalf("10-10 = %v qualifying %d", got, result.Qualifying)
	}
	// 10-03 holds carol at 12:00:00 and dave at 11:59:59: the day is inclusive of both.
	result, _ = engine.PowerUsers(ctx, 1, dateForTest("2026-10-03"), dateForTest("2026-10-03"), "sessions", 1, 10)
	if got := ids(result); !reflect.DeepEqual(got, []string{"carol", "dave"}) {
		t.Fatalf("10-03 = %v", got)
	}
	result, _ = engine.PowerUsers(ctx, 1, from, to, "sessions", 4, 10)
	if len(result.Data) != 0 || result.Qualifying != 0 || result.Data == nil {
		t.Fatalf("sessions>=4 = %+v, want an empty non-nil list", result)
	}
}

func TestPowerUsersBucketDaysInTheSiteTimeZone(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	_, engine := setupCohortFixture(t, privacy.Identified, true, ist)
	// 10-04 in +05:30 is 10-03 18:30Z through 10-04 18:30Z: only alice's s11 (10:00Z and 10:05Z).
	result, err := engine.PowerUsers(context.Background(), 1, dateForTest("2026-10-04"), dateForTest("2026-10-04"), "sessions", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 || result.Data[0].ExternalId != "alice" || result.Data[0].Sessions != 1 || result.Data[0].Events != 2 {
		t.Fatalf("IST 10-04 = %+v, want alice with 1 session and 2 events", result.Data)
	}
}

func TestPowerUsersRejectsBadInput(t *testing.T) {
	_, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	ctx := context.Background()
	from, to := dateForTest("2026-10-01"), dateForTest("2026-10-10")
	for name, call := range map[string]func() error{
		"metric": func() error { _, err := engine.PowerUsers(ctx, 1, from, to, "visitors", 2, 10); return err },
		"min":    func() error { _, err := engine.PowerUsers(ctx, 1, from, to, "sessions", -1, 10); return err },
		"limit":  func() error { _, err := engine.PowerUsers(ctx, 1, from, to, "sessions", 2, 101); return err },
		"range":  func() error { _, err := engine.PowerUsers(ctx, 1, to, from, "sessions", 2, 10); return err },
	} {
		if err := call(); !errors.Is(err, ErrCohortInput) {
			t.Errorf("%s: error = %v, want ErrCohortInput", name, err)
		}
	}
}

func saveCohort(t *testing.T, pool *pgxpool.Pool, siteID int32, name string, def api.CohortDefinition) int64 {
	t.Helper()
	normalized, err := NormalizeCohortDefinition(def)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(context.Background(), `insert into cohorts(site_id,name,definition) values ($1,$2,$3) returning id`, siteID, name, raw).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func cohortVisits(t *testing.T, engine *Engine, id string, from, to string) float64 {
	t.Helper()
	filters := []api.Filter{{Dimension: "cohort", Operator: api.Is, Values: []string{id}}}
	response, err := engine.Query(context.Background(), api.QueryRequest{
		SiteId:    1,
		DateRange: &api.DateRange{From: types.Date{Time: dateForTest(from)}, To: types.Date{Time: dateForTest(to)}},
		Metrics:   []api.Metric{api.Visits},
		Filters:   &filters,
	})
	if err != nil {
		t.Fatal(err)
	}
	return response.Totals["visits"]
}

func TestCohortFilterNarrowsHotAndClosedQueries(t *testing.T) {
	pool, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	ctx := context.Background()
	signup := saveCohort(t, pool, 1, "Signed up", definition("all", 7, condEvent("signup", 1)))
	idText := func(id int64) string { return strconv.FormatInt(id, 10) }
	hot := engine.WithForcedRoute(RouteHot)
	// Members alice, bob and erin own sessions 11, 12, 13, 21, 22 and 51.
	if got := cohortVisits(t, hot, idText(signup), "2026-10-01", "2026-10-10"); got != 6 {
		t.Fatalf("hot visits for the signup cohort = %v, want 6", got)
	}
	// Day-scoped: alice s11 (10-04) and s12 (10-06) only.
	if got := cohortVisits(t, hot, idText(signup), "2026-10-04", "2026-10-07"); got != 2 {
		t.Fatalf("hot visits 10-04..10-07 = %v, want 2", got)
	}
	// Closed tier: one closed session each for alice (1) and carol (3).
	if _, err := pool.Exec(ctx, `
insert into sessions_closed
 (site_id,day,visitor_id,session_id,entry_path,exit_path,pageviews,custom,duration_ms,engagement_ms,vd_n,flags,seq,seq_engage,seq_scroll,profile_id)
values (1,'2026-09-20',901,9001,1,1,1,0,1000,0,1,1,array[5],array[0],array[0],1),
       (1,'2026-09-20',903,9003,1,1,1,0,1000,0,1,1,array[5],array[0],array[0],3);
insert into day_close(site_id,day,status,version) values (1,'2026-09-20','done',1)`); err != nil {
		t.Fatal(err)
	}
	engine.closeMemo.InvalidateSite(1)
	closed := engine.WithForcedRoute(RouteClosed)
	lastSeen := saveCohort(t, pool, 1, "Active today", definition("all", 7, condAge("last_seen", "within", 1)))
	if got := cohortVisits(t, closed, idText(lastSeen), "2026-09-20", "2026-09-20"); got != 1 {
		t.Fatalf("closed visits for the active cohort = %v, want 1 (alice only)", got)
	}
	// Automatic planning must also avoid the rollup and cube routes for a cohort filter.
	if got := cohortVisits(t, engine, idText(lastSeen), "2026-09-20", "2026-09-20"); got != 1 {
		t.Fatalf("planned visits for the active cohort = %v, want 1", got)
	}
}

func TestCohortFilterNarrowsTheSessionsList(t *testing.T) {
	pool, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	signup := saveCohort(t, pool, 1, "Signed up", definition("all", 7, condEvent("signup", 1)))
	list := func(filters []api.Filter) api.SessionList {
		t.Helper()
		result, err := engine.Sessions(context.Background(), 1, api.ListSessionsParams{
			From: types.Date{Time: dateForTest("2026-10-01")}, To: types.Date{Time: dateForTest("2026-10-10")},
		}, filters)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if all := list(nil); all.Total != 8 {
		t.Fatalf("unfiltered sessions = %d, want the 8 fixture sessions", all.Total)
	}
	// alice, bob and erin own sessions 11, 12, 13, 21, 22 and 51; carol's and dave's sessions drop out.
	filtered := list([]api.Filter{{Dimension: "cohort", Operator: api.Is, Values: []string{strconv.FormatInt(signup, 10)}}})
	if filtered.Total != 6 || len(filtered.Data) != 6 {
		t.Fatalf("cohort sessions = %d (%d rows), want 6", filtered.Total, len(filtered.Data))
	}
}

func TestCohortFilterRejectsBadReferences(t *testing.T) {
	pool, engine := setupCohortFixture(t, privacy.Identified, true, nil)
	ctx := context.Background()
	run := func(op api.FilterOperator, values ...string) error {
		filters := []api.Filter{{Dimension: "cohort", Operator: op, Values: values}}
		_, err := engine.Query(ctx, api.QueryRequest{
			SiteId: 1, DateRange: &api.DateRange{From: types.Date{Time: dateForTest("2026-10-01")}, To: types.Date{Time: dateForTest("2026-10-10")}},
			Metrics: []api.Metric{api.Visits}, Filters: &filters,
		})
		return err
	}
	if err := run(api.Is, "999"); !errors.Is(err, ErrCohortNotFound) {
		t.Fatalf("unknown cohort: %v", err)
	}
	if err := run(api.Is, "abc"); !errors.Is(err, ErrCohortInput) {
		t.Fatalf("non numeric id: %v", err)
	}
	if err := run(api.IsNot, "1"); !errors.Is(err, ErrCohortInput) {
		t.Fatalf("is_not: %v", err)
	}
	// A cohort of another site resolves as not found.
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value values (2,1,'cohortsite02','Other','UTC',30)`); err != nil {
		t.Fatal(err)
	}
	other := saveCohort(t, pool, 2, "Elsewhere", definition("all", 7, condEvent("signup", 1)))
	if err := run(api.Is, strconv.FormatInt(other, 10)); !errors.Is(err, ErrCohortNotFound) {
		t.Fatalf("cohort of another site: %v", err)
	}
	anonymous := NewEngine(pool, nil, vitalsRegistry{site: sites.Site{ID: 1, TZ: time.UTC, Retention: db.Ret30, ProfilesEnabled: true}})
	anonymous.SetTrackingMode(privacy.Anonymous)
	anonymous.now = engine.now
	_, err := anonymous.resolveCohortFilters(ctx, 1, []api.Filter{{Dimension: "cohort", Operator: api.Is, Values: []string{"1"}}})
	if !errors.Is(err, ErrCohortsUnavailable) {
		t.Fatalf("anonymous mode: %v", err)
	}
}
