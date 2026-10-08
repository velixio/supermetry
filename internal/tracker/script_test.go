package tracker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"panel/internal/config"
	"panel/internal/replay"
	"panel/internal/sites"
)

func noReplay() replay.SiteSettings { return replay.SiteSettings{} }

func TestBuildScriptDefaultsToFullTracker(t *testing.T) {
	t.Parallel()

	const publicID = "abc123def456"
	got := BuildScript(publicID, (sites.TrackerSettingsInput{}).Resolve(true), noReplay())
	wantPrelude := `(d=>{d.site??="abc123def456";d.api??=new URL(document.currentScript.src).origin+"/api/public/v1/events";d.heatmap??="true";})(document.currentScript.dataset);` + "\n"
	if !bytes.HasPrefix(got.Body, []byte(wantPrelude)) {
		t.Fatalf("body prefix = %q, want %q", got.Body[:min(len(got.Body), len(wantPrelude))], wantPrelude)
	}
	if !bytes.Equal(got.Body[len(wantPrelude):], fullScript) {
		t.Fatal("default script does not contain the embedded full tracker")
	}
	if len(wantPrelude) >= 300 {
		t.Fatalf("prelude = %d bytes, want under 300", len(wantPrelude))
	}
	if len(got.ETag) != 18 || got.ETag[0] != '"' || got.ETag[len(got.ETag)-1] != '"' {
		t.Fatalf("ETag = %q, want quoted 16-character hex digest", got.ETag)
	}
	digest := sha256.Sum256(got.Body)
	wantETag := `"` + hex.EncodeToString(digest[:])[:16] + `"`
	if got.ETag != wantETag {
		t.Fatalf("ETag = %q, want %q", got.ETag, wantETag)
	}
}

func TestBuildScriptEmitsOnlyNonDefaultSwitches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings sites.TrackerSettings
		want     string
	}{
		{name: "outbound off", settings: sites.TrackerSettings{Downloads: true, Forms: true, Vitals: true, NotFound: true}, want: `d.outbound??="false";`},
		{name: "downloads off", settings: sites.TrackerSettings{Outbound: true, Forms: true, Vitals: true, NotFound: true}, want: `d.downloads??="false";`},
		{name: "forms off", settings: sites.TrackerSettings{Outbound: true, Downloads: true, Vitals: true, NotFound: true}, want: `d.forms??="false";`},
		{name: "vitals off", settings: sites.TrackerSettings{Outbound: true, Downloads: true, Forms: true, NotFound: true}, want: `d.vitals??="false";`},
		{name: "not found off", settings: sites.TrackerSettings{Outbound: true, Downloads: true, Forms: true, Vitals: true}, want: `d[404]??="false";`},
		{name: "clicks on", settings: sites.TrackerSettings{Outbound: true, Downloads: true, Forms: true, Vitals: true, NotFound: true, Clicks: true}, want: `d.heatmap??="true";`},
		{name: "hash on", settings: sites.TrackerSettings{Outbound: true, Downloads: true, Forms: true, Vitals: true, NotFound: true, Hash: true}, want: `d.hash??="true";`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := string(BuildScript("abc123def456", test.settings, noReplay()).Body)
			prelude, _, ok := strings.Cut(body, "\n")
			if !ok {
				t.Fatal("script has no prelude separator")
			}
			if !strings.Contains(prelude, test.want) {
				t.Fatalf("prelude = %q, want assignment %q", prelude, test.want)
			}
			if got := strings.Count(prelude, "??="); got != 3 {
				t.Fatalf("prelude = %q, nullish assignments = %d, want site, endpoint and one switch", prelude, got)
			}
		})
	}
}

func TestBuildScriptPreludeStaysUnderLimit(t *testing.T) {
	t.Parallel()

	settings := sites.TrackerSettings{Hash: true}
	body := string(BuildScript("abc123def456", settings, noReplay()).Body)
	prelude, _, ok := strings.Cut(body, "\n")
	if !ok {
		t.Fatal("script has no prelude separator")
	}
	if got := len(prelude) + 1; got >= 300 {
		t.Fatalf("largest prelude = %d bytes, want under 300", got)
	}
}

