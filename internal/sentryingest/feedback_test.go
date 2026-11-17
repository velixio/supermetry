package sentryingest

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/appissues"
	"supermetry/internal/sites"
)

type feedbackSink struct {
	mu    sync.Mutex
	items []appissues.FeedbackItem
	full  bool
}

func (s *feedbackSink) Add(it appissues.FeedbackItem) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return false
	}
	s.items = append(s.items, it)
	return true
}

var feedbackSite = sites.Site{ID: 42, Kind: sites.KindApp, TZ: time.UTC, App: &sites.AppConfig{HashSecret: []byte("secret-secret-secret")}}

func TestParseFeedbackItem(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	raw := `{"event_id":"ffffffffffffffffffffffffffffffff","timestamp":1790000000,"contexts":{"feedback":{
"message":"Broken, mail me at jane.doe@example.com card 4242424242424242","contact_email":"Jane.Doe@Example.com","name":"Jane","associated_event_id":"AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA","url":"https://shop.example.com/checkout?token=abc#x","source":"widget"}}}`
	it, err := ParseFeedback("feedback", []byte(raw), feedbackSite, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(it.Message, "jane.doe") || strings.Contains(it.Message, "4242") || !strings.Contains(it.Message, "[email]") {
		t.Errorf("message not scrubbed: %q", it.Message)
	}
	if it.EventID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || it.SiteID != 42 || !it.At.Equal(now) {
		t.Errorf("item = %+v", it)
	}
	if it.URL != "/checkout" {
		t.Errorf("url = %q", it.URL)
	}
	if want := HashUser(feedbackSite.App.HashSecret, "email:jane.doe@example.com"); !bytes.Equal(it.EmailHash, want) || len(want) == 0 {
		t.Errorf("email hash = %x", it.EmailHash)
	}
	if bytes.Contains(it.EmailHash, []byte("jane")) {
		t.Error("raw email in hash")
	}
}

func TestParseUserReportItem(t *testing.T) {
	now := time.Now()
	it, err := ParseFeedback("user_report", []byte(`{"event_id":"11111111111111111111111111111111","name":"Bo","email":"bo@example.com","comments":"crashed on save"}`), feedbackSite, now)
	if err != nil || it.EventID != "11111111111111111111111111111111" || it.Message != "crashed on save" || len(it.EmailHash) == 0 {
		t.Fatalf("item = %+v err %v", it, err)
	}
}

func TestParseFeedbackRejectsAndClips(t *testing.T) {
	now := time.Now()
	for name, raw := range map[string]string{
		"not json":      `x`,
		"empty message": `{"contexts":{"feedback":{"message":"   "}}}`,
		"no feedback":   `{"contexts":{}}`,
	} {
		if _, err := ParseFeedback("feedback", []byte(raw), feedbackSite, now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	long := strings.Repeat("word ", 3000)
	it, err := ParseFeedback("feedback", []byte(`{"contexts":{"feedback":{"message":"`+long+`","associated_event_id":"not-an-id"}}}`), feedbackSite, now)
	if err != nil || len(it.Message) > 5000 || it.EventID != "" || it.EmailHash != nil {
		t.Fatalf("clip: len %d event %q err %v", len(it.Message), it.EventID, err)
	}
	// No secret yet: the email cannot be hashed, so it is dropped, never stored raw.
	bare := sites.Site{ID: 1, App: &sites.AppConfig{}}
	it, err = ParseFeedback("user_report", []byte(`{"email":"a@b.example","comments":"x"}`), bare, now)
	if err != nil || it.EmailHash != nil {
		t.Fatalf("no secret: %+v %v", it, err)
	}
}

const feedbackEnvelope = `{"event_id":"11111111111111111111111111111111"}` + "\n" +
	`{"type":"feedback"}` + "\n" +
	`{"contexts":{"feedback":{"message":"hello there","associated_event_id":"22222222222222222222222222222222"}}}` + "\n" +
	`{"type":"user_report"}` + "\n" +
	`{"event_id":"33333333333333333333333333333333","comments":"legacy"}` + "\n"

func TestHandlerStoresFeedbackAndStopsRejectingIt(t *testing.T) {
	e := newStubEnv(t)
	sink := &feedbackSink{}
	e.h.WithFeedback(sink)
	w := e.do(post(fdPath, fdQuery, feedbackEnvelope, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if len(sink.items) != 2 || sink.items[0].EventID != "22222222222222222222222222222222" || sink.items[1].Message != "legacy" {
		t.Fatalf("items = %+v", sink.items)
	}
	limits := w.Header().Get("X-Sentry-Rate-Limits")
	if strings.Contains(limits, "feedback") || strings.Contains(limits, "user_report") || !strings.Contains(limits, "transaction") {
		t.Fatalf("rate limits = %q", limits)
	}
	if got := e.h.Stats().Snapshot()["feedback_stored"]; got != 2 {
		t.Errorf("feedback_stored = %d", got)
	}
}

func TestHandlerFeedbackIsCountedAgainstSpikeProtectionAndSurvivesFullQueue(t *testing.T) {
	e := newStubEnv(t)
	sink := &feedbackSink{full: true}
	e.h.WithFeedback(sink)
	if w := e.do(post(fdPath, fdQuery, feedbackEnvelope, nil)); w.Code != http.StatusOK {
		t.Fatalf("full queue: %d", w.Code)
	}
	if got := e.h.Stats().Snapshot()["feedback_dropped"]; got != 2 {
		t.Errorf("feedback_dropped = %d", got)
	}
	e = newStubEnv(t)
	e.h.WithFeedback(&feedbackSink{}).WithSpikeLimit(1)
	if w := e.do(post(fdPath, fdQuery, feedbackEnvelope, nil)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("2 reports under a 1/min cap: %d", w.Code)
	}
}

func TestHandlerWithoutFeedbackSinkDiscardsQuietly(t *testing.T) {
	e := newStubEnv(t)
	if w := e.do(post(fdPath, fdQuery, feedbackEnvelope, nil)); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}
