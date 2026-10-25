package tracker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"supermetry/internal/config"
	"supermetry/internal/privacy"
	"supermetry/internal/replay"
	"supermetry/internal/sites"
)

func noReplay() replay.SiteSettings { return replay.SiteSettings{} }

func TestBuildScriptDefaultsToEnabledModules(t *testing.T) {
	t.Parallel()

	const publicID = "abc123def456"
	got := BuildScript(publicID, (sites.TrackerSettingsInput{}).Resolve(true), noReplay())
	wantPrelude := `(d=>{d.site??="abc123def456";d.api??=document.currentScript.src.replace(/\/js\/[^/?#]*([?#].*)?$/,"/api/public/v1/events");d.heatmap??="true";d.replay="";})(document.currentScript.dataset);` + "\n"
	if !bytes.HasPrefix(got.Body, []byte(wantPrelude)) {
		t.Fatalf("body prefix = %q, want %q", got.Body[:min(len(got.Body), len(wantPrelude))], wantPrelude)
	}
	for _, marker := range [][]byte{[]byte("Outbound Link: Click"), []byte("File Download"), []byte("Form: Submit")} {
		if !bytes.Contains(got.Body, marker) {
			t.Errorf("default script is missing enabled module %q", marker)
		}
	}
	if bytes.Contains(got.Body, []byte("/replay.js")) {
		t.Fatal("default script contains disabled replay module")
	}
	if !bytes.Contains(got.Body, []byte(`searchParams.set("module","vitals,heatmap")`)) {
		t.Fatal("default script does not lazily load Vitals and heatmaps")
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
		{name: "outbound off", settings: sites.TrackerSettings{Downloads: true, Forms: true, Vitals: true, NotFound: true}, want: `d.outbound="false";`},
		{name: "downloads off", settings: sites.TrackerSettings{Outbound: true, Forms: true, Vitals: true, NotFound: true}, want: `d.downloads="false";`},
		{name: "forms off", settings: sites.TrackerSettings{Outbound: true, Downloads: true, Vitals: true, NotFound: true}, want: `d.forms="false";`},
		{name: "vitals off", settings: sites.TrackerSettings{Outbound: true, Downloads: true, Forms: true, NotFound: true}, want: `d.vitals="false";`},
		{name: "not found off", settings: sites.TrackerSettings{Outbound: true, Downloads: true, Forms: true, Vitals: true}, want: `d[404]="false";`},
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
			wantDefaults := 2 + strings.Count(test.want, "??=")
			if got := strings.Count(prelude, "??="); got != wantDefaults {
				t.Fatalf("defaults=%d want=%d", got, wantDefaults)
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

func TestBuildScriptSelectsModuleForEachSetting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings sites.TrackerSettings
		marker   string
	}{
		{name: "outbound", settings: sites.TrackerSettings{Outbound: true}, marker: "Outbound Link: Click"},
		{name: "downloads", settings: sites.TrackerSettings{Downloads: true}, marker: "File Download"},
		{name: "forms", settings: sites.TrackerSettings{Forms: true}, marker: "Form: Submit"},
		{name: "vitals", settings: sites.TrackerSettings{Vitals: true}, marker: `searchParams.set("module","vitals")`},
		{name: "not found", settings: sites.TrackerSettings{NotFound: true}, marker: `track("404"`},
		{name: "hash routing", settings: sites.TrackerSettings{Hash: true}, marker: "hashchange"},
		{name: "clicks", settings: sites.TrackerSettings{Clicks: true}, marker: `searchParams.set("module","heatmap")`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := BuildScript("abc123def456", test.settings, noReplay())
			if !bytes.Contains(got.Body, []byte(test.marker)) {
				t.Fatalf("enabled setting did not select module marker %q", test.marker)
			}
		})
	}
}

