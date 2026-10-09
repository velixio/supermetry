package privacy

import "testing"

func TestCanonicalDimensionValue(t *testing.T) {
	for _, tc := range []struct {
		dimension, value string
		valid            bool
	}{
		{"country", "US", true}, {"country", "IN", true}, {"country", "ZZ", false}, {"country", "us", false}, {"country", "person@example.test", false},
		{"browser", "Chrome", true}, {"browser", "private-account", false}, {"os", "Linux", true}, {"os", "Linux account", false},
		{"device", "mobile", true}, {"device", "128", false}, {"channel", "Email", true}, {"channel", "private", false},
		{"time", "2026-10-09", true}, {"hour", "2026-10-09T12:00:00", true}, {"time", "private", false}, {"page", "/", false},
	} {
		t.Run(tc.dimension+"/"+tc.value, func(t *testing.T) {
			got, valid := CanonicalDimensionValue(tc.dimension, tc.value)
			if valid != tc.valid || (valid && got != tc.value) || (!valid && got != "") {
				t.Fatalf("got %q/%t", got, valid)
			}
		})
	}
}
