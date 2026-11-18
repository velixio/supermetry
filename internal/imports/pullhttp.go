package imports

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"supermetry/internal/safehttp"
)

// Pull imports call servers the user names (a self-hosted Plausible or Matomo
// base URL), so every request goes through the SSRF-guarded dialer.

const (
	pullHTTPTimeout   = 60 * time.Second
	maxPullResponse   = 32 << 20
	maxPullAttempts   = 5
	maxRetryAfter     = 2 * time.Minute
	initialRetryDelay = time.Second
)

// PullHTTPOptions configure the outbound client. AllowPrivate must stay false
// in production; tests set it so httptest servers on loopback are reachable.
type PullHTTPOptions struct {
	AllowPrivate bool
	// Sleep waits between retries; tests replace it to avoid real delays.
	Sleep func(context.Context, time.Duration) error
}

// ErrUnsafeBaseURL is returned for a base URL that is malformed or not http(s).
var ErrUnsafeBaseURL = errors.New("base URL must be an http or https URL without credentials")

// errRemoteAuth reports a rejected key or token (HTTP 401/403).
var errRemoteAuth = errors.New("the remote service rejected the credentials")

type pullClient struct {
	client *http.Client
	sleep  func(context.Context, time.Duration) error
}

func newPullClient(opts PullHTTPOptions) *pullClient {
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &pullClient{
		sleep: sleep,
		client: &http.Client{
			Timeout: pullHTTPTimeout,
			Transport: &http.Transport{
				Proxy:                 nil,
				DialContext:           safehttp.GuardedDial(net.DefaultResolver, dialer.DialContext, opts.AllowPrivate),
				TLSHandshakeTimeout:   20 * time.Second,
				ResponseHeaderTimeout: pullHTTPTimeout,
			},
			// A redirect could point at an internal address after the guard
			// has approved the first hop, so redirects are never followed.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

// ParseBaseURL validates a user-supplied service origin and returns it
// without a trailing slash. Host safety is enforced at dial time.
func ParseBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(raw) > 512 {
		return "", ErrUnsafeBaseURL
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

// retryDelay honours Retry-After (seconds) and otherwise backs off
// exponentially from one second.
func retryDelay(response *http.Response, attempt int) time.Duration {
	if value := response.Header.Get("Retry-After"); value != "" {
		if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
			return min(time.Duration(seconds)*time.Second, maxRetryAfter)
		}
	}
	return min(initialRetryDelay<<attempt, maxRetryAfter)
}

// do sends the request built by build for every attempt, backing off on 429
// and transient 5xx answers, and returns the status and a bounded body. The
// secret is redacted from any error text so credentials never reach logs.
func (c *pullClient) do(ctx context.Context, secret string, build func() (*http.Request, error)) (int, []byte, error) {
	var lastStatus int
	for attempt := 0; attempt < maxPullAttempts; attempt++ {
		request, err := build()
		if err != nil {
			return 0, nil, err
		}
		response, err := c.client.Do(request.WithContext(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return 0, nil, context.Cause(ctx)
			}
			return 0, nil, fmt.Errorf("request failed: %s", redact(err.Error(), secret))
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxPullResponse+1))
		response.Body.Close()
		if readErr != nil {
			return 0, nil, fmt.Errorf("read response: %s", redact(readErr.Error(), secret))
		}
		if len(body) > maxPullResponse {
			return 0, nil, errors.New("response is larger than the import limit")
		}
		lastStatus = response.StatusCode
		retryable := lastStatus == http.StatusTooManyRequests || lastStatus == http.StatusBadGateway ||
			lastStatus == http.StatusServiceUnavailable || lastStatus == http.StatusGatewayTimeout
		if !retryable {
			return lastStatus, body, nil
		}
		if attempt == maxPullAttempts-1 {
			break
		}
		if err := c.sleep(ctx, retryDelay(response, attempt)); err != nil {
			return 0, nil, err
		}
	}
	if lastStatus == http.StatusTooManyRequests {
		return lastStatus, nil, errors.New("the remote service kept rate limiting requests; try again later")
	}
	return lastStatus, nil, fmt.Errorf("the remote service is unavailable (status %d)", lastStatus)
}

func redact(text, secret string) string {
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "***")
	}
	return text
}

// snippet returns a short single-line excerpt of a remote error body.
func snippet(body []byte, secret string) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return redact(text, secret)
}
