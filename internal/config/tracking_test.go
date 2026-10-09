package config_test

import (
	"strings"
	"testing"

	"panel/internal/config"
	"panel/internal/privacy"
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
				if got != (config.TrackingMode{}) || !strings.Contains(err.Error(), "PANEL_TRACKING_MODE") {
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

func TestTrackingParserRemainsInactive(t *testing.T) {
	// Existing Load must not start consulting the new environment variable.
	_, _ = config.Load(func(name string) string {
		if name == config.TrackingModeEnv {
			t.Fatal("tracking mode activated in Load")
		}
		return ""
	})
}
