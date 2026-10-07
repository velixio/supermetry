// Package geoupdate securely downloads and installs MaxMind GeoIP databases.
package geoupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

const (
	DefaultBaseURL       = "https://download.maxmind.com"
	defaultClientTimeout = 2 * time.Minute
	freshFor             = 72 * time.Hour
	maxUncompressedSize  = int64(200 << 20)
	maxArchiveSize       = int64(200 << 20)
	maxChecksumSize      = int64(1 << 20)
	maxAttempts          = 3
)

var allowedEditions = map[string]struct{}{
	"GeoLite2-ASN":     {},
	"GeoLite2-City":    {},
	"GeoLite2-Country": {},
}

// Config describes one MaxMind database update.
type Config struct {
	Edition    string
	AccountID  string
	LicenseKey string
	DataDir    string
	Force      bool

	// BaseURL, Client, Now, and Backoff are optional test and deployment seams.
	BaseURL string
	Client  *http.Client
	Now     func() time.Time
	Backoff func(context.Context, time.Duration) error
}

// Result reports the installed database, or the fresh database that was kept.
type Result struct {
	Edition string
	Path    string
	ModTime time.Time
	Updated bool
}

// Update downloads, verifies, and atomically installs one MaxMind database.
func Update(ctx context.Context, cfg Config) (result Result, resultErr error) {
	if _, ok := allowedEditions[cfg.Edition]; !ok {
		return Result{}, errors.New("unsupported MaxMind edition")
	}
	if cfg.AccountID == "" {
		return Result{}, errors.New("MaxMind account ID is required")
	}
	if cfg.LicenseKey == "" {
		return Result{}, errors.New("MaxMind license key is required")
	}
	if cfg.DataDir == "" {
		return Result{}, errors.New("GeoIP data directory is required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return Result{}, redact(cfg, fmt.Errorf("create GeoIP data directory: %w", err))
	}
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		return Result{}, redact(cfg, fmt.Errorf("secure GeoIP data directory: %w", err))
	}

	destination := filepath.Join(cfg.DataDir, cfg.Edition+".mmdb")
	result = Result{Edition: cfg.Edition, Path: destination}
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	info, err := os.Stat(destination)
	if err == nil {
		result.ModTime = info.ModTime()
		if !info.Mode().IsRegular() {
			return Result{}, redact(cfg, fmt.Errorf("GeoIP database path is not a regular file: %s", destination))
		}
		if !cfg.Force && now().Sub(info.ModTime()) < freshFor {
			return result, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, redact(cfg, fmt.Errorf("inspect GeoIP database: %w", err))
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return Result{}, errors.New("invalid MaxMind base URL")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: defaultClientTimeout}
	}
	client = withoutRedirectCredentials(client)
	backoff := cfg.Backoff
	if backoff == nil {
		backoff = waitForRetry
	}
	download := downloader{cfg: cfg, baseURL: strings.TrimRight(baseURL, "/"), client: client, backoff: backoff}

	wantChecksum, err := download.checksum(ctx)
	if err != nil {
		return Result{}, redact(cfg, err)
	}
	archiveFile, archivePath, gotChecksum, err := download.archive(ctx, cfg.DataDir)
	if err != nil {
		return Result{}, redact(cfg, err)
	}
	defer func() {
		_ = archiveFile.Close()
		_ = os.Remove(archivePath)
	}()
	if gotChecksum != wantChecksum {
		return Result{}, errors.New("MaxMind archive checksum mismatch")
	}
	if _, err := archiveFile.Seek(0, io.SeekStart); err != nil {
		return Result{}, redact(cfg, fmt.Errorf("rewind MaxMind archive: %w", err))
	}

	modTime, err := installArchive(ctx, archiveFile, cfg.DataDir, destination, cfg.Edition)
	if err != nil {
		return Result{}, redact(cfg, err)
	}
	result.ModTime = modTime
	result.Updated = true
	return result, nil
}

