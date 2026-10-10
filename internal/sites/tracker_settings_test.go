package sites

import "testing"

func TestResolveDefaultsAndOverrides(t *testing.T) {
	in, err := ParseTrackerSettings([]byte(`{"hash": true, "outbound": false}`))
	if err != nil {
		t.Fatal(err)
	}
	got := in.Resolve(true)
	want := TrackerSettings{Outbound: false, Downloads: true, Forms: true, Vitals: true, NotFound: true, Hash: true, Clicks: true}
	if got != want {
		t.Fatalf("Resolve(true) = %+v, want %+v", got, want)
	}
	if clicks := in.Resolve(false).Clicks; clicks {
		t.Fatal("clicks must follow heatmaps_enabled when unset")
	}
}

func TestResolveExplicitClicksIgnoreHeatmaps(t *testing.T) {
	in, err := ParseTrackerSettings([]byte(`{"clicks": false}`))
	if err != nil {
		t.Fatal(err)
	}
	if in.Resolve(true).Clicks {
		t.Fatal("explicit clicks=false must win over heatmaps_enabled")
	}
}

func TestParseTrackerSettingsStrict(t *testing.T) {
	for _, raw := range []string{`{"unknown": true}`, `{"hash": "yes"}`, `[]`, `{"forms": 1}`, `{"hash": true} {}`} {
		if _, err := ParseTrackerSettings([]byte(raw)); err == nil {
			t.Errorf("%s: want error", raw)
		}
	}
	if _, err := ParseTrackerSettings(nil); err != nil {
		t.Errorf("empty input must give defaults, got %v", err)
	}
	in, _ := ParseTrackerSettings(nil)
	want := TrackerSettings{Outbound: true, Downloads: true, Forms: true, Vitals: true, NotFound: true}
	if got := in.Resolve(false); got != want {
		t.Errorf("defaults = %+v, want %+v", got, want)
	}
}

func TestAnyExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings TrackerSettings
		want     bool
	}{
		{name: "none", settings: TrackerSettings{}, want: false},
		{name: "outbound", settings: TrackerSettings{Outbound: true}, want: true},
		{name: "downloads", settings: TrackerSettings{Downloads: true}, want: true},
		{name: "forms", settings: TrackerSettings{Forms: true}, want: true},
		{name: "vitals", settings: TrackerSettings{Vitals: true}, want: true},
		{name: "not found", settings: TrackerSettings{NotFound: true}, want: true},
		{name: "hash routing", settings: TrackerSettings{Hash: true}, want: true},
		{name: "clicks", settings: TrackerSettings{Clicks: true}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.settings.AnyExtension(); got != test.want {
				t.Fatalf("AnyExtension() = %t, want %t", got, test.want)
			}
		})
	}
}
