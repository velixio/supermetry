package stripe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/privacy"
	"supermetry/internal/salts"
	"supermetry/internal/session"
	"supermetry/internal/sites"
)

type fakeProcessor struct {
	mu       sync.Mutex
	status   int
	calls    int
	endpoint string
	sig      string
	body     string
}

func (f *fakeProcessor) HandleWebhook(_ context.Context, endpoint, signature string, body []byte) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.endpoint, f.sig, f.body = endpoint, signature, string(body)
	return f.status
}

type denyAfter struct{ left int }

func (d *denyAfter) Allow(string) bool {
	d.left--
	return d.left >= 0
}

func webhookMux(processor webhookProcessor, limiter RateLimiter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+WebhookPath+"{endpoint}", func(w http.ResponseWriter, r *http.Request) {
		serveWebhook(w, r, processor, limiter, nil)
	})
	return mux
}

func TestWebhookHandlerPassesDeliveryAndMapsStatuses(t *testing.T) {
	for _, test := range []struct {
		status int
		want   int
		text   string
	}{
		{http.StatusOK, http.StatusOK, `"received":true`},
		{http.StatusBadRequest, http.StatusBadRequest, "could not be verified"},
		{http.StatusServiceUnavailable, http.StatusServiceUnavailable, "retry later"},
		{http.StatusInternalServerError, http.StatusInternalServerError, "could not be processed"},
	} {
		processor := &fakeProcessor{status: test.status}
		request := httptest.NewRequest(http.MethodPost, WebhookPath+"abc", strings.NewReader(`{"id":"evt_1"}`))
		request.Header.Set("Stripe-Signature", "t=1,v1=00")
		recorder := httptest.NewRecorder()
		webhookMux(processor, nil).ServeHTTP(recorder, request)
		if recorder.Code != test.want || !strings.Contains(recorder.Body.String(), test.text) {
			t.Fatalf("status %d: got %d %s", test.status, recorder.Code, recorder.Body)
		}
		if processor.endpoint != "abc" || processor.sig != "t=1,v1=00" || processor.body != `{"id":"evt_1"}` {
			t.Fatalf("processor saw %+v", processor)
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("webhook response is cacheable")
		}
		if test.status == http.StatusServiceUnavailable && recorder.Header().Get("Retry-After") == "" {
			t.Fatal("503 without Retry-After")
		}
	}
}

func TestWebhookHandlerBodyLimit(t *testing.T) {
	processor := &fakeProcessor{status: http.StatusOK}
	big := strings.Repeat("a", MaxWebhookBytes+1)

	request := httptest.NewRequest(http.MethodPost, WebhookPath+"abc", strings.NewReader(big))
	recorder := httptest.NewRecorder()
	webhookMux(processor, nil).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared oversized body: %d", recorder.Code)
	}

	// Chunked: no Content-Length, so the reader limit has to stop it.
	request = httptest.NewRequest(http.MethodPost, WebhookPath+"abc", struct{ *strings.Reader }{strings.NewReader(big)})
	request.ContentLength = -1
	recorder = httptest.NewRecorder()
	webhookMux(processor, nil).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("streamed oversized body: %d", recorder.Code)
	}
	if processor.calls != 0 {
		t.Fatal("oversized body reached the processor")
	}

	request = httptest.NewRequest(http.MethodPost, WebhookPath+"abc", strings.NewReader(strings.Repeat("a", MaxWebhookBytes)))
	recorder = httptest.NewRecorder()
	webhookMux(processor, nil).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || processor.calls != 1 {
		t.Fatalf("body at the limit: %d calls=%d", recorder.Code, processor.calls)
	}
}

