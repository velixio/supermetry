package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supermetry/internal/clientip"
	"supermetry/internal/dict"
	"supermetry/internal/dropstats"
	"supermetry/internal/enrich"
	"supermetry/internal/health"
	"supermetry/internal/privacy"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/writer"
)

const (
	previousSaltWindow             = 30 * time.Minute
	defaultDimensionCardinalityCap = 10_000
	defaultCompactCardinalityCap   = 200
)

// Bots counts filtered requests by enrich.BotReason. Index zero is unused.
// Invalid counts rejected payloads without retaining request details.
var (
	Bots    [5]atomic.Int64
	Invalid atomic.Int64
	// DimensionOverflow counts dimension values folded into Other because a
	// site's local-day cardinality bound was reached.
	DimensionOverflow health.Counter
)

// Clock supplies the server timestamp used for accepted events.
type Clock interface {
	Now() time.Time
}

// Handler implements the event intake endpoint. Its dependencies are caches
// or in-memory stores on the normal path; the bounded Writer applies the final
// nonblocking backpressure decision.
type Handler struct {
	TrackingMode   privacy.Mode
	PathPolicy     privacy.PathPolicy
	Sites          sites.Registry
	Enricher       *enrich.Enricher
	Salts          *salts.Store
	Dict           *dict.Dict
	Sessions       session.Store
	Writer         *writer.Writer
	Limiter        RateLimiter
	TrustedProxies []netip.Prefix
	Clock          Clock
	Dedup          *Deduplicator
	DropStats      *dropstats.Recorder
	Diag           bool // log how each client address was resolved (SUPERMETRY_INGEST_DIAG=1)
	// DimensionCardinalityLimit configures the per-site, per-local-day limit
	// shared by every dictionary kind. Zero uses 10,000 for previously
	// unbounded kinds and retains 200 for version, language and hostname.
	DimensionCardinalityLimit int
	capsOnce                  sync.Once
	caps                      *valueCap
	compactCaps               *valueCap
	ProfileTombstones         interface {
		Contains(siteID int32, externalID string) bool
	}
}

