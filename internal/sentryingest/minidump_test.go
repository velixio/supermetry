package sentryingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"supermetry/internal/appingest"
)

const dumpURL = "/api/42/minidump/?sentry_key=" + fdKey

type formPart struct {
	name, filename string
	data           []byte
}

func multipartBody(t testing.TB, parts ...formPart) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		var w interface{ Write([]byte) (int, error) }
		var err error
		if p.filename != "" {
			w, err = mw.CreateFormFile(p.name, p.filename)
		} else {
			w, err = mw.CreateFormField(p.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		w.Write(p.data)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func dumpRequest(t testing.TB, url string, body *bytes.Buffer, ctype string, gz bool) *http.Request {
	t.Helper()
	var rd = body
	if gz {
		var z bytes.Buffer
		zw := gzip.NewWriter(&z)
		zw.Write(body.Bytes())
		zw.Close()
		rd = &z
	}
	r := httptest.NewRequest(http.MethodPost, url, rd)
	r.Header.Set("Content-Type", ctype)
	if gz {
		r.Header.Set("Content-Encoding", "gzip")
	}
	return r
}

// dumpEnv is a handler with the real normaliser and a symbolicator running the
// minidump resolver with the app.sym cache.
func dumpEnv(t *testing.T, mut ...func(*SymbolicatorConfig)) *env {
	t.Helper()
	e := newEnv(t)
	s, _, _ := dumpSymbolicator(t, true, mut...)
	s.cfg.Deliver = nil
	e.h.WithSymbolicator(s)
	return e
}

func (e *env) waitBody(t testing.TB) appingest.Body {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.bodies.mu.Lock()
		n := len(e.bodies.items)
		var b appingest.Body
		if n > 0 {
			b = e.bodies.items[n-1]
		}
		e.bodies.mu.Unlock()
		if n > 0 {
			return b
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no body stored")
	return appingest.Body{}
}

func TestMinidumpEndpointAcceptsCrashpadUpload(t *testing.T) {
	e := dumpEnv(t)
	body, ct := multipartBody(t,
		formPart{"upload_file_minidump", "x.dmp", appDump()},
		formPart{"sentry[release]", "", []byte("fixture@1.0.0")},
		formPart{"sentry[tags][process]", "", []byte("browser")},
		formPart{"secret", "", []byte("xyz")},
	)
	w := e.do(dumpRequest(t, dumpURL, body, ct, false))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("status %d %q ct %q", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(w.Body.String()) {
		t.Fatalf("body %q is not a UUID", w.Body.String())
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS offered on /minidump/")
	}
	q := recvQueued(t, e.intake)
	b := e.waitBody(t)
	if q.Event.Release != "fixture@1.0.0" || q.Event.Tags["process"] != "main" || b.Tags["process"] != "main" || q.Event.Kind != "crash" {
		t.Fatalf("event %+v tags %v", q.Event, b.Tags)
	}
	if q.Event.Symbolication != "ok" || b.Exceptions[0].Frames[1].Function != "crash_here" {
		t.Fatalf("symbolication %q frames %+v", q.Event.Symbolication, b.Exceptions[0].Frames)
	}
	if strings.Contains(fmt.Sprint(b, q.Event), "xyz") {
		t.Fatal("an unknown form field reached the event")
	}
	if b.Minidump != nil {
		t.Fatal("the stored body carries the dump")
	}
}

func TestMinidumpEndpointCrashpadAnnotationsAndSentryJSON(t *testing.T) {
	e := dumpEnv(t)
	body, ct := multipartBody(t,
		formPart{"upload_file_minidump", "x.dmp", appDump()},
		formPart{"sentry", "", []byte(`{"release":"j@2","tags":{"a":"b"}}`)},
		formPart{"ptype", "", []byte("gpu-process")},
		formPart{"prod", "", []byte("Fixture")},
		formPart{"ver", "", []byte("9.9")},
	)
	if w := e.do(dumpRequest(t, dumpURL, body, ct, false)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	q := recvQueued(t, e.intake)
	if q.Event.Release != "j@2" || q.Event.Tags["process"] != "gpu" || q.Event.Tags["a"] != "b" {
		t.Fatalf("event %+v", q.Event)
	}
}

func TestMinidumpEndpointRejections(t *testing.T) {
	e := dumpEnv(t)
	good, ct := multipartBody(t, formPart{"upload_file_minidump", "x.dmp", appDump()})
	noFile, ctNo := multipartBody(t, formPart{"sentry[release]", "", []byte("r")})
	badMagic, ctBad := multipartBody(t, formPart{"upload_file_minidump", "x.dmp", append([]byte("MDMQ"), make([]byte, 64)...)})
	big := append([]byte("MDMP"), make([]byte, 17<<20)...)
	bomb, ctBomb := multipartBody(t, formPart{"upload_file_minidump", "x.dmp", big})

	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"missing file", dumpRequest(t, dumpURL, noFile, ctNo, false), 400},
		{"bad magic", dumpRequest(t, dumpURL, badMagic, ctBad, false), 400},
		{"gzip bomb", dumpRequest(t, dumpURL, bomb, ctBomb, true), 413},
		{"not multipart", dumpRequest(t, dumpURL, bytes.NewBufferString("x"), "application/json", false), 400},
		{"wrong key", dumpRequest(t, "/api/42/minidump/?sentry_key="+fdOtherKey, good, ct, false), 401},
		{"no key", dumpRequest(t, "/api/42/minidump/", good, ct, false), 401},
		{"key of another site", dumpRequest(t, "/api/43/minidump/?sentry_key="+fdKey, good, ct, false), 401},
	}
	for _, c := range cases {
		if w := e.do(c.req); w.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, w.Code, c.want, w.Body.String())
		}
		good, ct = multipartBody(t, formPart{"upload_file_minidump", "x.dmp", appDump()}) // a fresh body each time
	}
	if got := e.h.Stats().Snapshot()["minidump_too_large"]; got != 1 {
		t.Fatalf("minidump_too_large = %d", got)
	}
	if e.queued() != 0 {
		t.Fatal("a rejected upload reached the intake")
	}
}

func TestMinidumpEndpointAuthHeaderAndMethod(t *testing.T) {
	e := dumpEnv(t)
	body, ct := multipartBody(t, formPart{"upload_file_minidump", "x.dmp", appDump()})
	r := dumpRequest(t, "/api/42/minidump/", body, ct, false)
	r.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7, sentry_key="+fdKey)
	if w := e.do(r); w.Code != 200 {
		t.Fatalf("header auth: status %d", w.Code)
	}
	for _, m := range []string{http.MethodGet, http.MethodOptions} {
		w := e.do(httptest.NewRequest(m, dumpURL, nil))
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s: status %d, headers %v", m, w.Code, w.Header())
		}
	}
}

