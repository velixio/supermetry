package sites_test

import (
	"testing"

	"supermetry/internal/sites"
)

func TestLogRetentionDaysDefaultsAndBounds(t *testing.T) {
	got, err := sites.ParseAppSettings(nil)
	if err != nil || got.LogRetentionDays != 7 {
		t.Fatalf("default = %d %v", got.LogRetentionDays, err)
	}
	for in, want := range map[string]int{`{"log_retention_days":1}`: 1, `{"log_retention_days":30}`: 30} {
		got, err := sites.ParseAppSettings([]byte(in))
		if err != nil || got.LogRetentionDays != want {
			t.Fatalf("%s = %d %v", in, got.LogRetentionDays, err)
		}
	}
	for _, in := range []string{`{"log_retention_days":31}`, `{"log_retention_days":-1}`} {
		if _, err := sites.ParseAppSettings([]byte(in)); err == nil {
			t.Fatalf("%s accepted", in)
		}
	}
}
