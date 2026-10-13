package platformhttp

import (
	"testing"

	"supermetry/internal/platform/store"
)

func TestEffectivePreferencesDefaultToTheLastSite(t *testing.T) {
	if got := effectiveUserPreferences(store.UserPreferences{}).LandingPage; got != "last_site" {
		t.Fatalf("default landing page = %q, want last_site", got)
	}
	if got := effectiveUserPreferences(store.UserPreferences{LandingPage: "overview"}).LandingPage; got != "overview" {
		t.Fatalf("explicit overview = %q, want it kept", got)
	}
}
