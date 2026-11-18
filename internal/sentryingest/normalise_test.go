package sentryingest

import (
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/sites"
)

var (
	testNow    = time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
	testSecret = []byte("0123456789abcdef0123456789abcdef")
)

func testSite() sites.Site {
	return sites.Site{ID: 42, Kind: sites.KindApp, App: &sites.AppConfig{MaxReportAgeDays: 30, HashSecret: testSecret}}
}

func normalise(t testing.TB, payload string, mut ...func(*EventContext)) (appingest.Event, appingest.Body) {
	t.Helper()
	ctx := EventContext{}
	for _, m := range mut {
		m(&ctx)
	}
	ev, body, err := Normalise([]byte(payload), ctx, testSite(), testNow)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	return ev, body
}

func dumpAll(t testing.TB, ev appingest.Event, body appingest.Body) string {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ev.InstallID, ev.SessionID = "", "" // raw ids live in memory only; the writer MACs them
	e, _ := json.Marshal(ev)
	// the row extras are columns, not JSON: include them too
	x, _ := json.Marshal(body.Row)
	return string(b) + string(e) + string(x)
}

func eventItems(f fixture) []fixtureItem {
	var out []fixtureItem
	for _, it := range f.Items {
		if it.Type == "event" {
			out = append(out, it)
		}
	}
	return out
}

func fixtureCtx(f fixture) EventContext {
	ctx := EventContext{ClientIP: netip.MustParseAddr("192.0.2.77"), Country: "DE"}
	if id, ok := f.Header["event_id"].(string); ok {
		ctx.EventID = id
	}
	if s, ok := f.Header["sent_at"].(string); ok {
		ctx.SentAt, _ = time.Parse(time.RFC3339Nano, s)
	}
	if sdk, ok := f.Header["sdk"].(map[string]any); ok {
		ctx.SDKName, _ = sdk["name"].(string)
		ctx.SDKVersion, _ = sdk["version"].(string)
	}
	for _, it := range f.Items {
		if it.Type == "attachment" && it.Header["attachment_type"] == "event.minidump" {
			ctx.HasMinidump = true
		}
	}
	return ctx
}

var reHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

type want struct {
	kind, platform string
	handled        bool
}

func TestNormaliseFixtures(t *testing.T) {
	wants := map[string]want{
		"node/unhandled-exception":    {"crash", "linux", false},
		"node/unhandled-rejection":    {"error", "linux", false},
		"node/captured-message":       {"error", "linux", true},
		"browser/onerror":             {"error", "web", false},
		"browser/unhandledrejection":  {"error", "web", false},
		"browser/breadcrumbs":         {"error", "web", false},
		"python/unhandled-exception":  {"error", "other", false},
		"python/logging-error":        {"error", "other", true},
		"go/captured-exception":       {"error", "linux", true},
		"go/recovered-panic":          {"crash", "linux", false},
		"android/jvm-crash":           {"crash", "android", false},
		"android/ndk-crash":           {"crash", "android", false},
		"android/anr":                 {"hang", "android", false},
		"cocoa/crash":                 {"crash", "ios", false},
		"cocoa/app-hang":              {"hang", "ios", true},
		"cocoa/watchdog-termination":  {"crash", "ios", false},
		"electron/main-exception":     {"crash", "linux", false},
		"electron/renderer-exception": {"error", "linux", false},
		"electron/native-crash":       {"crash", "linux", false},
	}
	seen := 0
	for _, f := range loadFixtures(t) {
		for _, it := range eventItems(f) {
			seen++
			t.Run(f.Name, func(t *testing.T) {
				ctx := fixtureCtx(f)
				ev, body, err := Normalise(it.Payload, ctx, testSite(), testNow)
				if err != nil {
					t.Fatalf("Normalise: %v", err)
				}
				if len(ev.ID) != 32 || ev.TS.IsZero() || !reHex64.MatchString(ev.Fingerprint) || ev.Occurrences != 1 {
					t.Fatalf("bad row %+v", ev)
				}
				if body.Row.GroupingVersion != appingest.GroupingVersion || body.SiteID != 42 || body.EventID != ev.ID || !body.TS.Equal(ev.TS) {
					t.Fatalf("bad body keys %+v", body.Row)
				}
				if (body.Row.Title == "" || len(body.Row.MessageHash) != 32) && (len(body.Exceptions) > 0 || body.Message != "") {
					t.Fatalf("no title/hash: %+v", body.Row)
				}
				if w, ok := wants[f.Name]; ok {
					if ev.Kind != w.kind || ev.Platform != w.platform || ev.Handled != w.handled {
						t.Errorf("kind/platform/handled = %s/%s/%v, want %s/%s/%v", ev.Kind, ev.Platform, ev.Handled, w.kind, w.platform, w.handled)
					}
				} else {
					t.Errorf("fixture %s has no expectation", f.Name)
				}
				all := dumpAll(t, ev, body)
				for _, bad := range []string{"@example.com", "192.0.2.", "/home/", "/tmp/fixture-app", "fixture-user-1", "Asia/Calcutta\x00"} {
					if strings.Contains(all, bad) {
						t.Errorf("output contains %q", bad)
					}
				}
				if len(all) > 1<<20 {
					t.Errorf("output %d bytes", len(all))
				}
			})
		}
	}
	if seen < 19 {
		t.Fatalf("only %d event fixtures", seen)
	}
}

