package sentryingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mileusna/useragent"

	"supermetry/internal/appingest"
	"supermetry/internal/sites"
)

const (
	maxMessage        = 8 << 10
	maxExceptionValue = 2 << 10
	maxExceptions     = 10
	maxFrames         = 100
	maxContextLines   = 5
	maxContextLine    = 200
	maxBodyTags       = 50
	maxEventTags      = 20
	defaultMaxAgeDays = 7
	futureSkew        = 5 * time.Minute
	skewCorrectAfter  = 5 * time.Minute
	eventFrameLines   = 10
	hexID             = 32
)

var (
	reEventID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	reToken   = regexp.MustCompile(`[^A-Za-z0-9_.:-]`)
	reCountry = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Normalise turns one Sentry event payload into the narrow app_events row and
// the scrubbed body. Everything personal is dropped or scrubbed here (spec
// §4.3): user id becomes an HMAC, messages and breadcrumbs are scrubbed, the
// request keeps its path only, frame variables never leave this function. The
// client IP is not read at all; Country comes from the caller's lookup.
//
// It returns ErrInvalid for a payload that is not a JSON object and ErrTooOld
// for an event older than the site's report age.
func Normalise(raw []byte, ctx EventContext, site sites.Site, now time.Time) (appingest.Event, appingest.Body, error) {
	var ev appingest.Event
	var body appingest.Body
	m, err := decodeObject(raw)
	if err != nil {
		return ev, body, err
	}
	ev.ID = eventID(str(m["event_id"]), ctx.EventID, raw)
	ev.TS, err = eventTime(m["timestamp"], ctx.SentAt, site, now)
	if err != nil {
		return ev, body, err
	}

	sdk := asMap(m["sdk"])
	body.SDK = appingest.SDKInfo{Name: clip(firstNonEmpty(str(sdk["name"]), ctx.SDKName), 64), Version: clip(firstNonEmpty(str(sdk["version"]), ctx.SDKVersion), 32)}

	body.Exceptions = parseExceptions(m["exception"])
	body.Message = scrubClip(messageOf(m), maxMessage)
	contexts := asMap(m["contexts"])
	body.Contexts = scrubContexts(contexts)
	reqRaw := asMap(m["request"])
	body.Request = ScrubRequest(reqRaw)
	body.Breadcrumbs = scrubBreadcrumbs(asList(m["breadcrumbs"]))
	tags := tagPairs(m["tags"])
	if p := processTag(tags, body.Contexts); p != "" {
		tags["process"] = p
	}
	body.Tags = limitTags(tags, maxBodyTags)
	ev.Tags = limitTags(body.Tags, maxEventTags)

	level := strings.ToLower(str(m["level"]))
	switch level {
	case "fatal", "error", "warning", "info", "debug":
	default:
		level = "error"
	}
	platform := firstNonEmpty(strings.ToLower(str(m["platform"])), "other")
	body.Platform = clip(platform, 32)

	ua := ctx.UserAgent
	if ua == "" {
		ua = headerPairs(reqRaw["headers"])["User-Agent"]
		if ua == "" {
			ua = headerPairs(reqRaw["headers"])["user-agent"]
		}
	}
	client := parseUA(ua)
	if client != nil {
		if _, ok := body.Contexts["browser"]; !ok && client.Name != "" {
			body.Contexts["browser"] = map[string]any{"name": client.Name, "version": client.VersionNoFull()}
		}
		if _, ok := body.Contexts["os"]; !ok && client.OS != "" {
			version := client.OSVersion
			if strings.Contains(version, "_") { // "x86_64" is an architecture, not a version
				version = ""
			}
			body.Contexts["os"] = map[string]any{"name": client.OS, "version": version}
		}
	}

	ev.Kind, ev.ExitReason = classify(level, body.Exceptions, platform, ctx.HasMinidump)
	if ev.Kind == "crash" {
		level = "fatal"
	}
	ev.Level = level
	body.Level = level
	ev.Handled = handled(body.Exceptions, ev.Kind)
	ev.Release = clip(str(m["release"]), 64)
	body.Release = ev.Release
	body.DebugImages = parseDebugImages(m["debug_meta"], platform)
	ev.Environment = clip(str(m["environment"]), 32)
	ev.Build = clip(str(asMap(contexts["app"])["app_build"]), 64)
	osCtx, dev, rt := body.Contexts["os"], body.Contexts["device"], body.Contexts["runtime"]
	ev.Platform = platformOf(str(osCtx["name"]), platform, client != nil, ctx.SDKName+str(sdk["name"]))
	ev.OSVersion = clip(strings.TrimSpace(fmt.Sprint(valueOr(osCtx["version"]))), 32)
	ev.Arch = archOf(firstNonEmpty(str(dev["arch"]), str(body.Contexts["app"]["app_arch"])))
	ev.DeviceClass = deviceClass(client, str(dev["family"]))
	ev.Runtime = runtimeOf(rt, body.Contexts["browser"])
	ev.ErrorType, ev.Signal = errorTypeAndSignal(body.Exceptions)
	ev.Frames = eventFrames(body.Exceptions)
	ev.Occurrences = 1

	// identity: the raw id lives in memory only until the writer MACs it.
	user := asMap(m["user"])
	userID := clip(userIDString(user["id"]), 128)
	ev.InstallID = userID

	body.Country = country(ctx, sdk)
	body.SiteID, body.TS, body.EventID = site.ID, ev.TS, ev.ID

	title := titleOf(body)
	ex := &body.Row
	ex.Title = title
	if title != "" {
		sum := sha256.Sum256([]byte(title))
		ex.MessageHash = sum[:]
	}
	var secret []byte
	if site.App != nil {
		secret = site.App.HashSecret
	}
	ex.UserHash = HashUser(secret, userID)
	ex.Transaction = scrubClip(str(m["transaction"]), 200)
	ex.Culprit = culprit(body.Exceptions, ex.Transaction)
	ex.Dist = clip(str(m["dist"]), 64)
	ex.SDKName, ex.SDKVersion = body.SDK.Name, body.SDK.Version

	if len(body.Exceptions) == 0 {
		ev.ErrorType = "message"
	}
	ex.SDKFingerprint = sdkFingerprint(m["fingerprint"])
	if site.App != nil {
		ex.Rules = site.App.Rules
	}
	ev.Fingerprint, ex.GroupingVersion = appingest.GroupingKeyRules(ev, body, ex.SDKFingerprint, ex.Rules)
	ev.OwnerUserID = appingest.OwnerOf(body, ex.Rules)
	if len(body.Contexts) == 0 {
		body.Contexts = nil
	}
	return ev, body, nil
}

func valueOr(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// eventID returns a 32-hex id from the payload, else the envelope header,
// else a digest of the payload so retries still deduplicate.
func eventID(payload, header string, raw []byte) string {
	for _, c := range []string{payload, header} {
		c = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(c), "-", ""))
		if reEventID.MatchString(c) {
			return c
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:hexID/2])
}

