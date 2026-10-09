package alerts

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"supermetry/internal/api"
	"supermetry/internal/platform/goals"
	queryengine "supermetry/internal/query"
	"supermetry/internal/sites"
)

// Queryer is implemented by query.Engine.
type Queryer interface {
	Query(context.Context, api.QueryRequest) (api.QueryResponse, error)
}

type GoalLookup interface {
	Goals(int32) []goals.Goal
}

// EngineSource adapts the query engine's inclusive civil-date API to the exact
// instant windows used by Evaluator. Until the engine supports timestamps, a
// partial local day necessarily queries that whole civil day.
type EngineSource struct {
	query Queryer
	goals GoalLookup
}

func NewEngineSource(query Queryer, definitions GoalLookup) *EngineSource {
	if configurable, ok := query.(interface{ SetGoalLookup(queryengine.GoalLookup) }); ok {
		configurable.SetGoalLookup(definitions)
	}
	return &EngineSource{query: query, goals: definitions}
}

func (s *EngineSource) Visitors(ctx context.Context, site sites.Site, from, to time.Time) (float64, error) {
	response, err := s.query.Query(ctx, metricRequest(site, from, to, api.Visitors, nil))
	if err != nil {
		return 0, err
	}
	return response.Totals[string(api.Visitors)], nil
}

func (s *EngineSource) Conversions(ctx context.Context, site sites.Site, goalID int64, from, to time.Time) (float64, error) {
	if s.goals == nil {
		return 0, errors.New("goal definitions are unavailable")
	}
	found := false
	for _, goal := range s.goals.Goals(site.ID) {
		if goal.ID == goalID {
			found = true
			break
		}
	}
	if !found {
		return 0, fmt.Errorf("goal %d is unavailable for site %d", goalID, site.ID)
	}
	filters := []api.Filter{{Dimension: "goal_id", Operator: api.Is, Values: []string{strconv.FormatInt(goalID, 10)}}}
	response, err := s.query.Query(ctx, metricRequest(site, from, to, api.Conversions, &filters))
	if err != nil {
		return 0, err
	}
	return response.Totals[string(api.Conversions)], nil
}

func metricRequest(site sites.Site, from, to time.Time, metric api.Metric, filters *[]api.Filter) api.QueryRequest {
	location := site.TZ
	if location == nil {
		location = time.UTC
	}
	timezone := location.String()
	return api.QueryRequest{
		SiteId: site.ID,
		DateRange: &api.DateRange{
			From: openapi_types.Date{Time: civilDateIn(from, location)},
			To:   openapi_types.Date{Time: civilDateIn(to, location)},
		},
		Timezone: &timezone,
		Metrics:  []api.Metric{metric},
		Filters:  filters,
	}
}

func civilDateIn(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}
