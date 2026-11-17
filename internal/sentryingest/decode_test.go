package sentryingest

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func compress(t testing.TB, encoding string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	switch encoding {
	case "gzip":
		w := gzip.NewWriter(&buf)
		_, _ = w.Write(data)
		_ = w.Close()
	case "deflate":
		w := zlib.NewWriter(&buf)
		_, _ = w.Write(data)
		_ = w.Close()
	case "deflate-raw":
		w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
		_, _ = w.Write(data)
		_ = w.Close()
	case "br":
		w := brotli.NewWriter(&buf)
		_, _ = w.Write(data)
		_ = w.Close()
	case "zstd":
		w, _ := zstd.NewWriter(&buf)
		_, _ = w.Write(data)
		_ = w.Close()
	default:
		t.Fatalf("unknown encoding %q", encoding)
	}
	return buf.Bytes()
}

func bodyRequest(encoding string, body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/42/envelope/", bytes.NewReader(body))
	if encoding != "" {
		r.Header.Set("Content-Encoding", encoding)
	}
	return r
}

func TestDecodeBodyEncodings(t *testing.T) {
	plain := []byte(strings.Repeat(`{"type":"event"}`, 100))
	for _, enc := range []string{"gzip", "deflate", "deflate-raw", "br", "zstd"} {
		header := enc
		if enc == "deflate-raw" {
			header = "deflate"
		}
		got, err := decodeBody(bodyRequest(header, compress(t, enc, plain)), 1<<20)
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("%s: err=%v equal=%v", enc, err, bytes.Equal(got, plain))
		}
	}
	for _, enc := range []string{"", "UTF-8", "identity", "GZIP "} {
		body := plain
		if strings.TrimSpace(strings.ToLower(enc)) == "gzip" {
			body = compress(t, "gzip", plain)
		}
		got, err := decodeBody(bodyRequest(enc, body), 1<<20)
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("encoding %q: err=%v", enc, err)
		}
	}
}

func TestDecodeBodyRejects(t *testing.T) {
	if _, err := decodeBody(bodyRequest("compress", []byte("x")), 1<<20); !errors.Is(err, ErrEncoding) {
		t.Errorf("unknown encoding: %v", err)
	}
	if _, err := decodeBody(bodyRequest("gzip", []byte("not gzip")), 1<<20); !errors.Is(err, ErrMalformed) {
		t.Errorf("bad gzip: %v", err)
	}
	if _, err := decodeBody(bodyRequest("br", []byte("\xff\xff\xff\xff garbage")), 1<<20); err == nil {
		t.Error("bad brotli accepted")
	}
}

func TestDecodeBodySizeCaps(t *testing.T) {
	const max = 1 << 20
	if _, err := decodeBody(bodyRequest("", bytes.Repeat([]byte("a"), max)), max); err != nil {
		t.Errorf("exactly max: %v", err)
	}
	if _, err := decodeBody(bodyRequest("", bytes.Repeat([]byte("a"), max+1)), max); !errors.Is(err, ErrTooLarge) {
		t.Errorf("max+1 identity: %v", err)
	}
	for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
		small := compress(t, enc, bytes.Repeat([]byte("a"), max+1))
		if len(small) >= max {
			t.Fatalf("%s test body is not small", enc)
		}
		if _, err := decodeBody(bodyRequest(enc, small), max); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s decompressed max+1: %v", enc, err)
		}
	}
}

// TestDecodeBodyBombStaysBounded decodes small bodies that expand to 1 GiB
// (gzip reaches about 1000:1, zstd far more) and checks the live heap never
// grows past 64 MiB.
func TestDecodeBodyBombStaysBounded(t *testing.T) {
	zero := make([]byte, 1<<20)
	for _, enc := range []string{"gzip", "zstd"} {
		var buf bytes.Buffer
		var w interface {
			Write([]byte) (int, error)
			Close() error
		}
		if enc == "gzip" {
			w, _ = gzip.NewWriterLevel(&buf, gzip.BestCompression)
		} else {
			w, _ = zstd.NewWriter(&buf)
		}
		for i := 0; i < 1024; i++ {
			_, _ = w.Write(zero)
		}
		_ = w.Close()
		t.Logf("%s bomb: %d bytes for 1 GiB", enc, buf.Len())
		if enc == "zstd" && buf.Len() > 256<<10 {
			t.Fatalf("zstd bomb is %d bytes", buf.Len())
		}
		runtime.GC()
		var base runtime.MemStats
		runtime.ReadMemStats(&base)
		var peak atomic.Uint64
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			var m runtime.MemStats
			for {
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak.Load() {
					peak.Store(m.HeapAlloc)
				}
				select {
				case <-stop:
					return
				case <-time.After(time.Millisecond):
				}
			}
		}()
		_, err := decodeBody(bodyRequest(enc, buf.Bytes()), MaxEnvelopeBytes)
		close(stop)
		<-done
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("%s: err = %v, want too large", enc, err)
		}
		if grew := int64(peak.Load()) - int64(base.HeapAlloc); grew > 64<<20 {
			t.Fatalf("%s: heap grew by %d MiB", enc, grew>>20)
		}
	}
}

func FuzzDecodeBody(f *testing.F) {
	plain := []byte(`{"event_id":"x"}` + "\n" + `{"type":"event"}` + "\n" + `{"a":1}` + "\n")
	for i, enc := range []string{"gzip", "deflate", "br", "zstd"} {
		f.Add(uint8(i), compress(f, enc, plain))
	}
	f.Add(uint8(4), plain)
	f.Add(uint8(0), []byte("\x1f\x8b\x08"))
	encodings := []string{"gzip", "deflate", "br", "zstd", "", "bogus"}
	f.Fuzz(func(t *testing.T, which uint8, data []byte) {
		enc := encodings[int(which)%len(encodings)]
		out, err := decodeBody(bodyRequest(enc, data), 1<<20)
		if err == nil && len(out) > 1<<20 {
			t.Fatalf("returned %d bytes past the cap", len(out))
		}
	})
}
