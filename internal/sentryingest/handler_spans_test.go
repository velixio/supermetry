package sentryingest

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"supermetry/internal/sites"
	"supermetry/internal/spans"
)

type spanSink struct {
	mu      sync.Mutex
	batches [][]spans.Span
	full    bool
}

func (s *spanSink) Enqueue(b []spans.Span) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return false
	}
	s.batches = append(s.batches, b)
	return true
}

func (s *spanSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		n += len(b)
	}
	return n
}

func itemEnv(eventID string, items ...[2]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"event_id":%q}`+"\n", eventID)
	for _, it := range items {
		fmt.Fprintf(&b, "%s\n%s\n", strings.Replace(it[0], "}", fmt.Sprintf(`,"length":%d}`, len(it[1])), 1), it[1])
	}
	return b.String()
}

func txItem(payload []byte) [2]string { return [2]string{`{"type":"transaction"}`, string(payload)} }
func v2ItemEnv(payload []byte) [2]string {
	return [2]string{`{"type":"span","content_type":"` + SpanV2ContentType + `"}`, string(payload)}
}

const txEventID = "cccccccccccccccccccccccccccccccc"

func newSpanEnv(t *testing.T, budget int) (*env, *spanSink) {
	t.Helper()
	e := newEnv(t)
	sink := &spanSink{}
	e.h.WithSpans(sink, spans.NewBudget(budget))
	return e, sink
}

func TestHandlerTransactionStoredWithErrorEvent(t *testing.T) {
	e, sink := newSpanEnv(t, 1000)
	body := itemEnv(txEventID, [2]string{`{"type":"event"}`, `{"message":"boom","event_id":"` + txEventID + `"}`},
		txItem(txPayload("", txSpan("1111111111111111", txRoot, "db.query", "select 1", 0, 0.01))))
	w := e.do(post(fdPath, fdQuery, body, nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("X-Sentry-Rate-Limits"); got != perfUnsupported {
		t.Errorf("limits = %q", got)
	}
	if sink.count() != 2 || e.queued() != 1 {
		t.Errorf("spans %d, events %d", sink.count(), e.queued())
	}
}

func TestHandlerTransactionRetryStoredOnce(t *testing.T) {
	e, sink := newSpanEnv(t, 1000)
	body := itemEnv(txEventID, txItem(txPayload("")))
	for range 3 {
		if w := e.do(post(fdPath, fdQuery, body, nil)); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if len(sink.batches) != 1 {
		t.Errorf("batches = %d, want 1", len(sink.batches))
	}
	if e.h.Stats().Snapshot()["transaction_duplicate"] != 2 {
		t.Errorf("stats %v", e.h.Stats().Snapshot())
	}
}

func TestHandlerRetryAfterQueueFullIsNotADuplicate(t *testing.T) {
	e, sink := newSpanEnv(t, 1000)
	sink.full = true
	body := itemEnv(txEventID, txItem(txPayload("")))
	if w := e.do(post(fdPath, fdQuery, body, nil)); w.Code != 503 {
		t.Fatalf("status %d", w.Code)
	}
	sink.full = false
	if w := e.do(post(fdPath, fdQuery, body, nil)); w.Code != 200 || sink.count() != 1 {
		t.Fatalf("retry: %d, spans %d", w.Code, sink.count())
	}
}

func TestHandlerPerformanceOff(t *testing.T) {
	e, sink := newSpanEnv(t, 1000)
	off := false
	site := appSite(fdSite, sites.DSNKey{PublicKey: fdKey})
	site.App.PerformanceEnabled = &off
	e.reg.sites[fdSite] = site
	w := e.do(post(fdPath, fdQuery, itemEnv(txEventID, txItem(txPayload(""))), nil))
	if w.Code != 200 || sink.count() != 0 || w.Header().Get("X-Sentry-Rate-Limits") != rejectUnsupported {
		t.Errorf("%d spans=%d limits=%q", w.Code, sink.count(), w.Header().Get("X-Sentry-Rate-Limits"))
	}
}

func TestHandlerSpanBudget(t *testing.T) {
	e, sink := newSpanEnv(t, 2)
	// Transaction with 3 spans on a budget of 2: the first two (segment first) are kept.
	body := itemEnv(txEventID, txItem(txPayload("", txSpan("1111111111111111", txRoot, "x", "d", 0, 0.01), txSpan("2222222222222222", txRoot, "x", "d", 0, 0.01))))
	w := e.do(post(fdPath, fdQuery, body, nil))
	if w.Code != 200 || sink.count() != 2 || !sink.batches[0][0].IsSegment || !strings.Contains(w.Header().Get("X-Sentry-Rate-Limits"), spanLimits) {
		t.Fatalf("partial: %d spans=%d limits=%q", w.Code, sink.count(), w.Header().Get("X-Sentry-Rate-Limits"))
	}
	// Budget gone and the request holds only a transaction: 429.
	other := itemEnv("dddddddddddddddddddddddddddddddd", txItem(txPayload("")))
	w = e.do(post(fdPath, fdQuery, other, nil))
	if w.Code != 429 || w.Header().Get("X-Sentry-Rate-Limits") != spanLimits {
		t.Fatalf("exhausted: %d %q", w.Code, w.Header().Get("X-Sentry-Rate-Limits"))
	}
	// An error event next to an over-budget transaction: 200, event accepted, span limit in the header.
	mixed := itemEnv("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", [2]string{`{"type":"event"}`, `{"message":"boom"}`}, txItem(txPayload("")))
	w = e.do(post(fdPath, fdQuery, mixed, nil))
	if w.Code != 200 || e.queued() != 1 || !strings.Contains(w.Header().Get("X-Sentry-Rate-Limits"), spanLimits) {
		t.Fatalf("mixed: %d queued=%d %q", w.Code, e.queued(), w.Header().Get("X-Sentry-Rate-Limits"))
	}
}

func TestHandlerQueueFullOnlyTransaction(t *testing.T) {
	e, sink := newSpanEnv(t, 1000)
	sink.full = true
	w := e.do(post(fdPath, fdQuery, itemEnv(txEventID, txItem(txPayload(""))), nil))
	if w.Code != 503 {
		t.Fatalf("%d", w.Code)
	}
	// With an accepted error event in the same request the answer stays 200.
	mixed := itemEnv("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", [2]string{`{"type":"event"}`, `{"message":"boom"}`}, txItem(txPayload("")))
	if w := e.do(post(fdPath, fdQuery, mixed, nil)); w.Code != 200 {
		t.Fatalf("mixed %d", w.Code)
	}
}

func TestHandlerSpanContainer(t *testing.T) {
	e, sink := newSpanEnv(t, 1000)
	body := itemEnv("", v2ItemEnv(v2Container(v2Item(txRoot, "", true, "GET /x", 0, 0.1, "{}"))))
	if w := e.do(post(fdPath, fdQuery, body, nil)); w.Code != 200 || sink.count() != 1 {
		t.Fatalf("%d spans=%d", w.Code, sink.count())
	}
	// A span item with another content type is counted, not stored.
	other := itemEnv("", [2]string{`{"type":"span"}`, `{}`})
	if w := e.do(post(fdPath, fdQuery, other, nil)); w.Code != 200 || sink.count() != 1 {
		t.Fatalf("other %d", w.Code)
	}
}

func TestHandlerTransactionsDoNotUseSpikeGuard(t *testing.T) {
	e, sink := newSpanEnv(t, 100000)
	e.h.WithSpikeLimit(1)
	for i := range 5 {
		id := fmt.Sprintf("%032x", i+1)
		if w := e.do(post(fdPath, fdQuery, itemEnv(id, txItem(txPayload(""))), nil)); w.Code != 200 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	if len(sink.batches) != 5 {
		t.Errorf("batches %d", len(sink.batches))
	}
}

func TestHandlerTransactionNameCap(t *testing.T) {
	e, sink := newSpanEnv(t, 1_000_000)
	e.h.names = spans.NewNameCaps(2)
	for i := range 4 {
		p := strings.Replace(string(txPayload("")), "GET /api/items", fmt.Sprintf("GET /r%d", i), 1)
		e.do(post(fdPath, fdQuery, itemEnv(fmt.Sprintf("%032x", i+1), txItem([]byte(p))), nil))
	}
	var names []string
	for _, b := range sink.batches {
		names = append(names, b[0].Name)
	}
	if strings.Join(names, ",") != "GET /r0,GET /r1,<other>,<other>" || e.h.Stats().Snapshot()["transaction_name_overflow"] != 2 {
		t.Errorf("names %v stats %v", names, e.h.Stats().Snapshot())
	}
}