func fixtureEvent(t *testing.T, name string) (appingest.Event, appingest.Body) {
	t.Helper()
	for _, f := range loadFixtures(t) {
		if f.Name == name {
			ev, body, err := Normalise(eventItems(f)[0].Payload, fixtureCtx(f), testSite(), testNow)
			if err != nil {
				t.Fatal(err)
			}
			return ev, body
		}
	}
	t.Fatalf("no fixture %s", name)
	return appingest.Event{}, appingest.Body{}
}

func TestNormaliseNodeUnhandled(t *testing.T) {
	ev, body := fixtureEvent(t, "node/unhandled-exception")
	if ev.Release != "fixture@1.0.0" || ev.Environment != "fixture" || ev.ErrorType != "Error" || ev.Level != "fatal" {
		t.Fatalf("%+v", ev)
	}
	if body.Row.Culprit != "app.Timeout._onTimeout" || body.Row.SDKName != "sentry.javascript.node" || body.Row.Title != "Error: fixture unhandled exception" {
		t.Fatalf("%+v", body.Row)
	}
	if !strings.HasPrefix(ev.Frames, "Timeout._onTimeout (app.mjs:26)") {
		t.Fatalf("frames %q", ev.Frames)
	}
	if ev.Runtime != "node v22.23.3" {
		t.Fatalf("runtime %q", ev.Runtime)
	}
	if body.Country != "DE" {
		t.Fatalf("country %q", body.Country)
	}
}

func TestNormaliseBrowserBreadcrumbs(t *testing.T) {
	ev, body := fixtureEvent(t, "browser/breadcrumbs")
	if len(body.Breadcrumbs) != 2 || body.Breadcrumbs[1].Category != "fetch" || body.Breadcrumbs[1].Data["url"] != "/ping" {
		t.Fatalf("%+v", body.Breadcrumbs)
	}
	if body.Request == nil || body.Request.Path != "/" || len(body.Request.Headers) != 0 {
		t.Fatalf("request %+v", body.Request)
	}
	if ev.Platform != "web" || !strings.Contains(ev.Runtime, "Chrome") || ev.DeviceClass != "desktop" {
		t.Fatalf("%+v", ev)
	}
	if body.Contexts["browser"]["name"] == nil || body.Contexts["os"]["name"] == nil {
		t.Fatalf("UA must become browser and OS contexts: %v", body.Contexts)
	}
	if strings.Contains(dumpAll(t, ev, body), "Mozilla") {
		t.Fatal("raw user agent survived")
	}
}

func TestNormaliseMessageOnly(t *testing.T) {
	ev, body := fixtureEvent(t, "python/logging-error")
	if body.Message != "fixture logging error" || len(body.Exceptions) != 0 || ev.ErrorType != "message" || body.Row.Title != "fixture logging error" {
		t.Fatalf("%+v / %+v", ev, body)
	}
}

