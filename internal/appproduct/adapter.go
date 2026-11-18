package appproduct

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"supermetry/internal/db"
	"supermetry/internal/dict"
	"supermetry/internal/dropstats"
	"supermetry/internal/health"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
	"supermetry/internal/writer"
)

const (
	maxScreenBytes   = 200
	MaxNameBytes     = 64
	maxProps         = 20
	maxPropKeyBytes  = 64
	maxPropValBytes  = 200
	futureSkew       = 5 * time.Minute
	defaultMaxAgeDay = 7
)

var (
	namePattern = regexp.MustCompile(`^[A-Za-z0-9_.:$ -]+$`)

	platforms = map[string]struct{}{
		"ios": {}, "android": {}, "macos": {}, "windows": {}, "linux": {}, "web": {}, "other": {},
	}
)

// ValidName reports whether name is an acceptable custom event name: 1 to 64
// bytes of letters, digits and _.:$ - space, and not starting with "$" (that
// prefix is reserved for system properties).
func ValidName(name string) bool {
	return name != "" && len(name) <= MaxNameBytes && !strings.HasPrefix(name, "$") && namePattern.MatchString(name)
}

// DropRecorder counts dropped hits per site and day.
type DropRecorder interface {
	Record(siteID int32, day time.Time, reason dropstats.Reason, botRule string) bool
}

// Hit is one product hit: a screen view (Screen set) or a custom event (Name
// set). InstallID is raw and only lives in memory until it is hashed with the
// app's secret.
type Hit struct {
	SiteID       int32
	TS           time.Time // zero means now
	InstallID    string
	Screen       string // "" for events
	Name         string // custom event name, "" for screens
	Props        map[string]string
	Release      string
	Platform     string
	OSName       string
	OSVersion    string
	DeviceClass  string
	Locale       string
	Country      string
	Region, City string
	EngagementMS int32
}

// Outcome is the result of Accept. Dropped is set when the hit was refused
// for Reason (dropstats.Reason has no "none" value, so Reason alone cannot say
// accepted). QueueFull asks the client to retry; the hit was not stored.
type Outcome struct {
	Reason    dropstats.Reason
	Dropped   bool
	QueueFull bool
}

// Accepted reports whether the hit was queued.
func (o Outcome) Accepted() bool { return !o.Dropped && !o.QueueFull }

// Adapter feeds product hits into the web writer and session store. It skips
// the web-only gates (hostname allowlist, bot user agent, daily salt, the
// 24 hour age limit) and never reads the database per hit beyond the
// dictionary cache.
type Adapter struct {
	w     *writer.Writer
	sess  session.Store
	dict  *dict.Dict
	drops DropRecorder
	now   func() time.Time
}

// New builds an adapter. now may be nil.
func New(w *writer.Writer, sess session.Store, d *dict.Dict, drops DropRecorder, now func() time.Time) *Adapter {
	if now == nil {
		now = time.Now
	}
	return &Adapter{w: w, sess: sess, dict: d, drops: drops, now: now}
}

func (a *Adapter) drop(site sites.Site, now time.Time, reason dropstats.Reason) Outcome {
	if a.drops != nil {
		a.drops.Record(site.ID, salts.LocalDay(now, site.TZ), reason, "")
	}
	return Outcome{Reason: reason, Dropped: true}
}

