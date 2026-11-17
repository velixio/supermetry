package appingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"supermetry/internal/appproduct"
)

const (
	maxEventsPerRequest = 20
	maxRequestBytes     = 16 << 10
	maxTags             = 20
	maxTagKeyBytes      = 64
	maxTagValueBytes    = 200
	maxOccurrences      = 1_000_000
)

var (
	kinds     = setOf("crash", "error", "hang", "install_failed", "install_stalled", "incompatibility", "operation_failed", "session", KindScreen, KindEvent)
	levels    = setOf("fatal", "error", "warning")
	platforms = setOf("windows", "macos", "linux", "ios", "android", "web", "other")
	arches    = setOf("x64", "arm64", "x86", "arm", "other")
	token     = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,32}$`)
)

// Product kinds exist on the wire only: they go to the product adapter and
// never into app_events, whose kind CHECK is unchanged.
const (
	KindScreen = "screen"
	KindEvent  = "event"

	maxScreenBytes = 200
)

// IsProduct reports whether the event is a screen view or a custom event.
func (e Event) IsProduct() bool { return e.Kind == KindScreen || e.Kind == KindEvent }

func setOf(values ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(values))
	for _, v := range values {
		m[v] = struct{}{}
	}
	return m
}

// Event is one validated app report. InstallID and SessionID are raw: they
// live in memory only until the writer hashes them with the app's secret.
type Event struct {
	ID          string
	TS          time.Time // zero when the client sent none
	Kind        string
	Level       string
	Handled     bool
	Release     string
	Build       string
	Environment string
	Platform    string
	OSVersion   string
	Arch        string
	DeviceClass string
	Runtime     string
	ErrorType   string
	ExitReason  string
	Signal      string
	ExitCode    *int32
	Frames      string // scrubbed
	Fingerprint string
	Occurrences int
	InstallID   string
	SessionID   string
	Tags        map[string]string

	// Product kinds only (KindScreen, KindEvent); Props are the custom event
	// properties, validated like tags but strings only.
	Screen string
	Name   string
	Props  map[string]string

	// Sentry-shaped events only; the zero values are the column defaults.
	Culprit         string
	Transaction     string
	MessageHash     []byte // hash of the scrubbed message, nil when none
	TraceID         []byte
	SpanID          []byte
	UserHash        []byte // already HMACed by the normaliser, never a raw id
	SDKName         string
	SDKVersion      string
	Dist            string
	Symbolication   string // "", "ok", "missing" or "partial"
	GroupingVersion int16  // 0 means 1
	OwnerUserID     int64  // the user ownership rules route a new issue to; 0 for none
	Title           string // issue title when the normaliser derived one; else built from kind, type and frames
}

var errInvalid = errors.New("invalid app event")

func invalid(field string) error { return fmt.Errorf("%w: %s", errInvalid, field) }

// decodeObject decodes a JSON object keeping numbers exact.
func decodeObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil || out == nil {
		return nil, errInvalid
	}
	if decoder.More() {
		return nil, errInvalid
	}
	return out, nil
}

// ParseEvent validates one decoded event against the wire format. A wrong
// type, an over-long value or an unknown enum value is an error. Frames are
// scrubbed and the fingerprint is final.
func ParseEvent(m map[string]any) (Event, error) {
	var e Event
	var err error
	if e.ID, err = str(m, "id", 64, true); err != nil {
		return e, err
	}
	if e.Kind, err = enum(m, "kind", kinds, true); err != nil {
		return e, err
	}
	if e.Level, err = enum(m, "level", levels, false); err != nil {
		return e, err
	}
	if e.Level == "" {
		e.Level = "error"
		if e.Kind == "crash" {
			e.Level = "fatal"
		}
	}
	if e.Platform, err = enum(m, "platform", platforms, false); err != nil {
		return e, err
	}
	if e.Arch, err = enum(m, "arch", arches, false); err != nil {
		return e, err
	}
	if v, ok := m["handled"]; ok && v != nil {
		b, isBool := v.(bool)
		if !isBool {
			return e, invalid("handled")
		}
		e.Handled = b
	}
	if v, ok := m["ts"]; ok && v != nil {
		ms, err := integer(v)
		if err != nil || ms < 0 || ms > math.MaxInt64/2 {
			return e, invalid("ts")
		}
		e.TS = time.UnixMilli(ms)
	}
	for _, f := range []struct {
		name  string
		limit int
		dst   *string
	}{
		{"release", 64, &e.Release}, {"build", 64, &e.Build}, {"environment", 32, &e.Environment},
		{"os_version", 32, &e.OSVersion}, {"device_class", 32, &e.DeviceClass}, {"runtime", 32, &e.Runtime},
		{"error_type", 64, &e.ErrorType}, {"install_id", 128, &e.InstallID}, {"session_id", 64, &e.SessionID},
	} {
		if *f.dst, err = str(m, f.name, f.limit, false); err != nil {
			return e, err
		}
	}
	for _, f := range []struct {
		name string
		dst  *string
	}{{"exit_reason", &e.ExitReason}, {"signal", &e.Signal}} {
		value, err := str(m, f.name, 32, false)
		if err != nil || (value != "" && !token.MatchString(value)) {
			return e, invalid(f.name)
		}
		*f.dst = value
	}
	if v, ok := m["exit_code"]; ok && v != nil {
		code, err := integer(v)
		if err != nil || code < math.MinInt32 || code > math.MaxInt32 {
			return e, invalid("exit_code")
		}
		c := int32(code)
		e.ExitCode = &c
	}
	e.Occurrences = 1
	if v, ok := m["occurrences"]; ok && v != nil {
		n, err := integer(v)
		if err != nil || n < 1 || n > maxOccurrences {
			return e, invalid("occurrences")
		}
		e.Occurrences = int(n)
	}
	if e.IsProduct() {
		return parseProduct(m, e)
	}
	frames, err := str(m, "frames", math.MaxInt, false)
	if err != nil {
		return e, err
	}
	e.Frames = ScrubFrames(frames)
	client, err := str(m, "fingerprint", math.MaxInt, false)
	if err != nil {
		return e, err
	}
	e.Fingerprint = Fingerprint(e.Kind, e.ErrorType, e.Frames, client)
	if e.Tags, err = tags(m["tags"]); err != nil {
		return e, err
	}
	return e, nil
}

func str(m map[string]any, field string, limit int, required bool) (string, error) {
	v, ok := m[field]
	if !ok || v == nil {
		if required {
			return "", invalid(field)
		}
		return "", nil
	}
	s, isString := v.(string)
	if !isString || len(s) > limit || !utf8.ValidString(s) || strings.ContainsRune(s, 0) || (required && s == "") {
		return "", invalid(field)
	}
	return s, nil
}

func enum(m map[string]any, field string, allowed map[string]struct{}, required bool) (string, error) {
	s, err := str(m, field, 32, required)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", nil
	}
	if _, ok := allowed[s]; !ok {
		return "", invalid(field)
	}
	return s, nil
}

func integer(v any) (int64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, errInvalid
	}
	i, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil {
		return 0, errInvalid
	}
	return i, nil
}

func tags(v any) (map[string]string, error) {
	out := map[string]string{}
	if v == nil {
		return out, nil
	}
	obj, ok := v.(map[string]any)
	if !ok || len(obj) > maxTags {
		return nil, invalid("tags")
	}
	for key, raw := range obj {
		if key == "" || len(key) > maxTagKeyBytes || !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
			return nil, invalid("tags")
		}
		var value string
		switch typed := raw.(type) {
		case string:
			value = typed
		case json.Number:
			value = typed.String()
		case bool:
			value = strconv.FormatBool(typed)
		default:
			return nil, invalid("tags")
		}
		if len(value) > maxTagValueBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return nil, invalid("tags")
		}
		out[key] = value
	}
	return out, nil
}

// parseProduct validates the fields only screen and event reports carry.
func parseProduct(m map[string]any, e Event) (Event, error) {
	var err error
	if e.InstallID == "" {
		return e, invalid("install_id")
	}
	if e.Kind == KindScreen {
		if e.Screen, err = str(m, "screen", maxScreenBytes, true); err != nil {
			return e, err
		}
		if _, present := m["name"]; present && m["name"] != nil {
			return e, invalid("name")
		}
	} else {
		if e.Name, err = str(m, "name", appproduct.MaxNameBytes, true); err != nil {
			return e, err
		}
		if !appproduct.ValidName(e.Name) {
			return e, invalid("name")
		}
		if _, present := m["screen"]; present && m["screen"] != nil {
			return e, invalid("screen")
		}
	}
	if e.Props, err = productProps(m["props"]); err != nil {
		return e, err
	}
	return e, nil
}

// productProps accepts an object of up to 20 string values; "$"-prefixed
// keys are reserved for system properties.
func productProps(v any) (map[string]string, error) {
	out := map[string]string{}
	if v == nil {
		return out, nil
	}
	obj, ok := v.(map[string]any)
	if !ok || len(obj) > maxTags {
		return nil, invalid("props")
	}
	for key, raw := range obj {
		value, isString := raw.(string)
		if !isString || key == "" || strings.HasPrefix(key, "$") || len(key) > maxTagKeyBytes || len(value) > maxTagValueBytes ||
			!utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
			return nil, invalid("props")
		}
		out[key] = value
	}
	return out, nil
}
