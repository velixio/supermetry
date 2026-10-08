package replay

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestS3StoreContract(t *testing.T) {
	t.Parallel()
	server := newFakeS3(t)
	store, err := NewS3Store(S3Settings{
		Endpoint: server.URL(), Bucket: "replays", Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret", Prefix: "panel",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	testBlobStoreContract(t, store)
}

func TestS3DeletePrefixDoesNotDeleteSiblingPrefix(t *testing.T) {
	t.Parallel()
	server := newFakeS3(t)
	store, err := NewS3Store(S3Settings{
		Endpoint: server.URL(), Bucket: "replays", Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret", Prefix: "panel",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	if _, err := store.PutIfAbsent(context.Background(), "replay/420/keep.gz", bytes.NewBufferString("keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeletePrefix(context.Background(), "replay/42"); err != nil {
		t.Fatal(err)
	}
	if got := string(server.object("panel/replay/420/keep.gz")); got != "keep" {
		t.Fatalf("sibling-prefix backing object = %q, want keep", got)
	}
	reader, err := store.Open(context.Background(), "replay/420/keep.gz")
	if err != nil {
		t.Fatalf("sibling-prefix object was deleted: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("sibling-prefix object = %q, want keep", got)
	}
}

func TestS3StoreMinIO(t *testing.T) {
	endpoint := os.Getenv("PANEL_TEST_S3")
	if endpoint == "" {
		t.Skip("PANEL_TEST_S3 is not set")
	}
	store, err := NewS3Store(S3Settings{
		Endpoint:  endpoint,
		Bucket:    envOr("PANEL_TEST_S3_BUCKET", "panel-test"),
		Region:    envOr("PANEL_TEST_S3_REGION", "us-east-1"),
		AccessKey: envOr("PANEL_TEST_S3_ACCESS_KEY", "minioadmin"),
		SecretKey: envOr("PANEL_TEST_S3_SECRET_KEY", "minioadmin"),
		Prefix:    "contract",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	testBlobStoreContract(t, store)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

type fakeS3 struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	fake := &fakeS3{t: t, objects: make(map[string][]byte)}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeS3) URL() string { return f.server.URL }

func (f *fakeS3) object(key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bytes.Clone(f.objects[key])
}

func (f *fakeS3) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		http.Error(w, "missing signature", http.StatusForbidden)
		return
	}
	key := strings.TrimPrefix(r.URL.EscapedPath(), "/replays/")
	key, _ = strings.CutSuffix(key, "/")
	f.mu.Lock()
	defer f.mu.Unlock()

	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		prefix := r.URL.Query().Get("prefix")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult>`)
		for objectKey, body := range f.objects {
			if strings.HasPrefix(objectKey, prefix) {
				_ = xml.NewEncoder(w).Encode(struct {
					XMLName xml.Name `xml:"Contents"`
					Key     string   `xml:"Key"`
					Size    int      `xml:"Size"`
				}{Key: objectKey, Size: len(body)})
			}
		}
		_, _ = io.WriteString(w, `</ListBucketResult>`)
		return
	}

	switch r.Method {
	case http.MethodPut:
		if r.Header.Get("If-None-Match") == "*" {
			if _, exists := f.objects[key]; exists {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Errorf("read PUT: %v", err)
			http.Error(w, "read", http.StatusInternalServerError)
			return
		}
		f.objects[key] = body
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		body, exists := f.objects[key]
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Header().Set("Last-Modified", "Thu, 08 Oct 2026 00:00:00 GMT")
		w.Header().Set("ETag", `"fake-etag"`)
		_, _ = w.Write(body)
	case http.MethodHead:
		body, exists := f.objects[key]
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Header().Set("Last-Modified", "Thu, 08 Oct 2026 00:00:00 GMT")
		w.Header().Set("ETag", `"fake-etag"`)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}
