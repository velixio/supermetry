package writer

import (
	"supermetry/internal/privacy"
	"supermetry/internal/session"
	"supermetry/internal/sites"
)

// SetTrackingPolicy sets the process permission ceiling and cached project
// registry. Production sets this before Run; environment changes require a
// maintenance restart. It does not synchronize an already-running transaction
// with a site edit or other server processes.
func (w *Writer) SetTrackingPolicy(mode privacy.Mode, registry sites.Registry) {
	w.trackingMu.Lock()
	w.trackingMode, w.trackingSites = mode, registry
	w.trackingMu.Unlock()
}

func (w *Writer) identityAllowed(siteID int32) bool {
	w.trackingMu.RLock()
	mode, registry := w.trackingMode, w.trackingSites
	w.trackingMu.RUnlock()
	if mode != privacy.Identified || registry == nil {
		return false
	}
	site, ok := registry.Site(siteID)
	return ok && site.ProfilesEnabled
}

// Recheck on every flush attempt: accepting a hit is not permission to persist
// identity later. Unknown/deleted projects fail closed. Already interned URL
// and campaign dimensions cannot be safely reconstructed here, so omit them.
func (w *Writer) suppressRestrictedIdentity(events []Event) {
	for i := range events {
		e := &events[i]
		w.enforceRevenueCurrency(e)
		identityAllowed := w.identityAllowed(e.SiteID)
		w.validatePathProvenance(e, identityAllowed)
		if identityAllowed {
			continue
		}
		e.ProfileExternalID, e.ProfileID, e.ProfileTraits, e.Props = "", 0, nil, nil
		e.ReferrerID, e.Source, e.UTMSource, e.Medium, e.Campaign = 0, 0, 0, 0, 0
	}
}

// Recheck queued events against the latest local registry snapshot. This
// fails closed during initial configuration, conflicts, deletion, and rollout.
func (w *Writer) enforceRevenueCurrency(event *Event) {
	if event.RevenueMicros == 0 {
		return
	}
	w.trackingMu.RLock()
	registry := w.trackingSites
	w.trackingMu.RUnlock()
	if registry == nil {
		event.RevenueMicros, event.RevenueCurrency = 0, ""
		return
	}
	site, ok := registry.Site(event.SiteID)
	if !ok || site.RevenueCurrencyState != sites.RevenueCurrencyConfigured || event.RevenueCurrency != site.RevenueCurrency {
		event.RevenueMicros, event.RevenueCurrency = 0, ""
	}
}

func (w *Writer) suppressRestrictedSessions(snapshots []session.Snapshot) {
	for i := range snapshots {
		s := &snapshots[i]
		if w.identityAllowed(s.SiteID) {
			continue
		}
		s.Source, s.UTMSource, s.Medium, s.Campaign = 0, 0, 0, 0
	}
}