func TestMinidumpFieldsDroppedUnreadBeyondAllowlist(t *testing.T) {
	var parts []formPart
	parts = append(parts, formPart{"upload_file_minidump", "x.dmp", appDump()})
	for i := 0; i < 50; i++ {
		parts = append(parts, formPart{fmt.Sprintf("junk%d", i), "", bytes.Repeat([]byte("z"), 20<<10)})
	}
	body, ct := multipartBody(t, parts...)
	form, err := parseMinidumpForm(body.Bytes(), strings.TrimPrefix(ct, "multipart/form-data; boundary="))
	if err != nil || form.file == nil || len(form.fields) != 0 {
		t.Fatalf("err %v file %v fields %v", err, form.file != nil, form.fields)
	}
}

func FuzzMinidumpForm(f *testing.F) {
	body, ct := multipartBody(f, formPart{"upload_file_minidump", "x.dmp", appDump()}, formPart{"sentry[release]", "", []byte("r")})
	boundary := strings.TrimPrefix(ct, "multipart/form-data; boundary=")
	f.Add(body.Bytes(), boundary)
	f.Add([]byte("--b\r\nContent-Disposition: form-data; name=\"upload_file_minidump\"\r\n\r\nMDMP\r\n--b--"), "b")
	f.Fuzz(func(t *testing.T, data []byte, boundary string) {
		if len(data) > 1<<20 {
			return
		}
		form, _ := parseMinidumpForm(data, boundary)
		total := len(form.file) + len(form.sentry)
		for k, v := range form.fields {
			total += len(k) + len(v)
		}
		if total > len(data) || total > MaxMinidump+maxMinidumpFields+MaxEventBytes {
			t.Fatalf("parsed %d bytes from %d", total, len(data))
		}
		if _, err := minidumpEvent(form, "00000000000000000000000000000001"); err != nil {
			t.Fatal(err)
		}
	})
}

// envelopeWithDump is an envelope with one event item and an event.minidump
// attachment of the given payload.
func envelopeWithDump(id string, dump []byte) string {
	ev := fmt.Sprintf(`{"event_id":%q,"timestamp":%d,"platform":"native","level":"fatal"}`, id, fdNow.Unix())
	return fmt.Sprintf("{\"event_id\":%q}\n{\"type\":\"event\",\"length\":%d}\n%s\n{\"type\":\"attachment\",\"length\":%d,\"attachment_type\":\"event.minidump\",\"filename\":\"x.dmp\"}\n%s\n",
		id, len(ev), ev, len(dump), dump)
}

