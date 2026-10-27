package ghstars

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func starBody(action, repo string, stars int, startedAt string) string {
	starred := "null"
	if startedAt != "" {
		starred = `"` + startedAt + `"`
	}
	return `{"action":"` + action + `","starred_at":` + starred + `,` +
		`"repository":{"full_name":"` + repo + `","stargazers_count":` + strconv.Itoa(stars) + `,"forks_count":4,"open_issues_count":7},` +
		`"sender":{"id":4242,"login":"Octo"}}`
}

type hookRequest struct {
	event, delivery, body, signature, path string
}

type hookEnv struct {
	handler http.Handler
	hook    *Handler
	svc     *Service
	store   *Store
	repo    Repo
	secret  string
}

func newHook(t *testing.T) (*hookEnv, *Store) {
	t.Helper()
	svc, _ := newTestService(t)
	repo, secret, err := svc.Create(context.Background(), orgOf(t, svc), CreateInput{Repo: "Velixio/Supermetry", PublicCounter: true, TimeZone: "Asia/Kathmandu"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := at("2026-10-10T12:00:00Z")
	svc.Now = func() time.Time { return now }
	store, _, err := svc.StoreFor(context.Background(), orgOf(t, svc), repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	hook := &Handler{Service: svc}
	mux.Handle("POST /api/github/webhook/{public_id}", hook)
	return &hookEnv{handler: mux, hook: hook, svc: svc, store: store, repo: repo, secret: secret}, store
}

func (h *hookEnv) do(r hookRequest) *httptest.ResponseRecorder {
	path := h.repo.WebhookURL
	if r.path != "" {
		path = r.path
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(r.body))
	req.Header.Set("X-GitHub-Event", r.event)
	req.Header.Set("X-GitHub-Delivery", r.delivery)
	if r.signature != "" {
		req.Header.Set("X-Hub-Signature-256", r.signature)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func (h *hookEnv) signed(event, delivery, body string) hookRequest {
	return hookRequest{event: event, delivery: delivery, body: body, signature: sign(h.secret, body)}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	h, store := newHook(t)
	body := starBody("created", testRepo, 10, "2026-10-10T11:00:00Z")
	for name, sig := range map[string]string{
		"missing":   "",
		"wrong":     sign("other-secret", body),
		"no prefix": strings.TrimPrefix(sign(h.secret, body), "sha256="),
		"not hex":   "sha256=zz",
	} {
		rec := h.do(hookRequest{event: "star", delivery: "d1", body: body, signature: sig})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d", name, rec.Code)
		}
	}
	if n, _ := store.EventCount(context.Background()); n != 0 {
		t.Fatalf("events = %d", n)
	}
}

func TestWebhookStarAndUnstar(t *testing.T) {
	h, store := newHook(t)
	ctx := context.Background()
	rec := h.do(h.signed("star", "d1", starBody("created", testRepo, 10, "2026-10-10T11:00:00Z")))
	if rec.Code != http.StatusOK {
		t.Fatalf("star status = %d %s", rec.Code, rec.Body)
	}
	rec = h.do(h.signed("star", "d2", starBody("deleted", testRepo, 9, "")))
	if rec.Code != http.StatusOK {
		t.Fatalf("unstar status = %d %s", rec.Code, rec.Body)
	}
	events, _, err := store.Events(ctx, "", 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	unstar, star := events[0], events[1]
	if unstar.Action != ActionUnstarred || !unstar.OccurredAt.Equal(at("2026-10-10T12:00:00Z")) || unstar.Login != "Octo" || unstar.UserID != 4242 || unstar.Source != SourceWebhook {
		t.Errorf("unstar = %+v", unstar)
	}
	if star.Action != ActionStarred || !star.OccurredAt.Equal(at("2026-10-10T11:00:00Z")) {
		t.Errorf("star = %+v", star)
	}
	sum, _ := store.Summary(ctx, at("2026-10-10T12:00:00Z"))
	if sum.Stars != 9 || sum.Forks != 4 || sum.OpenIssues != 7 {
		t.Errorf("state = %+v", sum)
	}
}

func TestWebhookDuplicateDeliveryWritesNothing(t *testing.T) {
	h, store := newHook(t)
	req := h.signed("star", "same", starBody("created", testRepo, 10, "2026-10-10T11:00:00Z"))
	if rec := h.do(req); rec.Code != http.StatusOK {
		t.Fatalf("first = %d", rec.Code)
	}
	req = h.signed("star", "same", starBody("created", testRepo, 50, "2026-10-10T11:00:00Z"))
	if rec := h.do(req); rec.Code != http.StatusOK {
		t.Fatalf("duplicate = %d", rec.Code)
	}
	if n, _ := store.EventCount(context.Background()); n != 1 {
		t.Fatalf("events = %d", n)
	}
	if stars, _, _ := store.Stars(context.Background()); stars != 10 {
		t.Fatalf("stars = %d", stars)
	}
}

func TestWebhookPing(t *testing.T) {
	h, store := newHook(t)
	if rec := h.do(h.signed("ping", "p1", `{"zen":"Keep it logically awesome."}`)); rec.Code != http.StatusOK {
		t.Fatalf("ping = %d", rec.Code)
	}
	if n, _ := store.EventCount(context.Background()); n != 0 {
		t.Fatalf("events = %d", n)
	}
}

func TestWebhookIgnoredEvents(t *testing.T) {
	h, store := newHook(t)
	cases := map[string]hookRequest{
		"other event":     h.signed("push", "i1", `{"ref":"refs/heads/main"}`),
		"other star kind": h.signed("star", "i2", starBody("edited", testRepo, 1, "")),
		"different repo":  h.signed("star", "i3", starBody("created", "someone/else", 1, "2026-10-10T11:00:00Z")),
	}
	for name, req := range cases {
		if rec := h.do(req); rec.Code != http.StatusAccepted {
			t.Errorf("%s: status = %d", name, rec.Code)
		}
	}
	if n, _ := store.EventCount(context.Background()); n != 0 {
		t.Fatalf("events = %d", n)
	}
	// The repository name is matched case-insensitively.
	if rec := h.do(h.signed("star", "i4", starBody("created", strings.ToUpper(testRepo), 1, "2026-10-10T11:00:00Z"))); rec.Code != http.StatusOK {
		t.Fatalf("upper-case repo status = %d", rec.Code)
	}
}

func TestWebhookOversizeBody(t *testing.T) {
	h, store := newHook(t)
	h.hook.MaxBody = 64
	body := starBody("created", testRepo, 1, "2026-10-10T11:00:00Z")
	if rec := h.do(h.signed("star", "big", body)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
	if n, _ := store.EventCount(context.Background()); n != 0 {
		t.Fatalf("events = %d", n)
	}
}

func TestWebhookMalformedPayloads(t *testing.T) {
	h, _ := newHook(t)
	cases := map[string]hookRequest{
		"bad json":         h.signed("star", "m1", `{`),
		"no sender":        h.signed("star", "m2", `{"action":"created","repository":{"full_name":"`+testRepo+`"}}`),
		"missing delivery": h.signed("star", "", starBody("created", testRepo, 1, "")),
	}
	for name, req := range cases {
		if rec := h.do(req); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", name, rec.Code)
		}
	}
}

func TestWebhookIgnoresErasedUser(t *testing.T) {
	h, store := newHook(t)
	ctx := context.Background()
	if rec := h.do(h.signed("star", "e1", starBody("created", testRepo, 10, "2026-10-10T11:00:00Z"))); rec.Code != http.StatusOK {
		t.Fatalf("first star = %d", rec.Code)
	}
	if n, err := store.EraseUser(ctx, "octo", 0); err != nil || n != 1 {
		t.Fatalf("erase = %d, %v", n, err)
	}
	rec := h.do(h.signed("star", "e2", starBody("created", testRepo, 11, "2026-10-10T11:30:00Z")))
	if rec.Code != http.StatusOK {
		t.Fatalf("erased user's star = %d", rec.Code)
	}
	if n, _ := store.EventCount(ctx); n != 0 {
		t.Fatalf("events = %d", n)
	}
	// The repository counter in the payload is still the truth about GitHub.
	if stars, _, _ := store.Stars(ctx); stars != 11 {
		t.Fatalf("stars = %d", stars)
	}
}

func TestWebhookUnknownOrDisabledRepoIs404(t *testing.T) {
	h, store := newHook(t)
	body := starBody("created", testRepo, 10, "2026-10-10T11:00:00Z")
	req := h.signed("star", "n1", body)
	req.path = "/api/github/webhook/zzzzzzzzzzzzzzzz"
	if rec := h.do(req); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d", rec.Code)
	}
	req.path = "/api/github/webhook/short"
	if rec := h.do(req); rec.Code != http.StatusNotFound {
		t.Fatalf("short id = %d", rec.Code)
	}
	off := false
	if _, err := h.svc.Update(context.Background(), orgOf(t, h.svc), h.repo.ID, UpdateInput{Enabled: &off}, 0); err != nil {
		t.Fatal(err)
	}
	if rec := h.do(h.signed("star", "n2", body)); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled = %d", rec.Code)
	}
	if n, _ := store.EventCount(context.Background()); n != 0 {
		t.Fatalf("events = %d", n)
	}
}

func TestWebhookUsesThePerRepositorySecret(t *testing.T) {
	h, store := newHook(t)
	ctx := context.Background()
	other, otherSecret, err := h.svc.Create(ctx, orgOf(t, h.svc), CreateInput{Repo: "velixio/other"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := starBody("created", testRepo, 10, "2026-10-10T11:00:00Z")
	// The other repository's secret does not open this repository's endpoint.
	if rec := h.do(hookRequest{event: "star", delivery: "s1", body: body, signature: sign(otherSecret, body)}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("cross secret = %d", rec.Code)
	}
	// A payload for a different repository on a valid endpoint is ignored.
	req := hookRequest{event: "star", delivery: "s2", body: body, signature: sign(otherSecret, body), path: other.WebhookURL}
	if rec := h.do(req); rec.Code != http.StatusAccepted {
		t.Fatalf("mismatched payload = %d", rec.Code)
	}
	if n, _ := store.EventCount(ctx); n != 0 {
		t.Fatalf("events = %d", n)
	}
}

func TestWebhookRecordsLastDelivery(t *testing.T) {
	h, _ := newHook(t)
	if rec := h.do(h.signed("ping", "p9", `{}`)); rec.Code != http.StatusOK {
		t.Fatalf("ping = %d", rec.Code)
	}
	repo, err := h.svc.Get(context.Background(), orgOf(t, h.svc), h.repo.ID)
	if err != nil || repo.LastWebhookAt == nil || !repo.LastWebhookAt.Equal(at("2026-10-10T12:00:00Z")) {
		t.Fatalf("last webhook = %+v, %v", repo.LastWebhookAt, err)
	}
	// A bad signature does not count as a delivery.
	h.svc.Now = func() time.Time { return at("2026-10-11T00:00:00Z") }
	h.do(hookRequest{event: "ping", delivery: "p10", body: `{}`, signature: "sha256=00"})
	repo, _ = h.svc.Get(context.Background(), orgOf(t, h.svc), h.repo.ID)
	if !repo.LastWebhookAt.Equal(at("2026-10-10T12:00:00Z")) {
		t.Fatalf("last webhook moved to %v", repo.LastWebhookAt)
	}
}
