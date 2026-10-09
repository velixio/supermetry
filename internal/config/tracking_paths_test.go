package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/privacy"
)

func TestParseTrackingPublicPaths(t *testing.T) {
	t.Parallel()
	tooMany := make([]string, 129)
	for i := range tooMany {
		tooMany[i] = "/" + strings.Repeat("a", i+1)
	}
	tooManyJSON, _ := json.Marshal(tooMany)
	longJSON, _ := json.Marshal([]string{"/" + strings.Repeat("a", 512)})
	unicodeLongJSON, _ := json.Marshal([]string{"/" + strings.Repeat("é", 256)})
	for _, tc := range []struct {
		name, raw        string
		present, wantErr bool
	}{
		{"unset", "", false, false},
		{"absent ignores value", "invalid", false, false},
		{"empty allowlist", "[]", true, false},
		{"public literals", `["/","/pricing"]`, true, false},
		{"empty value", "", true, true},
		{"whitespace only", "  ", true, true},
		{"null", "null", true, true},
		{"object", "{}", true, true},
		{"string", `"/pricing"`, true, true},
		{"number element", "[1]", true, true},
		{"null element", "[null]", true, true},
		{"trailing value", "[] []", true, true},
		{"malformed", "[", true, true},
		{"relative", `["pricing"]`, true, true},
		{"query", `["/pricing?token=private"]`, true, true},
		{"fragment", `["/pricing#private"]`, true, true},
		{"wildcard", `["/users/*"]`, true, true},
		{"noncanonical", `["/a/../pricing"]`, true, true},
		{"too many", string(tooManyJSON), true, true},
		{"too long", string(longJSON), true, true},
		{"too many UTF-8 bytes", string(unicodeLongJSON), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := config.ParseTrackingPublicPaths(tc.raw, tc.present)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), config.TrackingPublicPathsEnv) {
				t.Fatalf("unactionable error: %v", err)
			}
			if err != nil && policy.Fingerprint() != (privacy.PathPolicy{}).Fingerprint() {
				t.Fatal("invalid config returned nonempty policy")
			}
		})
	}
}

func TestLoadLookupTrackingPublicPaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw        string
		present, wantErr bool
	}{
		{"unset", "", false, false}, {"empty", "", true, true},
		{"valid", `["/pricing"]`, true, false}, {"explicit deny", "[]", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"SUPERMETRY_DATABASE_URL": "postgres://localhost/test", "SUPERMETRY_BASE_URL": "http://localhost", "SUPERMETRY_SECRET_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
			if tc.present {
				env[config.TrackingPublicPathsEnv] = tc.raw
			}
			got, err := config.LoadLookup(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			if err == nil {
				want, parseErr := config.ParseTrackingPublicPaths(tc.raw, tc.present)
				if parseErr != nil || got.TrackingPaths.Fingerprint() != want.Fingerprint() {
					t.Fatal("LoadLookup lost path policy")
				}
				expectedPath := privacy.RedactedPath
				if tc.name == "valid" {
					expectedPath = "/pricing"
				}
				if got.TrackingPaths.PublicPath("/pricing") != expectedPath || got.TrackingPaths.PublicPath("/private") != privacy.RedactedPath {
					t.Fatal("LoadLookup changed exact public-path membership")
				}

			}
		})
	}
}
