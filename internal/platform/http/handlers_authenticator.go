package platformhttp

import (
	"net/http"
	"strconv"
	"strings"

	platformauth "supermetry/internal/platform/auth"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/keys"
	"supermetry/internal/platform/orgs"
	"supermetry/internal/platform/shares"
)

// ServiceAuthenticator resolves the three credential families without
// discarding actor IDs or roles. The analytics authenticator intentionally
// exposes a smaller principal, while platform writes need the complete value.
type ServiceAuthenticator struct {
	auth   *platformauth.Auth
	keys   *keys.Keys
	shares *shares.Shares
	orgs   *orgs.Orgs
}

func NewAuthenticator(a *platformauth.Auth, k *keys.Keys, s *shares.Shares, o *orgs.Orgs) *ServiceAuthenticator {
	return &ServiceAuthenticator{auth: a, keys: k, shares: s, orgs: o}
}

func (a *ServiceAuthenticator) Authenticate(r *http.Request) (authz.Principal, bool) {
	if slug, ok := pathSegment(r.URL.Path, "/api/private/share/"); ok {
		if a == nil || a.shares == nil {
			return authz.Principal{}, false
		}
		cookieValue := ""
		if cookie, err := r.Cookie("supermetry_share"); err == nil {
			cookieValue = cookie.Value
		}
		return a.shares.Principal(r.Context(), slug, cookieValue)
	}
	if strings.HasPrefix(r.URL.Path, "/api/public/v1/") {
		if a == nil || a.keys == nil {
			return authz.Principal{}, false
		}
		scheme, credential, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || credential == "" {
			return authz.Principal{}, false
		}
		return a.keys.Verify(r.Context(), credential)
	}
	if !strings.HasPrefix(r.URL.Path, "/api/private/") || a == nil || a.auth == nil || a.orgs == nil {
		return authz.Principal{}, false
	}
	cookie, err := r.Cookie(platformauth.CookieName)
	if err != nil || cookie.Value == "" {
		return authz.Principal{}, false
	}
	session, ok := a.auth.SessionForRequest(r, cookie.Value)
	if !ok {
		return authz.Principal{}, false
	}
	// Account operations authenticate the person, not a selected tenant. Never
	// attach organization grants to this principal, even with a stale header.
	if accountRequest(r) {
		return authz.Principal{UserID: session.UserID, SiteIDs: []int32{}}, true
	}
	memberships, err := a.orgs.MembershipsFor(r.Context(), session.UserID)
	if err != nil {
		return authz.Principal{}, false
	}
	membership, ok := selectMembership(memberships, r.Header.Get("X-Supermetry-Org"))
	if !ok {
		return authz.Principal{}, false
	}
	return authz.Principal{
		UserID: session.UserID, OrgID: membership.OrgID, Role: authz.Role(membership.Role),
		SiteIDs:      copySiteIDs(membership.SiteIDs),
		Capabilities: append([]string{}, membership.Capabilities...), BillingAdmin: membership.BillingAdmin,
	}, true
}

func selectMembership(memberships []orgs.Membership, requested string) (orgs.Membership, bool) {
	if requested == "" {
		if len(memberships) != 1 {
			return orgs.Membership{}, false
		}
		return memberships[0], true
	}
	orgID, err := strconv.ParseInt(requested, 10, 64)
	if err != nil || orgID <= 0 {
		return orgs.Membership{}, false
	}
	for _, membership := range memberships {
		if membership.OrgID == orgID {
			return membership, true
		}
	}
	return orgs.Membership{}, false
}

func pathSegment(path, prefix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	segment, _, _ := strings.Cut(strings.TrimPrefix(path, prefix), "/")
	return segment, segment != ""
}

var _ Authenticator = (*ServiceAuthenticator)(nil)

// copySiteIDs keeps nil (all sites) distinct from empty (no sites).
func copySiteIDs(values []int32) []int32 {
	if values == nil {
		return nil
	}
	out := make([]int32, len(values))
	copy(out, values)
	return out
}

// accountRequest lists only routes whose authorization is scoped to the current
// user. Organization resources must still resolve an explicit membership.
func accountRequest(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /api/private/me", "HEAD /api/private/me", "PATCH /api/private/me", "DELETE /api/private/me",
		"GET /api/private/sites/overview", "HEAD /api/private/sites/overview",
		"GET /api/private/me/orgs/deleted",
		"GET /api/private/me/ip", "HEAD /api/private/me/ip", "GET /api/private/me/sessions", "HEAD /api/private/me/sessions", "DELETE /api/private/me/sessions",
		"POST /api/private/me/email", "POST /api/private/me/password", "POST /api/private/me/totp", "PATCH /api/private/me/totp", "DELETE /api/private/me/totp", "POST /api/private/me/totp/recovery-codes",
		"GET /api/private/orgs", "HEAD /api/private/orgs", "POST /api/private/orgs", "POST /api/private/auth/logout",
		"GET /api/private/auth/email-change/confirm", "HEAD /api/private/auth/email-change/confirm", "POST /api/private/auth/email-change/confirm":
		return true
	}
	if r.Method == http.MethodDelete {
		value, ok := strings.CutPrefix(r.URL.Path, "/api/private/me/sessions/")
		return ok && value != "" && !strings.Contains(value, "/")
	}
	if r.Method == http.MethodPost {
		value, ok := strings.CutPrefix(r.URL.Path, "/api/private/me/orgs/")
		org, suffix, found := strings.Cut(value, "/")
		return ok && found && org != "" && suffix == "restore"
	}
	return false
}
