package geoupdate_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"panel/internal/geoupdate"
)

const (
	testEdition = "GeoLite2-Country"
	testAccount = "42"
	testKey     = "very-secret-license-key"
)

func TestUpdateDownloadsVerifiesAndAtomicallyInstalls(t *testing.T) {
	t.Parallel()

	database := validDatabase(t)
	archive := makeArchive(t, tarEntry{name: "GeoLite2-Country_20261008/GeoLite2-Country.mmdb", body: database})
	digest := sha256.Sum256(archive)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/geoip/databases/" + testEdition + "/download":
			user, password, ok := r.BasicAuth()
			if !ok || user != testAccount || password != testKey {
				t.Errorf("BasicAuth = %q/%q/%v", user, password, ok)
			}
			switch r.URL.Query().Get("suffix") {
			case "tar.gz":
				http.Redirect(w, r, server.URL+"/cdn/database.tar.gz", http.StatusFound)
			case "tar.gz.sha256":
				fmt.Fprintf(w, "%x  database.tar.gz\n", digest)
			default:
				t.Errorf("unexpected suffix %q", r.URL.Query().Get("suffix"))
				http.NotFound(w, r)
			}
		case "/cdn/database.tar.gz":
			if _, _, ok := r.BasicAuth(); ok {
				t.Error("BasicAuth forwarded to redirect target")
			}
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dataDir := filepath.Join(t.TempDir(), "geoip")
	if err := os.Mkdir(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: dataDir, BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.Edition != testEdition || result.Path != filepath.Join(dataDir, testEdition+".mmdb") || result.ModTime.IsZero() {
		t.Fatalf("result = %+v", result)
	}
	contents, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents, database) {
		t.Fatal("installed database differs from downloaded database")
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode = %o, want 700", info.Mode().Perm())
	}
}

func TestUpdateChecksumMismatchKeepsOldDatabase(t *testing.T) {
	t.Parallel()

	archive := makeArchive(t, tarEntry{name: "database.mmdb", body: validDatabase(t)})
	server := archiveServer(t, archive, strings.Repeat("0", sha256.Size*2), http.StatusOK)
	defer server.Close()

	path := existingDatabase(t, "old database")
	_, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: filepath.Dir(path), BaseURL: server.URL, Force: true,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("error = %v, want checksum error", err)
	}
	assertFileContents(t, path, "old database")
}

func TestUpdateUnauthorizedKeepsOldDatabaseAndRedactsKey(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "license key "+testKey+" rejected", http.StatusUnauthorized)
	}))
	defer server.Close()

	path := existingDatabase(t, "old database")
	_, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: filepath.Dir(path), BaseURL: server.URL, Force: true,
	})
	if err == nil {
		t.Fatal("Update succeeded with 401 response")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error disclosed license key: %v", err)
	}
	assertFileContents(t, path, "old database")
}

func TestUpdateRedactsEncodedBasicAuthFromTransportErrors(t *testing.T) {
	t.Parallel()
	credential := base64.StdEncoding.EncodeToString([]byte(testAccount + ":" + testKey))
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("request headers contained Basic " + credential)
	})}
	_, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: t.TempDir(), Client: client,
		Backoff: func(context.Context, time.Duration) error { return nil },
	})
	if err == nil || strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), credential) {
		t.Fatalf("Update() error disclosed credentials: %v", err)
	}
}

func TestUpdateRejectsArchiveTraversalAndKeepsOldDatabase(t *testing.T) {
	t.Parallel()

	archive := makeArchive(t,
		tarEntry{name: "../escape", body: []byte("bad")},
		tarEntry{name: "database.mmdb", body: validDatabase(t)},
	)
	digest := sha256.Sum256(archive)
	server := archiveServer(t, archive, fmt.Sprintf("%x", digest), http.StatusOK)
	defer server.Close()

	path := existingDatabase(t, "old database")
	_, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: filepath.Dir(path), BaseURL: server.URL, Force: true,
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("error = %v, want traversal error", err)
	}
	assertFileContents(t, path, "old database")
}

func TestUpdateRejectsOversizedArchiveAndKeepsOldDatabase(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "database.mmdb", Mode: 0o600, Size: (200 << 20) + 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close() // Deliberately missing the declared body; extraction rejects the header first.
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	archive := compressed.Bytes()
	digest := sha256.Sum256(archive)
	server := archiveServer(t, archive, fmt.Sprintf("%x", digest), http.StatusOK)
	defer server.Close()

	path := existingDatabase(t, "old database")
	_, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: filepath.Dir(path), BaseURL: server.URL, Force: true,
	})
	if err == nil || !strings.Contains(err.Error(), "200MB") {
		t.Fatalf("error = %v, want size-limit error", err)
	}
	assertFileContents(t, path, "old database")
}

