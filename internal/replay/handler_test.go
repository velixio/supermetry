package replay

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"panel/internal/config"
	"panel/internal/health"
	"panel/internal/salts"
	"panel/internal/session"
	"panel/internal/sites"
)

type replayTestClock struct{ now time.Time }

func (c replayTestClock) Now() time.Time { return c.now }

type replayTestSalts map[time.Time][]byte

func (s replayTestSalts) For(day time.Time) ([]byte, bool) {
	value, ok := s[day]
	return value, ok
}

type replayTestRegistry struct{ site sites.Site }

func (r replayTestRegistry) ByPublicID(id string) (sites.Site, bool) {
	return r.site, id == r.site.PublicID
}
func (r replayTestRegistry) Site(id int32) (sites.Site, bool) { return r.site, id == r.site.ID }
func (r replayTestRegistry) All() []sites.Site                { return []sites.Site{r.site} }

type replayTestBlob struct {
	mu      sync.Mutex
	keys    []string
	bodies  [][]byte
	entered chan struct{}
	release chan struct{}
}

func (b *replayTestBlob) Append(_ context.Context, key string, body io.Reader) (int64, error) {
	if b.entered != nil {
		b.entered <- struct{}{}
		<-b.release
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, err
	}
	b.mu.Lock()
	b.keys = append(b.keys, key)
	b.bodies = append(b.bodies, append([]byte(nil), data...))
	b.mu.Unlock()
	return int64(len(data)), nil
}
func (*replayTestBlob) Open(context.Context, string) (io.ReadCloser, error) { return nil, nil }
func (*replayTestBlob) PutIfAbsent(context.Context, string, io.Reader) (bool, error) {
	return false, nil
}
func (*replayTestBlob) DeletePrefix(context.Context, string) (int64, error) { return 0, nil }
func (*replayTestBlob) Delete(context.Context, string) (int64, error)       { return 0, nil }

type replayTestIndex struct {
	mu      sync.Mutex
	updates []IndexUpdate
}

func (i *replayTestIndex) Add(update IndexUpdate) {
	i.mu.Lock()
	i.updates = append(i.updates, update)
	i.mu.Unlock()
}

func TestStartIgnoresStoredSiteSwitch(t *testing.T) {
	// PANEL_REPLAY_ENABLED is the only switch: a stale per-site "enabled":false
	// stored before that rule must not stop recording.
	input := replayInput(100)
	input.Enabled = false
	h, _, _, _, _ := newReplayTestHandler(t, input)
	response := requestStart(t, h, "https://example.com/page", "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusOK || decodeStart(t, response).Mode != "full" {
		t.Fatalf("status/body = %d/%s, want 200 with full", response.Code, response.Body.String())
	}
	assertCORS(t, response)
}

func TestStartOffWhenDiskGuardTripped(t *testing.T) {
	h, _, _, _, _ := newReplayTestHandler(t, replayInput(100), WithDiskGuard(func() bool { return true }))
	response := requestStart(t, h, "https://example.com/page", "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusOK || decodeStart(t, response).Mode != "off" {
		t.Fatalf("status/body = %d/%s, want 200 with off", response.Code, response.Body.String())
	}
}

