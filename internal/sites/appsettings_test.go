package sites_test

import (
	"fmt"
	"reflect"
	"testing"

	"supermetry/internal/sites"
)

func TestParseAppSettingsDefaults(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(`{}`), []byte(`null`)} {
		got, err := sites.ParseAppSettings(raw)
		if err != nil || got.MaxReportAgeDays != 7 || len(got.TagAllowlist) != 0 || len(got.Environments) != 0 || got.TagAllowlist == nil || got.Environments == nil {
			t.Fatalf("%s -> %+v err=%v", raw, got, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	got, err := sites.AppSettings{TagAllowlist: []string{"Source", "source", " stage "}, Environments: []string{"Beta", "production", "beta"}}.Normalize()
	if err != nil || !reflect.DeepEqual(got.TagAllowlist, []string{"source", "stage"}) || !reflect.DeepEqual(got.Environments, []string{"beta", "production"}) || got.MaxReportAgeDays != 7 {
		t.Fatalf("got %+v err=%v", got, err)
	}
	many := func(n int, format string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf(format, i)
		}
		return out
	}
	for name, in := range map[string]sites.AppSettings{
		"age 31":   {MaxReportAgeDays: 31},
		"age -1":   {MaxReportAgeDays: -1},
		"51 tags":  {TagAllowlist: many(51, "tag%d")},
		"bad tag":  {TagAllowlist: []string{"9x"}},
		"21 envs":  {Environments: many(21, "env%d")},
		"bad env":  {Environments: []string{"-x"}},
		"long tag": {TagAllowlist: []string{"a" + string(make([]byte, 0)) + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
	} {
		if _, err := in.Normalize(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if got, err := (sites.AppSettings{MaxReportAgeDays: 30, TagAllowlist: many(50, "tag%d")}).Normalize(); err != nil || got.MaxReportAgeDays != 30 {
		t.Fatalf("boundary: %+v %v", got, err)
	}
}

func TestNormalizeSkipsBlankEntries(t *testing.T) {
	got, err := (sites.AppSettings{TagAllowlist: []string{"a", " ", ""}, Environments: []string{"", "beta"}}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TagAllowlist) != 1 || got.TagAllowlist[0] != "a" || len(got.Environments) != 1 || got.Environments[0] != "beta" {
		t.Fatalf("got %+v", got)
	}
}

func TestAppSettingsPerformanceDefaults(t *testing.T) {
	got, err := sites.ParseAppSettings(nil)
	if err != nil || got.PerformanceEnabled == nil || !*got.PerformanceEnabled || got.SpanRetentionDays != 14 {
		t.Fatalf("defaults %+v err=%v", got, err)
	}
	off, err := sites.ParseAppSettings([]byte(`{"performance_enabled":false,"span_retention_days":3}`))
	if err != nil || *off.PerformanceEnabled || off.SpanRetentionDays != 3 {
		t.Fatalf("explicit %+v err=%v", off, err)
	}
	for _, days := range []string{"-1", "15"} {
		if _, err := sites.ParseAppSettings([]byte(`{"span_retention_days":` + days + `}`)); err == nil {
			t.Fatalf("span_retention_days %s accepted", days)
		}
	}
}
