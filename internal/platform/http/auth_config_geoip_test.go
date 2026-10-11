package platformhttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"supermetry/internal/db"
	platformauth "supermetry/internal/platform/auth"
	platformcrypto "supermetry/internal/platform/crypto"
	platformhttp "supermetry/internal/platform/http"
	"supermetry/internal/platform/mail"
	"supermetry/internal/platform/store"
	"supermetry/internal/testpg"
)

// The dashboard footer credits the geolocation provider that is actually in use, so the
// public config endpoint reports it. Anything unexpected falls back to the bundled one.
func TestAuthConfigReportsTheGeoIPSource(t *testing.T) {
	pool, _ := testpg.New(t)
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	box, err := platformcrypto.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"), "supermetry/totp/v1")
	if err != nil {
		t.Fatal(err)
	}
	authService := platformauth.New(&store.Store{Pool: pool}, &mail.Fake{}, box, "https://supermetry.example")

	tests := []struct {
		name   string
		source func() string
		want   string
	}{
		{name: "not configured uses the bundled database", source: nil, want: "dbip"},
		{name: "bundled database", source: func() string { return "dbip" }, want: "dbip"},
		{name: "maxmind", source: func() string { return "maxmind" }, want: "maxmind"},
		{name: "unknown values are never echoed", source: func() string { return "<script>" }, want: "dbip"},
		{name: "empty falls back", source: func() string { return "" }, want: "dbip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := platformhttp.New(platformhttp.Deps{Auth: authService, Signup: "open", GeoIPSource: tt.source})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/private/auth/config", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body struct {
				GeoIPSource string `json:"geoip_source"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.GeoIPSource != tt.want {
				t.Fatalf("geoip_source = %q, want %q", body.GeoIPSource, tt.want)
			}
		})
	}
}
