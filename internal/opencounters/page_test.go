package opencounters

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supermetry/internal/pixels"
)

func TestStatsPage(t *testing.T) {
	recorder := NewRecorder(newMemoryStore(), 50, 500)
	h := &Handler{
		Recorder: recorder, Proxy: pixels.NewProxyLimiter(), Blocklist: map[string]bool{"github.com/spam": true},
		HomepageURL: "https://supermetry.example", Now: func() time.Time { return time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC) },
	}
	h.Stats = func(_ context.Context, key, from, to string) ([]DayCount, error) {
		if key != "github.com/velixio" || from != "2026-09-15" || to != "2026-10-14" {
			t.Errorf("stats args %s %s %s", key, from, to)
		}
		return []DayCount{{"2026-10-13", 3}, {"2026-10-14", 5}}, nil
	}
	mux := http.NewServeMux()
	Register(mux, h, true)
	hit := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	res := hit("/c/github.com/Velixio")
	body := res.Body.String()
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("%d %v", res.Code, res.Header())
	}
	for _, want := range []string{"github.com/velixio", "<polyline", "Track your own site with Supermetry", `href="https://supermetry.example/?ref=counter"`, "views, not unique visitors"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("page must not ship scripts")
	}
	if res := hit("/c/github.com/spam"); res.Code != http.StatusNotFound {
		t.Errorf("blocked key page = %d", res.Code)
	}
}

func TestRenderSparklineEmptyAndFlat(t *testing.T) {
	if s := RenderSparkline(nil, 300, 40); !strings.Contains(s, "<svg") {
		t.Fatalf("empty = %s", s)
	}
	if s := RenderSparkline([]DayCount{{"2026-10-14", 0}}, 300, 40); strings.Contains(s, "NaN") {
		t.Fatalf("flat = %s", s)
	}
}
