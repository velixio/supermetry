package authz

import (
	"slices"
	"testing"
)

func TestSensitiveCapabilities(t *testing.T) {
	for _, role := range []Role{RoleOwner, RoleAdmin, RoleEditor, RoleViewer, RoleKey, RoleShare, "unknown"} {
		for _, action := range []Action{ActionProfilesRead, ActionReplayRead, ActionDataExport} {
			p := Principal{OrgID: 1, Role: role, SiteIDs: []int32{7}, Scopes: []string{"stats:read"}}
			if got, want := Can(p, action, 7), role == RoleOwner || role == RoleAdmin; got != want {
				t.Errorf("role=%s action=%s got=%v want=%v", role, action, got, want)
			}
			p.Capabilities = []string{string(action)}
			if got, want := Can(p, action, 7), role == RoleOwner || role == RoleAdmin || role == RoleEditor || role == RoleViewer; got != want {
				t.Errorf("explicit role=%s action=%s got=%v want=%v", role, action, got, want)
			}
			if role != RoleOwner && role != RoleAdmin && Can(p, action, 8) {
				t.Errorf("explicit capability escaped site boundary: %s %s", role, action)
			}
		}
	}
}

func TestBillingDoesNotGrantData(t *testing.T) {
	p := Principal{OrgID: 1, Role: RoleViewer, SiteIDs: []int32{}, BillingAdmin: true}
	if !Can(p, ActionBillingManage, 0) {
		t.Fatal("billing assignment missing")
	}
	for _, action := range []Action{ActionStatsRead, ActionProfilesRead, ActionReplayRead, ActionDataExport, ActionSiteEdit, ActionMembersManage} {
		if Can(p, action, 7) || Can(p, action, 0) {
			t.Errorf("billing grants %s", action)
		}
	}
	if len(APIScopes(p)) != 0 {
		t.Fatalf("billing-only API scopes: %v", APIScopes(p))
	}
	if Can(Principal{OrgID: 1, Role: RoleAdmin}, ActionBillingManage, 0) {
		t.Fatal("admin implicitly got billing")
	}
	if !Can(Principal{OrgID: 1, Role: RoleOwner}, ActionBillingManage, 0) {
		t.Fatal("owner lacks billing")
	}
	if Can(Principal{OrgID: 1, Role: "unknown", BillingAdmin: true}, ActionBillingManage, 0) {
		t.Fatal("unknown role got billing")
	}
}

func TestRestrictedKeyCannotDeleteOtherSite(t *testing.T) {
	p := Principal{OrgID: 1, Role: RoleKey, SiteIDs: []int32{7}, Scopes: []string{"sites:write"}}
	if !Can(p, ActionSiteDelete, 7) || Can(p, ActionSiteDelete, 8) || Can(p, ActionSiteDelete, 0) {
		t.Fatal("delete does not honor site scope")
	}
	p.SiteIDs = []int32{}
	if Can(p, ActionSiteDelete, 7) {
		t.Fatal("empty key restriction allowed deletion")
	}
}

func TestCapabilityValidationAndAPIScopes(t *testing.T) {
	if !ValidCapabilities(nil) || !ValidCapabilities([]string{"profiles.read", "replay.read", "data.export"}) {
		t.Fatal("valid grants rejected")
	}
	for _, grant := range []string{"*", "members.manage", "billing.manage", "unknown"} {
		if ValidCapabilities([]string{grant}) {
			t.Errorf("accepted %q", grant)
		}
		if Can(Principal{OrgID: 1, Role: RoleViewer, Capabilities: []string{grant}}, Action(grant), 1) {
			t.Errorf("unknown grant honored %q", grant)
		}
	}
	p := Principal{OrgID: 1, Role: RoleViewer, Capabilities: []string{"profiles.read"}}
	if scopes := APIScopes(p); !slices.Contains(scopes, "stats:read") || !slices.Contains(scopes, "profiles:read") || slices.Contains(scopes, "replay:read") || slices.Contains(scopes, "*") {
		t.Fatalf("wrong scopes: %v", scopes)
	}
	p.SiteIDs = []int32{}
	if slices.Contains(EffectiveCapabilities(p), "profiles.read") {
		t.Fatal("empty project access has effective project capability")
	}
	for _, scope := range []string{"profiles:read", "replay:read", "data:export"} {
		if !ValidScopes([]string{scope}) {
			t.Errorf("valid scope rejected %q", scope)
		}
	}
	if ValidScopes([]string{"*"}) || ValidScopes([]string{"not:real"}) {
		t.Fatal("invalid key scope accepted")
	}
}

func TestInvalidIdentifiersFailClosed(t *testing.T) {
	for _, p := range []Principal{{OrgID: -1, Role: RoleOwner}, {OrgID: 0, Role: RoleAdmin}} {
		if Can(p, ActionStatsRead, 1) || Can(p, ActionBillingManage, 0) {
			t.Fatal("invalid organization accepted")
		}
	}
	if Can(Principal{OrgID: 1, Role: RoleOwner}, ActionStatsRead, -1) {
		t.Fatal("negative site accepted")
	}
}