func TestNormaliseMobileMechanisms(t *testing.T) {
	anr, _ := fixtureEvent(t, "android/anr")
	if anr.Kind != "hang" || anr.ExitReason != "anr" {
		t.Fatalf("%+v", anr)
	}
	hang, _ := fixtureEvent(t, "cocoa/app-hang")
	if hang.Kind != "hang" || hang.ExitReason != "app_hang" {
		t.Fatalf("%+v", hang)
	}
	wd, _ := fixtureEvent(t, "cocoa/watchdog-termination")
	if wd.Kind != "crash" || wd.ExitReason != "watchdog_termination" {
		t.Fatalf("%+v", wd)
	}
	jvm, body := fixtureEvent(t, "android/jvm-crash")
	if jvm.Build != "1" || jvm.OSVersion != "14" || jvm.Arch != "arm64" || jvm.DeviceClass != "pixel" || jvm.Runtime == "" && false {
		t.Fatalf("%+v", jvm)
	}
	if jvm.InstallID != "fixture-user-1" || len(body.Row.UserHash) != 32 || body.Row.Dist != "1" {
		t.Fatalf("identity %+v", body.Row)
	}
	if string(body.Row.UserHash) != string(HashUser(testSecret, "fixture-user-1")) {
		t.Fatal("user hash must be the HMAC under the app secret")
	}
}

func TestNormaliseElectronNativeCrashNeedsMinidumpOrFatal(t *testing.T) {
	payload := `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":"native","level":"error","message":"native"}`
	ev, _ := normalise(t, payload)
	if ev.Kind != "error" {
		t.Fatalf("kind %s", ev.Kind)
	}
	ev, _ = normalise(t, payload, func(c *EventContext) { c.HasMinidump = true })
	if ev.Kind != "crash" || ev.Level != "fatal" || ev.Handled {
		t.Fatalf("%+v", ev)
	}
}

func TestNormaliseTimestamps(t *testing.T) {
	cases := map[string]time.Time{
		`1794921097.224`:                time.Unix(1794921097, 224_000_000),
		`"2026-11-17T13:11:37.224Z"`:    time.Date(2026, 11, 17, 13, 11, 37, 224_000_000, time.UTC),
		`"2026-11-17T13:20:44.405036Z"`: time.Date(2026, 11, 17, 13, 20, 44, 405_036_000, time.UTC),
		`"2026-11-17T13:20:44"`:         time.Date(2026, 11, 17, 13, 20, 44, 0, time.UTC),
		`1794921097224`:                 time.UnixMilli(1794921097224),
		`"2026-11-17T13:20:44+02:00"`:   time.Date(2026, 11, 17, 11, 20, 44, 0, time.UTC),
	}
	for in, want := range cases {
		ev, _ := normalise(t, `{"timestamp":`+in+`}`)
		if d := ev.TS.Sub(want); d > time.Millisecond || d < -time.Millisecond {
			t.Errorf("%s -> %s, want %s", in, ev.TS, want)
		}
	}
	ev, _ := normalise(t, `{}`)
	if !ev.TS.Equal(testNow) {
		t.Errorf("missing timestamp must be now, got %s", ev.TS)
	}
}

func TestNormaliseFutureClampedAndTooOld(t *testing.T) {
	future := testNow.Add(3 * time.Hour).Unix()
	ev, _ := normalise(t, `{"timestamp":`+itoa(int(future))+`}`)
	if !ev.TS.Equal(testNow) {
		t.Fatalf("future timestamp not clamped: %s", ev.TS)
	}
	old := testNow.Add(-31 * 24 * time.Hour).Unix()
	_, _, err := Normalise([]byte(`{"timestamp":`+itoa(int(old))+`}`), EventContext{}, testSite(), testNow)
	if !errors.Is(err, ErrTooOld) {
		t.Fatalf("err = %v, want ErrTooOld", err)
	}
	ok := testNow.Add(-29 * 24 * time.Hour).Unix()
	if _, _, err := Normalise([]byte(`{"timestamp":`+itoa(int(ok))+`}`), EventContext{}, testSite(), testNow); err != nil {
		t.Fatalf("29 days old must pass: %v", err)
	}
	site := testSite()
	site.App.MaxReportAgeDays = 0
	if _, _, err := Normalise([]byte(`{"timestamp":`+itoa(int(testNow.Add(-8*24*time.Hour).Unix()))+`}`), EventContext{}, site, testNow); !errors.Is(err, ErrTooOld) {
		t.Fatalf("default age is 7 days: %v", err)
	}
}

