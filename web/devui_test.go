//go:build !embedui

package web

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a fake Vite: it records what reached it and answers 200.
type recorder struct {
	mu   sync.Mutex
	seen []*http.Request
}

func (u *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.seen = append(u.seen, r)
	u.mu.Unlock()
	w.Header().Set("X-Upstream", "vite")
	_, _ = io.WriteString(w, "vite:"+r.RequestURI)
}

func (u *recorder) last(t *testing.T) *http.Request {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.seen) == 0 {
		t.Fatal("the request never reached the upstream")
	}
	return u.seen[len(u.seen)-1]
}

func (u *recorder) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.seen)
}

func teapot() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
}

func rawRequest(t *testing.T, line, header string) *http.Request {
	t.Helper()
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(line + " HTTP/1.1\r\nHost: localhost:8787\r\n" + header + "\r\n")))
	if err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	return r
}

func TestDevUIProxiesPathAndQueryVerbatim(t *testing.T) {
	up := &recorder{}
	srv := httptest.NewServer(up)
	defer srv.Close()
	h := devUI(teapot(), srv.URL)
	for target, want := range map[string]string{
		"/":                   "/",
		"/app/acme/blog?r=7d": "/app/acme/blog?r=7d",
		"/@vite/client":       "/@vite/client",
		"/src/main.tsx?t=123": "/src/main.tsx?t=123",
		"/a%2fb?x=%2F":        "/a%2fb?x=%2F",
		"/node_modules/.vite/deps/react.js?v=abc": "/node_modules/.vite/deps/react.js?v=abc",
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, rawRequest(t, "GET "+target, ""))
		if rr.Code != 200 || rr.Header().Get("X-Upstream") != "vite" || rr.Body.String() != "vite:"+want {
			t.Errorf("%s: %d %q", target, rr.Code, rr.Body.String())
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, rawRequest(t, "HEAD /app", ""))
	if rr.Header().Get("X-Upstream") != "vite" {
		t.Errorf("HEAD was not proxied")
	}
}

func TestDevUILeavesAPIAndOtherMethodsAlone(t *testing.T) {
	up := &recorder{}
	srv := httptest.NewServer(up)
	defer srv.Close()
	h := devUI(teapot(), srv.URL)
	for _, tc := range [][2]string{{"GET", "/api"}, {"GET", "/api/private/me"}, {"HEAD", "/api/x"}, {"POST", "/app"}, {"PUT", "/"}, {"GET", "/invite?token=abc"}} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(tc[0], tc[1], nil))
		if rr.Code != http.StatusTeapot {
			t.Errorf("%s %s: status %d, want pass-through", tc[0], tc[1], rr.Code)
		}
	}
	if up.count() != 0 {
		t.Errorf("%d requests reached the upstream, want 0", up.count())
	}
}

func TestDevUIDropsCredentialsBeforeForwarding(t *testing.T) {
	up := &recorder{}
	srv := httptest.NewServer(up)
	defer srv.Close()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Cookie", "supermetry_session=secret")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	req.Header.Set("Accept", "text/html")
	devUI(teapot(), srv.URL).ServeHTTP(rr, req)
	got := up.last(t)
	for _, name := range []string{"Cookie", "Authorization", "X-Forwarded-For"} {
		if got.Header.Get(name) != "" {
			t.Errorf("%s reached the upstream: %q", name, got.Header.Get(name))
		}
	}
	if got.Header.Get("Accept") != "text/html" {
		t.Errorf("ordinary headers must be forwarded")
	}
	if got.Host != "example.com" {
		t.Errorf("upstream saw Host %q, want the client's Host", got.Host)
	}
}

// Vite's allowedHosts check (DNS-rebinding protection) only works if it sees the
// Host the client sent, so the proxy must forward it unchanged.
func TestDevUIForwardsTheClientHostVerbatim(t *testing.T) {
	up := &recorder{}
	srv := httptest.NewServer(up)
	defer srv.Close()
	h := devUI(teapot(), srv.URL)
	for _, host := range []string{"localhost:8787", "evil.example", "192.168.1.20:8787", "evil.example:80"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = host
		h.ServeHTTP(rr, req)
		if got := up.last(t).Host; got != host {
			t.Errorf("client Host %q reached the upstream as %q", host, got)
		}
	}
}

