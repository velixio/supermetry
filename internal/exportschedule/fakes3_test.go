package exportschedule

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeS3 implements the object operations the exporter uses: PUT object,
// multipart create/part/complete/abort, DELETE. It decodes aws-chunked bodies
// (what minio-go sends) and records requests.
type fakeS3 struct {
	mu        sync.Mutex
	objects   map[string][]byte
	uploads   map[string]map[int][]byte // uploadId -> part -> data
	keys      map[string]string         // uploadId -> key
	Requests  int
	Multipart int // completed multipart uploads
	Parts     int
	Aborted   int
	FailPut   bool
	Server    *httptest.Server
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	f := &fakeS3{objects: map[string][]byte{}, uploads: map[string]map[int][]byte{}, keys: map[string]string{}}
	f.Server = httptest.NewTLSServer(f)
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeS3) tlsConfig() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(f.Server.Certificate())
	return &tls.Config{RootCAs: pool}
}

func (f *fakeS3) object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	return b, ok
}

func (f *fakeS3) keysStored() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.objects {
		out = append(out, k)
	}
	return out
}

func readBody(r *http.Request) ([]byte, error) {
	if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") || strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "UNSIGNED-PAYLOAD-TRAILER") ||
		strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
		br := bufio.NewReader(r.Body)
		var out bytes.Buffer
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return nil, err
			}
			sizeText, _, _ := strings.Cut(strings.TrimSpace(line), ";")
			size, err := strconv.ParseInt(sizeText, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("bad chunk header %q", line)
			}
			if size == 0 {
				io.Copy(io.Discard, br) // trailers
				return out.Bytes(), nil
			}
			if _, err := io.CopyN(&out, br, size); err != nil {
				return nil, err
			}
			if _, err := br.Discard(2); err != nil {
				return nil, err
			}
		}
	}
	return io.ReadAll(r.Body)
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.Requests++
	f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=") {
		http.Error(w, "unsigned", http.StatusForbidden)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/") // path style: bucket/key
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.mu.Lock()
		id := "up" + strconv.Itoa(len(f.uploads)+1)
		f.uploads[id] = map[int][]byte{}
		f.keys[id] = key
		f.mu.Unlock()
		fmt.Fprintf(w, `<?xml version="1.0"?><InitiateMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, id)
	case r.Method == http.MethodPut && q.Has("partNumber"):
		body, err := readBody(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		n, _ := strconv.Atoi(q.Get("partNumber"))
		f.mu.Lock()
		f.uploads[q.Get("uploadId")][n] = body
		f.Parts++
		f.mu.Unlock()
		w.Header().Set("ETag", `"part`+strconv.Itoa(n)+`"`)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		id := q.Get("uploadId")
		var all []byte
		for n := 1; n <= len(f.uploads[id]); n++ {
			all = append(all, f.uploads[id][n]...)
		}
		f.objects[f.keys[id]] = all
		f.Multipart++
		f.mu.Unlock()
		fmt.Fprint(w, `<?xml version="1.0"?><CompleteMultipartUploadResult><Bucket>b</Bucket><Key>k</Key><ETag>"done"</ETag></CompleteMultipartUploadResult>`)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		f.mu.Lock()
		f.Aborted++
		delete(f.uploads, q.Get("uploadId"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		body, err := readBody(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if f.FailPut {
			http.Error(w, `<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`, http.StatusForbidden)
			return
		}
		f.mu.Lock()
		f.objects[key] = body
		f.mu.Unlock()
		w.Header().Set("ETag", `"x"`)
	case r.Method == http.MethodDelete:
		f.mu.Lock()
		delete(f.objects, key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unsupported", http.StatusNotImplemented)
	}
}
