package replay

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
)

const (
	maxCompressedCSSBytes   = 1 << 20
	maxDecompressedCSSBytes = 4 << 20
	cssPathPrefix           = "/api/replay/css/"
)

func NewCSSHandler(store BlobStore, lookupSite func(publicID string) (siteID int32, ok bool), sem chan struct{}, onDrop func()) http.Handler {
	if onDrop == nil {
		onDrop = func() {}
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		setCSSCORS(response, request)
		if request.Method == http.MethodOptions {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method != http.MethodPut {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		hash := strings.TrimPrefix(request.URL.Path, cssPathPrefix)
		if request.URL.Path == hash || !validCSSHash(hash) {
			http.Error(response, "invalid CSS hash", http.StatusBadRequest)
			return
		}
		siteID, ok := lookupSite(request.URL.Query().Get("s"))
		if !ok {
			http.NotFound(response, request)
			return
		}
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			onDrop()
			http.Error(response, "replay upload busy", http.StatusServiceUnavailable)
			return
		}

		compressed, err := io.ReadAll(io.LimitReader(request.Body, maxCompressedCSSBytes+1))
		if err != nil {
			http.Error(response, "read CSS", http.StatusBadRequest)
			return
		}
		if len(compressed) > maxCompressedCSSBytes {
			http.Error(response, "compressed CSS too large", http.StatusRequestEntityTooLarge)
			return
		}
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			http.Error(response, "invalid gzip", http.StatusBadRequest)
			return
		}
		digest := sha256.New()
		decompressed, copyErr := io.Copy(digest, io.LimitReader(reader, maxDecompressedCSSBytes+1))
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil {
			http.Error(response, "invalid gzip", http.StatusBadRequest)
			return
		}
		if decompressed > maxDecompressedCSSBytes {
			http.Error(response, "decompressed CSS too large", http.StatusRequestEntityTooLarge)
			return
		}
		if hex.EncodeToString(digest.Sum(nil)) != hash {
			http.Error(response, "CSS hash mismatch", http.StatusBadRequest)
			return
		}
		stored, err := store.PutIfAbsent(request.Context(), CSSKey(siteID, hash), bytes.NewReader(compressed))
		if err != nil {
			http.Error(response, "store CSS", http.StatusServiceUnavailable)
			return
		}
		if stored {
			response.WriteHeader(http.StatusCreated)
			return
		}
		response.WriteHeader(http.StatusOK)
	})
}

func setCSSCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	} else {
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func validCSSHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}
