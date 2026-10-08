package icon

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"panel/internal/safehttp"
)

func TestDiscoverResolvesRelativeAndAbsoluteIcons(t *testing.T) {
	tests := []struct {
		name string
		href string
		want string
	}{
		{name: "relative", href: "../brand.svg", want: "http://example.test/brand.svg"},
		{name: "absolute", href: "http://assets.test/icon.png", want: "http://assets.test/icon.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/start/page":
					_, _ = io.WriteString(w, `<html><head><link rel="icon" href="`+tt.href+`"></head></html>`)
				case "/brand.svg", "/icon.png":
					w.Header().Set("Content-Type", "image/svg+xml")
					_, _ = io.WriteString(w, "icon")
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			discoverer := testDiscoverer(t, server.URL, map[string][]netip.Addr{
				"example.test": {netip.MustParseAddr("93.184.216.34")},
				"assets.test":  {netip.MustParseAddr("93.184.216.35")},
			})
			if got := discoverer.discoverPage(context.Background(), "http://example.test/start/page"); got != tt.want {
				t.Fatalf("discoverPage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiscoverPrefersIconRelations(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		if r.URL.Path == "/" {
			_, _ = io.WriteString(w, `<link rel="apple-touch-icon" href="/apple.png"><link rel="shortcut icon" href="/shortcut.ico"><link rel="icon" href="/preferred.svg">`)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = io.WriteString(w, "icon")
	}))
	defer server.Close()

	discoverer := testDiscoverer(t, server.URL, map[string][]netip.Addr{"example.test": {netip.MustParseAddr("93.184.216.34")}})
	if got := discoverer.discoverPage(context.Background(), "http://example.test/"); got != "http://example.test/preferred.svg" {
		t.Fatalf("discoverPage() = %q", got)
	}
	if strings.Join(requested, ",") != "/,/preferred.svg" {
		t.Fatalf("requested = %v, want preferred icon only", requested)
	}
}

func TestDiscoverTriesAtMostFiveCandidatesIncludingFallback(t *testing.T) {
	var requested atomic.Int32
	var page strings.Builder
	addresses := map[string][]netip.Addr{"example.test": {netip.MustParseAddr("93.184.216.34")}}
	for index := 0; index < 50; index++ {
		host := "icon-" + strconv.Itoa(index) + ".test"
		addresses[host] = []netip.Addr{netip.MustParseAddr("93.184.216.35")}
		page.WriteString(`<link rel="icon" href="http://` + host + `/icon.png">`)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = io.WriteString(w, page.String())
			return
		}
		requested.Add(1)
		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "image/x-icon")
			_, _ = io.WriteString(w, "icon")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "not an image")
	}))
	defer server.Close()
	discoverer := testDiscoverer(t, server.URL, addresses)

	if got := discoverer.discoverPage(context.Background(), "http://example.test/"); got != "http://example.test/favicon.ico" {
		t.Fatalf("discoverPage() = %q, want fallback", got)
	}
	if got := requested.Load(); got != 5 {
		t.Fatalf("icon candidate requests = %d, want 5", got)
	}
}

func TestDiscoverRejectsCandidatePortsOutsideAllowlist(t *testing.T) {
	var page strings.Builder
	addresses := map[string][]netip.Addr{"example.test": {netip.MustParseAddr("93.184.216.34")}}
	for index := 0; index < 50; index++ {
		host := "odd-" + strconv.Itoa(index) + ".test"
		addresses[host] = []netip.Addr{netip.MustParseAddr("93.184.216.35")}
		page.WriteString(`<link rel="icon" href="http://` + host + `:` + strconv.Itoa(3001+index*2) + `/icon.png">`)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, page.String())
		case "/favicon.ico":
			w.Header().Set("Content-Type", "image/x-icon")
			_, _ = io.WriteString(w, "icon")
		default:
			http.Error(w, "unexpected candidate request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var ports []string
	dialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil {
			return nil, splitErr
		}
		mu.Lock()
		ports = append(ports, port)
		mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
	}
	discoverer := New(Options{Resolver: mapResolver{addresses: addresses}, DialContext: dialer})

	if got := discoverer.discoverPage(context.Background(), "http://example.test:8080/"); got != "http://example.test:8080/favicon.ico" {
		t.Fatalf("discoverPage() = %q, want same-port fallback", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ports) == 0 || len(ports) > 2 {
		t.Fatalf("dialed ports = %v, want only page/fallback connections", ports)
	}
	for _, port := range ports {
		if port != "8080" {
			t.Fatalf("dialed disallowed port %q in %v", port, ports)
		}
	}
}

func TestDiscoverRejectsRedirectToDisallowedPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			_, _ = io.WriteString(w, `<link rel="icon" href="/icon.png">`)
		case r.URL.Path == "/icon.png" && strings.HasPrefix(r.Host, "redirect.test:"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "icon")
		case r.URL.Path == "/icon.png":
			http.Redirect(w, r, "http://redirect.test:4444/icon.png", http.StatusFound)
		case r.URL.Path == "/favicon.ico":
			w.Header().Set("Content-Type", "image/x-icon")
			_, _ = io.WriteString(w, "icon")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var dialedDisallowed atomic.Bool
	dialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		if _, port, splitErr := net.SplitHostPort(address); splitErr == nil && port == "4444" {
			dialedDisallowed.Store(true)
		}
		return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
	}
	discoverer := New(Options{
		Resolver: mapResolver{addresses: map[string][]netip.Addr{
			"example.test":  {netip.MustParseAddr("93.184.216.34")},
			"redirect.test": {netip.MustParseAddr("93.184.216.35")},
		}},
		DialContext: dialer,
	})

	if got := discoverer.discoverPage(context.Background(), "http://example.test/"); got != "http://example.test/favicon.ico" {
		t.Fatalf("discoverPage() = %q, want fallback", got)
	}
	if dialedDisallowed.Load() {
		t.Fatal("redirect dialed disallowed port 4444")
	}
}

