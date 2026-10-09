// Package sites provides the site metadata needed by the analytics core.
package sites

import (
	"strings"
	"time"

	"supermetry/internal/db"
)

// Site is the analytics-core view of a tracked website.
type Site struct {
	ID                    int32
	OrgID                 int64 // owning organization; site checks compare it with the caller's org
	PublicID              string
	TZ                    *time.Location
	Retention             db.RetentionClass
	AllowedHosts          []string
	Exclusions            Exclusions
	Tracker               TrackerSettingsInput
	Replay                ReplaySiteInput
	ProfilesEnabled       bool
	HeatmapsEnabled       bool
	RespectPrivacySignals bool
	IconURL               string
	RevenueCurrency       string
	RevenueCurrencyState  RevenueCurrencyState
}

// Registry provides in-memory site lookups for the analytics core.
type Registry interface {
	ByPublicID(id string) (Site, bool)
	Site(id int32) (Site, bool)
	All() []Site
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
