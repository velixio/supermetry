// Package updatecheck performs bounded, read-only checks for official stable
// Supermetry releases. It never downloads or applies release artifacts.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/sync/singleflight"
)

const (
	latestReleaseURL    = "https://api.github.com/repos/velixio/supermetry/releases/latest"
	githubJSONMediaType = "application/vnd.github+json"
	cacheTTL            = 6 * time.Hour
	requestTimeout      = 10 * time.Second
	refreshCooldown     = 15 * time.Second
	maxResponseBytes    = 64 << 10
)

// Status is the update availability contract returned to an instance admin.
type Status struct {
	CurrentVersion  string     `json:"current_version"`
	LatestVersion   *string    `json:"latest_version"`
	UpdateAvailable bool       `json:"update_available"`
	ReleaseURL      *string    `json:"release_url"`
	PublishedAt     *time.Time `json:"published_at"`
	CheckedAt       *time.Time `json:"checked_at"`
	CheckError      *string    `json:"check_error"`
}

// Checker caches both successful and failed checks and coalesces concurrent
// cache misses. It starts no persistent goroutines.
type Checker struct {
	currentVersion string
	currentSemver  string
	client         *http.Client
	now            func() time.Time

	mu     sync.RWMutex
	cached Status
	group  singleflight.Group
}

// New returns a checker for the running build version. Development and invalid
// versions are intentionally disabled and never make a network request.
func New(currentVersion string) *Checker {
	client := &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return newChecker(currentVersion, client, time.Now)
}

func newChecker(currentVersion string, client *http.Client, now func() time.Time) *Checker {
	if client == nil {
		client = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}
	return &Checker{
		currentVersion: currentVersion,
		currentSemver:  normalizeVersion(currentVersion),
		client:         client,
		now:            now,
	}
}

// Check returns cached availability when possible. A cache miss is bounded by
// requestTimeout even if the caller disconnects; callers may stop waiting via
// ctx without canceling the one shared request needed by other callers.
func (c *Checker) Check(ctx context.Context) Status {
	return c.check(ctx, cacheTTL, "latest-stable")
}

// Refresh asks GitHub again even when a fresh result is cached, so an
// administrator can see a release published moments ago. Results younger than
// refreshCooldown are reused to keep repeated clicks from exhausting GitHub's
// unauthenticated rate limit.
func (c *Checker) Refresh(ctx context.Context) Status {
	return c.check(ctx, refreshCooldown, "latest-stable-refresh")
}

func (c *Checker) check(ctx context.Context, maxAge time.Duration, key string) Status {
	if c.currentSemver == "" {
		return Status{CurrentVersion: c.currentVersion}
	}
	if cached, ok := c.cachedStatus(c.now(), maxAge); ok {
		return cached
	}

	result := c.group.DoChan(key, func() (any, error) {
		now := c.now()
		if cached, ok := c.cachedStatus(now, maxAge); ok {
			return cached, nil
		}
		status := c.fetch(now)
		c.mu.Lock()
		c.cached = status
		c.mu.Unlock()
		return status, nil
	})
	select {
	case <-ctx.Done():
		message := ctx.Err().Error()
		return Status{CurrentVersion: c.currentVersion, CheckError: &message}
	case checked := <-result:
		return checked.Val.(Status)
	}
}

func (c *Checker) cachedStatus(now time.Time, maxAge time.Duration) (Status, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cached, c.cached.CheckedAt != nil && now.Before(c.cached.CheckedAt.Add(maxAge))
}

func (c *Checker) fetch(checkedAt time.Time) Status {
	status := Status{CurrentVersion: c.currentVersion, CheckedAt: &checkedAt}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return failed(status, err)
	}
	request.Header.Set("Accept", githubJSONMediaType)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "supermetry-update-check")
	response, err := c.client.Do(request)
	if err != nil {
		return failed(status, fmt.Errorf("check GitHub release: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return failed(status, fmt.Errorf("GitHub release check returned HTTP %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return failed(status, fmt.Errorf("read GitHub release: %w", err))
	}
	if len(body) > maxResponseBytes {
		return failed(status, errors.New("GitHub release response is too large"))
	}
	var release struct {
		TagName     string     `json:"tag_name"`
		HTMLURL     string     `json:"html_url"`
		PublishedAt *time.Time `json:"published_at"`
		Draft       bool       `json:"draft"`
		Prerelease  bool       `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return failed(status, fmt.Errorf("decode GitHub release: %w", err))
	}
	latest := normalizeVersion(release.TagName)
	if latest == "" {
		return failed(status, errors.New("GitHub release has an invalid version"))
	}
	if release.Draft || release.Prerelease || semver.Prerelease(latest) != "" {
		return failed(status, errors.New("GitHub latest release is not stable"))
	}
	if release.HTMLURL != "" && !strings.HasPrefix(release.HTMLURL, "https://github.com/velixio/supermetry/releases/") {
		return failed(status, errors.New("GitHub release URL is not official"))
	}

	status.LatestVersion = stringPointer(release.TagName)
	status.UpdateAvailable = semver.Compare(latest, c.currentSemver) > 0
	status.PublishedAt = release.PublishedAt
	if release.HTMLURL != "" {
		status.ReleaseURL = stringPointer(release.HTMLURL)
	}
	return status
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "dev" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) {
		return ""
	}
	return semver.Canonical(version)
}

func failed(status Status, err error) Status {
	message := err.Error()
	status.CheckError = &message
	return status
}

func stringPointer(value string) *string { return &value }