func maxAge(site sites.Site) time.Duration {
	days := defaultMaxAgeDays
	if site.App != nil && site.App.MaxReportAgeDays > 0 {
		days = site.App.MaxReportAgeDays
	}
	if r := int(site.Retention); r > 0 && r < days {
		days = r
	}
	return time.Duration(days) * 24 * time.Hour
}

// eventTime resolves the event time: now when missing, corrected by the
// envelope clock skew when the device clock is far off, clamped to now when in
// the future, and ErrTooOld past the site's report age.
func eventTime(v any, sentAt time.Time, site sites.Site, now time.Time) (time.Time, error) {
	ts, ok := parseTime(v)
	if !ok {
		return now, nil
	}
	if !sentAt.IsZero() {
		skew := now.Sub(sentAt)
		if skew > skewCorrectAfter || skew < -skewCorrectAfter {
			ts = ts.Add(skew)
		}
	}
	if ts.After(now.Add(futureSkew)) {
		ts = now
	}
	if ts.Before(now.Add(-maxAge(site))) {
		return ts, ErrTooOld
	}
	return ts, nil
}

func messageOf(m map[string]any) string {
	if s, ok := m["message"].(string); ok && s != "" {
		return s
	}
	if mm := asMap(m["message"]); mm != nil {
		if s := firstNonEmpty(str(mm["formatted"]), str(mm["message"])); s != "" {
			return s
		}
	}
	le := asMap(m["logentry"])
	return firstNonEmpty(str(le["formatted"]), str(le["message"]))
}

