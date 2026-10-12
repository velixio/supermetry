// Package authz defines application principals and the platform's central
// authorization policy.
package authz

import "slices"

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
	UserID       int64
	OrgID        int64
	KeyID        int64
	ShareID      int64
	Role         Role
	SiteIDs      []int32
	Scopes       []string
	Capabilities []string
	BillingAdmin bool
}

type Action string

const (
	ActionProfilesRead    Action = "profiles.read"
	ActionReplayRead      Action = "replay.read"
	ActionDataExport      Action = "data.export"
	ActionBillingManage   Action = "billing.manage"
	ActionStatsRead       Action = "stats.read"
	ActionSiteEdit        Action = "site.edit"
	ActionGoalsEdit       Action = "goals.edit"
	ActionSegmentsEdit    Action = "segments.edit"
	ActionAnnotationsEdit Action = "annotations.edit"
	ActionLinksManage     Action = "links.manage"
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
	ActionOrgUpdate       Action = "org.update"
	ActionOrgDelete       Action = "org.delete"
	ActionOrgTransfer     Action = "org.transfer"
)

var roleActions = map[Role]map[Action]bool{
	RoleOwner: {
		ActionProfilesRead: true, ActionReplayRead: true, ActionDataExport: true, ActionBillingManage: true,
		ActionStatsRead: true, ActionSiteEdit: true, ActionGoalsEdit: true, ActionSegmentsEdit: true, ActionAnnotationsEdit: true, ActionLinksManage: true, ActionSiteCreate: true,
		ActionSiteDelete: true, ActionMembersManage: true, ActionKeysManage: true,
		ActionSharesManage: true, ActionReportsManage: true, ActionAuditRead: true,
		ActionChannelsRead: true, ActionChannelsManage: true, ActionAlertsManage: true,
		ActionOrgUpdate: true, ActionOrgDelete: true, ActionOrgTransfer: true,
	},
	RoleAdmin: {
		ActionProfilesRead: true, ActionReplayRead: true, ActionDataExport: true,
		ActionStatsRead: true, ActionSiteEdit: true, ActionGoalsEdit: true, ActionSegmentsEdit: true, ActionAnnotationsEdit: true, ActionLinksManage: true, ActionSiteCreate: true,
		ActionSiteDelete: true, ActionMembersManage: true, ActionKeysManage: true,
		ActionSharesManage: true, ActionReportsManage: true, ActionAuditRead: true,
		ActionChannelsRead: true, ActionChannelsManage: true, ActionAlertsManage: true,
		ActionOrgUpdate: true,
	},
	RoleEditor: {
		ActionStatsRead: true, ActionSiteEdit: true, ActionGoalsEdit: true, ActionSegmentsEdit: true, ActionAnnotationsEdit: true, ActionLinksManage: true,
		ActionSharesManage: true, ActionReportsManage: true, ActionChannelsRead: true, ActionAlertsManage: true,
	},
	RoleViewer: {
		ActionStatsRead: true, ActionReportsManage: true,
	},
}

var keyScopes = map[Action][]string{
	ActionProfilesRead:    {"profiles:read"},
	ActionReplayRead:      {"replay:read"},
	ActionDataExport:      {"data:export"},
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
	if p.OrgID <= 0 || siteID < 0 {
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
		if !roleAllows(p, action) {
			return false
		}
	}

	if action == ActionSiteCreate {
		// A restricted key must not create a new site outside its grant.
		return p.Role != RoleKey || p.SiteIDs == nil
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
	case ActionProfilesRead, ActionReplayRead, ActionDataExport, ActionSiteDelete, ActionStatsRead, ActionSiteEdit, ActionGoalsEdit, ActionSegmentsEdit, ActionAnnotationsEdit, ActionLinksManage, ActionSharesManage, ActionReportsManage, ActionAlertsManage:
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

// ValidCapabilities accepts only additive sensitive project grants. Role and
// billing administration cannot be smuggled into this list.
func ValidCapabilities(capabilities []string) bool {
	for _, capability := range capabilities {
		switch Action(capability) {
		case ActionProfilesRead, ActionReplayRead, ActionDataExport:
		default:
			return false
		}
	}
	return true
}

func roleAllows(p Principal, action Action) bool {
	if _, known := roleActions[p.Role]; !known {
		return false
	}
	if roleActions[p.Role][action] {
		return true
	}
	if action == ActionBillingManage {
		return p.BillingAdmin
	}
	switch action {
	case ActionProfilesRead, ActionReplayRead, ActionDataExport:
		return slices.Contains(p.Capabilities, string(action))
	default:
		return false
	}
}

// EffectiveCapabilities is a presentation of the same policy used by Can.
// Site-specific checks and tenant ownership are still required at the resource.
func EffectiveCapabilities(p Principal) []string {
	result := make([]string, 0)
	actions := make(map[Action]bool)
	for _, grants := range roleActions {
		for action := range grants {
			actions[action] = true
		}
	}
	for action := range actions {
		siteID := int32(0)
		if isSiteAction(action) {
			if p.SiteIDs != nil && len(p.SiteIDs) == 0 && p.Role != RoleOwner && p.Role != RoleAdmin {
				continue
			}
			siteID = 1
			if len(p.SiteIDs) > 0 {
				siteID = p.SiteIDs[0]
			}
		}
		if Can(p, action, siteID) {
			result = append(result, string(action))
		}
	}
	slices.Sort(result)
	return result
}

// APIScopes projects the centralized capability policy into the analytics API.
// It deliberately never returns a wildcard, including for session admins.
func APIScopes(p Principal) []string {
	result := make([]string, 0)
	for action, scopes := range keyScopes {
		siteID := int32(0)
		if isSiteAction(action) {
			if p.SiteIDs != nil && len(p.SiteIDs) == 0 && p.Role != RoleOwner && p.Role != RoleAdmin {
				continue
			}
			siteID = 1
			if len(p.SiteIDs) > 0 {
				siteID = p.SiteIDs[0]
			}
		}
		if !Can(p, action, siteID) {
			continue
		}
		// Aliases such as sites:write for segments.edit must not broaden a grant.
		scope := scopes[0]
		if p.Role == RoleKey && !slices.Contains(p.Scopes, scope) {
			continue
		}
		if !slices.Contains(result, scope) {
			result = append(result, scope)
		}
	}
	slices.Sort(result)
	return result
}

// ValidScopes rejects unknown and wildcard persisted API-key scopes.
func ValidScopes(scopes []string) bool {
	for _, scope := range scopes {
		known := false
		for _, allowed := range keyScopes {
			if slices.Contains(allowed, scope) {
				known = true
				break
			}
		}
		if !known {
			return false
		}
	}
	return true
}
