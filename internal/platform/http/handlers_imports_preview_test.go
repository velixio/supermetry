package platformhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/imports"
	"supermetry/internal/platform/authz"
	"supermetry/internal/privacy"
)

type importPreviewStore struct {
	importTestStore
	siteID  int32
	days    int
	overlap imports.Overlap
}

func (s *importPreviewStore) Preview(_ context.Context, siteID int32, _ [32]byte, aggregate imports.Aggregate) (imports.Overlap, error) {
	s.siteID, s.days = siteID, len(aggregate.Days)
	return s.overlap, nil
}

func previewRequest(t *testing.T, source, contents string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("source", source); err != nil {
		t.Fatal(err)
	}
	file, err := w.CreateFormFile("file", "report.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/private/sites/7/imports/preview", &body)
	request.Header.Set("Content-Type", w.FormDataContentType())
	request.Header.Set("X-CSRF-Token", "token")
	request.AddCookie(&http.Cookie{Name: "supermetry_csrf", Value: "token"})
	return request
}

func previewHandler(role authz.Role, store any) http.Handler {
	deps := Deps{TrackingMode: privacy.Identified,
		Authenticator: importTestAuth{principal: authz.Principal{UserID: 1, OrgID: 4, Role: role}},
		Sites:         importTestSites{7: {ID: 7, OrgID: 4, TZ: time.UTC}}, ImportQueue: &importTestQueue{},
	}
	switch s := store.(type) {
	case *importPreviewStore:
		deps.Imports = s
	case *importTestStore:
		deps.Imports = s
	}
	return New(deps)
}

const previewCSV = "Date,Sessions,Views\n20240301,5,9\nbad,1,1\n20240302,2,3\n"

func TestImportPreviewReportsSummaryWithoutQueueingOrCreating(t *testing.T) {
	store := &importPreviewStore{overlap: imports.Overlap{ImportedDays: 1}}
	response := httptest.NewRecorder()
	previewHandler(authz.RoleAdmin, store).ServeHTTP(response, previewRequest(t, "ga4", previewCSV))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got struct {
		Rows     int64
		BadRows  int64 `json:"bad_rows"`
		Days     int
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
		Totals   struct{ Visits, Pageviews int64 }
		Errors   []struct {
			Row     int
			Message string
		}
		Overlap struct {
			ImportedDays int `json:"imported_days"`
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rows != 2 || got.BadRows != 1 || got.Days != 2 || got.DateFrom != "2024-03-01" || got.DateTo != "2024-03-02" ||
		got.Totals.Visits != 7 || got.Totals.Pageviews != 12 || len(got.Errors) != 1 || got.Errors[0].Row != 3 ||
		!strings.Contains(got.Errors[0].Message, "date") || got.Overlap.ImportedDays != 1 || store.siteID != 7 {
		t.Fatalf("got=%+v store=%+v body=%s", got, store, response.Body.String())
	}
	if store.item.ID != 0 {
		t.Fatal("preview created an import")
	}
}

func TestImportPreviewRequiresOwnerOrAdmin(t *testing.T) {
	for _, role := range []authz.Role{authz.RoleEditor, authz.RoleViewer} {
		response := httptest.NewRecorder()
		previewHandler(role, &importPreviewStore{}).ServeHTTP(response, previewRequest(t, "ga4", previewCSV))
		if response.Code != http.StatusForbidden {
			t.Errorf("%s: status=%d", role, response.Code)
		}
	}
}

func TestImportPreviewRejectsBadInput(t *testing.T) {
	tests := []struct {
		name, source, body string
		want               int
	}{
		{"unknown source", "mixpanel", previewCSV, http.StatusBadRequest},
		{"no date column", "ga4", "Country,Sessions\nUS,1\n", http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		response := httptest.NewRecorder()
		previewHandler(authz.RoleOwner, &importPreviewStore{}).ServeHTTP(response, previewRequest(t, tt.source, tt.body))
		if response.Code != tt.want {
			t.Errorf("%s: status=%d body=%s", tt.name, response.Code, response.Body.String())
		}
	}
	// A store without preview support reports the feature as unavailable.
	response := httptest.NewRecorder()
	previewHandler(authz.RoleOwner, &importTestStore{}).ServeHTTP(response, previewRequest(t, "ga4", previewCSV))
	if response.Code != http.StatusInternalServerError {
		t.Errorf("unavailable: status=%d", response.Code)
	}
}

func TestImportPreviewIsBoundedByConcurrencySlots(t *testing.T) {
	for range cap(importPreviewSlots) {
		importPreviewSlots <- struct{}{}
	}
	defer func() {
		for range cap(importPreviewSlots) {
			<-importPreviewSlots
		}
	}()
	response := httptest.NewRecorder()
	previewHandler(authz.RoleOwner, &importPreviewStore{}).ServeHTTP(response, previewRequest(t, "ga4", previewCSV))
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestGA4SourceIsAcceptedForUpload(t *testing.T) {
	data := &importTestStore{}
	queue := &importTestQueue{}
	handler := New(Deps{TrackingMode: privacy.Identified,
		Authenticator: importTestAuth{principal: authz.Principal{UserID: 1, OrgID: 4, Role: authz.RoleOwner}},
		Sites:         importTestSites{7: {ID: 7, OrgID: 4, TZ: time.UTC}}, Imports: data, ImportQueue: queue,
	})
	request := previewRequest(t, "ga4", previewCSV)
	request.URL.Path = "/api/private/sites/7/imports"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || data.input.Source != imports.SourceGA4 || !queue.queued {
		t.Fatalf("status=%d source=%q", response.Code, data.input.Source)
	}
}