func TestBuildScriptUsesCoreWhenEveryExtensionIsOff(t *testing.T) {
	t.Parallel()

	got := BuildScript("abc123def456", sites.TrackerSettings{}, noReplay())
	_, source, ok := bytes.Cut(got.Body, []byte("\n"))
	if !ok {
		t.Fatal("script has no prelude separator")
	}
	if !bytes.Equal(source, script) {
		t.Fatal("all-off settings do not select the core tracker")
	}
	if bytes.Contains(got.Body, []byte("Form: Submit")) {
		t.Fatal("core generated script contains full-tracker form support")
	}
}

func TestBuildScriptIncludesSiteInBodyAndETag(t *testing.T) {
	t.Parallel()

	settings := (sites.TrackerSettingsInput{}).Resolve(true)
	first := BuildScript("abc123def456", settings, noReplay())
	second := BuildScript("xyz987uvw654", settings, noReplay())
	if bytes.Equal(first.Body, second.Body) {
		t.Fatal("different site IDs produced identical bodies")
	}
	if first.ETag == second.ETag {
		t.Fatalf("different site IDs produced identical ETag %q", first.ETag)
	}
}

type scriptRegistry map[string]sites.Site

func (r scriptRegistry) ByPublicID(id string) (sites.Site, bool) { site, ok := r[id]; return site, ok }
func (r scriptRegistry) Site(id int32) (sites.Site, bool) {
	for _, site := range r {
		if site.ID == id {
			return site, true
		}
	}
	return sites.Site{}, false
}
func (r scriptRegistry) All() []sites.Site { return nil }

func TestSiteHandlerServesAndRevalidatesGeneratedScript(t *testing.T) {
	registry := scriptRegistry{"abc123def456": {PublicID: "abc123def456", HeatmapsEnabled: true}}
	handler := SiteHandler(registry, config.ReplayEnv{})

	request := httptest.NewRequest(http.MethodGet, "/js/abc123def456.js", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", response.Code)
	}
	for name, want := range map[string]string{
		"Content-Type":  "application/javascript",
		"Cache-Control": "public, max-age=60",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	etag := response.Header().Get("ETag")
	if etag == "" || response.Body.Len() == 0 {
		t.Fatalf("ETag = %q, body length = %d", etag, response.Body.Len())
	}

	notModifiedRequest := httptest.NewRequest(http.MethodGet, "/js/abc123def456.js", nil)
	notModifiedRequest.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	handler.ServeHTTP(notModified, notModifiedRequest)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional GET status = %d, body = %q", notModified.Code, notModified.Body.String())
	}

	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/js/abc123def456.js", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("ETag") != etag {
		t.Fatalf("HEAD status = %d, body length = %d, ETag = %q", head.Code, head.Body.Len(), head.Header().Get("ETag"))
	}
}

func TestSiteHandlerRejectsUnknownAndMalformedIDs(t *testing.T) {
	t.Parallel()

	handler := SiteHandler(scriptRegistry{"abc123def456": {PublicID: "abc123def456"}}, config.ReplayEnv{})
	tests := []struct {
		name string
		path string
	}{
		{name: "unknown", path: "/js/zzz123def456.js"},
		{name: "slash", path: "/js/abc123/ef456.js"},
		{name: "dot dot", path: "/js/abc123..f456.js"},
		{name: "encoded traversal", path: "/js/abc123..%2fxx.js"},
		{name: "encoded traversal before valid id", path: "/js/..%2fabc123def456.js"},
		{name: "upper case", path: "/js/Abc123def456.js"},
		{name: "extra extension", path: "/js/abc123def456.js.map"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", response.Code)
			}
		})
	}
}

