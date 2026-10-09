package platformhttp

import (
	"net/http"
	"strconv"
	"strings"

	platformauth "panel/internal/platform/auth"
	"panel/internal/platform/authz"
	"panel/internal/platform/keys"
	"panel/internal/platform/orgs"
	"panel/internal/platform/shares"
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
		if cookie, err := r.Cookie("panel_share"); err == nil {
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
	session, ok := a.auth.Session(r.Context(), cookie.Value)
	if !ok {
		return authz.Principal{}, false
	}
	memberships, err := a.orgs.MembershipsFor(r.Context(), session.UserID)
	if err != nil {
		return authz.Principal{}, false
	}
	membership, ok := selectMembership(memberships, r.Header.Get("X-Panel-Org"))
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
