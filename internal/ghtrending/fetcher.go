package ghtrending

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// Host is the only host the fetcher talks to.
	Host = "https://github.com"

	fetchTimeout = 10 * time.Second
	// MaxBody caps how much of a response is read.
	MaxBody = 2 << 20
	// MinGap is the least time between two requests.
	MinGap = time.Second
	// UserAgent identifies this software to GitHub.
	UserAgent = "Supermetry-trending-watcher/1.0 (+https://github.com/velixio/supermetry; polite, 10-minute interval)"
)

// Since is a trending time window.
type Since string

// The windows watched.
const (
	Daily  Since = "daily"
	Weekly Since = "weekly"
)

// Windows lists the windows fetched for every list.
var Windows = []Since{Daily, Weekly}

var (
	// ErrNoList reports a language GitHub has no trending list for (HTTP 404).
	ErrNoList = errors.New("ghtrending: no trending list for this language")
	// ErrBodyTooLarge reports a response beyond MaxBody.
	ErrBodyTooLarge = errors.New("ghtrending: response too large")
)

// ListFetcher fetches and parses one trending list. language is the lowercase
// language name, empty for the all-languages list.
type ListFetcher interface {
	Fetch(ctx context.Context, language string, since Since) ([]Entry, error)
}

// Fetcher is the HTTPS client for github.com/trending: fixed host, no
// redirects, no cookies, a size cap, a timeout and at most one request per
// second.
type Fetcher struct {
	// Client supplies the transport (tests inject a TLS test server's). Its
	// redirect policy, jar and timeout are always overridden.
	Client *http.Client

	base  string // overridden by tests only; empty means Host
	sleep func(context.Context, time.Duration) error
	now   func() time.Time

	mu   sync.Mutex
	last time.Time
}

// NewFetcher returns a fetcher for github.com.
func NewFetcher() *Fetcher { return &Fetcher{} }

// Slug returns the URL path segment of a language list: lowercase, spaces as
// hyphens, everything outside [a-z0-9._-] percent-encoded (c++ -> c%2B%2B).
func Slug(language string) string {
	language = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(language)), " ", "-")
	var b strings.Builder
	for _, c := range []byte(language) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ListURL returns the page URL of a list.
func ListURL(base, language string, since Since) string {
	if base == "" {
		base = Host
	}
	path := "/trending"
	if s := Slug(language); s != "" {
		path += "/" + s
	}
	return base + path + "?since=" + url.QueryEscape(string(since))
}

func (f *Fetcher) wait(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now
	if f.now != nil {
		now = f.now
	}
	if !f.last.IsZero() {
		if gap := MinGap - now().Sub(f.last); gap > 0 {
			sleep := sleepContext
			if f.sleep != nil {
				sleep = f.sleep
			}
			if err := sleep(ctx, gap); err != nil {
				return err
			}
		}
	}
	f.last = now()
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Fetch implements ListFetcher.
func (f *Fetcher) Fetch(ctx context.Context, language string, since Since) ([]Entry, error) {
	target := ListURL(f.base, language, since)
	if !strings.HasPrefix(target, "https://") {
		return nil, errors.New("ghtrending: only https is allowed")
	}
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	client := http.Client{}
	if f.Client != nil {
		client = *f.Client
	}
	client.Timeout = fetchTimeout
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch trending: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNoList
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("fetch trending: unexpected redirect (status %d) refused", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("fetch trending: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("fetch trending: %w", err)
	}
	if len(body) > MaxBody {
		return nil, ErrBodyTooLarge
	}
	return ParseList(strings.NewReader(string(body)))
}