func parseExceptions(v any) []appingest.Exception {
	list := asList(v)
	if len(list) > maxExceptions {
		list = list[len(list)-maxExceptions:]
	}
	var out []appingest.Exception
	for _, item := range list {
		m := asMap(item)
		if m == nil {
			continue
		}
		ex := appingest.Exception{
			Type:   clip(str(m["type"]), 128),
			Value:  scrubClip(str(m["value"]), maxExceptionValue),
			Module: clip(str(m["module"]), 128),
		}
		if mech := asMap(m["mechanism"]); mech != nil {
			mm := &appingest.Mechanism{Type: clip(str(mech["type"]), 64)}
			if h, ok := mech["handled"].(bool); ok {
				mm.Handled = &h
			}
			if sig := asMap(asMap(mech["meta"])["signal"]); sig != nil {
				mm.Signal = clip(str(sig["name"]), 32)
			}
			ex.Mechanism = mm
		}
		st := asMap(m["stacktrace"])
		frames := asList(st["frames"])
		if len(frames) > maxFrames {
			frames = frames[len(frames)-maxFrames:]
		}
		for _, f := range frames {
			if fm := asMap(f); fm != nil {
				ex.Frames = append(ex.Frames, parseFrame(fm))
			}
		}
		out = append(out, ex)
	}
	return out
}

func parseFrame(m map[string]any) appingest.Frame {
	f := appingest.Frame{
		Filename:        scrubFilename(firstNonEmpty(str(m["filename"]), str(m["abs_path"]))),
		AbsURL:          stripQuery(clip(firstNonEmpty(str(m["abs_path"]), str(m["filename"])), 1024)),
		Module:          clip(str(m["module"]), 200),
		Function:        clip(str(m["function"]), 200),
		Package:         scrubFilename(str(m["package"])),
		Lineno:          asInt(m["lineno"]),
		Colno:           asInt(m["colno"]),
		InstructionAddr: clip(reToken.ReplaceAllString(str(m["instruction_addr"]), ""), 32),
	}
	f.InApp, _ = m["in_app"].(bool)
	f.ContextLine = scrubClip(str(m["context_line"]), maxContextLine)
	f.PreContext = contextLines(m["pre_context"])
	f.PostContext = contextLines(m["post_context"])
	return f
}

func contextLines(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if len(out) == maxContextLines {
			break
		}
		if s, ok := x.(string); ok {
			out = append(out, scrubClip(s, maxContextLine))
		}
	}
	return out
}

// scrubFilename reduces a URL to its path and a local path to a home-relative
// or short form.
func scrubFilename(s string) string {
	if s == "" {
		return ""
	}
	return clip(appingest.ScrubFrames(urlPath(s)), 200)
}

func sdkFingerprint(v any) []string {
	var out []string
	for _, x := range asList(v) {
		switch t := x.(type) {
		case string:
			out = append(out, clip(t, 200))
		case fmt.Stringer:
			out = append(out, t.String())
		}
		if len(out) == 10 {
			break
		}
	}
	return out
}

func userIDString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	}
	return ""
}