func TestEnvelopeMinidumpLargerThanAttachmentCapIsProcessed(t *testing.T) {
	e := dumpEnv(t)
	dump := append(appDump(), make([]byte, 2<<20)...) // 2 MiB, past MaxAttachment
	if w := e.do(post(fdPath, fdQuery, envelopeWithDump(fmt.Sprintf("%032d", 1), dump), nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	q := recvQueued(t, e.intake)
	b := e.waitBody(t)
	if q.Event.Symbolication != "ok" || q.Event.Kind != "crash" || b.Exceptions[0].Frames[1].Function != "crash_here" {
		t.Fatalf("event %+v frames %+v", q.Event, b.Exceptions)
	}
	snap := e.h.Stats().Snapshot()
	if snap["minidump_deferred"] != 0 || snap["attachment_too_large"] != 0 {
		t.Fatalf("stats %v", snap)
	}
}

func TestEnvelopeMinidumpOverCapIsCountedAndEventStored(t *testing.T) {
	e := dumpEnv(t)
	dump := append(appDump(), make([]byte, 17<<20)...)
	if w := e.do(post(fdPath, fdQuery, envelopeWithDump(fmt.Sprintf("%032d", 2), dump), nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	q := recvQueued(t, e.intake)
	b := e.waitBody(t)
	if q.Event.Kind != "crash" || len(b.Exceptions) != 0 || b.Minidump != nil || q.Event.Symbolication != "" {
		t.Fatalf("event %+v body exceptions %v", q.Event, b.Exceptions)
	}
	if e.h.Stats().Snapshot()["minidump_too_large"] != 1 {
		t.Fatalf("stats %v", e.h.Stats().Snapshot())
	}
}

// blockingResolver holds every minidump job until released.
type blockingResolver struct {
	enter   chan int
	release chan struct{}
}

func (blockingResolver) Wants(b appingest.Body) bool { return len(b.Minidump) > 0 }
func (r blockingResolver) Resolve(_ context.Context, _ appingest.Event, b *appingest.Body, _ *Symbolicator) (int, int) {
	r.enter <- len(b.Minidump)
	<-r.release
	return 0, 0
}

func TestMinidumpInflightBudgetSkipsTheSecondDump(t *testing.T) {
	br := blockingResolver{enter: make(chan int, 4), release: make(chan struct{})}
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(br.release) }) })
	e := dumpEnv(t, func(c *SymbolicatorConfig) {
		c.Resolvers = []Resolver{br}
		c.MinidumpInflightBytes = 1 << 20
		c.Workers = 1
	})
	dump := append(appDump(), make([]byte, 768<<10)...)
	if w := e.do(post(fdPath, fdQuery, envelopeWithDump(fmt.Sprintf("%032d", 3), dump), nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	select {
	case <-br.enter:
	case <-time.After(5 * time.Second):
		t.Fatal("the first dump never reached the resolver")
	}
	if w := e.do(post(fdPath, fdQuery, envelopeWithDump(fmt.Sprintf("%032d", 4), dump), nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if got := e.h.symb.Stats().Snapshot()["minidump_skipped"]; got != 1 {
		t.Fatalf("minidump_skipped = %d", got)
	}
	// The skipped event proceeds at once, without a stack.
	q := recvQueued(t, e.intake)
	if q.Event.ID != fmt.Sprintf("%032d", 4) || q.Event.Symbolication != "" {
		t.Fatalf("event %+v", q.Event)
	}
	once.Do(func() { close(br.release) })
	// Once the first finishes its budget is free again.
	recvQueued(t, e.intake)
	if w := e.do(post(fdPath, fdQuery, envelopeWithDump(fmt.Sprintf("%032d", 5), dump), nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if got := e.h.symb.Stats().Snapshot()["minidump_skipped"]; got != 1 {
		t.Fatalf("minidump_skipped = %d after the budget freed", got)
	}
}

func TestEnvelopeWithoutSymbolicatorDropsTheDump(t *testing.T) {
	e := newEnv(t)
	if w := e.do(post(fdPath, fdQuery, envelopeWithDump(fmt.Sprintf("%032d", 6), appDump()), nil)); w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if b := e.waitBody(t); b.Minidump != nil {
		t.Fatal("the dump reached the body queue")
	}
	if e.h.Stats().Snapshot()["minidump_unprocessed"] != 1 {
		t.Fatalf("stats %v", e.h.Stats().Snapshot())
	}
}

func TestProcessTag(t *testing.T) {
	for in, want := range map[string]string{"browser": "main", "main": "main", "renderer": "renderer", "gpu-process": "gpu", "utility": "utility"} {
		if got := processTag(map[string]string{"event.process": in}, nil); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	if got := processTag(map[string]string{"process": "worker"}, nil); got != "" {
		t.Errorf("an app's own process tag was rewritten to %q", got)
	}
	if got := processTag(map[string]string{"event.process": "zygote"}, nil); got != "other" {
		t.Errorf("unknown -> %q", got)
	}
	if got := processTag(nil, map[string]map[string]any{"electron": {"crashpad.process_type": "renderer"}}); got != "renderer" {
		t.Errorf("context -> %q", got)
	}
	if got := processTag(nil, nil); got != "" {
		t.Errorf("none -> %q", got)
	}
}
