package sentryingest

import (
	"strings"
	"testing"

	"supermetry/internal/spans"
)

func TestTransactionFixtures(t *testing.T) {
	want := map[string]struct {
		n        int
		name     string
		appStart string
		ms       int32
	}{
		"transactions/python":                {3, "GET /api/items", "", 0},
		"transactions/javascript-static":     {4, "/users/*", "", 0},
		"transactions/javascript-v11-stream": {3, "/users/1234", "", 0},
		"transactions/node-v11-stream":       {3, "GET /users/:id", "", 0},
		"transactions/go-v0.50":              {3, "GET /api/items", "", 0},
		"transactions/browser-v11-pageload":  {13, "/", "", 0},
		"transactions/android-app-start":     {4, "MainActivity", "cold", 1234},
		"transactions/cocoa-app-start":       {4, "ViewController", "warm", 620},
	}
	seen := 0
	for _, f := range loadFixtures(t) {
		w, ok := want[f.Name]
		if !ok {
			continue
		}
		seen++
		var out []spans.Span
		for _, it := range f.Items {
			var err error
			var got []spans.Span
			switch it.Type {
			case "transaction":
				got, err = ParseTransaction(it.Payload, fixtureCtx(f), txSite(), txNow)
			case "span":
				got, err = ParseSpanV2(it.Payload, fixtureCtx(f), txSite(), txNow)
			default:
				continue
			}
			if err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
			out = append(out, got...)
		}
		segs := 0
		for _, s := range out {
			if s.IsSegment {
				segs++
				if s.AppStartType != w.appStart || (w.ms != 0 && (s.AppStartMS == nil || *s.AppStartMS != w.ms)) {
					t.Errorf("%s: app start %q %v", f.Name, s.AppStartType, s.AppStartMS)
				}
				if s.Name != w.name {
					t.Errorf("%s: name %q", f.Name, s.Name)
				}
			}
			if strings.Contains(s.Description, "'fixture'") || strings.Contains(s.Description, "access_token") || strings.Contains(s.Description, "12345?") || strings.Contains(s.Description, "token=") {
				t.Errorf("%s: leak %q", f.Name, s.Description)
			}
		}
		if len(out) != w.n || segs != 1 {
			t.Errorf("%s: %d spans, %d segments", f.Name, len(out), segs)
		}
	}
	if seen != len(want) {
		t.Errorf("saw %d of %d fixtures", seen, len(want))
	}
}