func TestStartRejectsOriginOutsideSite(t *testing.T) {
	h, _, _, _, _ := newReplayTestHandler(t, replayInput(100))
	response := requestStartWithOrigin(t, h, "https://example.com/page", "https://attacker.example", "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestSamplingStableAcrossMidnightWindow(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 5, 0, 0, time.UTC)
	currentSalt := bytes.Repeat([]byte{1}, 32)
	previousSalt := bytes.Repeat([]byte{2}, 32)
	var ip string
	for candidate := 1; candidate < 255; candidate++ {
		ip = "192.0.2." + itoa(candidate)
		current := salts.VisitorID(currentSalt, 7, ip, "midnight-agent")
		previous := salts.VisitorID(previousSalt, 7, ip, "midnight-agent")
		if !Sampled(current, 15) && Sampled(previous, 15) {
			break
		}
		ip = ""
	}
	if ip == "" {
		t.Fatal("failed to find deterministic previous-day sample fixture")
	}
	h, _, _, _, _ := newReplayTestHandlerAt(t, replayInput(15), now, currentSalt, previousSalt)
	response := requestStart(t, h, "https://example.com/page", ip+":1234", "midnight-agent")
	if got := decodeStart(t, response).Mode; got != "full" {
		t.Fatalf("mode = %q, want full when previous visitor ID is sampled", got)
	}
}

func TestChunkRejectsNonGzip(t *testing.T) {
	h, _, _, _, _ := newReplayTestHandler(t, replayInput(100))
	before := health.ReplayRejected.Load()
	response := requestChunk(h, 0, true, "", []byte("not gzip"), "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if got := health.ReplayRejected.Load() - before; got != 1 {
		t.Fatalf("ReplayRejected delta = %d, want 1", got)
	}
	assertCORS(t, response)
}

func TestChunkRejectsOversize(t *testing.T) {
	h, _, blob, _, _ := newReplayTestHandler(t, replayInput(100))
	body := make([]byte, maxChunkBytes+1)
	body[0], body[1] = 0x1f, 0x8b
	response := requestChunk(h, 0, true, "", body, "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.Code)
	}
	if len(blob.keys) != 0 {
		t.Fatalf("oversize body wrote %d blobs", len(blob.keys))
	}
}

func TestChunkRejectsOriginOutsideSite(t *testing.T) {
	h, sessions, blob, _, visitorID := newReplayTestHandler(t, replayInput(100))
	touchReplaySession(t, sessions, visitorID)
	response := requestChunkWithOrigin(h, 0, true, "", gzipBody(t, []byte("chunk")), "https://attacker.example", "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if len(blob.keys) != 0 {
		t.Fatalf("cross-origin upload wrote %d blobs", len(blob.keys))
	}
}

func TestChunkRejectsUnsampledWithoutTrigger(t *testing.T) {
	h, sessions, blob, _, visitorID := newReplayTestHandler(t, replayInput(0))
	touchReplaySession(t, sessions, visitorID)
	response := requestChunk(h, 0, true, "", gzipBody(t, []byte("chunk")), "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if len(blob.keys) != 0 {
		t.Fatalf("unsampled upload wrote %d blobs", len(blob.keys))
	}
}

func TestChunkAllowsTriggeredContinuation(t *testing.T) {
	h, sessions, blob, _, visitorID := newReplayTestHandler(t, replayInput(0))
	touchReplaySession(t, sessions, visitorID)
	body := gzipBody(t, []byte("chunk"))
	if response := requestChunk(h, 0, true, "error", body, "192.0.2.1:1234", "test-agent"); response.Code != http.StatusAccepted {
		t.Fatalf("trigger status = %d, want 202", response.Code)
	}
	if response := requestChunk(h, 1, false, "", body, "192.0.2.1:1234", "test-agent"); response.Code != http.StatusAccepted {
		t.Fatalf("continuation status = %d, want 202", response.Code)
	}
	if len(blob.keys) != 2 {
		t.Fatalf("triggered appends = %d, want 2", len(blob.keys))
	}
}

func TestChunkDropsWhenSemaphoreFull(t *testing.T) {
	h, sessions, blob, _, visitorID := newReplayTestHandler(t, replayInput(100), WithUploadConcurrency(1))
	touchReplaySession(t, sessions, visitorID)
	body := gzipBody(t, []byte("first"))
	blob.entered = make(chan struct{}, 1)
	blob.release = make(chan struct{})
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- requestChunk(h, 0, true, "", body, "192.0.2.1:1234", "test-agent") }()
	<-blob.entered
	before := health.ReplayDropped.Load()
	second := requestChunk(h, 1, false, "", body, "192.0.2.1:1234", "test-agent")
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", second.Code)
	}
	if got := health.ReplayDropped.Load() - before; got != 1 {
		t.Fatalf("ReplayDropped delta = %d, want 1", got)
	}
	close(blob.release)
	if response := <-firstDone; response.Code != http.StatusAccepted {
		t.Fatalf("first upload status = %d, want 202", response.Code)
	}
}

func TestChunkNoLiveSessionDropped(t *testing.T) {
	h, _, blob, index, _ := newReplayTestHandler(t, replayInput(100))
	before := health.ReplayNoSession.Load()
	response := requestChunk(h, 0, true, "", gzipBody(t, []byte("chunk")), "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.Code)
	}
	if got := health.ReplayNoSession.Load() - before; got != 1 {
		t.Fatalf("ReplayNoSession delta = %d, want 1", got)
	}
	if len(blob.keys) != 0 || len(index.updates) != 0 {
		t.Fatalf("no-session upload wrote blobs=%d index=%d", len(blob.keys), len(index.updates))
	}
}