func TestUpdateRejectsInvalidOrWrongEditionDatabase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		database []byte
		want     string
	}{
		{name: "invalid MMDB", database: []byte("not an MMDB"), want: "verify"},
		{name: "wrong edition", database: dbIPDatabase(t), want: "database type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := makeArchive(t, tarEntry{name: "database.mmdb", body: tt.database})
			digest := sha256.Sum256(archive)
			server := archiveServer(t, archive, fmt.Sprintf("%x", digest), http.StatusOK)
			defer server.Close()

			path := existingDatabase(t, "old database")
			_, err := geoupdate.Update(context.Background(), geoupdate.Config{
				Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
				DataDir: filepath.Dir(path), BaseURL: server.URL, Force: true,
			})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			assertFileContents(t, path, "old database")
		})
	}
}

func TestUpdateSkipsDatabaseYoungerThan72Hours(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	path := existingDatabase(t, "current database")
	modTime := now.Add(-71 * time.Hour)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP request made for a fresh database")
		return nil, nil
	})}

	result, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: filepath.Dir(path), Client: client, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated || !result.ModTime.Equal(modTime) {
		t.Fatalf("result = %+v", result)
	}
	assertFileContents(t, path, "current database")
}

func TestUpdateRejectsEditionsOutsideGeoLiteAllowlist(t *testing.T) {
	t.Parallel()

	_, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: "GeoIP2-Country", AccountID: testAccount, LicenseKey: testKey, DataDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported MaxMind edition") {
		t.Fatalf("error = %v, want unsupported-edition error", err)
	}
}

func TestUpdateRetriesWithExponentialContextAwareBackoff(t *testing.T) {
	t.Parallel()

	database := validDatabase(t)
	archive := makeArchive(t, tarEntry{name: "database.mmdb", body: database})
	digest := sha256.Sum256(archive)
	archiveAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("suffix") {
		case "tar.gz.sha256":
			fmt.Fprintf(w, "%x  database.tar.gz\n", digest)
		case "tar.gz":
			archiveAttempts++
			if archiveAttempts < 3 {
				http.Error(w, "temporary outage", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var delays []time.Duration
	result, err := geoupdate.Update(context.Background(), geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: t.TempDir(), BaseURL: server.URL,
		Backoff: func(ctx context.Context, delay time.Duration) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			delays = append(delays, delay)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || archiveAttempts != 3 {
		t.Fatalf("result=%+v attempts=%d", result, archiveAttempts)
	}
	if len(delays) != 2 || delays[0] != 250*time.Millisecond || delays[1] != 500*time.Millisecond {
		t.Fatalf("backoff delays = %v", delays)
	}
}

func TestUpdateStopsRetryBackoffWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		cancel()
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     "503 Service Unavailable",
			Body:       io.NopCloser(strings.NewReader("temporary outage")),
			Request:    request,
		}, nil
	})}

	_, err := geoupdate.Update(ctx, geoupdate.Config{
		Edition: testEdition, AccountID: testAccount, LicenseKey: testKey,
		DataDir: t.TempDir(), Client: client, Force: true,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

type tarEntry struct {
	name string
	body []byte
}

func makeArchive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(zw)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func validDatabase(t *testing.T) []byte {
	t.Helper()
	database := dbIPDatabase(t)
	oldMetadata := append([]byte{0x51}, []byte("DBIP-Country-Lite")...)
	newMetadata := append([]byte{0x50}, []byte(testEdition)...)
	updated := bytes.Replace(database, oldMetadata, newMetadata, 1)
	if bytes.Equal(updated, database) {
		t.Fatal("test MMDB metadata marker not found")
	}
	return updated
}

func dbIPDatabase(t *testing.T) []byte {
	t.Helper()
	database, err := os.ReadFile(filepath.Join("..", "enrich", "lists", "dbip-country-lite.mmdb"))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func archiveServer(t *testing.T, archive []byte, checksum string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, "request failed", status)
			return
		}
		if r.URL.Query().Get("suffix") == "tar.gz.sha256" {
			fmt.Fprintf(w, "%s  database.tar.gz\n", checksum)
			return
		}
		_, _ = w.Write(archive)
	}))
}

func existingDatabase(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, testEdition+".mmdb")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("contents = %q, want %q", got, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