// Accept validates one hit and hands it to the writer and session store.
func (a *Adapter) Accept(ctx context.Context, site sites.Site, h Hit) Outcome {
	now := a.now()
	if site.App == nil || site.TZ == nil {
		return a.drop(site, now, dropstats.AppInvalid)
	}
	props, ok := buildProps(h)
	path, validPath := screenPath(h.Screen)
	if !ok || h.InstallID == "" || (h.Screen == "") == (h.Name == "") ||
		(h.Name != "" && !ValidName(h.Name)) || (h.Screen != "" && !validPath) {
		return a.drop(site, now, dropstats.AppInvalid)
	}
	secret := site.App.HashSecret
	if len(secret) == 0 {
		health.AppSecretMissing.Add(1)
		return Outcome{QueueFull: true}
	}

	ts := h.TS
	if ts.IsZero() || ts.After(now.Add(futureSkew)) {
		if !ts.IsZero() {
			health.IngestClockSkew.Add(1)
		}
		ts = now
	}
	if salts.LocalDay(ts, site.TZ).Before(salts.LocalDay(now, site.TZ).AddDate(0, 0, -maxAgeDays(site))) {
		return a.drop(site, now, dropstats.TooOld)
	}

	reservation, ok := a.w.ReserveEvent()
	if !ok {
		health.AppIngestQueueFull.Add(1)
		return Outcome{QueueFull: true}
	}
	defer reservation.Cancel()

	localDay := salts.LocalDay(ts, site.TZ)
	ids, err := a.dimensions(ctx, site.ID, h, path)
	if err != nil {
		return Outcome{QueueFull: true}
	}
	kind := int16(0)
	if h.Name != "" {
		kind = 1
	}
	hit := session.Hit{
		SiteID: site.ID, Retention: site.Retention, TS: ts, LocalDay: localDay,
		VisitorID: VisitorID(secret, h.InstallID), Kind: kind,
		PathID: ids.path, NameID: ids.name, Country: clipBytes(strings.ToUpper(h.Country), 2),
		Region: ids.region, City: ids.city, OS: ids.os, OSVer: ids.osVersion, Hostname: ids.hostname,
		Device: deviceCode(h.DeviceClass), EngagementMS: max(h.EngagementMS, 0),
	}
	result := a.sess.Touch(hit)
	event := writer.Event{
		Hit: hit, SessionID: result.SessionID, Props: props,
		ProfileExternalID: ProfileExternalID(secret, h.InstallID),
	}
	event.VisitorID = result.VisitorID
	reservation.Commit(event)
	return Outcome{}
}

type dimensionIDs struct {
	path, name, region, city, os, osVersion, hostname int32
}

func (a *Adapter) dimensions(ctx context.Context, siteID int32, h Hit, path string) (dimensionIDs, error) {
	var out dimensionIDs
	for _, d := range []struct {
		dst   *int32
		kind  dict.Kind
		value string
	}{
		{&out.path, dict.Path, path},
		{&out.name, dict.EventName, h.Name},
		{&out.region, dict.Region, clipBytes(h.Region, 100)},
		{&out.city, dict.City, clipBytes(h.City, 100)},
		{&out.os, dict.OS, clipBytes(h.OSName, 64)},
		{&out.osVersion, dict.OSVersion, clipBytes(h.OSVersion, 32)},
		{&out.hostname, dict.Hostname, platformOf(h.Platform)},
	} {
		if d.value == "" {
			continue
		}
		ids, err := a.dict.IDsForSite(ctx, siteID, d.kind, []string{d.value})
		if err != nil {
			return out, err
		}
		*d.dst = ids[0]
	}
	return out, nil
}

// buildProps validates the user props and merges the reserved ones.
func buildProps(h Hit) (map[string]string, bool) {
	if len(h.Props) > maxProps {
		return nil, false
	}
	out := make(map[string]string, len(h.Props)+3)
	for key, value := range h.Props {
		if key == "" || strings.HasPrefix(key, "$") || len(key) > maxPropKeyBytes || len(value) > maxPropValBytes ||
			!utf8.ValidString(key) || !utf8.ValidString(value) {
			return nil, false
		}
		out[key] = value
	}
	for key, value := range map[string]string{"$release": h.Release, "$device_class": h.DeviceClass, "$locale": h.Locale} {
		if value != "" {
			out[key] = clipBytes(value, maxPropValBytes)
		}
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}

// screenPath turns a screen name into a path dictionary value: clipped to
// 200 bytes with exactly one leading slash.
func screenPath(screen string) (string, bool) {
	if screen == "" {
		return "", true
	}
	if !utf8.ValidString(screen) || strings.ContainsRune(screen, 0) {
		return "", false
	}
	screen = clipBytes(strings.TrimLeft(screen, "/"), maxScreenBytes)
	return "/" + screen, true
}

func platformOf(platform string) string {
	if _, ok := platforms[platform]; ok {
		return platform
	}
	return "other"
}

func deviceCode(class string) int16 {
	switch strings.ToLower(class) {
	case "mobile", "phone":
		return 1
	case "tablet":
		return 2
	default:
		return 0
	}
}

func maxAgeDays(site sites.Site) int {
	days := site.App.MaxReportAgeDays
	if days <= 0 {
		days = defaultMaxAgeDay
	}
	if retention := int(site.Retention); site.Retention != db.RetForever && retention > 0 && retention < days {
		days = retention
	}
	return days
}

// clipBytes cuts s to at most n bytes without splitting a UTF-8 character.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
