package config_test

import (
	"strings"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/privacy"
)

func TestParseTrackingMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, raw string
		set       bool
		mode      privacy.Mode
		wantErr   bool
	}{
		{"unset", "", false, privacy.Anonymous, false},
		{"explicit anonymous", "anonymous", true, privacy.Anonymous, false},
		{"explicit identified", "identified", true, privacy.Identified, false},
		{"explicit empty", "", true, privacy.Anonymous, true},
		{"unknown", "enabled", true, privacy.Anonymous, true},
		{"case mismatch", "Identified", true, privacy.Anonymous, true},
		{"leading whitespace", " anonymous", true, privacy.Anonymous, true},
		{"trailing whitespace", "identified\n", true, privacy.Anonymous, true},
		{"absent ignores value", "identified", false, privacy.Anonymous, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.ParseTrackingMode(tt.raw, tt.set)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErr {
				if got != (config.TrackingMode{}) || !strings.Contains(err.Error(), "SUPERMETRY_TRACKING_MODE") {
					t.Fatalf("unsafe result or missing env name: %+v, %v", got, err)
				}
				return
			}
			if got.Mode != tt.mode || got.Explicit != tt.set {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestLoadLookupTrackingMode(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, raw        string
		present, wantErr bool
	}{
		{"unset", "", false, false}, {"anonymous", "anonymous", true, false},
		{"identified", "identified", true, false}, {"empty", "", true, true},
		{"unknown", "identifed", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"SUPERMETRY_DATABASE_URL": "postgres://localhost/test", "SUPERMETRY_BASE_URL": "http://localhost", "SUPERMETRY_SECRET_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
			if tt.present {
				env[config.TrackingModeEnv] = tt.raw
			}
			got, err := config.LoadLookup(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v", err)
			}
			if !tt.wantErr && got.Tracking.Explicit != tt.present {
				t.Fatalf("lost presence: %+v", got.Tracking)
			}
		})
	}
}

func TestValidateTrackingUpgrade(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name              string
		mode              config.TrackingMode
		profiles, wantErr bool
	}{
		{"fresh", config.TrackingMode{}, false, false},
		{"legacy profiles require choice", config.TrackingMode{}, true, true},
		{"explicit keep profiles", config.TrackingMode{Mode: privacy.Identified, Explicit: true}, true, false},
		{"explicit downgrade", config.TrackingMode{Mode: privacy.Anonymous, Explicit: true}, true, false},
		{"invalid", config.TrackingMode{Mode: privacy.Mode(255), Explicit: true}, false, true},
		{"implicit identified forbidden", config.TrackingMode{Mode: privacy.Identified}, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mode.ValidateUpgrade(tt.profiles)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
			if err != nil && !strings.Contains(err.Error(), config.TrackingModeEnv) {
				t.Fatalf("unactionable error=%v", err)
			}
		})
	}
}
