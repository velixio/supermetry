package ghtrending

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/trending_all_daily.html")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testFetcher(srv *httptest.Server) *Fetcher {
	return &Fetcher{Client: srv.Client(), base: srv.URL}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"Go":               "go",
		"C++":              "c%2B%2B",
		"C#":               "c%23",
		"Jupyter Notebook": "jupyter-notebook",
		"Objective-C":      "objective-c",
		"a/b":              "a%2Fb",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ListURL("", "C++", Weekly); got != "https://github.com/trending/c%2B%2B?since=weekly" {
		t.Fatalf("url = %s", got)
	}
	if got := ListURL("", "", Daily); got != "https://github.com/trending?since=daily" {
		t.Fatalf("url = %s", got)
	}
}

func TestFetchSuccessSendsPoliteRequest(t *testing.T) {
	var gotURI, gotUA, gotCookie string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI, gotUA, gotCookie = r.RequestURI, r.UserAgent(), r.Header.Get("Cookie")
		w.Write(fixtureBytes(t))
	}))
	defer srv.Close()
	f := testFetcher(srv)
	f.Client.Jar = nil
	entries, err := f.Fetch(context.Background(), "C++", Daily)
	if err != nil || len(entries) != 14 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	if gotURI != "/trending/c%2B%2B?since=daily" {
		t.Fatalf("request uri = %q", gotURI)
	}
	if !strings.Contains(gotUA, "Supermetry") || gotCookie != "" {
		t.Fatalf("ua=%q cookie=%q", gotUA, gotCookie)
	}
}

func TestFetchRefusesRedirects(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/elsewhere" {
			w.Write(fixtureBytes(t))
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	_, err := testFetcher(srv).Fetch(context.Background(), "", Daily)
	if err == nil || !strings.Contains(err.Error(), "redirect") || hits != 1 {
		t.Fatalf("err=%v hits=%d", err, hits)
	}
}

func TestFetchRejectsPlainHTTP(t *testing.T) {
	f := &Fetcher{base: "http://example.com"}
	if _, err := f.Fetch(context.Background(), "", Daily); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchOversized(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", MaxBody+10)))
	}))
	defer srv.Close()
	if _, err := testFetcher(srv).Fetch(context.Background(), "", Daily); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchStatuses(t *testing.T) {
	for status, check := range map[int]func(error) bool{
		404: func(err error) bool { return errors.Is(err, ErrNoList) },
		429: func(err error) bool { return err != nil && strings.Contains(err.Error(), "429") },
		503: func(err error) bool { return err != nil && strings.Contains(err.Error(), "503") },
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		_, err := testFetcher(srv).Fetch(context.Background(), "x", Daily)
		srv.Close()
		if !check(err) {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
}

func TestFetchUnreadablePage(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>Please verify you are human</body></html>"))
	}))
	defer srv.Close()
	if _, err := testFetcher(srv).Fetch(context.Background(), "", Daily); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := testFetcher(srv).Fetch(ctx, "", Daily); err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("did not give up")
	}
	if fetchTimeout != 10*time.Second {
		t.Fatalf("fetchTimeout = %v", fetchTimeout)
	}
}

// Requests are at least a second apart; the fake clock proves it without waiting.
func TestFetchRateLimit(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(fixtureBytes(t)) }))
	defer srv.Close()
	var mu sync.Mutex
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var slept []time.Duration
	f := testFetcher(srv)
	f.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	f.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		slept = append(slept, d)
		clock = clock.Add(d)
		return nil
	}
	for i := 0; i < 3; i++ {
		if _, err := f.Fetch(context.Background(), "", Daily); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(slept) != fmt.Sprint([]time.Duration{time.Second, time.Second}) {
		t.Fatalf("slept = %v", slept)
	}
	// A longer natural gap needs no sleep.
	mu.Lock()
	clock = clock.Add(5 * time.Second)
	mu.Unlock()
	if _, err := f.Fetch(context.Background(), "", Daily); err != nil || len(slept) != 2 {
		t.Fatalf("err=%v slept=%v", err, slept)
	}
}