func TestWebhookHandlerRateLimitAndMethods(t *testing.T) {
	processor := &fakeProcessor{status: http.StatusOK}
	handler := webhookMux(processor, &denyAfter{left: 1})
	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, WebhookPath+"abc", strings.NewReader(`{}`)))
		if recorder.Code != want {
			t.Fatalf("request %d: %d", i, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	(&WebhookHandler{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, WebhookPath+"abc", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	(&WebhookHandler{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, WebhookPath+"abc", strings.NewReader(`{}`)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("no service: %d", recorder.Code)
	}
}

type registry map[string]sites.Site

func (r registry) ByPublicID(id string) (sites.Site, bool) { s, ok := r[id]; return s, ok }
func (r registry) Site(id int32) (sites.Site, bool) {
	for _, s := range r {
		if s.ID == id {
			return s, true
		}
	}
	return sites.Site{}, false
}
func (r registry) All() []sites.Site { return nil }

type fixedSalts []byte

func (s fixedSalts) For(time.Time) ([]byte, bool) { return s, true }

func attributionFixture(t *testing.T) (*AttributionHandler, *session.Memory, sites.Site, time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	site := sites.Site{ID: 7, PublicID: "pub7", TZ: time.UTC, ProfilesEnabled: true, AllowedHosts: []string{"shop.example.test"}}
	memory := session.NewMemory(4, nil)
	salt := fixedSalts("0123456789abcdef0123456789abcdef")
	visitor := salts.VisitorID(salt, site.ID, "203.0.113.9", "Mozilla/5.0 test")
	memory.Touch(session.Hit{SiteID: site.ID, TS: now.Add(-5 * time.Minute), LocalDay: now, VisitorID: visitor, Source: 31, Medium: 32, Campaign: 33, Country: "FR"})
	handler := &AttributionHandler{
		Sites: registry{"pub7": site}, Salts: salt, Sessions: memory, Tokens: testTokens(t, "01234567890123456789012345678901"),
		Mode: privacy.Identified, Now: func() time.Time { return now },
	}
	return handler, memory, site, now
}

func attributionRequest(body, origin string, headers ...string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/public/v1/attribution", strings.NewReader(body))
	request.RemoteAddr = "203.0.113.9:4000"
	request.Header.Set("User-Agent", "Mozilla/5.0 test")
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	return request
}

func decodeID(t *testing.T, recorder *httptest.ResponseRecorder) *string {
	t.Helper()
	var response struct {
		ID *string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("body %s: %v", recorder.Body, err)
	}
	return response.ID
}

func TestAttributionHandlerReturnsTokenForLiveSession(t *testing.T) {
	handler, _, site, now := attributionFixture(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, attributionRequest(`{"site":"pub7"}`, "https://shop.example.test"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d %s", recorder.Code, recorder.Body)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "https://shop.example.test" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", recorder.Header())
	}
	id := decodeID(t, recorder)
	if id == nil {
		t.Fatal("no token for a live session")
	}
	attribution, err := handler.Tokens.Open(*id, site.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if attribution.SessionID == 0 || attribution.Source != 31 || attribution.Medium != 32 || attribution.Campaign != 33 || attribution.Country != "FR" {
		t.Fatalf("attribution = %+v", attribution)
	}
}

func TestAttributionHandlerReturnsNullWhenAttributionIsNotAllowed(t *testing.T) {
	cases := map[string]func(*AttributionHandler) *http.Request{
		"anonymous mode": func(h *AttributionHandler) *http.Request {
			h.Mode = privacy.Anonymous
			return attributionRequest(`{"site":"pub7"}`, "")
		},
		"profiles off": func(h *AttributionHandler) *http.Request {
			site := h.Sites.(registry)["pub7"]
			site.ProfilesEnabled = false
			h.Sites = registry{"pub7": site}
			return attributionRequest(`{"site":"pub7"}`, "")
		},
		"privacy signal": func(h *AttributionHandler) *http.Request {
			site := h.Sites.(registry)["pub7"]
			site.RespectPrivacySignals = true
			h.Sites = registry{"pub7": site}
			return attributionRequest(`{"site":"pub7"}`, "", "Sec-GPC", "1")
		},
		"no session": func(*AttributionHandler) *http.Request {
			return attributionRequest(`{"site":"pub7"}`, "", "User-Agent", "Other browser")
		},
		"unknown site": func(*AttributionHandler) *http.Request {
			return attributionRequest(`{"site":"nope"}`, "https://evil.example.test")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			handler, _, _, _ := attributionFixture(t)
			request := setup(handler)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || decodeID(t, recorder) != nil {
				t.Fatalf("status %d body %s", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestAttributionHandlerRejectsForeignOriginsAndBadInput(t *testing.T) {
	handler, _, _, _ := attributionFixture(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, attributionRequest(`{"site":"pub7"}`, "https://evil.example.test"))
	if recorder.Code != http.StatusForbidden || recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign origin: %d %v", recorder.Code, recorder.Header())
	}
	for _, body := range []string{``, `{}`, `{"site":""}`, `not json`, `{"site":"` + strings.Repeat("a", 2000) + `"}`} {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, attributionRequest(body, ""))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %q: %d", body, recorder.Code)
		}
	}
	recorder = httptest.NewRecorder()
	preflight := httptest.NewRequest(http.MethodOptions, "/api/public/v1/attribution", nil)
	handler.ServeHTTP(recorder, preflight)
	if recorder.Code != http.StatusNoContent || recorder.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatalf("preflight: %d", recorder.Code)
	}
	handler.Limiter = &denyAfter{left: 0}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, attributionRequest(`{"site":"pub7"}`, ""))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %d", recorder.Code)
	}
}

func TestAttributionHandlerJoinsPreviousDaySessionAfterMidnight(t *testing.T) {
	handler, memory, site, _ := attributionFixture(t)
	midnight := time.Date(2026, 11, 4, 0, 10, 0, 0, time.UTC)
	daySalts := map[string][]byte{"2026-11-03": []byte("salt-of-day-one-0123456789abcdef"), "2026-11-04": []byte("salt-of-day-two-0123456789abcdef")}
	handler.Salts = saltsByDay(daySalts)
	handler.Now = func() time.Time { return midnight }
	previous := salts.VisitorID(daySalts["2026-11-03"], site.ID, "203.0.113.9", "Mozilla/5.0 test")
	memory.Touch(session.Hit{SiteID: site.ID, TS: midnight.Add(-15 * time.Minute), LocalDay: midnight.AddDate(0, 0, -1), VisitorID: previous, Source: 44})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, attributionRequest(`{"site":"pub7"}`, ""))
	id := decodeID(t, recorder)
	if id == nil {
		t.Fatal("session from before midnight not joined")
	}
	if a, err := handler.Tokens.Open(*id, site.ID, midnight); err != nil || a.Source != 44 {
		t.Fatalf("attribution %+v %v", a, err)
	}
}

type saltsByDay map[string][]byte

func (s saltsByDay) For(day time.Time) ([]byte, bool) {
	salt, ok := s[day.Format(time.DateOnly)]
	return salt, ok
}
