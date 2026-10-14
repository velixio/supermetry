package pixels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"supermetry/internal/enrich"
	"supermetry/internal/sites"
)

type siteReg map[int32]sites.Site

func (r siteReg) ByPublicID(string) (sites.Site, bool) { return sites.Site{}, false }
func (r siteReg) Site(id int32) (sites.Site, bool)     { s, ok := r[id]; return s, ok }
func (r siteReg) All() []sites.Site                    { return nil }

const (
	camoUA   = "github-camo (876de43e)"
	chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	botUA    = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
)

type pxFixture struct {
	h        *Handler
	rows     map[ViewKey]int64
	fallback int
}

func newPxFixture(t *testing.T) *pxFixture {
	t.Helper()
	enricher, err := enrich.New("")
	if err != nil {
		t.Fatal(err)
	}
	f := &pxFixture{rows: map[ViewKey]int64{}}
	reg := NewRegistry(func(context.Context) ([]Pixel, error) {
		return []Pixel{
			{ID: 1, SiteID: 1, PublicID: "pAAAAAAAAAAA", BadgeLabel: "views", Enabled: true},
			{ID: 2, SiteID: 1, PublicID: "pDISABLED000", BadgeLabel: "views", Enabled: false},
		}, nil
	}, func(context.Context) (map[int64]int64, error) { return map[int64]int64{1: 1233}, nil }, NewTotalCache())
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.h = &Handler{
		Pixels: reg, Recorder: NewRecorder(func(_ context.Context, r map[ViewKey]int64) error {
			for k, n := range r {
				f.rows[k] += n
			}
			return nil
		}, reg.Totals()),
		Sites: siteReg{1: {ID: 1, Kind: sites.KindPixel, RespectPrivacySignals: true}}, Enricher: enricher,
		ProxyLimiter: NewProxyLimiter(), Fallback: func(w http.ResponseWriter, _ *http.Request) { f.fallback++; w.WriteHeader(http.StatusOK) },
		ClientIP: func(*http.Request) (string, netip.Addr) { return "k", netip.MustParseAddr("203.0.113.9") },
		Branded:  true, Now: func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
	}
	return f
}

func (f *pxFixture) get(t *testing.T, method, path, ua string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("User-Agent", ua)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	mux := http.NewServeMux()
	mux.Handle("GET /px/{file}", f.h)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	_ = f.h.Recorder.Flush(context.Background())
	return rec
}

func (f *pxFixture) total() (n int64) {
	for _, v := range f.rows {
		n += v
	}
	return n
}

func TestCamoIsCountedAsProxyAndBadgeShowsTotal(t *testing.T) {
	f := newPxFixture(t)
	res := f.get(t, http.MethodGet, "/px/pAAAAAAAAAAA.svg", camoUA)
	if res.Code != 200 || res.Header().Get("Content-Type") != "image/svg+xml" || res.Header().Get("Cache-Control") != "no-store, max-age=0" {
		t.Fatalf("status=%d headers=%v", res.Code, res.Header())
	}
	if !strings.Contains(res.Body.String(), "1.2k") {
		t.Fatalf("badge = %s", res.Body)
	}
	if f.rows[ViewKey{PixelID: 1, Day: "2026-10-14", Via: "proxy"}] != 1 {
		t.Fatalf("rows = %v", f.rows)
	}
}

func TestDirectViewGetsCountryAndGIF(t *testing.T) {
	f := newPxFixture(t)
	res := f.get(t, http.MethodGet, "/px/pAAAAAAAAAAA.gif", chromeUA)
	if res.Code != 200 || res.Header().Get("Content-Type") != "image/gif" || res.Body.Len() != 43 {
		t.Fatalf("gif: %d %v %d", res.Code, res.Header(), res.Body.Len())
	}
	for k := range f.rows {
		if k.Via != "direct" {
			t.Fatalf("row = %+v", k)
		}
	}
	if f.total() != 1 {
		t.Fatalf("total = %d", f.total())
	}
}

