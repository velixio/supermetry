package main

import (
	"os"
	"strings"
	"testing"
)

func TestRequiredBenchmarkScriptsContainSafetyAndEvidenceSteps(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{"scripts/p1-privacy.sh", []string{"pg_dump", "/api/event", `"$tmp/proto" load`, "SIGTERM", "grep"}},
		{"scripts/c2-restart.sh", []string{"kill -9", "SIGTERM", "/api/event", "sessions", "events"}},
		{"scripts/o5-retention.sh", []string{"--tier tiny", "SUPERMETRY_RAW_PARTITION=daily", "retention --raw-days 7", "schema_bytes"}},
	}
	for _, tc := range tests {
		b, err := os.ReadFile(tc.name)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s missing %q", tc.name, want)
			}
		}
	}
}