type downloader struct {
	cfg     Config
	baseURL string
	client  *http.Client
	backoff func(context.Context, time.Duration) error
}

func (d downloader) checksum(ctx context.Context) ([sha256.Size]byte, error) {
	var checksum [sha256.Size]byte
	var contents []byte
	err := d.fetch(ctx, "tar.gz.sha256", func(body io.Reader) error {
		limited := io.LimitReader(body, maxChecksumSize+1)
		data, err := io.ReadAll(limited)
		if err != nil {
			return fmt.Errorf("read MaxMind checksum: %w", err)
		}
		if len(data) > int(maxChecksumSize) {
			return errors.New("MaxMind checksum response is too large")
		}
		contents = data
		return nil
	})
	if err != nil {
		return checksum, err
	}
	fields := strings.Fields(string(contents))
	if len(fields) == 0 {
		return checksum, errors.New("MaxMind checksum response is empty")
	}
	decoded, err := hex.DecodeString(fields[0])
	if err != nil || len(decoded) != sha256.Size {
		return checksum, errors.New("MaxMind checksum response is invalid")
	}
	copy(checksum[:], decoded)
	return checksum, nil
}

func (d downloader) archive(ctx context.Context, dir string) (*os.File, string, [sha256.Size]byte, error) {
	var checksum [sha256.Size]byte
	temporary, err := os.CreateTemp(dir, ".maxmind-archive-*")
	if err != nil {
		return nil, "", checksum, fmt.Errorf("create temporary MaxMind archive: %w", err)
	}
	path := temporary.Name()
	failed := true
	defer func() {
		if failed {
			_ = temporary.Close()
			_ = os.Remove(path)
		}
	}()

	err = d.fetch(ctx, "tar.gz", func(body io.Reader) error {
		if err := temporary.Truncate(0); err != nil {
			return fmt.Errorf("truncate temporary MaxMind archive: %w", err)
		}
		if _, err := temporary.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind temporary MaxMind archive: %w", err)
		}
		hasher := sha256.New()
		limited := &io.LimitedReader{R: body, N: maxArchiveSize + 1}
		written, err := io.Copy(io.MultiWriter(temporary, hasher), limited)
		if err != nil {
			return fmt.Errorf("download MaxMind archive: %w", err)
		}
		if written > maxArchiveSize {
			return errors.New("MaxMind archive exceeds 200MB download limit")
		}
		copy(checksum[:], hasher.Sum(nil))
		return nil
	})
	if err != nil {
		return nil, "", checksum, err
	}
	failed = false
	return temporary, path, checksum, nil
}

