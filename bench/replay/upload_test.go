// Throwaway: CPU cost of the replay upload path (MaxBytesReader + io.Copy to an O_APPEND file).
// Run: cd bench/replay && go test -bench . -benchmem -cpu 1
package replay

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const maxChunk = 1 << 20 // 1 MiB per upload request

// handler appends the (still gzipped) body verbatim to the session's file.
func handler(f *os.File) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := http.MaxBytesReader(w, r.Body, maxChunk)
		if _, err := io.Copy(f, body); err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// payload builds a 64 KiB gzipped body of semi-compressible JSON-like data,
// roughly like an rrweb batch.
func payload(tb testing.TB) []byte {
	var raw bytes.Buffer
	for raw.Len() < 1200<<10 {
		b := make([]byte, 8)
		rand.Read(b)
		raw.WriteString(`{"type":3,"data":{"source":1,"positions":[{"x":512,"y":301,"id":42,"timeOffset":-` + hex.EncodeToString(b) + `}]},"timestamp":1760000000000},`)
	}
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, 6)
	zw.Write(raw.Bytes())
	zw.Close()
	out := gz.Bytes()
	if len(out) < 64<<10 {
		tb.Fatalf("payload too small: %d", len(out))
	}
	return out[:64<<10] // exact 64 KiB; handler never decodes, so truncation is fine
}

func BenchmarkUpload64K(b *testing.B) {
	dir := b.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "session.rr.gz"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	h := handler(f)
	body := payload(b)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/replay/abc", bytes.NewReader(body))
		req.Header.Set("Content-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusNoContent {
			b.Fatalf("status %d", rec.Code)
		}
		if i%1024 == 1023 { // keep the temp file from growing unbounded
			f.Truncate(0)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "chunks/s")
}

// BenchmarkUpload64KServer goes through a real loopback HTTP server (parsing, TCP),
// still with GOMAXPROCS set by -cpu.
func BenchmarkUpload64KServer(b *testing.B) {
	dir := b.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "session.rr.gz"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	srv := httptest.NewServer(handler(f))
	defer srv.Close()
	body := payload(b)
	client := srv.Client()
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Post(srv.URL+"/api/replay/abc", "application/octet-stream", bytes.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			b.Fatalf("status %d", resp.StatusCode)
		}
		if i%1024 == 1023 {
			f.Truncate(0)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "chunks/s")
}