func TestDevUIDropsUpstreamCSP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src *")
		w.Header().Add("Content-Security-Policy-Report-Only", "default-src *")
	}))
	defer srv.Close()
	rr := httptest.NewRecorder()
	devUI(teapot(), srv.URL).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if got := rr.Header().Values("Content-Security-Policy"); len(got) != 1 || got[0] != devCSP {
		t.Errorf("CSP headers %q, want only the dev policy", got)
	}
	if rr.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Errorf("upstream report-only policy leaked through")
	}
}

func TestDevCSPKeepsTheProductionBasics(t *testing.T) {
	for _, want := range []string{"form-action 'self'", "object-src 'none'", "base-uri 'self'", "font-src 'self' data: blob: https:"} {
		if !strings.Contains(devCSP, want) {
			t.Errorf("devCSP lacks %q", want)
		}
	}
}

func TestDevUIPassThroughResponsesGetNoDevCSP(t *testing.T) {
	srv := httptest.NewServer(&recorder{})
	defer srv.Close()
	rr := httptest.NewRecorder()
	devUI(teapot(), srv.URL).ServeHTTP(rr, httptest.NewRequest("GET", "/api/private/me", nil))
	if rr.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("an /api response carries the dev CSP: %q", rr.Header().Get("Content-Security-Policy"))
	}
}

func TestDevUIHostileTargetsNeverChangeTheUpstream(t *testing.T) {
	up := &recorder{}
	srv := httptest.NewServer(up)
	defer srv.Close()
	h := devUI(teapot(), srv.URL)
	long := "/" + strings.Repeat("a", 8192)
	// target -> escaped path and raw query the upstream must receive.
	targets := []struct{ target, path, query string }{
		{"//evil.example", "//evil.example", ""},
		{"//evil.example/x", "//evil.example/x", ""},
		{"/%5Cevil.example", "/%5Cevil.example", ""},
		{"/%2f%2fevil.example", "/%2f%2fevil.example", ""},
		{"///evil.example", "///evil.example", ""},
		{"/..//evil.example", "/..//evil.example", ""},
		{"/%0d%0aX-Injected:1", "/%0d%0aX-Injected:1", ""},
		{"/a?b=%0d%0aX-Injected:1", "/a", "b=%0d%0aX-Injected:1"},
		{"/@evil.example", "/@evil.example", ""},
		{"/a?next=//evil.example", "/a", "next=//evil.example"},
		{"http://evil.example/x", "/x", ""},
		{"http://evil.example//evil2.example", "//evil2.example", ""},
		{long, long, ""},
		{"/%2e%2e/%2e%2e/evil.example", "/%2e%2e/%2e%2e/evil.example", ""},
		{"http://user@evil.example/x?q=1", "/x", "q=1"},
	}
	for _, tc := range targets {
		before := up.count()
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, rawRequest(t, "GET "+tc.target, ""))
		if rr.Code != 200 || up.count() != before+1 {
			t.Errorf("%.40q: status %d, upstream hits %d", tc.target, rr.Code, up.count()-before)
			continue
		}
		got := up.last(t)
		// The client's Host is forwarded for Vite's own host check; the connection
		// itself always goes to the configured upstream (the recorder is the only
		// server listening, and it received the request).
		if got.Header.Get("X-Injected") != "" {
			t.Errorf("%.40q: header injection reached the upstream", tc.target)
		}
		if got.URL.EscapedPath() != tc.path || got.URL.RawQuery != tc.query {
			t.Errorf("%.40q: upstream received path %.60q query %q, want %.60q %q", tc.target, got.URL.EscapedPath(), got.URL.RawQuery, tc.path, tc.query)
		}
	}
}

