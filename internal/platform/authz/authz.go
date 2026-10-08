// Package authz defines application principals and the platform's central
// authorization policy.
package authz

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
	RoleKey    Role = "key"
	RoleShare  Role = "share"
)

type Principal struct {
	UserID  int64
	OrgID   int64
	KeyID   int64
	ShareID int64
	Role    Role
	SiteIDs []int32
	Scopes  []string
}

type Action string

const (
	ActionStatsRead       Action = "stats.read"
	ActionSiteEdit        Action = "site.edit"
	ActionGoalsEdit       Action = "goals.edit"
	ActionSegmentsEdit    Action = "segments.edit"
	ActionAnnotationsEdit Action = "annotations.edit"
	ActionSiteCreate      Action = "site.create"
	ActionSiteDelete      Action = "site.delete"
	ActionMembersManage   Action = "members.manage"
	ActionKeysManage      Action = "keys.manage"
	ActionSharesManage    Action = "shares.manage"
	ActionReportsManage   Action = "reports.manage"
	ActionChannelsRead    Action = "channels.read"
	ActionChannelsManage  Action = "channels.manage"
	ActionAlertsManage    Action = "alerts.manage"
	ActionAuditRead       Action = "audit.read"
	ActionOrgDelete       Action = "org.delete"
	ActionOrgTransfer     Action = "org.transfer"
)

var roleActions = map[Role]map[Action]bool{
	RoleOwner: {
		ActionStatsRead: true, ActionSiteEdit: true, ActionGoalsEdit: true, ActionSegmentsEdit: true, ActionAnnotationsEdit: true, ActionSiteCreate: true,
		ActionSiteDelete: true, ActionMembersManage: true, ActionKeysManage: true,
		ActionSharesManage: true, ActionReportsManage: true, ActionAuditRead: true,
		ActionChannelsRead: true, ActionChannelsManage: true, ActionAlertsManage: true,
		ActionOrgDelete: true, ActionOrgTransfer: true,
	},
	RoleAdmin: {
		ActionStatsRead: true, ActionSiteEdit: true, ActionGoalsEdit: true, ActionSegmentsEdit: true, ActionAnnotationsEdit: true, ActionSiteCreate: true,
		ActionSiteDelete: true, ActionMembersManage: true, ActionKeysManage: true,
		ActionSharesManage: true, ActionReportsManage: true, ActionAuditRead: true,
		ActionChannelsRead: true, ActionChannelsManage: true, ActionAlertsManage: true,
	},
	RoleEditor: {
		ActionStatsRead: true, ActionSiteEdit: true, ActionGoalsEdit: true, ActionSegmentsEdit: true, ActionAnnotationsEdit: true,
		ActionSharesManage: true, ActionReportsManage: true, ActionChannelsRead: true, ActionAlertsManage: true,
	},
	RoleViewer: {
		ActionStatsRead: true, ActionReportsManage: true,
	},
}

var keyScopes = map[Action][]string{
	ActionStatsRead:       {"stats:read"},
	ActionSiteEdit:        {"sites:write"},
	ActionGoalsEdit:       {"goals:write"},
	ActionSegmentsEdit:    {"segments:write", "sites:write"},
	ActionAnnotationsEdit: {"annotations:write", "sites:write"},
	ActionSiteCreate:      {"sites:write"},
	ActionSiteDelete:      {"sites:write"},
	ActionKeysManage:      {"keys:manage"},
}

// Can reports whether p may perform action for siteID. A zero siteID denotes
// an organization-level action. A nil SiteIDs slice grants access to every
// site in the organization; an empty non-nil slice grants none.
func Can(p Principal, action Action, siteID int32) bool {
	if p.OrgID == 0 {
		return false
	}

	switch p.Role {
	case RoleKey:
		scopes, ok := keyScopes[action]
		if !ok || !hasAnyScope(p.Scopes, scopes) {
			return false
		}
	case RoleShare:
		if action != ActionStatsRead {
			return false
		}
		return siteID != 0 && len(p.SiteIDs) == 1 && p.SiteIDs[0] == siteID
	default:
		if !roleActions[p.Role][action] {
			return false
		}
	}

	if action == ActionSiteCreate || action == ActionSiteDelete {
		return true
	}
	if !isSiteAction(action) {
		return siteID == 0
	}
	if siteID == 0 {
		return false
	}
	if p.Role == RoleOwner || p.Role == RoleAdmin {
		return true
	}
	return allowsSite(p.SiteIDs, siteID)
}

func isSiteAction(action Action) bool {
	switch action {
	case ActionStatsRead, ActionSiteEdit, ActionGoalsEdit, ActionSegmentsEdit, ActionAnnotationsEdit, ActionSharesManage, ActionReportsManage, ActionAlertsManage:
		return true
	default:
		return false
	}
}

func allowsSite(siteIDs []int32, siteID int32) bool {
	if siteIDs == nil {
		return true
	}
	for _, allowed := range siteIDs {
		if allowed == siteID {
			return true
		}
	}
	return false
}

func hasScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func hasAnyScope(scopes, wanted []string) bool {
	for _, want := range wanted {
		if hasScope(scopes, want) {
			return true
		}
	}
	return false
}
