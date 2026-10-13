package updatecheck

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChecker_Check(t *testing.T) {
	now := time.Date(2026, 10, 14, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name             string
		current          string
		response         string
		status           int
		transportErr     error
		wantLatest       string
		wantAvailable    bool
		wantURL          string
		wantPublished    time.Time
		wantError        bool
		wantRequests     int32
		wantCheckedAtNil bool
	}{
		{
			name:     "newer stable release is available",
			current:  "1.2.3",
			response: `{"tag_name":"v1.3.0","html_url":"https://github.com/velixio/supermetry/releases/tag/v1.3.0","published_at":"2026-10-13T11:12:13Z","draft":false,"prerelease":false}`,
			status:   http.StatusOK, wantLatest: "v1.3.0", wantAvailable: true,
			wantURL:       "https://github.com/velixio/supermetry/releases/tag/v1.3.0",
			wantPublished: time.Date(2026, 10, 13, 11, 12, 13, 0, time.UTC), wantRequests: 1,
		},
		{
			name: "same version is current", current: "v1.3.0",
			response: `{"tag_name":"v1.3.0","html_url":"https://github.com/velixio/supermetry/releases/tag/v1.3.0","published_at":"2026-10-13T11:12:13Z"}`,
			status:   http.StatusOK, wantLatest: "v1.3.0", wantURL: "https://github.com/velixio/supermetry/releases/tag/v1.3.0",
			wantPublished: time.Date(2026, 10, 13, 11, 12, 13, 0, time.UTC), wantRequests: 1,
		},
		{
			name: "older latest release does not offer downgrade", current: "v2.0.0",
			response: `{"tag_name":"v1.9.0","html_url":"https://github.com/velixio/supermetry/releases/tag/v1.9.0","published_at":"2026-10-13T11:12:13Z"}`,
			status:   http.StatusOK, wantLatest: "v1.9.0", wantURL: "https://github.com/velixio/supermetry/releases/tag/v1.9.0",
			wantPublished: time.Date(2026, 10, 13, 11, 12, 13, 0, time.UTC), wantRequests: 1,
		},
		{name: "development version never reaches network", current: "dev", wantRequests: 0, wantCheckedAtNil: true},
		{name: "invalid version never reaches network", current: "not-a-version", wantRequests: 0, wantCheckedAtNil: true},
		{name: "github failure is reported", current: "1.2.3", status: http.StatusServiceUnavailable, wantError: true, wantRequests: 1},
		{name: "transport failure is reported", current: "1.2.3", transportErr: errors.New("offline"), wantError: true, wantRequests: 1},
		{name: "invalid release version is reported", current: "1.2.3", response: `{"tag_name":"nightly"}`, status: http.StatusOK, wantError: true, wantRequests: 1},
		{name: "prerelease is rejected", current: "1.2.3", response: `{"tag_name":"v1.3.0-rc.1","prerelease":true}`, status: http.StatusOK, wantError: true, wantRequests: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				if request.URL.String() != latestReleaseURL {
					t.Fatalf("request URL=%q, want %q", request.URL, latestReleaseURL)
				}
				if got := request.Header.Get("Accept"); got != githubJSONMediaType {
					t.Fatalf("Accept=%q", got)
				}
				if tt.transportErr != nil {
					return nil, tt.transportErr
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.response)), Header: make(http.Header)}, nil
			})}
			checker := newChecker(tt.current, client, func() time.Time { return now })
			got := checker.Check(context.Background())

			if got.CurrentVersion != tt.current || value(got.LatestVersion) != tt.wantLatest || got.UpdateAvailable != tt.wantAvailable || value(got.ReleaseURL) != tt.wantURL {
				t.Fatalf("status=%+v", got)
			}
			if tt.wantPublished.IsZero() != (got.PublishedAt == nil) || got.PublishedAt != nil && !got.PublishedAt.Equal(tt.wantPublished) {
				t.Fatalf("published_at=%v, want %v", got.PublishedAt, tt.wantPublished)
			}
			if (got.CheckError != nil) != tt.wantError {
				t.Fatalf("check_error=%v, want error=%v", got.CheckError, tt.wantError)
			}
			if (got.CheckedAt == nil) != tt.wantCheckedAtNil || got.CheckedAt != nil && !got.CheckedAt.Equal(now) {
				t.Fatalf("checked_at=%v", got.CheckedAt)
			}
			if requests.Load() != tt.wantRequests {
				t.Fatalf("requests=%d, want %d", requests.Load(), tt.wantRequests)
			}
		})
	}
}

func TestChecker_CachesSuccessAndFailureForSixHours(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "success", status: http.StatusOK, body: `{"tag_name":"v2.0.0"}`},
		{name: "failure", status: http.StatusServiceUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
			var requests atomic.Int32
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: make(http.Header)}, nil
			})}
			checker := newChecker("v1.0.0", client, func() time.Time { return now })

			first := checker.Check(context.Background())
			now = now.Add(cacheTTL - time.Nanosecond)
			second := checker.Check(context.Background())
			if requests.Load() != 1 || first.CheckedAt == nil || second.CheckedAt == nil || !first.CheckedAt.Equal(*second.CheckedAt) {
				t.Fatalf("within TTL requests=%d first=%+v second=%+v", requests.Load(), first, second)
			}
			now = now.Add(time.Nanosecond)
			third := checker.Check(context.Background())
			if requests.Load() != 2 || third.CheckedAt == nil || !third.CheckedAt.Equal(now) {
				t.Fatalf("after TTL requests=%d third=%+v", requests.Load(), third)
			}
		})
	}
}

func TestChecker_DeduplicatesConcurrentChecks(t *testing.T) {
	var requests atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			close(started)
		}
		<-release
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v2.0.0"}`)), Header: make(http.Header)}, nil
	})}
	checker := newChecker("v1.0.0", client, time.Now)

	const callers = 20
	results := make(chan Status, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- checker.Check(context.Background())
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	for result := range results {
		if !result.UpdateAvailable {
			t.Fatalf("result=%+v", result)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d, want 1", requests.Load())
	}
}

func TestChecker_BoundsResponseBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1))), Header: make(http.Header)}, nil
	})}
	got := newChecker("v1.0.0", client, time.Now).Check(context.Background())
	if got.CheckError == nil || !strings.Contains(*got.CheckError, "too large") {
		t.Fatalf("check_error=%v", got.CheckError)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func value(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
