package release

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/mod/semver"
)

const (
	MaxManifestBytes       = int64(256 << 10)
	maxSignatureBytes      = int64(8 << 10)
	clientTimeout          = 15 * time.Second
	officialReleaseBaseURL = "https://github.com/velixio/supermetry/releases/download/"
)

type Client struct {
	http *http.Client
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clone := *httpClient
	clone.Timeout = clientTimeout
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many GitHub release redirects")
		}
		if request.URL.Scheme != "https" || request.URL.Hostname() != "github.com" && request.URL.Hostname() != "release-assets.githubusercontent.com" && request.URL.Hostname() != "objects.githubusercontent.com" {
			return errors.New("GitHub release redirected to an untrusted host")
		}
		return nil
	}
	return &Client{http: &clone}
}

// FetchArtifact streams one manifest-selected artifact from its fixed official
// release URL while enforcing the signed size and SHA-256. Callers must write
// to a disposable staging file because an error can occur after partial output.
func (c *Client) FetchArtifact(ctx context.Context, version string, artifact Artifact, destination io.Writer) error {
	if destination == nil {
		return errors.New("artifact destination is required")
	}
	if !semver.IsValid(version) || semver.Canonical(version) != version || semver.Prerelease(version) != "" {
		return errors.New("release version must be a canonical stable semantic version")
	}
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	endpoint := officialReleaseBaseURL + url.PathEscape(version) + "/" + url.PathEscape(artifact.Name)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create artifact request: %w", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "supermetry-updater")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request official GitHub release artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("official GitHub release artifact returned HTTP %d", response.StatusCode)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(response.Body, artifact.Size+1))
	if err != nil {
		return fmt.Errorf("read official GitHub release artifact: %w", err)
	}
	if written != artifact.Size {
		return fmt.Errorf("release artifact size mismatch: got %d, want %d", written, artifact.Size)
	}
	want, _ := hex.DecodeString(artifact.SHA256)
	if subtle.ConstantTimeCompare(hasher.Sum(nil), want) != 1 {
		return errors.New("release artifact digest mismatch")
	}
	return nil
}

func (c *Client) FetchManifest(ctx context.Context, version string) (manifest, signature []byte, resultErr error) {
	if !semver.IsValid(version) || semver.Canonical(version) != version || semver.Prerelease(version) != "" {
		return nil, nil, errors.New("release version must be a canonical stable semantic version")
	}
	base := officialReleaseBaseURL + url.PathEscape(version) + "/release-manifest.json"
	manifest, err := c.fetch(ctx, base, MaxManifestBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch release manifest: %w", err)
	}
	signature, err = c.fetch(ctx, base+".sig", maxSignatureBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch release signature: %w", err)
	}
	return manifest, signature, nil
}

func (c *Client) fetch(ctx context.Context, endpoint string, limit int64) (result []byte, resultErr error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "supermetry-updater")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request official GitHub release: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, response.Body.Close())
	}()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("official GitHub release returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read official GitHub release: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("official GitHub release response is too large (limit %s)", byteCount(limit))
	}
	return data, nil
}

func byteCount(value int64) string {
	if value%(1<<10) == 0 {
		return fmt.Sprintf("%d KiB", value>>10)
	}
	return fmt.Sprintf("%d bytes", value)
}