func TestDevUIIgnoresInvalidValues(t *testing.T) {
	for _, raw := range []string{"", "localhost:5173", "ftp://x", "http://", "javascript:alert(1)", "//evil.example", "http://a b", "http://u@evil.example", "http://u:p@evil.example"} {
		rr := httptest.NewRecorder()
		devUI(teapot(), raw).ServeHTTP(rr, httptest.NewRequest("GET", "/app", nil))
		if rr.Code != http.StatusTeapot {
			t.Errorf("%q: status %d, want the stub handler", raw, rr.Code)
		}
	}
}

func TestDevUIAnswers502WhenViteIsDown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	l.Close()
	rr := httptest.NewRecorder()
	devUI(teapot(), url).ServeHTTP(rr, httptest.NewRequest("GET", "/app", nil))
	body := rr.Body.String()
	if rr.Code != http.StatusBadGateway || !strings.Contains(body, "The Vite dev server is not reachable at "+url) || !strings.Contains(body, "make dev") {
		t.Errorf("%d %q", rr.Code, body)
	}
	if strings.Contains(body, "UI not built") {
		t.Errorf("502 page must not use the old stub text")
	}
}

func TestDevUIRelaxesTheCSPForViteOnly(t *testing.T) {
	srv := httptest.NewServer(&recorder{})
	defer srv.Close()
	rr := httptest.NewRecorder()
	rr.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'sha256-x'")
	devUI(teapot(), srv.URL).ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || strings.Contains(csp, "sha256") || !strings.Contains(csp, "ws:") {
		t.Errorf("CSP %q", csp)
	}
}

// echoUpgrade is a fake Vite HMR socket: it answers 101 and echoes bytes.
func echoUpgrade(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "no upgrade", 400)
		return
	}
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	_ = rw.Flush()
	buf := make([]byte, 64)
	for {
		n, err := rw.Read(buf)
		if n > 0 {
			_, _ = conn.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func TestDevUIProxiesWebSocketUpgradesAndKeepsThemOpen(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(echoUpgrade))
	defer upstream.Close()
	// The front server has a short write timeout, like the real one could have.
	front := httptest.NewUnstartedServer(devUI(teapot(), upstream.URL))
	front.Config.WriteTimeout = 200 * time.Millisecond
	front.Config.ReadTimeout = 200 * time.Millisecond
	front.Start()
	defer front.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "GET /?token=abc HTTP/1.1\r\nHost: localhost:8787\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Protocol: vite-hmr\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	time.Sleep(500 * time.Millisecond) // longer than the server timeouts
	_, _ = io.WriteString(conn, "ping")
	got := make([]byte, 4)
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo through the proxy: %q %v", got, err)
	}
}

func TestHandlerWiring(t *testing.T) {
	up := &recorder{}
	srv := httptest.NewServer(up)
	defer srv.Close()
	t.Setenv(DevUIEnv, srv.URL)
	h := Handler()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/app/x?y=1", nil))
	if rr.Code != 200 || rr.Body.String() != "vite:/app/x?y=1" {
		t.Errorf("/app/x: %d %q", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/x", nil))
	if rr.Code != http.StatusNotFound || rr.Header().Get("X-Upstream") != "" {
		t.Errorf("/api/x: %d, want the normal 404", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/app", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("POST /app: %d", rr.Code)
	}
}

// The legacy /invite?token= link is rewritten by the UI handler to /invite/<token>
// (the SPA only understands the path form), before anything is proxied.
func TestLegacyInviteLinkStillRewritesInDev(t *testing.T) {
	up := &recorder{}
	srv := httptest.NewServer(up)
	defer srv.Close()
	t.Setenv(DevUIEnv, srv.URL)
	h := Handler()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/invite?token=abc", nil))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/invite/abc" {
		t.Fatalf("first hop: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/invite/abc", nil))
	if rr.Body.String() != "vite:/invite/abc" {
		t.Errorf("second hop: %q", rr.Body.String())
	}
}

func TestStubPageIsUnchangedWithoutTheEnvVar(t *testing.T) {
	t.Setenv(DevUIEnv, "")
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "UI not built — run <code>make web</code>.") {
		t.Errorf("%d %q", rr.Code, rr.Body.String())
	}
}