func limitTags(in map[string]string, max int) map[string]string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]string{}
	for _, k := range keys {
		if len(out) == max {
			break
		}
		ck := clip(k, 64)
		if ck == "" {
			continue
		}
		v := in[k]
		if isURLKey(k) {
			out[ck] = urlPath(v)
			continue
		}
		out[ck] = scrubClip(v, 200)
	}
	return out
}

func parseUA(ua string) *useragent.UserAgent {
	if ua == "" {
		return nil
	}
	p := useragent.Parse(clip(ua, 512))
	if p.Name == "" && p.OS == "" {
		return nil
	}
	return &p
}

// classify decides the event kind. Hangs (ANR, app hang) come first. Then the
// terminations the OS carried out (watchdog, out of memory) are crashes with
// an exit reason: they are fatal and unhandled, and the API and UI expose
// them as the kinds "watchdog" and "oom". Then fatal level or a minidump means
// crash, else error.
func classify(level string, excs []appingest.Exception, platform string, minidump bool) (kind, exitReason string) {
	oom := false
	for _, ex := range excs {
		mech := ""
		unhandled := false
		if ex.Mechanism != nil {
			mech = strings.ToLower(ex.Mechanism.Type)
			unhandled = ex.Mechanism.Handled != nil && !*ex.Mechanism.Handled
		}
		switch {
		case mech == "anr" || ex.Type == "ApplicationNotResponding":
			return "hang", "anr"
		case mech == "apphang" || strings.HasPrefix(ex.Type, "App Hang"):
			return "hang", "app_hang"
		case mech == "watchdog_termination" || ex.Type == "WatchdogTermination":
			return "crash", "watchdog_termination"
		}
		// A caught OutOfMemoryError is an ordinary error; only a fatal or
		// unhandled one ended the process.
		if level == "fatal" || unhandled {
			switch {
			case ex.Type == "java.lang.OutOfMemoryError" || (ex.Type == "OutOfMemoryError" && (ex.Module == "java.lang" || ex.Module == "")):
				oom = true
			case ex.Type == "EXC_RESOURCE" && strings.Contains(strings.ToUpper(ex.Value), "RESOURCE_TYPE_MEMORY"):
				oom = true
			case mech == "memorylimiter": // inference: not found in the sentry-java or sentry-cocoa sources
				oom = true
			}
		}
	}
	if oom {
		return "crash", "oom"
	}
	if level == "fatal" || minidump {
		return "crash", ""
	}
	return "error", ""
}

// handled is false when any mechanism says unhandled, or for a crash; a
// message with no exceptions is handled.
func handled(excs []appingest.Exception, kind string) bool {
	if kind == "crash" {
		return false
	}
	for _, ex := range excs {
		if ex.Mechanism != nil && ex.Mechanism.Handled != nil && !*ex.Mechanism.Handled {
			return false
		}
	}
	return true
}

func platformOf(osName, sdkPlatform string, hasUA bool, sdkName string) string {
	n := strings.ToLower(osName)
	switch {
	case strings.Contains(strings.ToLower(sdkName), "javascript.browser"),
		sdkPlatform == "javascript" && hasUA && !strings.Contains(strings.ToLower(sdkName), "electron"):
		return "web"
	case strings.Contains(n, "windows"):
		return "windows"
	case n == "macos" || strings.Contains(n, "mac os") || n == "darwin":
		return "macos"
	case n == "android":
		return "android"
	case n == "ios" || n == "ipados" || n == "tvos" || n == "watchos" || n == "iphone os":
		return "ios"
	case strings.Contains(n, "linux") || n == "debian" || n == "ubuntu" || n == "fedora" || n == "arch" || n == "alpine" || n == "centos" || n == "red hat" || n == "chrome os":
		return "linux"
	}
	switch {
	case strings.Contains(sdkName, "android"):
		return "android"
	case strings.Contains(sdkName, "cocoa"):
		return "ios"
	case sdkPlatform == "javascript" && (hasUA || strings.Contains(sdkName, "browser")):
		return "web"
	}
	return "other"
}

