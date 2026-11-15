package ingest

import (
	"net/http"
	"net/netip"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/dropstats"
	"supermetry/internal/enrich"
	"supermetry/internal/health"
	"supermetry/internal/sites"
)

// linkedApp returns the app project that should receive a diagnostic report:
// the event must be a diagnostic report, the site linked to an existing app,
// and identity allowed (an anonymous instance or a profiles-off site keeps
// today's behavior).
func (h *Handler) linkedApp(site sites.Site, payload Payload, identityAllowed bool) (sites.Site, bool) {
	if h.AppIntake == nil || !identityAllowed || site.LinkedAppID == 0 || payload.Name != appingest.DiagnosticEvent {
		return sites.Site{}, false
	}
	app, ok := h.Sites.Site(site.LinkedAppID)
	if !ok || app.Kind != sites.KindApp || app.App == nil {
		return sites.Site{}, false
	}
	return app, true
}

// acceptDiagnostic maps a diagnostic report onto the linked app and queues it.
// It runs after the allowed-hosts, strict-origin, dedup, exclusion, rate-limit
// and bot checks, so the legacy trust rules still apply. The reply is always
// 202: drops are counted on the app project.
func (h *Handler) acceptDiagnostic(w http.ResponseWriter, r *http.Request, payload Payload, profileID string, site, app sites.Site, ip netip.Addr, receivedAt, eventTime time.Time) {
	if reason := h.Enricher.BotWithBlockedReferrers(r.UserAgent(), ip, payload.Referrer, site.BlockedReferrers); reason != enrich.BotNone {
		Bots[reason].Add(1)
		health.BotsFiltered[reason].Add(1)
		h.recordDrop(site, dropReasonForBot(reason), h.Enricher.BotRule(r.UserAgent(), payload.Referrer, reason), receivedAt)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	input := appingest.DiagnosticInput{ID: payload.ID, ProfileID: profileID, Props: payload.Props}
	if payload.TimestampMS != nil {
		ms := eventTime.UnixMilli() // already clamped when it was in the future
		input.TimestampMS = &ms
	}
	event, err := appingest.MapDiagnostic(input)
	if err != nil {
		h.AppIntake.Drop(app, receivedAt, dropstats.AppInvalid)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	outcome := h.AppIntake.Accept(app, "link:"+site.PublicID, receivedAt, event)
	if outcome.Reason == "" {
		if h.Dedup != nil {
			h.Dedup.Record(site.ID, payload.ID, receivedAt)
		}
		health.IngestAccepted.Add(1)
	}
	w.WriteHeader(http.StatusAccepted)
}