func TestDiscoverRejectsUnsafeRedirect(t *testing.T) {
	dialedPrivate := false
	resolver := mapResolver{addresses: map[string][]netip.Addr{
		"example.test": {netip.MustParseAddr("93.184.216.34")},
		"private.test": {netip.MustParseAddr("127.0.0.1")},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://private.test/icon.png", http.StatusFound)
	}))
	defer server.Close()
	serverURL, _ := url.Parse(server.URL)
	dialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.HasPrefix(address, "127.0.0.1:") && address != serverURL.Host {
			dialedPrivate = true
		}
		return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
	}
	discoverer := New(Options{Resolver: resolver, DialContext: dialer})

	if got := discoverer.discoverPage(context.Background(), "http://example.test/"); got != "" || dialedPrivate {
		t.Fatalf("discoverPage() = %q dialedPrivate=%t, want safe rejection", got, dialedPrivate)
	}
}

func TestDiscoverHandlesPageAndIconFailures(t *testing.T) {
	tests := []struct {
		name string
		page func(http.ResponseWriter)
		want string
	}{
		{
			name: "oversized page falls back",
			page: func(w http.ResponseWriter) {
				_, _ = io.WriteString(w, `<link rel="icon" href="/wrong.png">`+strings.Repeat("x", maxPageBytes))
			},
			want: "http://example.test/favicon.ico",
		},
		{
			name: "non image candidate falls back",
			page: func(w http.ResponseWriter) { _, _ = io.WriteString(w, `<link rel="icon" href="/text">`) },
			want: "http://example.test/favicon.ico",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					tt.page(w)
				case "/favicon.ico":
					w.Header().Set("Content-Type", "image/x-icon")
					_, _ = io.WriteString(w, "icon")
				default:
					w.Header().Set("Content-Type", "text/plain")
					_, _ = io.WriteString(w, "not an image")
				}
			}))
			defer server.Close()
			discoverer := testDiscoverer(t, server.URL, map[string][]netip.Addr{"example.test": {netip.MustParseAddr("93.184.216.34")}})
			if got := discoverer.discoverPage(context.Background(), "http://example.test/"); got != tt.want {
				t.Fatalf("discoverPage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiscoverRejectsOversizedIcon(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, `<link rel="icon" href="/oversized.png">`)
		case "/oversized.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, strings.Repeat("x", maxIconBytes+1))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	discoverer := testDiscoverer(t, server.URL, map[string][]netip.Addr{"example.test": {netip.MustParseAddr("93.184.216.34")}})

	if got := discoverer.discoverPage(context.Background(), "http://example.test/"); got != "" {
		t.Fatalf("discoverPage() = %q, want oversized icon rejected", got)
	}
}

func TestDiscoverFallsBackWhenPageIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/favicon.ico" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = io.WriteString(w, "icon")
	}))
	defer server.Close()
	discoverer := testDiscoverer(t, server.URL, map[string][]netip.Addr{"example.test": {netip.MustParseAddr("93.184.216.34")}})

	if got := discoverer.discoverPage(context.Background(), "http://example.test/"); got != "http://example.test/favicon.ico" {
		t.Fatalf("discoverPage() = %q, want favicon fallback", got)
	}
}

func TestDiscoverRefusesPrivateHost(t *testing.T) {
	dialed := false
	discoverer := New(Options{
		Resolver: mapResolver{addresses: map[string][]netip.Addr{"private.test": {netip.MustParseAddr("127.0.0.1")}}},
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, errors.New("unexpected dial")
		},
	})
	if got := discoverer.Discover(context.Background(), "private.test"); got != "" || dialed {
		t.Fatalf("Discover() = %q dialed=%t, want rejection", got, dialed)
	}
}

func testDiscoverer(t *testing.T, serverRawURL string, addresses map[string][]netip.Addr) *Discoverer {
	t.Helper()
	serverURL, err := url.Parse(serverRawURL)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	dialer := func(ctx context.Context, network, _ string) (net.Conn, error) {
		mu.Lock()
		defer mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
	}
	return New(Options{Resolver: mapResolver{addresses: addresses}, DialContext: dialer})
}

type mapResolver struct {
	addresses map[string][]netip.Addr
}

func (r mapResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	addresses := r.addresses[host]
	if len(addresses) == 0 {
		return nil, errors.New("unknown host")
	}
	return append([]netip.Addr(nil), addresses...), nil
}

var _ safehttp.Resolver = mapResolver{}
