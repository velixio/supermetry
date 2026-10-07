package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Principal describes the sites and operations available to an authenticated
// caller. A nil SiteIDs slice grants access to all sites.
type Principal struct {
	Scopes  []string
	SiteIDs []int32
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
	return Principal{Scopes: []string{"*"}}, true
}

func (p Principal) hasScope(scope string) bool {
	for _, candidate := range p.Scopes {
		if candidate == "*" || candidate == scope {
			return true
		}
	}
	return false
}

func (p Principal) allowsSite(siteID int32) bool {
	if p.SiteIDs == nil {
		return true
	}
	for _, allowed := range p.SiteIDs {
		if allowed == siteID {
			return true
		}
	}
	return false
}