func TestChunkJoinsLiveSessionAcrossPages(t *testing.T) {
	h, sessions, blob, index, visitorID := newReplayTestHandler(t, replayInput(100))
	result := touchReplaySession(t, sessions, visitorID)
	body := gzipBody(t, []byte("chunk"))
	beforeBytes := health.ReplayBytes.Load()
	for page := 0; page < 3; page++ {
		response := requestChunk(h, 0, true, "", body, "192.0.2.1:1234", "test-agent")
		if response.Code != http.StatusAccepted {
			t.Fatalf("page %d status = %d, want 202", page, response.Code)
		}
	}
	if len(blob.keys) != 3 {
		t.Fatalf("blob appends = %d, want 3", len(blob.keys))
	}
	for _, key := range blob.keys {
		if key != blob.keys[0] {
			t.Fatalf("blob keys = %v, want one session key", blob.keys)
		}
	}
	if want := SessionKey(7, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), result.SessionID); blob.keys[0] != want {
		t.Fatalf("blob key = %q, want %q", blob.keys[0], want)
	}
	var pages int
	for _, update := range index.updates {
		if update.SessionID != result.SessionID {
			t.Fatalf("index session = %d, want %d", update.SessionID, result.SessionID)
		}
		if update.Page {
			pages++
		}
	}
	if pages != 3 {
		t.Fatalf("indexed pages = %d, want 3", pages)
	}
	if got := health.ReplayBytes.Load() - beforeBytes; got != uint64(3*len(body)) {
		t.Fatalf("ReplayBytes delta = %d, want %d", got, 3*len(body))
	}
}

func TestChunkDoesNotMutateAnalyticsSession(t *testing.T) {
	h, sessions, _, _, visitorID := newReplayTestHandler(t, replayInput(100))
	result := touchReplaySession(t, sessions, visitorID)
	initial := sessions.Dirty(1)
	if len(initial) != 1 || initial[0].SessionID != result.SessionID || initial[0].Pageviews != 1 {
		t.Fatalf("initial session = %+v", initial)
	}
	response := requestChunk(h, 0, true, "", gzipBody(t, []byte("chunk")), "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.Code)
	}
	if dirty := sessions.Dirty(1); len(dirty) != 0 {
		t.Fatalf("replay chunk dirtied analytics session: %+v", dirty)
	}
}

func TestChunkRejectsOutOfOrderSequence(t *testing.T) {
	h, sessions, blob, _, visitorID := newReplayTestHandler(t, replayInput(100))
	touchReplaySession(t, sessions, visitorID)
	body := gzipBody(t, []byte("chunk"))
	if response := requestChunk(h, 0, true, "", body, "192.0.2.1:1234", "test-agent"); response.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", response.Code)
	}
	if response := requestChunk(h, 1, false, "", body, "192.0.2.1:1234", "test-agent"); response.Code != http.StatusAccepted {
		t.Fatalf("next status = %d, want 202", response.Code)
	}
	if response := requestChunk(h, 1, false, "", body, "192.0.2.1:1234", "test-agent"); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status = %d, want 400", response.Code)
	}
	if response := requestChunk(h, 0, false, "", body, "192.0.2.1:1234", "test-agent"); response.Code != http.StatusBadRequest {
		t.Fatalf("backwards status = %d, want 400", response.Code)
	}
	if len(blob.keys) != 2 {
		t.Fatalf("blob appends = %d, want 2", len(blob.keys))
	}
}

func TestChunkJoinsLiveSessionAcrossMidnight(t *testing.T) {
	now := time.Date(2026, 10, 9, 0, 5, 0, 0, time.UTC)
	currentSalt := bytes.Repeat([]byte{1}, 32)
	previousSalt := bytes.Repeat([]byte{2}, 32)
	h, sessions, blob, _, _ := newReplayTestHandlerAt(t, replayInput(100), now, currentSalt, previousSalt)
	previousVisitorID := salts.VisitorID(previousSalt, 7, "192.0.2.1", "test-agent")
	started := now.Add(-6 * time.Minute)
	result := sessions.Touch(session.Hit{
		SiteID: 7, Retention: 30, TS: started, LocalDay: salts.LocalDay(started, time.UTC), VisitorID: previousVisitorID,
	})
	response := requestChunk(h, 0, true, "", gzipBody(t, []byte("chunk")), "192.0.2.1:1234", "test-agent")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.Code)
	}
	want := SessionKey(7, salts.LocalDay(started, time.UTC), result.SessionID)
	if len(blob.keys) != 1 || blob.keys[0] != want {
		t.Fatalf("blob keys = %v, want [%q]", blob.keys, want)
	}
}

