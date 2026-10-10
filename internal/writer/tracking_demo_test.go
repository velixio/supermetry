package writer

import (
	"testing"
	"time"

	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
)

type demoPolicyRegistry map[int32]sites.Site

func (r demoPolicyRegistry) Site(id int32) (sites.Site, bool) {
	site, ok := r[id]
	return site, ok
}

func (r demoPolicyRegistry) ByPublicID(id string) (sites.Site, bool) {
	for _, site := range r {
		if site.PublicID == id {
			return site, true
		}
	}
	return sites.Site{}, false
}

func (r demoPolicyRegistry) All() []sites.Site {
	result := make([]sites.Site, 0, len(r))
	for _, site := range r {
		result = append(result, site)
	}
	return result
}

func TestDemoIdentityAndPathsAllowedInAnonymousMode(t *testing.T) {
	w := New(nil, nil, 1, time.Hour, time.Hour)
	w.SetTrackingPolicy(privacy.Anonymous, demoPolicyRegistry{
		1: {ID: 1, Demo: true},
		2: {ID: 2},
	})
	events := []Event{
		{Hit: session.Hit{SiteID: 1, PathID: 11, UTMSource: 12}, ProfileExternalID: "demo-profile", Props: map[string]string{"plan": "pro"}},
		{Hit: session.Hit{SiteID: 2, PathID: 21, UTMSource: 22}, ProfileExternalID: "real-profile", Props: map[string]string{"plan": "pro"}},
	}

	w.suppressRestrictedIdentity(events)

	if got := events[0]; got.PathID != 11 || got.UTMSource != 12 || got.ProfileExternalID != "demo-profile" || got.Props["plan"] != "pro" {
		t.Fatalf("demo identity suppressed: %+v", got)
	}
	if got := events[1]; got.PathID != 0 || got.UTMSource != 0 || got.ProfileExternalID != "" || got.Props != nil {
		t.Fatalf("normal identity retained: %+v", got)
	}
}
