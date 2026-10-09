package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"supermetry/internal/sites"
)

// Principal describes the sites and operations available to an authenticated
// caller. Access is always scoped to OrgID: a nil SiteIDs slice means every
// site of that organization, never sites of other organizations. Only the
// static administrator key sets AllSites.
type Principal struct {
	OrgID    int64
	AllSites bool
	Scopes   []string
	SiteIDs  []int32
}

// Authenticator resolves an HTTP request to its authenticated principal.
type Authenticator interface {
	Authenticate(r *http.Request) (Principal, bool)
}

type staticKey string

// StaticKey returns a Bearer authenticator for the configured administrator
// key. The administrator has every scope and access to every site.
func StaticKey(key string) Authenticator {
	return staticKey(key)
}

func (key staticKey) Authenticate(r *http.Request) (Principal, bool) {
	scheme, credential, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || scheme != "Bearer" || credential == "" || string(key) == "" {
		return Principal{}, false
	}
	want := sha256.Sum256([]byte(key))
	got := sha256.Sum256([]byte(credential))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return Principal{}, false
	}
	return Principal{AllSites: true, Scopes: []string{"*"}}, true
}

func (p Principal) hasScope(scope string) bool {
	for _, candidate := range p.Scopes {
		if candidate == "*" || candidate == scope {
			return true
		}
	}
	return false
}

func (p Principal) allowsSite(site sites.Site) bool {
	if p.AllSites {
		return true
	}
	if p.OrgID == 0 || site.OrgID != p.OrgID {
		return false
	}
	if p.SiteIDs == nil {
		return true
	}
	for _, allowed := range p.SiteIDs {
		if allowed == site.ID {
			return true
		}
	}
	return false
}

// requestHasScope checks an authenticated request capability. Callers must
// separately authorize the site before using it for site-specific data.
func requestHasScope(r *http.Request, scope string) bool {
	principal, ok := r.Context().Value(principalContextKey{}).(Principal)
	return ok && principal.hasScope(scope)
}

func redactSessionMetadata(r *http.Request, session *SessionSummary) {
	if !requestHasScope(r, "profiles:read") {
		session.ExternalId = nil
	}
	if !requestHasScope(r, "replay:read") {
		session.HasReplay = false
	}
}