// ServeHTTP validates, enriches and queues one analytics event.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.invalid(w)
		return
	}
	payload, err := ParsePayload(body)
	if errors.Is(err, errInvalidHeatmap) {
		health.HeatmapInvalid.Add(1)
	}
	if err != nil || payload.Site == "" || payload.Name == "" || payload.URL == "" {
		h.invalid(w)
		return
	}
	if h.Sites == nil || h.Enricher == nil || h.Salts == nil || h.Dict == nil || h.Sessions == nil || h.Writer == nil {
		h.unavailable(w)
		return
	}

	site, ok := h.Sites.ByPublicID(payload.Site)
	if !ok || site.TZ == nil || site.Demo {
		h.invalid(w)
		return
	}
	if payload.RevenueMicros > 0 {
		switch site.RevenueCurrencyState {
		case sites.RevenueCurrencyConfigured:
			if payload.RevenueCurrency == "" {
				payload.RevenueCurrency = site.RevenueCurrency
			} else if payload.RevenueCurrency != site.RevenueCurrency {
				payload.RevenueMicros, payload.RevenueCurrency, payload.RevenueInvalid = 0, "", true
			}
		default:
			payload.RevenueMicros, payload.RevenueCurrency, payload.RevenueInvalid = 0, "", true
		}
	}
	if site.RespectPrivacySignals && (r.Header.Get("Sec-GPC") == "1" || r.Header.Get("DNT") == "1") {
		health.PrivacySignalsDropped.Add(1)
		h.recordDrop(site, dropstats.PrivacySignal, "", time.Time{})
		w.WriteHeader(http.StatusAccepted)
		return
	}
	identityAllowed := h.TrackingMode == privacy.Identified && site.ProfilesEnabled
	profileID, profileTraits, profileSupplied, err := payload.Identity(identityAllowed)
	if err != nil {
		h.invalid(w)
		return
	}
	if profileID != "" && h.ProfileTombstones != nil && h.ProfileTombstones.Contains(site.ID, profileID) {
		profileID = ""
		profileTraits = nil
	}
	if !identityAllowed && payload.Name == identifyEvent {
		// The tracker sends identify only for opted-in use; on other sites
		// it would just add one anonymous custom event per page load.
		health.IngestProfilesSuppressed.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	pageURL, err := parsePageURL(payload.URL)
	if err != nil {
		h.invalid(w)
		return
	}
	if !site.Allows(strings.ToLower(pageURL.Hostname())) {
		h.recordDrop(site, dropstats.HostNotAllowed, "", time.Time{})
		h.invalid(w)
		return
	}
	if site.StrictOrigin && !site.Allows(requestOriginHost(r)) {
		health.IngestOriginRejected.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	receivedAt := time.Now()
	if h.Clock != nil {
		receivedAt = h.Clock.Now()
	}
	if payload.ID != "" && h.Dedup != nil && h.Dedup.Seen(site.ID, payload.ID, receivedAt) {
		health.IngestDuplicates.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if payload.ID != "" && h.Dedup != nil {
		defer h.Dedup.Release(site.ID, payload.ID)
	}
	eventTime := receivedAt
	if payload.TimestampMS != nil {
		eventTime = time.UnixMilli(*payload.TimestampMS)
		if eventTime.Before(receivedAt.Add(-24 * time.Hour)) {
			health.IngestLateDropped.Add(1)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if eventTime.After(receivedAt.Add(5 * time.Minute)) {
			eventTime = receivedAt
			health.IngestClockSkew.Add(1)
			h.recordDrop(site, dropstats.ClockSkew, "", receivedAt)
		}
	}
	originalURL := payload.URL
	if !identityAllowed {
		// Arbitrary property values and URL query/fragment values can contain
		// identity even when no profile_id was supplied. Path policy is applied
		// below, after exclusions have inspected the original request path.
		payload.Props = nil
		payload.URL = privacy.AnonymousURL(payload.URL)
		payload.Referrer = privacy.AnonymousURL(payload.Referrer)
	}
	if payload.Name == "heatmap" && !site.HeatmapsEnabled {
		health.HeatmapDropped.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	ip, ok := h.clientIP(r)
	if !ok {
		h.invalid(w)
		return
	}
	if site.Exclusions.ExcludesIP(ip) {
		health.ExclusionsFiltered[health.ExclusionIP].Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	path := pageURL.Path
	if path == "" {
		path = "/"
	}
	if site.Exclusions.ExcludesPath(path) {
		health.ExclusionsFiltered[health.ExclusionPath].Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	// An identified instance keeps real paths even for a site with profiles off:
	// the operator chose identified mode, and the query string and fragment are
	// still dropped. Only an anonymous instance substitutes paths.
	sanitizePaths := !identityAllowed && h.TrackingMode != privacy.Identified
	var pathValue privacy.PathValue
	if !identityAllowed {
		if sanitizePaths {
			pathValue = h.PathPolicy.SanitizePath(originalURL)
			// Only the server output reaches path interning, sessions and heatmaps.
			// Keep the original host only for enrichment; it was validated above.
			pageURL.Path, pageURL.RawPath = pathValue.Path, ""
		}
		pageURL.RawQuery, pageURL.Fragment, pageURL.RawFragment = "", "", ""
	}
	if len(site.Exclusions.Countries) != 0 {
		country := h.Enricher.Country(ip)
		if country != "" && site.Exclusions.ExcludesCountry(country) {
			health.ExclusionsFiltered[health.ExclusionCountry].Add(1)
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	if reason := ingestRateLimit(h.Limiter, ipRateKey(ip), "site:"+strconv.FormatInt(int64(site.ID), 10)); reason != RateLimitNone {
		recordIngestRateLimit(reason)
		h.recordDrop(site, dropReasonForRateLimit(reason), "", receivedAt)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if payload.Name == "heatmap" {
		if reason := h.Enricher.BotWithBlockedReferrers(r.UserAgent(), ip, payload.Referrer, site.BlockedReferrers); reason != enrich.BotNone {
			Bots[reason].Add(1)
			health.BotsFiltered[reason].Add(1)
			h.recordDrop(site, dropReasonForBot(reason), h.Enricher.BotRule(r.UserAgent(), payload.Referrer, reason), receivedAt)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if h.acceptHeatmap(w, r.Context(), payload, pageURL, site, eventTime) && h.Dedup != nil {
			h.Dedup.Record(site.ID, payload.ID, receivedAt)
		}
		return
	}

	// Preserve privacy, validation, exclusions, rate limits and independent
	// heatmaps above. Reserve capacity before enrichment or session/dictionary
	// mutation so a later producer cannot turn a touched session into a 503.
	reservation, ok := h.Writer.ReserveEvent()
	if !ok {
		h.unavailable(w)
		return
	}
	defer reservation.Cancel()

	location := h.Enricher.City(ip)
	if h.Diag {
		h.logDiag(r, ip, location)
	}

	ua := r.UserAgent()
	if reason := h.Enricher.BotWithBlockedReferrers(ua, ip, payload.Referrer, site.BlockedReferrers); reason != enrich.BotNone {
		Bots[reason].Add(1)
		health.BotsFiltered[reason].Add(1)
		h.recordDrop(site, dropReasonForBot(reason), h.Enricher.BotRule(ua, payload.Referrer, reason), receivedAt)
		if h.Diag {
			h.logBotDrop(reason, ip, location)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	localDay := salts.LocalDay(eventTime, site.TZ)
	salt, ok := h.Salts.For(localDay)
	if !ok {
		if localDay.Before(salts.LocalDay(receivedAt, site.TZ)) {
			health.IngestLateDropped.Add(1)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		h.unavailable(w)
		return
	}
	visitorID := salts.VisitorID(salt, site.ID, ip.String(), ua)
	var previousVisitorID int64
	localNow := eventTime.In(site.TZ)
	localMidnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, site.TZ)
	if localNow.Sub(localMidnight) <= previousSaltWindow {
		if previousSalt, found := h.Salts.For(localDay.AddDate(0, 0, -1)); found {
			previousVisitorID = salts.VisitorID(previousSalt, site.ID, ip.String(), ua)
		}
	}

	event, hit, err := h.buildEvent(
		r.Context(), payload, pageURL, site, location, ua,
		r.Header.Get("Accept-Language"), payload.ScreenWidth,
		eventTime, localDay, visitorID, previousVisitorID,
	)
	if err != nil {
		h.unavailable(w)
		return
	}
	result := h.Sessions.Touch(hit)
	if hit.JoinOnly && result.SessionID == 0 {
		// A background ping from a tab whose session already ended is not a
		// visit: storing it would add a phantom session and visitor.
		health.IngestStalePings.Add(1)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if profileID != "" {
		if links, ok := h.Sessions.(session.ProfileLinker); ok && !links.LinkProfile(site.ID, result.SessionID, profileID) {
			profileID = ""
			profileTraits = nil
		}
	}
	if sanitizePaths && hit.Kind == 0 {
		event.PathSanitizerVersion = pathValue.Version
		event.PathPolicyID = pathValue.Policy
	}
	event.Hit = hit
	event.VisitorID = result.VisitorID
	event.SessionID = result.SessionID
	event.ProfileExternalID = profileID
	event.ProfileTraits = profileTraits
	if !reservation.Commit(event) {
		// The handler owns this reservation and commits it exactly once.
		panic("ingest: event reservation already consumed")
	}
	if h.Dedup != nil {
		h.Dedup.Record(site.ID, payload.ID, receivedAt)
	}
	recordAcceptedActivity(h.Sessions, hit, result.VisitorID)
	if !identityAllowed && profileSupplied {
		health.IngestProfilesSuppressed.Add(1)
	}
	if payload.RevenueInvalid {
		health.IngestRevenueInvalid.Add(1)
	}
	if payload.propsTruncated {
		health.IngestPropsTruncated.Add(1)
	}
	health.IngestAccepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
}

func requestOriginHost(r *http.Request) string {
	source := strings.TrimSpace(r.Header.Get("Origin"))
	if source == "" {
		source = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if source == "" {
		return ""
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func (h *Handler) acceptHeatmap(w http.ResponseWriter, ctx context.Context, payload Payload, pageURL *url.URL, site sites.Site, eventTime time.Time) bool {
	path := pageURL.Path
	if path == "" {
		path = "/"
	}
	localDay := salts.LocalDay(eventTime, site.TZ)
	pathID, err := h.dimensionID(ctx, site.ID, localDay, dict.Path, path)
	if err != nil {
		h.unavailable(w)
		return false
	}
	if !h.Writer.EnqueueHeatmap(writer.HeatmapEvent{
		SiteID: site.ID, LocalDay: localDay, PathID: pathID,
		ViewportWidth: payload.ViewportWidth, Clicks: payload.Clicks,
	}) {
		health.HeatmapDropped.Add(1)
		health.IngestDropped.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		return false
	}
	health.IngestAccepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
	return true
}

func recordAcceptedActivity(store session.Store, hit session.Hit, visitorID int64) {
	if hit.Kind == 2 {
		return
	}
	if recorder, ok := store.(session.ActivityRecorder); ok {
		recorder.RecordActivity(session.Activity{
			SiteID: hit.SiteID, Timestamp: hit.TS, VisitorID: visitorID,
			Kind: hit.Kind, PathID: hit.PathID, NameID: hit.NameID,
			Country: hit.Country, CityID: hit.City, Device: hit.Device,
			BrowserID: hit.Browser, SourceID: hit.Source,
		})
	}
}

func (h *Handler) buildEvent(
	ctx context.Context,
	payload Payload,
	pageURL *url.URL,
	site sites.Site,
	location enrich.CityInfo,
	ua, acceptLanguage string,
	screenWidth int,
	now, localDay time.Time,
	visitorID, previousVisitorID int64,
) (writer.Event, session.Hit, error) {
	path := pageURL.Path
	if path == "" {
		path = "/"
	}
	utm := enrich.ParseUTM(payload.URL)
	source, medium := h.Enricher.Source(payload.Referrer, payload.URL, utm)
	client := h.Enricher.Client(ua)
	language := enrich.PrimaryLanguage(acceptLanguage)
	hostname := ""
	if multiHost(site) {
		hostname = enrich.HostLabel(pageURL.Host)
	}
	pathID, err := h.dimensionID(ctx, site.ID, localDay, dict.Path, path)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	referrerID, err := h.dimensionID(ctx, site.ID, localDay, dict.Referrer, payload.Referrer)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	sourceID, err := h.dimensionID(ctx, site.ID, localDay, dict.Source, source)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	utmSourceID, err := h.dimensionID(ctx, site.ID, localDay, dict.Source, utm.Source)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	mediumID, err := h.dimensionID(ctx, site.ID, localDay, dict.Medium, medium)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	campaignID, err := h.dimensionID(ctx, site.ID, localDay, dict.Campaign, utm.Campaign)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	utmContentID, err := h.dimensionID(ctx, site.ID, localDay, dict.UTMContent, utm.Content)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	utmTermID, err := h.dimensionID(ctx, site.ID, localDay, dict.UTMTerm, utm.Term)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	regionID, err := h.dimensionID(ctx, site.ID, localDay, dict.Region, location.Region)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	cityID, err := h.dimensionID(ctx, site.ID, localDay, dict.City, location.City)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	browserID, err := h.dimensionID(ctx, site.ID, localDay, dict.Browser, client.Browser)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	osID, err := h.dimensionID(ctx, site.ID, localDay, dict.OS, client.OS)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	browserVersionID, err := h.dimensionID(ctx, site.ID, localDay, dict.BrowserVersion, client.BrowserVersion)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	osVersionID, err := h.dimensionID(ctx, site.ID, localDay, dict.OSVersion, client.OSVersion)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	languageID, err := h.dimensionID(ctx, site.ID, localDay, dict.Language, language)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}
	hostnameID, err := h.dimensionID(ctx, site.ID, localDay, dict.Hostname, hostname)
	if err != nil {
		return writer.Event{}, session.Hit{}, err
	}

	kind := int16(1)
	var nameID int32
	switch payload.Name {
	case "pageview":
		kind = 0
	case "engagement":
		kind = 2
	default:
		nameID, err = h.dimensionID(ctx, site.ID, localDay, dict.EventName, payload.Name)
		if err != nil {
			return writer.Event{}, session.Hit{}, err
		}
	}
	hit := session.Hit{
		SiteID: site.ID, Retention: site.Retention, TS: now, LocalDay: localDay,
		VisitorID: visitorID, PrevVisitorID: previousVisitorID, Kind: kind,
		JoinOnly: kind == 2 || payload.Name == "Web Vitals",
		PathID:   pathID, NameID: nameID, Source: sourceID, UTMSource: utmSourceID, Medium: mediumID,
		Campaign: campaignID, Country: location.Country, Region: regionID, City: cityID,
		Browser: browserID, OS: osID, BrowserVer: browserVersionID, OSVer: osVersionID,
		Language: languageID, Hostname: hostnameID, ScreenBucket: enrich.ScreenBucket(screenWidth),
		Device: client.Device, EngagementMS: int32(payload.EngagementMS), ScrollPct: int16(payload.ScrollPct),
	}
	props := payload.Props
	if kind == 2 {
		props = nil
	}
	return writer.Event{
		Props: props, Screen: int16(screenWidth), ReferrerID: referrerID,
		UTMContent: utmContentID, UTMTerm: utmTermID,
		RevenueMicros: payload.RevenueMicros, RevenueCurrency: payload.RevenueCurrency,
	}, hit, nil
}

func (h *Handler) capValue(siteID int32, localDay time.Time, kind dict.Kind, value string) string {
	h.capsOnce.Do(func() {
		limit := h.DimensionCardinalityLimit
		compactLimit := defaultCompactCardinalityCap
		if limit <= 0 {
			limit = defaultDimensionCardinalityCap
		} else {
			compactLimit = limit
		}
		h.caps = newValueCap(limit)
		h.compactCaps = newValueCap(compactLimit)
	})
	caps := h.caps
	switch kind {
	case dict.BrowserVersion, dict.OSVersion, dict.Language, dict.Hostname:
		caps = h.compactCaps
	}
	return caps.Admit(siteID, localDay, kind, value)
}

func multiHost(site sites.Site) bool {
	if len(site.AllowedHosts) > 1 {
		return true
	}
	for _, host := range site.AllowedHosts {
		if strings.HasPrefix(host, "*.") {
			return true
		}
	}
	return false
}

func (h *Handler) dimensionID(ctx context.Context, siteID int32, localDay time.Time, kind dict.Kind, value string) (int32, error) {
	if value == "" {
		return 0, nil
	}
	original := value
	value = h.capValue(siteID, localDay, kind, value)
	ids, err := h.Dict.IDsForSite(ctx, siteID, kind, []string{value})
	if err != nil {
		return 0, err
	}
	if original != OtherValue {
		if resolved, ok := h.Dict.Value(kind, ids[0]); ok && resolved == OtherValue {
			DimensionOverflow.Add(1)
			h.DropStats.Record(siteID, localDay, dropstats.DimensionOverflow, "")
		}
	}
	return ids[0], nil
}

func parsePageURL(raw string) (*url.URL, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errInvalidURL
	}
	return u, nil
}

var errInvalidURL = errors.New("invalid page URL")

func (h *Handler) clientIP(r *http.Request) (netip.Addr, bool) {
	return clientip.Resolve(r, h.TrustedProxies)
}

func ipRateKey(ip netip.Addr) string {
	identity := ip.String()
	if ip.Is6() {
		identity = netip.PrefixFrom(ip, 64).Masked().String()
	}
	sum := sha256.Sum256([]byte(identity))
	return "ip:" + hex.EncodeToString(sum[:16])
}

func ingestRateLimit(limiter RateLimiter, ipKey, siteKey string) RateLimitReason {
	if limiter == nil {
		return RateLimitNone
	}
	if scoped, ok := limiter.(ingestRequestLimiter); ok {
		return scoped.AllowIngest(ipKey, siteKey)
	}
	if !limiter.Allow(ipKey) {
		return RateLimitIP
	}
	if !limiter.Allow(siteKey) {
		if refundable, ok := limiter.(refundableRateLimiter); ok {
			refundable.Refund(ipKey)
		}
		return RateLimitSite
	}
	if !limiter.Allow("global") {
		if refundable, ok := limiter.(refundableRateLimiter); ok {
			refundable.Refund(ipKey)
			refundable.Refund(siteKey)
		}
		return RateLimitGlobal
	}
	return RateLimitNone
}

func recordIngestRateLimit(reason RateLimitReason) {
	switch reason {
	case RateLimitIP:
		health.IngestRateLimitedIP.Add(1)
	case RateLimitSite:
		health.IngestRateLimitedSite.Add(1)
	case RateLimitGlobal:
		health.IngestRateLimitedGlobal.Add(1)
	}
}

func (h *Handler) recordDrop(site sites.Site, reason dropstats.Reason, rule string, eventTime time.Time) {
	if eventTime.IsZero() {
		eventTime = time.Now()
		if h.Clock != nil {
			eventTime = h.Clock.Now()
		}
	}
	h.DropStats.Record(site.ID, salts.LocalDay(eventTime, site.TZ), reason, rule)
}

func dropReasonForRateLimit(reason RateLimitReason) dropstats.Reason {
	switch reason {
	case RateLimitIP:
		return dropstats.RateLimitedIP
	case RateLimitSite:
		return dropstats.RateLimitedSite
	default:
		return dropstats.RateLimitedGlobal
	}
}

func dropReasonForBot(reason enrich.BotReason) dropstats.Reason {
	switch reason {
	case enrich.BotHeadless:
		return dropstats.BotHeadless
	case enrich.BotSpamReferrer:
		return dropstats.BotSpamReferrer
	default:
		return dropstats.BotUserAgent
	}
}

func (h *Handler) invalid(w http.ResponseWriter) {
	Invalid.Add(1)
	health.IngestInvalid.Add(1)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, "{\"type\":\"about:blank\",\"title\":\"Bad Request\",\"status\":400,\"detail\":\"invalid event payload\"}\n")
}

func (h *Handler) unavailable(w http.ResponseWriter) {
	health.IngestDropped.Add(1)
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
}
