package platformhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/imports"
	"supermetry/internal/platform/authz"
	"supermetry/internal/privacy"
)

type compareStore struct {
	*importTestStore
	days     []imports.DayTotals
	gotSite  int32
	gotImpID int64
}

func (s *compareStore) ImportedDaily(_ context.Context, siteID int32, _, _ time.Time, importID int64) ([]imports.DayTotals, error) {
	s.gotSite, s.gotImpID = siteID, importID
	return s.days, nil
}

type compareQuery struct{ request api.QueryRequest }

func (q *compareQuery) Query(_ context.Context, request api.QueryRequest) (api.QueryResponse, error) {
	q.request = request
	return api.QueryResponse{Rows: []map[string]interface{}{
		{"time": "2026-03-01", "visitors": float64(95), "visits": float64(100), "pageviews": float64(420)},
	}}, nil
}

func compareHandler(role authz.Role, orgID int64, store *compareStore, query *compareQuery) http.Handler {
	return New(Deps{TrackingMode: privacy.Identified,
		Authenticator: importTestAuth{principal: authz.Principal{UserID: 1, OrgID: orgID, Role: role}},
		Sites:         importTestSites{7: {ID: 7, OrgID: 4, TZ: time.UTC}}, Imports: store, Query: query,
	})
}

func TestCompareImportsSetsImportedAgainstCollectedDays(t *testing.T) {
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	store := &compareStore{importTestStore: &importTestStore{item: imports.Import{ID: 5, SiteID: 7}},
		days: []imports.DayTotals{{Day: day, Visitors: 100, Visits: 110, Pageviews: 400}, {Day: day.AddDate(0, 0, 1), Visitors: 10, Pageviews: 20}}}
	query := &compareQuery{}
	handler := compareHandler(authz.RoleOwner, 4, store, query)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/private/sites/7/imports/compare?from=2026-03-01&to=2026-03-02&import=5", nil))
	if response.Code != 200 {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	var got struct {
		Days []struct {
			Day             string                    `json:"day"`
			Native          *struct{ Visitors int64 } `json:"native"`
			VisitorsDiffPct *float64                  `json:"visitors_diff_pct"`
		} `json:"days"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || len(got.Days) != 2 {
		t.Fatalf("body = %s err=%v", response.Body.String(), err)
	}
	if got.Days[0].Native == nil || got.Days[0].Native.Visitors != 95 || got.Days[0].VisitorsDiffPct == nil || *got.Days[0].VisitorsDiffPct != -5 || got.Days[1].Native != nil {
		t.Fatalf("days = %+v", got.Days)
	}
	if store.gotSite != 7 || store.gotImpID != 5 {
		t.Fatalf("source asked for site=%d import=%d", store.gotSite, store.gotImpID)
	}
	if query.request.IncludeImported == nil || *query.request.IncludeImported || query.request.Dimensions == nil || (*query.request.Dimensions)[0] != "time" {
		t.Fatalf("native query must exclude imported rows and group by day: %+v", query.request)
	}
}

func TestCompareImportsValidatesAccessAndInput(t *testing.T) {
	newStore := func() *compareStore {
		return &compareStore{importTestStore: &importTestStore{item: imports.Import{ID: 5, SiteID: 8}}}
	}
	for _, tt := range []struct {
		name   string
		role   authz.Role
		org    int64
		target string
		want   int
	}{
		{"editor", authz.RoleEditor, 4, "/api/private/sites/7/imports/compare?from=2026-03-01&to=2026-03-02", 403},
		{"other org", authz.RoleOwner, 9, "/api/private/sites/7/imports/compare?from=2026-03-01&to=2026-03-02", 404},
		{"missing dates", authz.RoleOwner, 4, "/api/private/sites/7/imports/compare", 400},
		{"reversed", authz.RoleOwner, 4, "/api/private/sites/7/imports/compare?from=2026-03-05&to=2026-03-01", 400},
		{"too long", authz.RoleOwner, 4, "/api/private/sites/7/imports/compare?from=2024-01-01&to=2026-03-01", 400},
		{"bad import id", authz.RoleOwner, 4, "/api/private/sites/7/imports/compare?from=2026-03-01&to=2026-03-02&import=x", 400},
		{"import of another site", authz.RoleOwner, 4, "/api/private/sites/7/imports/compare?from=2026-03-01&to=2026-03-02&import=5", 404},
	} {
		response := httptest.NewRecorder()
		compareHandler(tt.role, tt.org, newStore(), &compareQuery{}).ServeHTTP(response, httptest.NewRequest("GET", tt.target, nil))
		if response.Code != tt.want {
			t.Errorf("%s: status = %d body=%s, want %d", tt.name, response.Code, strings.TrimSpace(response.Body.String()), tt.want)
		}
	}
}
