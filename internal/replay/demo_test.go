package replay

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"supermetry/internal/sites"
)

func TestDemoSiteReplayRejectedLikeUnknownSite(t *testing.T) {
	h, _, blob, index, _ := newReplayTestHandler(t, replayInput(100))
	site, _ := h.deps.Sites.ByPublicID("site-public")
	site.Demo = true
	h.deps.Sites = replayTestRegistry{site: site}

	unknownStartRequest := httptest.NewRequest(http.MethodPost, "/api/replay/start", bytes.NewBufferString(`{"s":"unknown","u":"https://example.com/page"}`))
	unknownStart := httptest.NewRecorder()
	h.Start(unknownStart, unknownStartRequest)
	demoStart := requestStart(t, h, "https://example.com/page", "192.0.2.1:1234", "test-agent")
	assertSameReplayRejection(t, demoStart, unknownStart)

	body := gzipBody(t, []byte("{}\n"))
	unknownChunkRequest := httptest.NewRequest(http.MethodPost, "/api/replay/chunk?s=unknown&q=0&p=1&v=1", bytes.NewReader(body))
	unknownChunk := httptest.NewRecorder()
	h.Chunk(unknownChunk, unknownChunkRequest)
	demoChunk := requestChunk(h, 0, true, "", body, "192.0.2.1:1234", "test-agent")
	assertSameReplayRejection(t, demoChunk, unknownChunk)
	if len(blob.keys) != 0 || len(index.updates) != 0 {
		t.Fatalf("demo replay persisted blobs=%d index_updates=%d", len(blob.keys), len(index.updates))
	}
}

func assertSameReplayRejection(t *testing.T, got, want *httptest.ResponseRecorder) {
	t.Helper()
	if got.Code != want.Code || got.Body.String() != want.Body.String() || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
		t.Fatalf("got=(%d,%q,%q), want=(%d,%q,%q)", got.Code, got.Body.String(), got.Header().Get("Content-Type"), want.Code, want.Body.String(), want.Header().Get("Content-Type"))
	}
}

var _ sites.Registry = replayTestRegistry{}