func TestSiteHandlerCachesBySiteAndSettings(t *testing.T) {
	registry := scriptRegistry{"abc123def456": {PublicID: "abc123def456", HeatmapsEnabled: true}}
	originalBuilder := siteScriptBuilder
	t.Cleanup(func() { siteScriptBuilder = originalBuilder })
	builds := 0
	siteScriptBuilder = func(publicID string, settings sites.TrackerSettings, replaySettings replay.SiteSettings) Script {
		builds++
		return BuildScript(publicID, settings, replaySettings)
	}
	handler := SiteHandler(registry, config.ReplayEnv{})
	var first []byte
	for requestNumber := range 2 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/js/abc123def456.js", nil))
		if requestNumber == 0 {
			first = append([]byte(nil), response.Body.Bytes()...)
		} else if !bytes.Equal(first, response.Body.Bytes()) {
			t.Fatal("cached response body changed")
		}
	}
	if builds != 1 {
		t.Fatalf("builder calls = %d, want 1", builds)
	}

	registry["abc123def456"] = sites.Site{PublicID: "abc123def456", HeatmapsEnabled: false}
	changed := httptest.NewRecorder()
	handler.ServeHTTP(changed, httptest.NewRequest(http.MethodGet, "/js/abc123def456.js", nil))
	if builds != 2 {
		t.Fatalf("builder calls after settings change = %d, want 2", builds)
	}
	if bytes.Equal(first, changed.Body.Bytes()) {
		t.Fatal("settings change reused stale cached body")
	}
}

func TestScriptHasNoReplayWhenDisabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  config.ReplayEnv
		site sites.ReplaySiteInput
	}{
		// PANEL_REPLAY_ENABLED is the only switch, so a stored site value cannot
		// turn replay on.
		{name: "instance off", site: sites.ReplaySiteInput{Enabled: true}},
		{name: "instance off, site never set"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			settings := replay.ResolveSite(test.env, test.site)
			body := BuildScript("abc123def456", (sites.TrackerSettingsInput{}).Resolve(false), settings).Body
			prelude, _, ok := bytes.Cut(body, []byte("\n"))
			if !ok {
				t.Fatal("script has no prelude separator")
			}
			if bytes.Contains(prelude, []byte("replay")) {
				t.Fatalf("disabled script prelude contains replay config: %q", prelude)
			}
		})
	}
}

func TestScriptHasReplayConfigWhenEnabled(t *testing.T) {
	t.Parallel()

	consent := "always"
	settings := replay.ResolveSite(config.ReplayEnv{Enabled: true}, sites.ReplaySiteInput{
		Enabled: true,
		Consent: &consent,
	})
	body := BuildScript("abc123def456", sites.TrackerSettings{}, settings).Body
	prelude, source, ok := bytes.Cut(body, []byte("\n"))
	if !ok {
		t.Fatal("script has no prelude separator")
	}
	if !bytes.Contains(prelude, []byte(`d.replay??="always";`)) {
		t.Fatalf("enabled script prelude = %q, want replay consent config", prelude)
	}
	if !bytes.Equal(source, fullScript) {
		t.Fatal("replay-enabled site did not receive the full tracker")
	}
}

func TestSitePath(t *testing.T) {
	t.Parallel()

	if got, want := SitePath("/assets/panel.js", "abc123def456"), "/assets/abc123def456.js"; got != want {
		t.Fatalf("SitePath = %q, want %q", got, want)
	}
}

// A site's snippet is a bare <script src>, so the generated script must point
// events at the Panel host it was loaded from, not at the page's own origin.
func TestBuildScriptSendsEventsToTheScriptOrigin(t *testing.T) {
	body := string(BuildScript("abc123def456", sites.TrackerSettings{}, noReplay()).Body)
	prelude, _, _ := strings.Cut(body, "\n")
	want := `d.api??=new URL(document.currentScript.src).origin+"/api/public/v1/events";`
	if !strings.Contains(prelude, want) {
		t.Fatalf("prelude %q does not set the absolute event endpoint", prelude)
	}
}

func TestBuildScriptUsesConfiguredEventPath(t *testing.T) {
	SetEventPath("/x/collect")
	t.Cleanup(func() { SetEventPath("") })
	body := string(BuildScript("abc123def456", sites.TrackerSettings{}, noReplay()).Body)
	if !strings.Contains(body, `origin+"/x/collect";`) {
		t.Fatalf("script does not use the configured event path:\n%s", body[:200])
	}
}