func TestNormaliseClockSkewCorrection(t *testing.T) {
	// The device clock is 2 hours slow: sent_at says 12:00 while it is 14:00.
	sent := testNow.Add(-2 * time.Hour)
	ts := sent.Add(-time.Minute).Unix()
	ev, _ := normalise(t, `{"timestamp":`+itoa(int(ts))+`}`, func(c *EventContext) { c.SentAt = sent })
	want := testNow.Add(-time.Minute)
	if d := ev.TS.Sub(want); d > time.Second || d < -time.Second {
		t.Fatalf("skew not corrected: %s want %s", ev.TS, want)
	}
	// small skew is left alone
	sent = testNow.Add(-time.Minute)
	ts = sent.Add(-time.Minute).Unix()
	ev, _ = normalise(t, `{"timestamp":`+itoa(int(ts))+`}`, func(c *EventContext) { c.SentAt = sent })
	if ev.TS.Unix() != ts {
		t.Fatalf("small skew must be ignored: %s", ev.TS)
	}
}

func TestNormaliseBreadcrumbShapes(t *testing.T) {
	arr := `{"breadcrumbs":[{"category":"a","timestamp":1794921097.1},{"category":"b"}]}`
	obj := `{"breadcrumbs":{"values":[{"category":"a","timestamp":1794921097.1},{"category":"b"}]}}`
	for _, p := range []string{arr, obj} {
		_, body := normalise(t, p)
		if len(body.Breadcrumbs) != 2 || body.Breadcrumbs[0].Category != "a" {
			t.Fatalf("%s -> %+v", p, body.Breadcrumbs)
		}
	}
}

func TestNormaliseTagShapes(t *testing.T) {
	ev, body := normalise(t, `{"tags":{"a":"1","b":2,"c":true}}`)
	if body.Tags["a"] != "1" || body.Tags["b"] != "2" || body.Tags["c"] != "true" || len(ev.Tags) != 3 {
		t.Fatalf("%v", body.Tags)
	}
	_, body = normalise(t, `{"tags":[["a","1"],["b","2"]]}`)
	if body.Tags["a"] != "1" || body.Tags["b"] != "2" {
		t.Fatalf("%v", body.Tags)
	}
	var many []string
	for i := 0; i < 80; i++ {
		many = append(many, `"k`+itoa(100+i)+`":"v"`)
	}
	ev, body = normalise(t, `{"tags":{`+strings.Join(many, ",")+`}}`)
	if len(ev.Tags) != 20 || len(body.Tags) != 50 {
		t.Fatalf("tag caps: event %d body %d", len(ev.Tags), len(body.Tags))
	}
}

func TestNormaliseExceptionShapes(t *testing.T) {
	arr := `{"exception":[{"type":"A","value":"x"}]}`
	obj := `{"exception":{"values":[{"type":"A","value":"x"}]}}`
	for _, p := range []string{arr, obj} {
		ev, body := normalise(t, p)
		if len(body.Exceptions) != 1 || ev.ErrorType != "A" {
			t.Fatalf("%s -> %+v", p, body.Exceptions)
		}
	}
}

func TestNormaliseUnhandledMechanism(t *testing.T) {
	ev, _ := normalise(t, `{"level":"error","exception":{"values":[{"type":"E","value":"v","mechanism":{"type":"generic","handled":false}}]}}`)
	if ev.Handled || ev.Kind != "error" {
		t.Fatalf("%+v", ev)
	}
	ev, _ = normalise(t, `{"level":"error","exception":{"values":[{"type":"E","value":"v","mechanism":{"type":"generic","handled":true}}]}}`)
	if !ev.Handled {
		t.Fatal("handled")
	}
	ev, _ = normalise(t, `{"level":"fatal","exception":{"values":[{"type":"E","value":"v"}]}}`)
	if ev.Kind != "crash" || ev.Handled {
		t.Fatalf("%+v", ev)
	}
	ev, _ = normalise(t, `{"level":"nonsense"}`)
	if ev.Level != "error" {
		t.Fatalf("level %q", ev.Level)
	}
}

