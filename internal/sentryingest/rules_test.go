package sentryingest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/appingest"
	"supermetry/internal/apprules"
	"supermetry/internal/sites"
)

func rulesSite(t *testing.T) sites.Site {
	t.Helper()
	own, err := apprules.CompileOwnership([]apprules.OwnershipRule{
		{Matcher: "path", Value: "src/billing/*", OwnerUserID: 11},
		{Matcher: "tag:team", Value: "web", OwnerUserID: 12},
	})
	if err != nil {
		t.Fatal(err)
	}
	grp, err := apprules.CompileGrouping([]apprules.GroupingRule{
		{Matcher: "error_type", Value: "TimeoutError", Fingerprint: "timeouts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := testSite()
	s.App.Rules = &apprules.Set{Ownership: own, Grouping: grp}
	return s
}

func stackEvent(typ, file, fn, tags string) string {
	return `{"event_id":"11111111111111111111111111111111","platform":"javascript","timestamp":1794578400,` + tags + `"exception":{"values":[{"type":"` + typ +
		`","value":"boom","stacktrace":{"frames":[{"filename":"` + file + `","function":"` + fn + `","lineno":3,"in_app":true}]}}]}}`
}

func TestNormaliseAppliesSiteRules(t *testing.T) {
	site := rulesSite(t)
	run := func(payload string, s sites.Site) appingest.Event {
		t.Helper()
		ev, _, err := Normalise([]byte(payload), EventContext{}, s, testNow)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	a := run(stackEvent("TimeoutError", "src/billing/a.js", "fetchUsers", ""), site)
	b := run(stackEvent("TimeoutError", "src/other/b.js", "fetchOrders", ""), site)
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("the grouping rule must put two different stacks into one issue")
	}
	if plain := run(stackEvent("TimeoutError", "src/billing/a.js", "fetchUsers", ""), testSite()); plain.Fingerprint == a.Fingerprint {
		t.Fatal("without rules the fingerprint must differ")
	}
	if a.OwnerUserID != 11 || b.OwnerUserID != 0 {
		t.Fatalf("owners = %d, %d; want 11, 0", a.OwnerUserID, b.OwnerUserID)
	}
	tagged := run(stackEvent("ValueError", "x.js", "f", `"tags":{"team":"web"},`), site)
	if tagged.OwnerUserID != 12 {
		t.Fatalf("tag owner = %d, want 12", tagged.OwnerUserID)
	}
}

// Rules ride on the site the DSN lookup returned: no registry lookup, and so no
// database read, happens per event, with or without rules.
func TestRulesCostNoLookupPerEvent(t *testing.T) {
	s := appSite(fdSite, sites.DSNKey{PublicKey: fdKey})
	s.App.Rules = rulesSite(t).App.Rules
	reg := &countingRegistry{fakeRegistry: newRegistry(s)}
	intake := appingest.NewIntake(appingest.Config{QueueSize: 100})
	h := NewHandler(reg, intake, &bodySink{}, fixedGeo("DE"), nil, func() time.Time { return fdNow })
	h.WithIPLimit(-1)
	n := 0
	item := func(file string) string {
		n++
		payload := strings.Replace(stackEvent("TimeoutError", file, "f", ""), "11111111111111111111111111111111", fmt.Sprintf("%032d", n), 1)
		return `{"type":"event"}` + "\n" + payload + "\n"
	}
	send := func(count int) {
		body := `{"event_id":"11111111111111111111111111111111"}` + "\n"
		for i := 0; i < count; i++ {
			body += item("src/billing/x.js")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, post(fdPath, fdQuery, body, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
	}
	send(1)
	perRequest := reg.dsn
	send(5)
	if reg.dsn != 2*perRequest || reg.site != 0 || reg.all != 0 {
		t.Fatalf("lookups: dsn %d (one request: %d), site %d, all %d", reg.dsn, perRequest, reg.site, reg.all)
	}
	got := 0
	for len(intake.Queue()) > 0 {
		q := <-intake.Queue()
		got++
		if q.Event.OwnerUserID != 11 {
			t.Fatalf("queued event owner = %d", q.Event.OwnerUserID)
		}
	}
	if got != 6 {
		t.Fatalf("queued %d events, want 6", got)
	}
}

// Symbolication regroups and re-routes with the original file names, so rules
// written against source paths work for minified scripts.
func TestSymbolicateReappliesRules(t *testing.T) {
	v := loadVite(t)
	store, blobs := newFakeArtifacts(), newMemBlobs()
	blobs.files["bundles/1/aa/app.js.map"] = v.mapBytes
	store.addDebug(42, v.debugID, "bundles/1/aa/app.js.map")
	s, col := newSymb(t, store, blobs)

	own, err := apprules.CompileOwnership([]apprules.OwnershipRule{{Matcher: "path", Value: "src/*", OwnerUserID: 21}})
	if err != nil {
		t.Fatal(err)
	}
	grp, err := apprules.CompileGrouping([]apprules.GroupingRule{{Matcher: "path", Value: "src/cart.js", Fingerprint: "cart"}})
	if err != nil {
		t.Fatal(err)
	}
	ev, body := jsEvent(t, v, "https://shop.example.com/assets/app.js", "1.0.0", v.debugID)
	body.Row.Rules = &apprules.Set{Ownership: own, Grouping: grp}
	if ev.OwnerUserID != 0 {
		t.Fatal("precondition: no owner before rules are attached")
	}
	if !s.Submit(ev, body, body.Row.SDKFingerprint) {
		t.Fatal("Submit refused")
	}
	got, gb := col.wait(t)
	if got.OwnerUserID != 21 {
		t.Fatalf("owner after symbolication = %d, want 21", got.OwnerUserID)
	}
	want, _ := appingest.GroupingKeyRules(got, gb, nil, body.Row.Rules)
	plain, _ := appingest.GroupingKey(got, gb, nil)
	if got.Fingerprint != want || got.Fingerprint == plain {
		t.Fatal("grouping rule not applied to the resolved frames")
	}
}
