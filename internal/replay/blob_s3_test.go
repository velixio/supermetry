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
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestS3StoreContract(t *testing.T) {
	t.Parallel()
	server := newFakeS3(t)
	store, err := NewS3Store(S3Settings{
		Endpoint: server.URL(), Bucket: "replays", Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret", Prefix: "supermetry",
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
		AccessKey: "test-access", SecretKey: "test-secret", Prefix: "supermetry",
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
	if got := string(server.object("supermetry/replay/420/keep.gz")); got != "keep" {
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

func TestS3StoreWalkConsumesPaginatedListing(t *testing.T) {
	t.Parallel()
	server := newFakeS3(t)
	server.setPageSize(1)
	store, err := NewS3Store(S3Settings{
		Endpoint: server.URL(), Bucket: "replays", Region: "us-east-1",
		AccessKey: "test-access", SecretKey: "test-secret", Prefix: "supermetry",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	for _, key := range []string{"replay/42/a.gz", "replay/42/b.gz", "replay/42/css/c.gz"} {
		if stored, err := store.PutIfAbsent(context.Background(), key, bytes.NewBufferString(key)); err != nil || !stored {
			t.Fatalf("PutIfAbsent(%q) = (%v, %v)", key, stored, err)
		}
	}
	var keys []string
	if err := store.Walk(context.Background(), "replay/42", func(blob BlobInfo) error {
		keys = append(keys, blob.Key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	want := []string{"replay/42/a.gz", "replay/42/b.gz", "replay/42/css/c.gz"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("Walk() keys = %v, want %v", keys, want)
	}
	if calls := server.listCallCount(); calls != 3 {
		t.Fatalf("S3 list calls = %d, want 3 pages", calls)
	}
}

func TestS3StoreMinIO(t *testing.T) {
	endpoint := os.Getenv("SUPERMETRY_TEST_S3")
	if endpoint == "" {
		t.Skip("SUPERMETRY_TEST_S3 is not set")
	}
	store, err := NewS3Store(S3Settings{
		Endpoint:  endpoint,
		Bucket:    envOr("SUPERMETRY_TEST_S3_BUCKET", "supermetry-test"),
		Region:    envOr("SUPERMETRY_TEST_S3_REGION", "us-east-1"),
		AccessKey: envOr("SUPERMETRY_TEST_S3_ACCESS_KEY", "minioadmin"),
		SecretKey: envOr("SUPERMETRY_TEST_S3_SECRET_KEY", "minioadmin"),
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
	t         *testing.T
	server    *httptest.Server
	mu        sync.Mutex
	objects   map[string][]byte
	pageSize  int
	listCalls int
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

func (f *fakeS3) setPageSize(size int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageSize = size
}

func (f *fakeS3) listCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls
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
		f.listCalls++
		var keys []string
		for objectKey := range f.objects {
			if strings.HasPrefix(objectKey, prefix) {
				keys = append(keys, objectKey)
			}
		}
		sort.Strings(keys)
		start, _ := strconv.Atoi(r.URL.Query().Get("continuation-token"))
		end := len(keys)
		if f.pageSize > 0 && start+f.pageSize < end {
			end = start + f.pageSize
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult>`)
		if end < len(keys) {
			_, _ = fmt.Fprintf(w, "<IsTruncated>true</IsTruncated><NextContinuationToken>%d</NextContinuationToken>", end)
		}
		for _, objectKey := range keys[start:end] {
			_ = xml.NewEncoder(w).Encode(struct {
				XMLName xml.Name `xml:"Contents"`
				Key     string   `xml:"Key"`
				Size    int      `xml:"Size"`
			}{Key: objectKey, Size: len(f.objects[objectKey])})
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
