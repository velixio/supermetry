// Package authn resolves platform credentials into analytics API principals.
package authn

import (
	"net/http"
	"strconv"
	"strings"

	"panel/internal/api"
	"panel/internal/platform/auth"
	"panel/internal/platform/authz"
	"panel/internal/platform/keys"
	"panel/internal/platform/orgs"
	"panel/internal/platform/shares"
)

const (
	orgHeader       = "X-Panel-Org"
	ShareCookieName = "panel_share"
)

type Authenticator struct {
	auth   *auth.Auth
	keys   *keys.Keys
	shares *shares.Shares
	orgs   *orgs.Orgs
}

func New(a *auth.Auth, k *keys.Keys, s *shares.Shares, o *orgs.Orgs) *Authenticator {
	return &Authenticator{auth: a, keys: k, shares: s, orgs: o}
}

func (a *Authenticator) Authenticate(r *http.Request) (api.Principal, bool) {
	if slug, ok := shareSlug(r.URL.Path); ok {
		return a.authenticateShare(r, slug)
	}
	if pathWithin(r.URL.Path, "/api/public/v1") {
		return a.authenticateKey(r)
	}
	if pathWithin(r.URL.Path, "/api/private") {
		return a.authenticateSession(r)
	}
	return api.Principal{}, false
}

func (a *Authenticator) authenticateKey(r *http.Request) (api.Principal, bool) {
	if a == nil || a.keys == nil {
		return api.Principal{}, false
	}
	scheme, credential, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || credential == "" {
		return api.Principal{}, false
	}
	principal, ok := a.keys.Verify(r.Context(), credential)
	if !ok {
		return api.Principal{}, false
	}
	return apiPrincipal(principal, principal.Scopes), true
}

func (a *Authenticator) authenticateSession(r *http.Request) (api.Principal, bool) {
	if a == nil || a.auth == nil || a.orgs == nil {
		return api.Principal{}, false
	}
	cookie, err := r.Cookie(auth.CookieName)
	if err != nil || cookie.Value == "" {
		return api.Principal{}, false
	}
	session, ok := a.auth.Session(r.Context(), cookie.Value)
	if !ok {
		return api.Principal{}, false
	}
	memberships, err := a.orgs.MembershipsFor(r.Context(), session.UserID)
	if err != nil {
		return api.Principal{}, false
	}
	membership, ok := selectMembership(memberships, r.Header.Get(orgHeader))
	if !ok {
		return api.Principal{}, false
	}
	return api.Principal{
		OrgID:   membership.OrgID,
		Scopes:  membershipScopes(membership.Role),
		SiteIDs: cloneSites(membership.SiteIDs),
	}, true
}

func (a *Authenticator) authenticateShare(r *http.Request, slug string) (api.Principal, bool) {
	if a == nil || a.shares == nil {
		return api.Principal{}, false
	}
	cookieValue := ""
	if cookie, err := r.Cookie(ShareCookieName); err == nil {
		cookieValue = cookie.Value
	}
	principal, ok := a.shares.Principal(r.Context(), slug, cookieValue)
	if !ok {
		return api.Principal{}, false
	}
	return apiPrincipal(principal, []string{"stats:read"}), true
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

func apiPrincipal(principal authz.Principal, scopes []string) api.Principal {
	return api.Principal{
		OrgID:   principal.OrgID,
		Scopes:  cloneStrings(scopes),
		SiteIDs: cloneSites(principal.SiteIDs),
	}
}

func membershipScopes(role orgs.Role) []string {
	switch role {
	case orgs.RoleOwner, orgs.RoleAdmin:
		return []string{"*"}
	case orgs.RoleEditor, orgs.RoleViewer:
		return []string{"stats:read"}
	default:
		return nil
	}
}

func shareSlug(path string) (string, bool) {
	const prefix = "/api/private/share/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	slug, _, _ := strings.Cut(strings.TrimPrefix(path, prefix), "/")
	return slug, slug != ""
}

func pathWithin(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneSites(values []int32) []int32 {
	if values == nil {
		return nil
	}
	return append([]int32(nil), values...)
}

var _ api.Authenticator = (*Authenticator)(nil)
