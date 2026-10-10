package authz

import "testing"

func TestCanMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		principal Principal
		action    Action
		siteID    int32
		want      bool
	}{
		{name: "owner deletes org", principal: Principal{Role: RoleOwner, OrgID: 1}, action: ActionOrgDelete, want: true},
		{name: "admin cannot delete org", principal: Principal{Role: RoleAdmin, OrgID: 1}, action: ActionOrgDelete, want: false},
		{name: "owner updates org", principal: Principal{Role: RoleOwner, OrgID: 1}, action: ActionOrgUpdate, want: true},
		{name: "admin updates org", principal: Principal{Role: RoleAdmin, OrgID: 1}, action: ActionOrgUpdate, want: true},
		{name: "editor cannot update org", principal: Principal{Role: RoleEditor, OrgID: 1}, action: ActionOrgUpdate, want: false},
		{name: "admin manages keys", principal: Principal{Role: RoleAdmin, OrgID: 1}, action: ActionKeysManage, want: true},
		{name: "editor cannot manage keys", principal: Principal{Role: RoleEditor, OrgID: 1}, action: ActionKeysManage, want: false},
		{name: "keys manager key manages keys", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"keys:manage"}}, action: ActionKeysManage, want: true},
		{name: "stats key cannot manage keys", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"stats:read"}}, action: ActionKeysManage, want: false},
		{name: "restricted editor edits assigned site", principal: Principal{Role: RoleEditor, OrgID: 1, SiteIDs: []int32{2}}, action: ActionSiteEdit, siteID: 2, want: true},
		{name: "restricted editor cannot edit other site", principal: Principal{Role: RoleEditor, OrgID: 1, SiteIDs: []int32{2}}, action: ActionSiteEdit, siteID: 1, want: false},
		{name: "restricted viewer reads assigned site", principal: Principal{Role: RoleViewer, OrgID: 1, SiteIDs: []int32{1}}, action: ActionStatsRead, siteID: 1, want: true},
		{name: "restricted viewer cannot read other site", principal: Principal{Role: RoleViewer, OrgID: 1, SiteIDs: []int32{1}}, action: ActionStatsRead, siteID: 2, want: false},
		{name: "viewer cannot edit site", principal: Principal{Role: RoleViewer, OrgID: 1, SiteIDs: []int32{1}}, action: ActionSiteEdit, siteID: 1, want: false},
		{name: "stats key reads site", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"stats:read"}}, action: ActionStatsRead, siteID: 9, want: true},
		{name: "stats key cannot edit site", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"stats:read"}}, action: ActionSiteEdit, siteID: 9, want: false},
		{name: "goals key edits goals", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"goals:write"}}, action: ActionGoalsEdit, siteID: 9, want: true},
		{name: "goals key cannot edit site settings", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"goals:write"}}, action: ActionSiteEdit, siteID: 9, want: false},
		{name: "editor edits segments", principal: Principal{Role: RoleEditor, OrgID: 1, SiteIDs: []int32{9}}, action: ActionSegmentsEdit, siteID: 9, want: true},
		{name: "viewer cannot edit segments", principal: Principal{Role: RoleViewer, OrgID: 1, SiteIDs: []int32{9}}, action: ActionSegmentsEdit, siteID: 9, want: false},
		{name: "stats key cannot edit segments", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"stats:read"}}, action: ActionSegmentsEdit, siteID: 9, want: false},
		{name: "segments key edits segments", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"segments:write"}}, action: ActionSegmentsEdit, siteID: 9, want: true},
		{name: "sites key edits segments", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"sites:write"}}, action: ActionSegmentsEdit, siteID: 9, want: true},
		{name: "annotations key edits annotations", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"annotations:write"}}, action: ActionAnnotationsEdit, siteID: 9, want: true},
		{name: "sites key edits annotations", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"sites:write"}}, action: ActionAnnotationsEdit, siteID: 9, want: true},
		{name: "stats key cannot edit annotations", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"stats:read"}}, action: ActionAnnotationsEdit, siteID: 9, want: false},
		{name: "share reads its site", principal: Principal{Role: RoleShare, OrgID: 1, SiteIDs: []int32{5}}, action: ActionStatsRead, siteID: 5, want: true},
		{name: "share cannot read other site", principal: Principal{Role: RoleShare, OrgID: 1, SiteIDs: []int32{5}}, action: ActionStatsRead, siteID: 6, want: false},
		{name: "share without site cannot read", principal: Principal{Role: RoleShare, OrgID: 1}, action: ActionStatsRead, siteID: 5, want: false},
		{name: "share with multiple sites cannot read", principal: Principal{Role: RoleShare, OrgID: 1, SiteIDs: []int32{5, 6}}, action: ActionStatsRead, siteID: 5, want: false},
		{name: "unrestricted editor reads any site", principal: Principal{Role: RoleEditor, OrgID: 1}, action: ActionStatsRead, siteID: 77, want: true},
		{name: "restricted admin creates site", principal: Principal{Role: RoleAdmin, OrgID: 1, SiteIDs: []int32{1}}, action: ActionSiteCreate, siteID: 2, want: true},
		{name: "empty restriction grants no site", principal: Principal{Role: RoleViewer, OrgID: 1, SiteIDs: []int32{}}, action: ActionStatsRead, siteID: 1, want: false},
		{name: "admin manages notification channels", principal: Principal{Role: RoleAdmin, OrgID: 1}, action: ActionChannelsManage, want: true},
		{name: "editor cannot manage notification channels", principal: Principal{Role: RoleEditor, OrgID: 1}, action: ActionChannelsManage, want: false},
		{name: "editor reads sanitized notification channels", principal: Principal{Role: RoleEditor, OrgID: 1}, action: ActionChannelsRead, want: true},
		{name: "viewer cannot read notification channels", principal: Principal{Role: RoleViewer, OrgID: 1}, action: ActionChannelsRead, want: false},
		{name: "editor manages alerts on assigned site", principal: Principal{Role: RoleEditor, OrgID: 1, SiteIDs: []int32{2}}, action: ActionAlertsManage, siteID: 2, want: true},
		{name: "editor cannot manage alerts on other site", principal: Principal{Role: RoleEditor, OrgID: 1, SiteIDs: []int32{2}}, action: ActionAlertsManage, siteID: 1, want: false},
		{name: "viewer cannot manage alerts", principal: Principal{Role: RoleViewer, OrgID: 1, SiteIDs: []int32{2}}, action: ActionAlertsManage, siteID: 2, want: false},
		{name: "API key cannot manage alerts", principal: Principal{Role: RoleKey, OrgID: 1, Scopes: []string{"alerts:write"}}, action: ActionAlertsManage, siteID: 2, want: false},
		{name: "unknown action denied", principal: Principal{Role: RoleOwner, OrgID: 1}, action: Action("unknown"), want: false},
		{name: "zero org denied", principal: Principal{Role: RoleOwner}, action: ActionOrgDelete, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Can(tt.principal, tt.action, tt.siteID); got != tt.want {
				t.Fatalf("Can(%+v, %q, %d) = %v, want %v", tt.principal, tt.action, tt.siteID, got, tt.want)
			}
		})
	}
}