func BenchmarkChunk64KB(b *testing.B) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	clock := replayTestClock{now: now}
	salt := bytes.Repeat([]byte{1}, 32)
	site := sites.Site{
		ID: 7, PublicID: "site-public", TZ: time.UTC, Retention: 30,
		AllowedHosts: []string{"example.com"}, Replay: replayInput(100),
	}
	sessions := session.NewMemory(1, clock)
	h := NewHandler(HandlerDeps{
		Sites: replayTestRegistry{site: site}, Salts: replayTestSalts{salts.LocalDay(now, time.UTC): salt}, Sessions: sessions,
		Blobs: benchmarkBlob{}, Index: benchmarkIndex{}, Replay: config.ReplayEnv{Enabled: true},
	}, WithClock(clock))
	visitorID := salts.VisitorID(salt, site.ID, "192.0.2.1", "test-agent")
	touchReplaySession(b, sessions, visitorID)
	body := make([]byte, 64<<10)
	body[0], body[1] = 0x1f, 0x8b
	target, err := url.Parse("/api/replay/chunk?s=site-public&q=0&p=1")
	if err != nil {
		b.Fatal(err)
	}
	reader := &benchmarkBody{body: body}
	request := &http.Request{
		Method: http.MethodPost, URL: target, Body: reader, ContentLength: int64(len(body)),
		RemoteAddr: "192.0.2.1:1234", Header: http.Header{"User-Agent": []string{"test-agent"}},
	}
	response := &benchmarkResponse{header: make(http.Header)}
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		reader.offset = 0
		response.status = 0
		h.Chunk(response, request)
		if response.status != http.StatusAccepted {
			b.Fatalf("status = %d", response.status)
		}
	}
}

type benchmarkBlob struct{}

func (benchmarkBlob) Append(_ context.Context, _ string, body io.Reader) (int64, error) {
	return io.Copy(io.Discard, body)
}
func (benchmarkBlob) Open(context.Context, string) (io.ReadCloser, error) { return nil, nil }
func (benchmarkBlob) PutIfAbsent(context.Context, string, io.Reader) (bool, error) {
	return false, nil
}
func (benchmarkBlob) DeletePrefix(context.Context, string) (int64, error) { return 0, nil }
func (benchmarkBlob) Delete(context.Context, string) (int64, error)       { return 0, nil }

type benchmarkIndex struct{}

func (benchmarkIndex) Add(IndexUpdate) {}

type benchmarkBody struct {
	body   []byte
	offset int
}

func (r *benchmarkBody) Read(destination []byte) (int, error) {
	if r.offset == len(r.body) {
		return 0, io.EOF
	}
	n := copy(destination, r.body[r.offset:])
	r.offset += n
	return n, nil
}
func (*benchmarkBody) Close() error { return nil }

type benchmarkResponse struct {
	header http.Header
	status int
}

func (w *benchmarkResponse) Header() http.Header          { return w.header }
func (*benchmarkResponse) Write(body []byte) (int, error) { return len(body), nil }
func (w *benchmarkResponse) WriteHeader(status int)       { w.status = status }

type startWire struct {
	Mode string `json:"m"`
}

func decodeStart(t testing.TB, response *httptest.ResponseRecorder) startWire {
	t.Helper()
	var wire startWire
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}

func newReplayTestHandler(t testing.TB, input sites.ReplaySiteInput, options ...HandlerOption) (*Handler, *session.Memory, *replayTestBlob, *replayTestIndex, int64) {
	t.Helper()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	salt := bytes.Repeat([]byte{1}, 32)
	return newReplayTestHandlerAt(t, input, now, salt, bytes.Repeat([]byte{2}, 32), options...)
}