func archOf(a string) string {
	a = strings.ToLower(a)
	switch {
	case a == "":
		return ""
	case a == "x64" || a == "x86_64" || a == "amd64" || strings.HasPrefix(a, "x86_64"):
		return "x64"
	case strings.HasPrefix(a, "arm64") || a == "aarch64":
		return "arm64"
	case a == "x86" || a == "i386" || a == "i686":
		return "x86"
	case strings.HasPrefix(a, "arm"):
		return "arm"
	}
	return "other"
}

func deviceClass(ua *useragent.UserAgent, family string) string {
	if ua != nil {
		switch {
		case ua.Tablet:
			return "tablet"
		case ua.Mobile:
			return "mobile"
		case ua.Desktop:
			return "desktop"
		}
	}
	return clip(strings.ToLower(strings.TrimSpace(family)), 32)
}

func runtimeOf(rt, browser map[string]any) string {
	for _, c := range []map[string]any{rt, browser} {
		name := strings.TrimSpace(str(c["name"]))
		if name == "" {
			continue
		}
		return clip(strings.TrimSpace(name+" "+str(c["version"])), 32)
	}
	return ""
}

func errorTypeAndSignal(excs []appingest.Exception) (string, string) {
	if len(excs) == 0 {
		return "", ""
	}
	last := excs[len(excs)-1]
	t := clip(last.Type, 64)
	if t == "" {
		t = "message"
	}
	sig := ""
	for _, ex := range excs {
		if ex.Mechanism != nil && ex.Mechanism.Signal != "" {
			sig = reToken.ReplaceAllString(ex.Mechanism.Signal, "")
		}
	}
	return t, clip(sig, 32)
}

// eventFrames renders the innermost in-app frames (all when none is in-app),
// innermost first, in the v1 frames format.
func eventFrames(excs []appingest.Exception) string {
	if len(excs) == 0 {
		return ""
	}
	frames := excs[len(excs)-1].Frames
	var pick []appingest.Frame
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].InApp {
			pick = append(pick, frames[i])
		}
	}
	if len(pick) == 0 {
		for i := len(frames) - 1; i >= 0; i-- {
			pick = append(pick, frames[i])
		}
	}
	if len(pick) > eventFrameLines {
		pick = pick[:eventFrameLines]
	}
	var lines []string
	for _, f := range pick {
		where := firstNonEmpty(f.Filename, f.Module, f.Package)
		fn := firstNonEmpty(f.Function, "?")
		line := fn
		if where != "" {
			line += " (" + where
			if f.Lineno > 0 {
				line += fmt.Sprintf(":%d", f.Lineno)
			}
			line += ")"
		}
		lines = append(lines, line)
	}
	return appingest.ScrubFrames(strings.Join(lines, "\n"))
}

func culprit(excs []appingest.Exception, transaction string) string {
	if len(excs) > 0 {
		frames := excs[len(excs)-1].Frames
		for i := len(frames) - 1; i >= 0; i-- {
			f := frames[i]
			if !f.InApp {
				continue
			}
			fn := firstNonEmpty(f.Function, "?")
			if f.Module != "" {
				return clip(f.Module+"."+fn, 200)
			}
			return clip(fn, 200)
		}
	}
	return transaction
}

func titleOf(b appingest.Body) string {
	if n := len(b.Exceptions); n > 0 {
		ex := b.Exceptions[n-1]
		switch {
		case ex.Type != "" && ex.Value != "":
			return clip(ex.Type+": "+ex.Value, 200)
		case ex.Type != "":
			return clip(ex.Type, 200)
		case ex.Value != "":
			return clip(ex.Value, 200)
		}
	}
	line, _, _ := strings.Cut(b.Message, "\n")
	return clip(line, 200)
}

