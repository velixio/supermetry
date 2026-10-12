package platformhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"supermetry/internal/platform/authz"
)

func TestImportPreviewAcceptsMixpanelAndAmplitudeFiles(t *testing.T) {
	tests := []struct {
		source, body string
	}{
		{"mixpanel", `{"event":"Page View","properties":{"time":1709287200,"distinct_id":"u","$insert_id":"1","$current_url":"/"}}` + "\n" +
			`{"event":"Page View","properties":{"time":1709287200,"distinct_id":"u","$insert_id":"1","$current_url":"/"}}` + "\n" +
			"oops\n"},
		{"amplitude", `{"event_type":"[Amplitude] Page Viewed","event_time":"2024-03-01 10:00:00.000000","user_id":"u","uuid":"1","event_properties":{"[Amplitude] Page Path":"/"}}` + "\n"},
	}
	for _, tt := range tests {
		store := &importPreviewStore{}
		response := httptest.NewRecorder()
		previewHandler(authz.RoleOwner, store).ServeHTTP(response, previewRequest(t, tt.source, tt.body))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", tt.source, response.Code, response.Body.String())
		}
		var got struct {
			Rows          int64
			BadRows       int64  `json:"bad_rows"`
			DuplicateRows int64  `json:"duplicate_rows"`
			DateFrom      string `json:"date_from"`
			Totals        struct{ Visitors, Pageviews int64 }
		}
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Totals.Pageviews != 1 || got.Totals.Visitors != 1 || got.DateFrom != "2024-03-01" || store.days != 1 {
			t.Errorf("%s: got=%+v days=%d", tt.source, got, store.days)
		}
		if tt.source == "mixpanel" && (got.Rows != 1 || got.BadRows != 1 || got.DuplicateRows != 1) {
			t.Errorf("mixpanel counts = %+v", got)
		}
	}
}
