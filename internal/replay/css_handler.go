package replay

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"supermetry/internal/clientip"
	"supermetry/internal/ingest"
)

const (
	maxCompressedCSSBytes   = 1 << 20
	maxDecompressedCSSBytes = 4 << 20
	cssPathPrefix           = "/api/replay/css/"
	defaultMaxCSSBlobs      = 10_000
	defaultMaxCSSBytes      = 512 << 20
)

type CSSHandlerOption func(*cssHandler)

type cssHandler struct {
	store          BlobStore
	lookupSite     func(publicID string) (siteID int32, ok bool)
	sem            chan struct{}
	onDrop         func()
	limiter        ingest.RateLimiter
	trustedProxies []netip.Prefix
	diskGuard      func() bool
	maxBlobs       int
	maxBytes       int64
	locksMu        sync.Mutex
	siteLocks      map[int32]*sync.Mutex
}

func WithCSSLimiter(limiter ingest.RateLimiter, trustedProxies []netip.Prefix) CSSHandlerOption {
	return func(handler *cssHandler) {
		handler.limiter = limiter
		handler.trustedProxies = trustedProxies
	}
}

func WithCSSDiskGuard(tripped func() bool) CSSHandlerOption {
	return func(handler *cssHandler) {
		if tripped != nil {
			handler.diskGuard = tripped
		}
	}
}

func WithCSSLimits(maxBlobs int, maxBytes int64) CSSHandlerOption {
	return func(handler *cssHandler) {
		if maxBlobs > 0 {
			handler.maxBlobs = maxBlobs
		}
		if maxBytes > 0 {
			handler.maxBytes = maxBytes
		}
	}
}

func NewCSSHandler(store BlobStore, lookupSite func(publicID string) (siteID int32, ok bool), sem chan struct{}, onDrop func(), options ...CSSHandlerOption) http.Handler {
	if onDrop == nil {
		onDrop = func() {}
	}
	handler := &cssHandler{
		store: store, lookupSite: lookupSite, sem: sem, onDrop: onDrop,
		diskGuard: func() bool { return false }, maxBlobs: defaultMaxCSSBlobs, maxBytes: defaultMaxCSSBytes,
		siteLocks: make(map[int32]*sync.Mutex),
	}
	for _, option := range options {
		option(handler)
	}
	return handler
}

func (h *cssHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
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
	siteID, ok := h.lookupSite(request.URL.Query().Get("s"))
	if !ok {
		http.NotFound(response, request)
		return
	}
	if h.diskGuard() {
		h.drop(response, http.StatusServiceUnavailable)
		return
	}
	if h.limiter != nil {
		ip, ok := clientip.Resolve(request, h.trustedProxies)
		if !ok {
			http.Error(response, "invalid client address", http.StatusBadRequest)
			return
		}
		if !h.limiter.Allow(replayRateLimitKey(siteID, ip)) {
			h.drop(response, http.StatusTooManyRequests)
			return
		}
	}
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		h.onDrop()
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
	key := CSSKey(siteID, hash)
	lock := h.siteLock(siteID)
	lock.Lock()
	defer lock.Unlock()
	if err := h.checkCapacity(request.Context(), siteID, key, int64(len(compressed))); err != nil {
		h.drop(response, http.StatusServiceUnavailable)
		return
	}
	stored, err := h.store.PutIfAbsent(request.Context(), key, bytes.NewReader(compressed))
	if err != nil {
		http.Error(response, "store CSS", http.StatusServiceUnavailable)
		return
	}
	if stored {
		response.WriteHeader(http.StatusCreated)
		return
	}
	response.WriteHeader(http.StatusOK)
}

func (h *cssHandler) siteLock(siteID int32) *sync.Mutex {
	h.locksMu.Lock()
	defer h.locksMu.Unlock()
	lock := h.siteLocks[siteID]
	if lock == nil {
		lock = &sync.Mutex{}
		h.siteLocks[siteID] = lock
	}
	return lock
}

func (h *cssHandler) checkCapacity(ctx context.Context, siteID int32, key string, incoming int64) error {
	prefix := "replay/" + strconv.FormatInt(int64(siteID), 10) + "/css/"
	blobs, err := h.store.List(ctx, prefix)
	if err != nil {
		return err
	}
	var total int64
	for _, blob := range blobs {
		if blob.Key == key {
			return nil
		}
		if blob.Size < 0 || total > h.maxBytes-blob.Size {
			return errors.New("CSS storage size overflow")
		}
		total += blob.Size
	}
	if len(blobs) >= h.maxBlobs || incoming > h.maxBytes-total {
		return errors.New("CSS storage cap exceeded")
	}
	return nil
}

func (h *cssHandler) drop(response http.ResponseWriter, status int) {
	h.onDrop()
	response.Header().Set("Retry-After", "1")
	http.Error(response, http.StatusText(status), status)
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
