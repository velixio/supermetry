package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/config"
	"supermetry/internal/ingest"
)

type revenueStub struct {
	report      api.RevenueReport
	customers   []api.RevenueCustomer
	err         error
	lastRequest api.RevenueReportRequest
	calls       int
}

func (s *revenueStub) RevenueReport(_ context.Context, _ int32, req api.RevenueReportRequest) (api.RevenueReport, error) {
	s.calls++
	s.lastRequest = req
	return s.report, s.err
}

func (s *revenueStub) RevenueCustomers(_ context.Context, _ int32, _, _ time.Time, _ string, _ int) ([]api.RevenueCustomer, error) {
	s.calls++
	return s.customers, s.err
}

func revenueRequest(t *testing.T, service *revenueStub, principal api.Principal, path string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	router := identifiedRouter(api.Deps{
		Config:      config.Config{},
		Revenue:     service,
		PrivateAuth: principalAuth{principal: principal},
		PublicAuth:  principalAuth{principal: principal},
		Sites:       orgSites{1: {ID: 1, OrgID: 7}},
		RateLimiter: ingest.NewTokenBuckets(0, 30),
	})
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if len(header) == 2 {
		request.Header.Set(header[0], header[1])
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

const revenueQuery = "/api/private/sites/1/revenue/report?from=2026-10-01&to=2026-10-05&breakdown=source"

func TestRevenueReportEndpoint(t *testing.T) {
	reader := api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}
	uniq := int64(2)
	stub := &revenueStub{report: api.RevenueReport{Breakdown: "source", Attribution: "last_click", LookbackDays: 30, Orders: 2, Revenue: 50, UniqueCustomers: &uniq,
		Rows: []api.RevenueReportRow{{Key: "=HYPERLINK(\"x\")", Orders: 2, Revenue: 50, AverageOrderValue: 25}}}}

	t.Run("returns JSON with parsed parameters", func(t *testing.T) {
		response := revenueRequest(t, stub, reader, revenueQuery+"&attribution=last_click&lookback_days=14&limit=5")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"unique_customers":2`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if got := stub.lastRequest; got.Dimension != "source" || got.Attribution != "last_click" || got.LookbackDays != 14 || got.Limit != 5 {
			t.Fatalf("request = %+v", got)
		}
	})

	t.Run("csv export needs the export scope and neutralises formulas", func(t *testing.T) {
		denied := revenueRequest(t, stub, reader, revenueQuery, "Accept", "text/csv")
		assertRecorderProblem(t, denied, http.StatusForbidden)
		exporter := api.Principal{OrgID: 7, Scopes: []string{"stats:read", "data:export"}, SiteIDs: []int32{1}}
		response := revenueRequest(t, stub, exporter, revenueQuery, "Accept", "text/csv")
		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/csv") ||
			!strings.HasPrefix(body, "source,orders,revenue,average_order_value,unique_customers,repeat_customers,repeat_rate\n") || strings.Contains(body, "\n=HYPERLINK") {
			t.Fatalf("status=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), body)
		}
	})

	t.Run("authorization and validation", func(t *testing.T) {
		calls := stub.calls
		for name, tt := range map[string]struct {
			principal api.Principal
			path      string
			status    int
		}{
			"missing scope":     {api.Principal{OrgID: 7, Scopes: []string{"events:write"}, SiteIDs: []int32{1}}, revenueQuery, http.StatusForbidden},
			"foreign site":      {api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{2}}, revenueQuery, http.StatusNotFound},
			"bad date":          {reader, "/api/private/sites/1/revenue/report?from=nope&to=2026-10-05&breakdown=source", http.StatusBadRequest},
			"non numeric limit": {reader, revenueQuery + "&limit=many", http.StatusBadRequest},
		} {
			response := revenueRequest(t, stub, tt.principal, tt.path)
			if response.Code != tt.status {
				t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
			}
		}
		if stub.calls != calls {
			t.Fatal("rejected requests must not reach the service")
		}
	})

	t.Run("service errors map to problems", func(t *testing.T) {
		mixed := revenueRequest(t, &revenueStub{err: api.ErrMixedRevenueCurrency}, reader, revenueQuery)
		assertRecorderProblem(t, mixed, http.StatusConflict)
		invalid := revenueRequest(t, &revenueStub{err: api.ErrRevenueInput}, reader, revenueQuery)
		assertRecorderProblem(t, invalid, http.StatusBadRequest)
	})
}

func TestRevenueCustomersEndpointNeedsProfileScope(t *testing.T) {
	stub := &revenueStub{customers: []api.RevenueCustomer{{ProfileId: 4, ExternalId: "cust-4", Orders: 2, Revenue: 40}}}
	path := "/api/private/sites/1/revenue/customers?from=2026-10-01&to=2026-10-05&search=cust"
	reader := api.Principal{OrgID: 7, Scopes: []string{"stats:read"}, SiteIDs: []int32{1}}
	assertRecorderProblem(t, revenueRequest(t, stub, reader, path), http.StatusForbidden)
	if stub.calls != 0 {
		t.Fatal("customers must not be queried without profiles:read")
	}
	allowed := api.Principal{OrgID: 7, Scopes: []string{"stats:read", "profiles:read"}, SiteIDs: []int32{1}}
	response := revenueRequest(t, stub, allowed, path)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"external_id":"cust-4"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	empty := revenueRequest(t, &revenueStub{}, allowed, path)
	if !strings.Contains(empty.Body.String(), `"data":[]`) {
		t.Fatalf("an empty list must serialize as [], got %s", empty.Body.String())
	}
}
