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
	if (TrackerSettings{Hash: true}).AnyExtension() {
		t.Fatal("hash alone must not count as an extension")
	}
	if !(TrackerSettings{Forms: true}).AnyExtension() {
		t.Fatal("forms is an extension")
	}
}
