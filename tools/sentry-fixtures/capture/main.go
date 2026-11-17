// Command capture is a dev-only HTTP server that records raw Sentry envelope
// requests as golden fixtures. It uses only the standard library and is never
// part of the production binary.
//
// Each request produces <seq>-<first item type>.envelope (the body exactly as
// received, still compressed if the SDK compressed it) and a .json sidecar
// with the method, path, query and a small allow-list of headers.
package main

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
)

var recordedHeaders = []string{"Content-Type", "Content-Encoding", "X-Sentry-Auth", "User-Agent"}

var safeName = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

type sidecar struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query"`
	Headers    map[string]string `json:"headers"`
	SDK        string            `json:"sdk"`
	SDKVersion string            `json:"sdk_version"`
	Notes      string            `json:"notes"`
}

// decode returns the plain envelope bytes for peeking at the headers. It
// returns the body unchanged when the encoding is unknown (e.g. br).
func decode(body []byte, enc string) []byte {
	var r io.Reader
	switch strings.ToLower(enc) {
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil
		}
		r = zr
	case "deflate":
		if zr, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
			r = zr
		} else {
			r = flate.NewReader(bytes.NewReader(body))
		}
	case "", "identity":
		return body
	default:
		return nil
	}
	out, _ := io.ReadAll(io.LimitReader(r, 64<<20))
	return out
}

// peek returns the envelope header event_id and the first item's type.
func peek(plain []byte) (eventID, itemType string) {
	if plain == nil {
		return "", ""
	}
	br := bufio.NewReaderSize(bytes.NewReader(plain), 1<<20)
	l1, _ := br.ReadBytes('\n')
	var hdr struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(bytes.TrimSpace(l1), &hdr)
	l2, _ := br.ReadBytes('\n')
	var item struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(bytes.TrimSpace(l2), &item)
	return hdr.EventID, item.Type
}

func sdkFromEnvelope(plain []byte) (name, version string) {
	if plain == nil {
		return "", ""
	}
	l1, _, _ := bytes.Cut(plain, []byte("\n"))
	var hdr struct {
		SDK struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"sdk"`
	}
	_ = json.Unmarshal(l1, &hdr)
	return hdr.SDK.Name, hdr.SDK.Version
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Content-Encoding, X-Sentry-Auth, Authorization")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Expose-Headers", "X-Sentry-Rate-Limits, Retry-After")
}

func newCaptureHandler(outDir, sdk string, seq *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPost:
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		plain := decode(body, r.Header.Get("Content-Encoding"))
		eventID, itemType := peek(plain)
		if itemType == "" {
			itemType = "unknown"
		}
		n := seq.Add(1)
		base := filepath.Join(outDir, fmt.Sprintf("%d-%s", n, safeName.ReplaceAllString(itemType, "_")))
		sc := sidecar{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Headers: map[string]string{}, SDK: sdk}
		for _, k := range recordedHeaders {
			if v := r.Header.Get(k); v != "" {
				sc.Headers[k] = v
			}
		}
		if name, ver := sdkFromEnvelope(plain); name != "" {
			sc.SDKVersion = name + "@" + ver
		}
		if err := os.WriteFile(base+".envelope", body, 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		js, _ := json.MarshalIndent(sc, "", "  ")
		if err := os.WriteFile(base+".json", append(js, '\n'), 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": eventID})
	})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8977", "listen address")
	out := flag.String("out", "", "output directory")
	sdk := flag.String("sdk", "", "sdk label recorded in the sidecar")
	flag.Parse()
	if *out == "" {
		log.Fatal("-out is required")
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	var seq atomic.Int64
	log.Printf("capturing %s envelopes on %s into %s", *sdk, *addr, *out)
	log.Fatal(http.ListenAndServe(*addr, newCaptureHandler(*out, *sdk, &seq)))
}