func newReplayTestHandlerAt(t testing.TB, input sites.ReplaySiteInput, now time.Time, currentSalt, previousSalt []byte, options ...HandlerOption) (*Handler, *session.Memory, *replayTestBlob, *replayTestIndex, int64) {
	t.Helper()
	clock := replayTestClock{now: now}
	sessions := session.NewMemory(1, clock)
	blob := &replayTestBlob{}
	index := &replayTestIndex{}
	site := sites.Site{
		ID: 7, PublicID: "site-public", TZ: time.UTC, Retention: 30,
		AllowedHosts: []string{"example.com"}, Replay: input,
	}
	day := salts.LocalDay(now, site.TZ)
	saltStore := replayTestSalts{day: currentSalt, day.AddDate(0, 0, -1): previousSalt}
	h := NewHandler(HandlerDeps{
		Sites: replayTestRegistry{site: site}, Salts: saltStore, Sessions: sessions,
		Blobs: blob, Index: index, Replay: config.ReplayEnv{Enabled: true},
	}, append([]HandlerOption{WithClock(clock)}, options...)...)
	visitorID := salts.VisitorID(currentSalt, site.ID, "192.0.2.1", "test-agent")
	return h, sessions, blob, index, visitorID
}

func replayInput(rate int) sites.ReplaySiteInput {
	return sites.ReplaySiteInput{Enabled: true, SampleRate: &rate}
}

func touchReplaySession(t testing.TB, sessions *session.Memory, visitorID int64) session.Result {
	t.Helper()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	result := sessions.Touch(session.Hit{
		SiteID: 7, Retention: 30, TS: now, LocalDay: salts.LocalDay(now, time.UTC), VisitorID: visitorID,
	})
	if result.SessionID == 0 {
		t.Fatal("failed to create live session")
	}
	return result
}

func requestStart(t testing.TB, h *Handler, pageURL, remoteAddr, userAgent string) *httptest.ResponseRecorder {
	return requestStartWithOrigin(t, h, pageURL, "", remoteAddr, userAgent)
}

func requestStartWithOrigin(t testing.TB, h *Handler, pageURL, origin, remoteAddr, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"s": "site-public", "u": pageURL})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/replay/start", bytes.NewReader(body))
	request.RemoteAddr = remoteAddr
	request.Header.Set("User-Agent", userAgent)
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	h.Start(response, request)
	return response
}

func requestChunk(h *Handler, sequence int, page bool, trigger string, body []byte, remoteAddr, userAgent string) *httptest.ResponseRecorder {
	return requestChunkWithOrigin(h, sequence, page, trigger, body, "", remoteAddr, userAgent)
}

func requestChunkWithOrigin(h *Handler, sequence int, page bool, trigger string, body []byte, origin, remoteAddr, userAgent string) *httptest.ResponseRecorder {
	pageValue := "0"
	if page {
		pageValue = "1"
	}
	target := "/api/replay/chunk?s=site-public&q=" + itoa(sequence) + "&p=" + pageValue
	if trigger != "" {
		target += "&t=" + trigger
	}
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	request.RemoteAddr = remoteAddr
	request.Header.Set("User-Agent", userAgent)
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	h.Chunk(response, request)
	return response
}

func gzipBody(t testing.TB, plain []byte) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func assertCORS(t testing.TB, response *httptest.ResponseRecorder) {
	t.Helper()
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}

// The browser keeps one sequence counter per tab, so a client-side navigation
// starts a new page with the next number rather than 0, and a dropped chunk
// leaves a gap. Both must be accepted; duplicates and reordering must not.
func TestChunkSequenceAcrossSPANavigation(t *testing.T) {
	h, sessions, blob, _, visitorID := newReplayTestHandler(t, replayInput(100))
	touchReplaySession(t, sessions, visitorID)
	body := gzipBody(t, []byte("chunk"))
	steps := []struct {
		sequence int
		page     bool
		want     int
	}{
		{0, true, http.StatusAccepted},
		{1, false, http.StatusAccepted},
		{2, true, http.StatusAccepted},    // SPA navigation keeps counting
		{3, false, http.StatusAccepted},   // continues on the new page
		{3, false, http.StatusBadRequest}, // duplicate
		{5, false, http.StatusAccepted},   // gap after a dropped chunk
		{4, false, http.StatusBadRequest}, // out of order
		{6, true, http.StatusAccepted},
	}
	for i, step := range steps {
		response := requestChunk(h, step.sequence, step.page, "", body, "192.0.2.1:1234", "test-agent")
		if response.Code != step.want {
			t.Fatalf("step %d (q=%d p=%t): status = %d, want %d", i, step.sequence, step.page, response.Code, step.want)
		}
	}
	if len(blob.keys) != 6 {
		t.Fatalf("blob appends = %d, want 6", len(blob.keys))
	}
}