func (d downloader) fetch(ctx context.Context, suffix string, consume func(io.Reader) error) error {
	endpoint := d.baseURL + "/geoip/databases/" + url.PathEscape(d.cfg.Edition) + "/download?suffix=" + url.QueryEscape(suffix)
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("create MaxMind %s request: %w", suffix, err)
		}
		request.SetBasicAuth(d.cfg.AccountID, d.cfg.LicenseKey)
		response, err := d.client.Do(request)
		if err == nil {
			if response.StatusCode == http.StatusOK {
				consumeErr := consume(response.Body)
				closeErr := response.Body.Close()
				if consumeErr == nil && closeErr == nil {
					return nil
				}
				lastErr = errors.Join(consumeErr, closeErr)
			} else {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
				_ = response.Body.Close()
				lastErr = fmt.Errorf("MaxMind %s request returned %s", suffix, response.Status)
				if !retryableStatus(response.StatusCode) {
					return lastErr
				}
			}
		} else {
			lastErr = fmt.Errorf("MaxMind %s request failed: %w", suffix, err)
		}
		if attempt+1 == maxAttempts {
			break
		}
		if err := d.backoff(ctx, (250*time.Millisecond)<<attempt); err != nil {
			return err
		}
	}
	return lastErr
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func withoutRedirectCredentials(client *http.Client) *http.Client {
	clone := *client
	original := clone.CheckRedirect
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if original != nil {
			if err := original(request, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		request.Header.Del("Authorization")
		return nil
	}
	return &clone
}

func installArchive(ctx context.Context, archive io.Reader, dir, destination, edition string) (modTime time.Time, resultErr error) {
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return time.Time{}, fmt.Errorf("open MaxMind archive: %w", err)
	}
	gzipClosed := false
	defer func() {
		if !gzipClosed {
			_ = gzipReader.Close()
		}
	}()

	temporary, err := os.CreateTemp(dir, ".maxmind-database-*")
	if err != nil {
		_ = gzipReader.Close()
		return time.Time{}, fmt.Errorf("create temporary GeoIP database: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			resultErr = errors.Join(resultErr, temporary.Close(), os.Remove(temporaryPath))
		}
	}()

	reader := tar.NewReader(contextReader{ctx: ctx, reader: gzipReader})
	var totalSize int64
	found := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return time.Time{}, fmt.Errorf("read MaxMind archive: %w", err)
		}
		if !safeArchivePath(header.Name) {
			return time.Time{}, fmt.Errorf("unsafe archive path %q", header.Name)
		}
		if header.Size < 0 || header.Size > maxUncompressedSize-totalSize {
			return time.Time{}, errors.New("MaxMind archive exceeds 200MB uncompressed limit")
		}
		totalSize += header.Size
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			if header.Typeflag == tar.TypeDir {
				continue
			}
			return time.Time{}, fmt.Errorf("unsupported archive entry %q", header.Name)
		}
		if !strings.EqualFold(path.Ext(header.Name), ".mmdb") {
			continue
		}
		if found {
			return time.Time{}, errors.New("MaxMind archive contains more than one MMDB file")
		}
		found = true
		if _, err := io.Copy(temporary, reader); err != nil {
			return time.Time{}, fmt.Errorf("extract MaxMind database: %w", err)
		}
	}
	if !found {
		return time.Time{}, errors.New("MaxMind archive contains no regular MMDB file")
	}
	if err := gzipReader.Close(); err != nil {
		return time.Time{}, fmt.Errorf("close MaxMind archive: %w", err)
	}
	gzipClosed = true
	if err := temporary.Chmod(0o600); err != nil {
		return time.Time{}, fmt.Errorf("secure temporary GeoIP database: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return time.Time{}, fmt.Errorf("sync temporary GeoIP database: %w", err)
	}
	info, err := temporary.Stat()
	if err != nil {
		return time.Time{}, fmt.Errorf("inspect temporary GeoIP database: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return time.Time{}, fmt.Errorf("close temporary GeoIP database: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	database, err := maxminddb.Open(temporaryPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("verify extracted MMDB: %w", err)
	}
	databaseType := database.Metadata.DatabaseType
	verifyErr := database.Verify()
	closeErr := database.Close()
	if verifyErr != nil || closeErr != nil {
		return time.Time{}, fmt.Errorf("verify extracted MMDB: %w", errors.Join(verifyErr, closeErr))
	}
	if databaseType != edition {
		return time.Time{}, fmt.Errorf("extracted MMDB database type %q does not match edition %q", databaseType, edition)
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return time.Time{}, fmt.Errorf("install GeoIP database: %w", err)
	}
	keep = true
	return info.ModTime(), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func safeArchivePath(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	cleaned := path.Clean(name)
	return name != "" && !path.IsAbs(name) && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func redact(cfg Config, err error) error {
	if err == nil || cfg.LicenseKey == "" {
		return err
	}
	redacted := strings.ReplaceAll(err.Error(), cfg.LicenseKey, "[REDACTED]")
	credential := base64.StdEncoding.EncodeToString([]byte(cfg.AccountID + ":" + cfg.LicenseKey))
	redacted = strings.ReplaceAll(redacted, credential, "[REDACTED]")
	if redacted == err.Error() {
		return err
	}
	return errors.New(redacted)
}