// country returns the caller's country code unless the SDK asked the server
// not to infer location (infer_ip "never").
func country(ctx EventContext, sdk map[string]any) string {
	if strings.EqualFold(ctx.InferIP, "never") || strings.EqualFold(str(asMap(sdk["settings"])["infer_ip"]), "never") {
		return ""
	}
	c := strings.ToUpper(strings.TrimSpace(ctx.Country))
	if reCountry.MatchString(c) {
		return c
	}
	return ""
}

// stripQuery drops the query string and fragment of a URL-like value.
func stripQuery(s string) string {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		return s[:i]
	}
	return s
}

const (
	maxDebugImages       = 50
	maxNativeDebugImages = 500 // iOS crashes list every loaded image
)

// parseDebugImages keeps the debug_meta images symbolication can use. Native
// platforms may carry up to 500 images, everything else 50.
func parseDebugImages(v any, platform string) []appingest.DebugImage {
	limit := maxDebugImages
	if platform == "native" || platform == "cocoa" {
		limit = maxNativeDebugImages
	}
	var out []appingest.DebugImage
	for _, x := range asList(asMap(v)["images"]) {
		im := asMap(x)
		if im == nil {
			continue
		}
		out = append(out, appingest.DebugImage{
			Type:        clip(str(im["type"]), 32),
			CodeFile:    stripQuery(clip(str(im["code_file"]), 1024)),
			DebugID:     clip(firstNonEmpty(str(im["debug_id"]), str(im["debugId"])), 64),
			CodeID:      clip(str(im["code_id"]), 128),
			DebugFile:   stripQuery(clip(str(im["debug_file"]), 1024)),
			Arch:        clip(str(im["arch"]), 32),
			UUID:        clip(str(im["uuid"]), 64),
			ImageAddr:   parseAddr(im["image_addr"]),
			ImageSize:   parseAddr(im["image_size"]),
			ImageVMAddr: parseAddr(im["image_vmaddr"]),
		})
		if len(out) == limit {
			break
		}
	}
	return out
}

// parseAddr reads an address or size that SDKs send as a "0x" hex string, a
// decimal string or a JSON number. Anything else, or a value that does not fit
// in 64 bits, is 0.
func parseAddr(v any) uint64 {
	switch x := v.(type) {
	case float64:
		if x < 0 || x >= 1<<63 || x != math.Trunc(x) {
			return 0
		}
		return uint64(x)
	case json.Number:
		n, err := strconv.ParseUint(x.String(), 10, 64)
		if err != nil {
			return 0
		}
		return n
	case string:
		if len(x) > 20 {
			return 0
		}
		if h, ok := strings.CutPrefix(strings.ToLower(x), "0x"); ok {
			n, err := strconv.ParseUint(h, 16, 64)
			if err != nil {
				return 0
			}
			return n
		}
		n, err := strconv.ParseUint(x, 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// processTag normalises the process an Electron or Crashpad event came from
// into main, renderer, gpu, utility or other; "" when the event names none.
// One Electron app reports all its processes to one project, and this tag
// tells them apart.
func processTag(tags map[string]string, contexts map[string]map[string]any) string {
	raw, fromTag := "", ""
	for _, k := range []string{"process", "event.process", "process_type"} {
		if v := strings.TrimSpace(tags[k]); v != "" {
			raw, fromTag = v, k
			break
		}
	}
	if raw == "" {
		for _, c := range []string{"electron", "crashpad"} {
			if v := strings.TrimSpace(str(contexts[c]["process_type"])); v != "" {
				raw = v
				break
			}
			if v := strings.TrimSpace(str(contexts[c]["crashpad.process_type"])); v != "" {
				raw = v
				break
			}
		}
	}
	if raw == "" {
		return ""
	}
	switch strings.ToLower(raw) {
	case "main", "browser":
		return "main"
	case "renderer":
		return "renderer"
	case "gpu", "gpu-process":
		return "gpu"
	case "utility":
		return "utility"
	}
	if fromTag == "process" {
		return "" // an app's own process tag keeps its own values
	}
	return "other"
}