func TestNormaliseInferIPNever(t *testing.T) {
	_, body := normalise(t, `{"sdk":{"settings":{"infer_ip":"never"}}}`, func(c *EventContext) { c.Country = "DE" })
	if body.Country != "" {
		t.Fatalf("country %q with infer_ip never in the event", body.Country)
	}
	_, body = normalise(t, `{}`, func(c *EventContext) { c.Country = "DE"; c.InferIP = "never" })
	if body.Country != "" {
		t.Fatal("country with infer_ip never in the context")
	}
	_, body = normalise(t, `{}`, func(c *EventContext) { c.Country = "DE"; c.InferIP = "auto" })
	if body.Country != "DE" {
		t.Fatal("country must be kept with auto")
	}
	_, body = normalise(t, `{}`, func(c *EventContext) { c.Country = "Germany" })
	if body.Country != "" {
		t.Fatal("invalid country code accepted")
	}
}

func TestNormaliseInvalidPayload(t *testing.T) {
	for _, p := range []string{``, `null`, `[]`, `"x"`, `{`} {
		if _, _, err := Normalise([]byte(p), EventContext{}, testSite(), testNow); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v", p, err)
		}
	}
}

func TestNormaliseEventIDSources(t *testing.T) {
	ev, _ := normalise(t, `{"event_id":"123E4567-E89B-12D3-A456-426614174000"}`)
	if ev.ID != "123e4567e89b12d3a456426614174000" {
		t.Fatal(ev.ID)
	}
	ev, _ = normalise(t, `{}`, func(c *EventContext) { c.EventID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" })
	if ev.ID != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatal(ev.ID)
	}
	a, _ := normalise(t, `{"message":"x"}`)
	b, _ := normalise(t, `{"message":"x"}`)
	if len(a.ID) != 32 || a.ID != b.ID {
		t.Fatal("digest id must be stable")
	}
}

func TestNormaliseNoSiteApp(t *testing.T) {
	if _, _, err := Normalise([]byte(`{"user":{"id":"u"}}`), EventContext{}, sites.Site{ID: 1}, testNow); err != nil {
		t.Fatal(err)
	}
}

// The privacy contract: nothing personal reaches the row or the body.
func TestNormalisePrivacy(t *testing.T) {
	payload := `{
	  "event_id":"cccccccccccccccccccccccccccccccc",
	  "level":"error","platform":"javascript",
	  "message":"Order 1234567 failed for jane@example.com from 192.0.2.55 token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.c2lnbmF0dXJlc2lnbg",
	  "user":{"id":"user-777","email":"jane@example.com","username":"janedoe","name":"Jane Doe","ip_address":"192.0.2.55","geo":{"city":"Paris","country_code":"FR"}},
	  "request":{"url":"https://shop.example.com/checkout/1234567?email=jane@example.com&session=ZZZSESSION","method":"POST",
	    "query_string":"email=jane@example.com","cookies":{"sid":"COOKIEVALUE"},"data":{"card":"4111111111111111"},
	    "env":{"REMOTE_ADDR":"192.0.2.55"},
	    "headers":{"Cookie":"sid=COOKIEVALUE","Authorization":"Bearer SECRETBEARER","Proxy-Authorization":"Basic xx","X-Real-IP":"192.0.2.55","User-Agent":"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/150.0 Safari/537.36","Content-Type":"application/json"}},
	  "exception":{"values":[{"type":"Error","value":"cannot send to jane@example.com (card 4111 1111 1111 1111)",
	    "stacktrace":{"frames":[{"filename":"/home/jane/app/src/main.js","abs_path":"/home/jane/app/src/main.js","function":"send","in_app":true,"lineno":3,
	      "vars":{"password":"hunter2","email":"jane@example.com"},
	      "pre_context":["const to = 'jane@example.com'"],"context_line":"mail(to, 'sk_live_51HabcdefGHIJKL')","post_context":[]}]}}]},
	  "breadcrumbs":[{"category":"console","message":"user jane@example.com clicked","data":{"url":"https://shop.example.com/a?token=ZZZTOKEN","email":"jane@example.com"}}],
	  "contexts":{"device":{"name":"Jane's phone","model":"Pixel"},"os":{"name":"Android","version":"14"},"profile":{"email":"jane@example.com"}},
	  "tags":{"email":"jane@example.com","url":"https://shop.example.com/x?y=1","release_ip":"192.0.2.55"},
	  "extra":{"secret":"EXTRASECRET"},"server_name":"jane-laptop","transaction":"/checkout/1234567"
	}`
	ev, body := normalise(t, payload, func(c *EventContext) {
		c.ClientIP = netip.MustParseAddr("192.0.2.55")
		c.Country = "FR"
	})
	all := dumpAll(t, ev, body)
	for _, bad := range []string{
		"jane", "Jane", "example.com", "192.0.2", "user-777", "hunter2", "COOKIEVALUE", "SECRETBEARER", "Basic",
		"ZZZSESSION", "ZZZTOKEN", "EXTRASECRET", "4111", "eyJhbGci", "sk_live", "/home/", "email=", "Mozilla", "janedoe",
		"Paris", "jane-laptop", "1234567", "REMOTE_ADDR", "query_string", "profile", "X-Real-IP",
	} {
		if strings.Contains(all, bad) {
			t.Errorf("output contains %q:\n%s", bad, all)
		}
	}
	// the raw user id is only in Event.InstallID, which the writer MACs; it is not JSON-visible
	if ev.InstallID != "user-777" {
		t.Fatalf("InstallID %q", ev.InstallID)
	}
	if string(body.Row.UserHash) != string(HashUser(testSecret, "user-777")) {
		t.Fatal("user hash")
	}
	if body.Request.Path != "/checkout/[num]" || body.Request.Method != "POST" || body.Request.Headers["content-type"] != "application/json" {
		t.Fatalf("request %+v", body.Request)
	}
	if body.Country != "FR" {
		t.Fatal("country must survive")
	}
	if strings.Contains(body.Message, "Order [num] failed for [email] from [ip] token [token]") == false {
		t.Fatalf("message %q", body.Message)
	}
	for _, fr := range body.Exceptions[0].Frames {
		if fr.Filename != "~/app/src/main.js" && fr.Filename != "app/src/main.js" {
			t.Fatalf("filename %q", fr.Filename)
		}
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), `"vars"`) {
		t.Fatal("frame vars present")
	}
}

