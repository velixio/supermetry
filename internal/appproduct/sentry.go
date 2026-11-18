package appproduct

import (
	"context"
	"time"

	"supermetry/internal/sites"
)

// AppOpenEvent is the system event name a Sentry SDK session start becomes.
// The "$" prefix is reserved, so clients cannot send it themselves.
const AppOpenEvent = "$app_open"

// SessionStart is the start of one Sentry SDK session. InstallID is the raw
// device id (did); it lives in memory only and is hashed under the app's
// secret before anything is stored.
type SessionStart struct {
	Started     time.Time
	InstallID   string
	Release     string
	Environment string
	Platform    string
	OSName      string
	OSVersion   string
	Country     string
}

// AcceptSession counts one session start as an "$app_open" product event.
// A start without a device id has no install to attribute and is ignored (the
// caller tallies it as session_no_install).
func (a *Adapter) AcceptSession(ctx context.Context, site sites.Site, s SessionStart) Outcome {
	if s.InstallID == "" {
		return Outcome{Dropped: true}
	}
	return a.accept(ctx, site, Hit{
		SiteID: site.ID, TS: s.Started, InstallID: s.InstallID, Name: AppOpenEvent,
		Release: s.Release, Platform: s.Platform, OSName: s.OSName, OSVersion: s.OSVersion, Country: s.Country,
	}, true)
}
