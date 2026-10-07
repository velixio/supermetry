package authz

import "testing"

// A member restricted to zero sites (e.g. after their sites were deleted) must
// see nothing, not everything: an empty restriction is not the nil "all sites".
func TestEmptySiteRestrictionGrantsNoSites(t *testing.T) {
	p := Principal{Role: RoleViewer, OrgID: 1, SiteIDs: []int32{}}
	if Can(p, ActionStatsRead, 1) {
		t.Fatal("empty site restriction granted access")
	}
	all := Principal{Role: RoleViewer, OrgID: 1}
	if !Can(all, ActionStatsRead, 1) {
		t.Fatal("nil site restriction must allow the org's sites")
	}
}