func TestNotCountedButImageServed(t *testing.T) {
	f := newPxFixture(t)
	for _, tc := range []struct {
		path, ua string
		h        []string
	}{
		{"/px/pAAAAAAAAAAA.svg", botUA, nil}, {"/px/pDISABLED000.svg", camoUA, nil},
		{"/px/pUNKNOWN0000.svg", camoUA, nil}, {"/px/pAAAAAAAAAAA.gif", chromeUA, []string{"Sec-GPC", "1"}},
	} {
		res := f.get(t, http.MethodGet, tc.path, tc.ua, tc.h...)
		if res.Code != 200 || res.Body.Len() == 0 {
			t.Errorf("%s: status %d", tc.path, res.Code)
		}
	}
	if f.total() != 0 {
		t.Fatalf("counted %v", f.rows)
	}
}

func TestBadgePreviewDoesNotCount(t *testing.T) {
	f := newPxFixture(t)
	res := f.get(t, http.MethodGet, "/px/pAAAAAAAAAAA.svg?preview=1", camoUA)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "1.2k") {
		t.Fatalf("preview response = %d %s", res.Code, res.Body.String())
	}
	if f.total() != 0 {
		t.Fatalf("preview counted rows %v", f.rows)
	}
}

func TestOddFileNamesServeImageWithoutCounting(t *testing.T) {
	f := newPxFixture(t)
	for _, path := range []string{"/px/pAAAAAAAAAAA.svg.gif", "/px/.svg", "/px/p" + strings.Repeat("A", 200) + ".svg"} {
		if res := f.get(t, http.MethodGet, path, camoUA); res.Code != 200 {
			t.Errorf("%s: %d", path, res.Code)
		}
	}
	if res := f.get(t, http.MethodHead, "/px/pAAAAAAAAAAA.svg", camoUA); res.Code != 200 || res.Body.Len() != 0 {
		t.Errorf("HEAD: %d len %d", res.Code, res.Body.Len())
	}
	if f.total() != 0 {
		t.Fatalf("counted %v", f.rows)
	}
}

func TestWebPixelStillServedForSiteIDStartingWithP(t *testing.T) {
	f := newPxFixture(t)
	f.get(t, http.MethodGet, "/px/pubsite00001.gif?c=spring", chromeUA)
	if f.fallback != 1 || f.total() != 0 {
		t.Fatalf("fallback=%d rows=%v", f.fallback, f.rows)
	}
}

func TestLegacySiteBadgeIsServedForTheEmailPixelID(t *testing.T) {
	f := newPxFixture(t)
	var seen []string
	f.h.LegacyBadge = func(r *http.Request, id string, n int) (int64, []int64, bool) {
		seen = append(seen, id)
		series := make([]int64, n)
		series[n-1] = 3
		return 40, series, id == "siteLegacy01"
	}
	rec := f.get(t, http.MethodGet, "/px/siteLegacy01.svg?style=stats", chromeUA)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "<polyline") || !strings.Contains(body, "today 3") || !strings.Contains(body, "40") {
		t.Fatalf("stats badge = %d %s", rec.Code, body)
	}
	flat := f.get(t, http.MethodGet, "/px/siteLegacy01.svg", chromeUA).Body.String()
	if strings.Contains(flat, "<polyline") || !strings.Contains(flat, "40") {
		t.Fatalf("flat badge = %s", flat)
	}
	if unknown := f.get(t, http.MethodGet, "/px/nosuchsite00.svg", chromeUA).Body.String(); !strings.Contains(unknown, ">0<") {
		t.Fatalf("unknown id must show an empty badge: %s", unknown)
	}
	if len(seen) != 3 {
		t.Fatalf("legacy lookups = %v", seen)
	}
	if f.get(t, http.MethodGet, "/px/pAAAAAAAAAAA.svg", camoUA); len(seen) != 3 {
		t.Fatal("a pixel-project id must not use the legacy lookup")
	}
}