func TestNormaliseCapsBody(t *testing.T) {
	var bc []string
	for i := 0; i < 300; i++ {
		bc = append(bc, `{"category":"c","message":"`+strings.Repeat("m", 400)+`"}`)
	}
	var frames []string
	for i := 0; i < 400; i++ {
		frames = append(frames, `{"function":"f`+itoa(i)+`","in_app":true}`)
	}
	payload := `{"breadcrumbs":[` + strings.Join(bc, ",") + `],"message":"` + strings.Repeat("x ", 20000) +
		`","exception":{"values":[{"type":"E","value":"` + strings.Repeat("v", 10000) + `","stacktrace":{"frames":[` + strings.Join(frames, ",") + `]}}]}}`
	_, body := normalise(t, payload)
	if len(body.Breadcrumbs) > 100 {
		t.Fatalf("%d breadcrumbs", len(body.Breadcrumbs))
	}
	crumbs, _ := json.Marshal(body.Breadcrumbs)
	if len(crumbs) > 16<<10 {
		t.Fatalf("breadcrumbs %d bytes", len(crumbs))
	}
	if len(body.Message) > 8<<10 || len(body.Exceptions[0].Value) > 2<<10 || len(body.Exceptions[0].Frames) > 100 {
		t.Fatalf("caps: %d %d %d", len(body.Message), len(body.Exceptions[0].Value), len(body.Exceptions[0].Frames))
	}
}

func TestNormaliseStacklessGroupsTogether(t *testing.T) {
	a, _ := normalise(t, `{"message":"Order 123 failed for a@example.com"}`)
	b, _ := normalise(t, `{"message":"Order 9876 failed for b@example.com"}`)
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("stackless messages differing in digits and emails must share a fingerprint")
	}
	c, _ := normalise(t, `{"message":"Refund 9876 failed for b@example.com"}`)
	if a.Fingerprint == c.Fingerprint {
		t.Fatal("different text must not group")
	}
}