func TestBuildScriptIncludesOnlyEnabledOptionalModules(t *testing.T) {
	t.Parallel()

	got := BuildScript("abc123def456", sites.TrackerSettings{Outbound: true}, noReplay()).Body
	if !bytes.Contains(got, []byte("Outbound Link: Click")) {
		t.Fatal("outbound-enabled script is missing outbound tracking")
	}
	for _, unwanted := range [][]byte{
		[]byte("File Download"),
		[]byte("Form: Submit"),
		[]byte("Web Vitals"),
		[]byte(`name:"heatmap"`),
		[]byte("/replay.js"),
	} {
		if bytes.Contains(got, unwanted) {
			t.Errorf("outbound-only script contains disabled module %q", unwanted)
		}
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

func TestSiteHandlerFullQuerySelectsFullTrackerWithDistinctETag(t *testing.T) {
	disabled := false
	registry := scriptRegistry{"abc123def456": {
		PublicID: "abc123def456",
		Tracker: sites.TrackerSettingsInput{
			Outbound: &disabled, Downloads: &disabled, Forms: &disabled,
			Vitals: &disabled, NotFound: &disabled, Hash: &disabled, Clicks: &disabled,
		},
	}}
	handler := SiteHandler(registry, config.ReplayEnv{})

	core := httptest.NewRecorder()
	handler.ServeHTTP(core, httptest.NewRequest(http.MethodGet, "/js/abc123def456.js", nil))
	full := httptest.NewRecorder()
	handler.ServeHTTP(full, httptest.NewRequest(http.MethodGet, "/js/abc123def456.js?full=1", nil))

	if !bytes.HasSuffix(core.Body.Bytes(), script) {
		t.Fatal("default request did not receive core tracker")
	}
	if !bytes.HasSuffix(full.Body.Bytes(), fullScript) {
		t.Fatal("full=1 request did not receive full tracker")
	}
	if core.Header().Get("ETag") == full.Header().Get("ETag") {
		t.Fatalf("core and full variants share ETag %q", core.Header().Get("ETag"))
	}

	conditional := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/js/abc123def456.js?full=1", nil)
	request.Header.Set("If-None-Match", full.Header().Get("ETag"))
	handler.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional full response = %d %q, want 304 with empty body", conditional.Code, conditional.Body.String())
	}

	wrongVariant := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/js/abc123def456.js", nil)
	request.Header.Set("If-None-Match", full.Header().Get("ETag"))
	handler.ServeHTTP(wrongVariant, request)
	if wrongVariant.Code != http.StatusOK {
		t.Fatalf("full ETag revalidated core variant with status %d", wrongVariant.Code)
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
	siteScriptBuilder = func(publicID string, settings sites.TrackerSettings, replaySettings replay.SiteSettings, forceFull bool) Script {
		builds++
		return buildScript(publicID, settings, replaySettings, forceFull)
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
		// Both instance permission and project opt-in are required.
		{name: "instance off", site: sites.ReplaySiteInput{Enabled: true}},
		{name: "instance off, site never set"},
		{name: "project off", env: config.ReplayEnv{Enabled: true}},
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
			if !bytes.Contains(prelude, []byte(`d.replay="";`)) {
				t.Fatalf("disabled script prelude must override supplied replay config: %q", prelude)
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
	if !bytes.Contains(prelude, []byte(`d.replay="always";`)) {
		t.Fatalf("enabled script prelude = %q, want replay consent config", prelude)
	}
	if !bytes.Contains(source, []byte("/replay.js")) {
		t.Fatal("replay-enabled site did not receive the replay bootstrap")
	}
}

func TestSiteHandlerServesOnlyEnabledLazyModules(t *testing.T) {
	enabled := true
	registry := scriptRegistry{"abc123def456": {
		PublicID: "abc123def456",
		Tracker:  sites.TrackerSettingsInput{Vitals: &enabled, Clicks: &enabled},
	}}
	handler := SiteHandler(registry, config.ReplayEnv{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/js/abc123def456.js?module=vitals,heatmap", nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("Web Vitals")) || !bytes.Contains(response.Body.Bytes(), []byte(`name:"heatmap"`)) {
		t.Fatalf("enabled module response = %d %q", response.Code, response.Body.String())
	}

	disabled := false
	registry["abc123def456"] = sites.Site{PublicID: "abc123def456", Tracker: sites.TrackerSettingsInput{Vitals: &disabled, Clicks: &disabled}}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/js/abc123def456.js?module=vitals", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled module status = %d, want 404", response.Code)
	}
}

func TestSitePath(t *testing.T) {
	t.Parallel()

	if got, want := SitePath("/assets/supermetry.js", "abc123def456"), "/assets/abc123def456.js"; got != want {
		t.Fatalf("SitePath = %q, want %q", got, want)
	}
}

// A site's snippet is a bare <script src>, so the generated script must point
// events at the Supermetry host it was loaded from, not at the page's own origin.
func TestBuildScriptSendsEventsToTheScriptOrigin(t *testing.T) {
	body := string(BuildScript("abc123def456", sites.TrackerSettings{}, noReplay()).Body)
	prelude, _, _ := strings.Cut(body, "\n")
	want := `d.api??=document.currentScript.src.replace(/\/js\/[^/?#]*([?#].*)?$/,"/api/public/v1/events");`
	if !strings.Contains(prelude, want) {
		t.Fatalf("prelude %q does not set the absolute event endpoint", prelude)
	}
}

func TestBuildScriptUsesConfiguredEventPath(t *testing.T) {
	SetEventPath("/x/collect")
	t.Cleanup(func() { SetEventPath("") })
	body := string(BuildScript("abc123def456", sites.TrackerSettings{}, noReplay()).Body)
	if !strings.Contains(body, `([?#].*)?$/,"/x/collect");`) {
		t.Fatalf("script does not use the configured event path:\n%s", body[:200])
	}
}

func TestSiteHandlerIdentityPolicyInvalidatesCachedScript(t *testing.T) {
	reg := scriptRegistry{"abc123def456": {PublicID: "abc123def456", ProfilesEnabled: true}}
	handler := SiteHandlerWithPolicy(reg, config.ReplayEnv{}, privacy.Identified)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/js/abc123def456.js", nil))
	if bytes.Contains(first.Body.Bytes(), []byte(`d.profiles="false"`)) {
		t.Fatal("identified opted-in project blocked")
	}
	site := reg["abc123def456"]
	site.ProfilesEnabled = false
	reg["abc123def456"] = site
	req := httptest.NewRequest(http.MethodGet, "/js/abc123def456.js", nil)
	req.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusOK || !bytes.Contains(second.Body.Bytes(), []byte(`d.profiles="false"`)) {
		t.Fatalf("project downgrade=%d %s", second.Code, second.Body.String())
	}
	site.ProfilesEnabled = true
	reg["abc123def456"] = site
	anonymous := httptest.NewRecorder()
	SiteHandlerWithPolicy(reg, config.ReplayEnv{}, privacy.Anonymous).ServeHTTP(anonymous, req)
	if anonymous.Code != http.StatusOK || !bytes.Contains(anonymous.Body.Bytes(), []byte(`d.profiles="false"`)) {
		t.Fatal("anonymous instance did not veto project opt in")
	}
}

// A per-site script served through a first-party proxy under a path prefix
// (/sm/js/<site>.js) must post events under the same prefix, not at the
// page's root; a script at /js/<site>.js keeps posting to the origin.
func TestBuildScriptKeepsTheProxyPrefixForEvents(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to evaluate the generated prelude")
	}
	body := string(BuildScript("abc123def456", sites.TrackerSettings{}, noReplay()).Body)
	prelude, _, _ := strings.Cut(body, "\n")
	for src, want := range map[string]string{
		"https://app.supermetry.com/js/abc123def456.js":                         "https://app.supermetry.com/api/public/v1/events",
		"https://shop.example.com/sm/js/abc123def456.js":                        "https://shop.example.com/sm/api/public/v1/events",
		"https://shop.example.com/a/b/js/abc123def456.js":                       "https://shop.example.com/a/b/api/public/v1/events",
		"https://app.supermetry.com/js/abc123def456.js?v=2":                     "https://app.supermetry.com/api/public/v1/events",
		"https://shop.example.com/sm/js/abc123def456.js?source=/campaign/x#a/b": "https://shop.example.com/sm/api/public/v1/events",
	} {
		script := `const vm=require("node:vm");const d={};vm.runInNewContext(process.argv[1],{document:{currentScript:{src:process.argv[2],dataset:d}},URL});process.stdout.write(d.api)`
		out, err := exec.Command("node", "-e", script, prelude, src).CombinedOutput()
		if err != nil || string(out) != want {
			t.Errorf("src %s: api = %q (%v), want %q", src, out, err, want)
		}
	}
}

func TestBuildScriptUsesConfiguredScriptDir(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to evaluate the generated prelude")
	}
	SetScriptDir("/insights")
	SetEventPath("/insights/collect")
	t.Cleanup(func() { SetScriptDir(""); SetEventPath("") })
	body := string(BuildScript("abc123def456", sites.TrackerSettings{}, noReplay()).Body)
	prelude, _, _ := strings.Cut(body, "\n")
	script := `const vm=require("node:vm");const d={};vm.runInNewContext(process.argv[1],{document:{currentScript:{src:process.argv[2],dataset:d}},URL});process.stdout.write(d.api)`
	out, err := exec.Command("node", "-e", script, prelude, "https://shop.example.com/sm/insights/abc123def456.js").CombinedOutput()
	if want := "https://shop.example.com/sm/insights/collect"; err != nil || string(out) != want {
		t.Fatalf("api = %q (%v), want %q", out, err, want)
	}
}
