// Package sites provides the site metadata needed by the analytics core.
package sites

import (
	"crypto/subtle"
	"strings"
	"time"

	"supermetry/internal/db"
)

const (
	KindWeb   = "web"
	KindPixel = "pixel"
	KindApp   = "app"
)

// AppKey is an ingest key hash; a zero ExpiresAt means no expiry.
type AppKey struct {
	Hash      [32]byte
	ExpiresAt time.Time
}

// DSNKey is a Sentry-style DSN public key of an app project. It is public by
// design (it ships in client bundles), so it is kept in plain text.
type DSNKey struct {
	PublicKey      string
	AllowedOrigins []string
	ExpiresAt      *time.Time
	RatePerMin     int // 0 means the site default
}

// Expired reports whether the key has expired at now.
func (k DSNKey) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)
}

// AppConfig is the ingest configuration of an app project.
type AppConfig struct {
	Keys             []AppKey
	DSNKeys          []DSNKey // unrevoked DSN keys; expiry is checked at lookup
	TagAllowlist     []string
	MaxReportAgeDays int
	Environments     []string
	FilterExtensions *bool  // nil means on
	FilterCrawlers   *bool  // nil means on
	FilterLocalhost  *bool  // nil means the server-wide default
	HashSecret       []byte // per-app HMAC secret for install and session ids; nil until the first event creates it
}

// KeyValid reports whether hash matches a key that has not expired at now.
// Every key is compared in constant time.
func (c *AppConfig) KeyValid(hash [32]byte, now time.Time) bool {
	if c == nil {
		return false
	}
	valid := 0
	for _, key := range c.Keys {
		match := subtle.ConstantTimeCompare(hash[:], key.Hash[:])
		if !key.ExpiresAt.IsZero() && !now.Before(key.ExpiresAt) {
			match = 0
		}
		valid |= match
	}
	return valid == 1
}

// Site is the analytics-core view of a tracked website.
type Site struct {
	ID                    int32
	OrgID                 int64 // owning organization; site checks compare it with the caller's org
	PublicID              string
	Kind                  string // KindWeb, KindPixel or KindApp; fixed at creation
	Slug                  string // readable per-organization URL segment; PublicID also resolves
	TZ                    *time.Location
	Retention             db.RetentionClass
	AllowedHosts          []string
	Exclusions            Exclusions
	Tracker               TrackerSettingsInput
	Replay                ReplaySiteInput
	App                   *AppConfig // nil unless Kind is KindApp
	LinkedAppID           int32      // web sites only: the app project that receives this site's diagnostic reports; 0 when unlinked
	ProfilesEnabled       bool
	HeatmapsEnabled       bool
	RespectPrivacySignals bool
	StrictOrigin          bool
	BlockedReferrers      []string
	IconURL               string
	RevenueCurrency       string
	RevenueCurrencyState  RevenueCurrencyState
	Demo                  bool
}

// Registry provides in-memory site lookups for the analytics core.
type Registry interface {
	ByPublicID(id string) (Site, bool)
	Site(id int32) (Site, bool)
	All() []Site
}

// DSNRegistry is a Registry that can also resolve DSN public keys. Registries
// that never hold DSN keys (file mode, test fakes) need not implement it.
type DSNRegistry interface {
	Registry
	// ByDSNKey returns the app site and key for a public key. Revoked keys are
	// never loaded; the caller checks DSNKey.Expired.
	ByDSNKey(publicKey string) (Site, DSNKey, bool)
}

// Allows reports whether host exactly matches an allowed host or matches a
// wildcard subdomain entry. Hosts are compared case-insensitively and must not
// include a port.
func (s Site) Allows(host string) bool {
	host = strings.ToLower(host)
	if strings.Contains(host, ":") {
		return false
	}
	for _, allowed := range s.AllowedHosts {
		if strings.HasPrefix(allowed, "*.") {
			if strings.HasSuffix(host, allowed[1:]) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

// Filters resolves the inbound filter switches; localhostDefault is the
// server-wide setting used when the project has not chosen one. A nil config
// gets the defaults.
func (c *AppConfig) Filters(localhostDefault bool) (extensions, crawlers, localhost bool) {
	extensions, crawlers, localhost = true, true, localhostDefault
	if c == nil {
		return
	}
	if c.FilterExtensions != nil {
		extensions = *c.FilterExtensions
	}
	if c.FilterCrawlers != nil {
		crawlers = *c.FilterCrawlers
	}
	if c.FilterLocalhost != nil {
		localhost = *c.FilterLocalhost
	}
	return
}