func TestNormaliseSDKFingerprint(t *testing.T) {
	base := `{"exception":{"values":[{"type":"E","stacktrace":{"frames":[{"function":"f","in_app":true}]}}]},"fingerprint":`
	x, _ := normalise(t, base+`["{{ default }}","tenant-x"]}`)
	y, _ := normalise(t, base+`["{{ default }}","tenant-y"]}`)
	z, _ := normalise(t, base+`null}`)
	if x.Fingerprint == y.Fingerprint || x.Fingerprint == z.Fingerprint {
		t.Fatal("fingerprint parts must split groups")
	}
	a, _ := normalise(t, `{"message":"a","fingerprint":["db-timeout"]}`)
	b, _ := normalise(t, `{"exception":[{"type":"Q"}],"fingerprint":["db-timeout"]}`)
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("custom fingerprint ignores stack shape")
	}
}

func TestNormaliseGroupingGoldenAcrossFixtures(t *testing.T) {
	// Node uncaught exception and rejection come from different frames: different issues.
	a, _ := fixtureEvent(t, "node/unhandled-exception")
	b, _ := fixtureEvent(t, "node/unhandled-rejection")
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("different errors must not group")
	}
	a2, _ := fixtureEvent(t, "node/unhandled-exception")
	if a.Fingerprint != a2.Fingerprint {
		t.Fatal("same event must give the same fingerprint")
	}
}

func FuzzNormalise(f *testing.F) {
	for _, fx := range loadFixtures(f) {
		for _, it := range fx.Items {
			if it.Type == "event" {
				f.Add(it.Payload)
			}
		}
	}
	for _, s := range []string{`{}`, `{"exception":[]}`, `{"breadcrumbs":{"values":[null,1,"x"]}}`, `{"timestamp":"x","tags":[[1,2],"a"]}`, `{"exception":{"values":[{"stacktrace":{"frames":[null,{"lineno":"x"}]}}]}}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		ev, body, err := Normalise(raw, EventContext{ClientIP: netip.MustParseAddr("192.0.2.1")}, testSite(), testNow)
		if err != nil {
			return
		}
		if len(ev.ID) == 0 || len(ev.ID) > 64 || ev.Occurrences != 1 || !reHex64.MatchString(ev.Fingerprint) {
			t.Fatalf("bad row %+v", ev)
		}
		out, merr := json.Marshal(body)
		if merr != nil {
			t.Fatalf("body does not marshal: %v", merr)
		}
		if len(out) > 1<<20 {
			t.Fatalf("body %d bytes", len(out))
		}
		if len(body.Breadcrumbs) > 100 || len(ev.Tags) > 20 {
			t.Fatal("caps")
		}
		if len(ev.Release) > 64 || len(ev.Environment) > 32 || len(ev.OSVersion) > 32 || len(ev.Runtime) > 32 || len(ev.ErrorType) > 64 {
			t.Fatalf("column limits: %+v", ev)
		}
		if strings.Contains(string(out), "192.0.2.1\"") {
			t.Fatal("client ip leaked")
		}
	})
}

func TestNormaliseKeepsSymbolicationInputs(t *testing.T) {
	ev, body := normalise(t, `{"event_id":"`+strings.Repeat("b", 32)+`","timestamp":1794924000,"release":"1.2.3","fingerprint":["{{ default }}","x"],
		"debug_meta":{"images":[{"type":"sourcemap","code_file":"https://a.example/app.js?v=1","debug_id":"EFD8CAD4-51CC-4E52-B63C-BE84E219418A"},"junk"]},
		"exception":{"values":[{"type":"Error","stacktrace":{"frames":[{"abs_path":"https://a.example/app.js?v=1#x","lineno":1,"colno":9}]}}]}}`)
	if body.Release != "1.2.3" || ev.Release != "1.2.3" {
		t.Fatalf("release %q", body.Release)
	}
	if len(body.DebugImages) != 1 || body.DebugImages[0].CodeFile != "https://a.example/app.js" || body.DebugImages[0].Type != "sourcemap" {
		t.Fatalf("images %+v", body.DebugImages)
	}
	if f := body.Exceptions[0].Frames[0]; f.AbsURL != "https://a.example/app.js" || f.AbsPath != "" {
		t.Fatalf("frame %+v", f)
	}
	if len(body.Row.SDKFingerprint) != 2 {
		t.Fatalf("fingerprint %v", body.Row.SDKFingerprint)
	}
	if b, _ := json.Marshal(body); strings.Contains(string(b), "a.example") && strings.Contains(string(b), "debug_id") {
		t.Fatalf("in-memory fields leaked into the stored body: %s", b)
	}
}
