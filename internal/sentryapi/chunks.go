package sentryapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1" //nolint:gosec // the upload protocol identifies chunks by SHA-1
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

func chunkKey(sha string) string { return "chunks/" + sha }

func validSHA1(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// chunkUpload stores content-addressed chunks from a multipart request. The
// part filename is the chunk's SHA-1, verified against the (decompressed) body.
func (h *Handler) chunkUpload(w http.ResponseWriter, r *http.Request, _ *call) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		writeDetail(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestSize+(1<<20))
	reader := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeDetail(w, http.StatusBadRequest, "invalid multipart body")
			return
		}
		name := part.FormName()
		if name != "file" && name != "file_gzip" {
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
			continue
		}
		sha := strings.ToLower(part.FileName())
		if !validSHA1(sha) {
			writeDetail(w, http.StatusBadRequest, "chunk filename must be its sha1")
			return
		}
		var src io.Reader = part
		if name == "file_gzip" {
			zr, err := gzip.NewReader(part)
			if err != nil {
				writeDetail(w, http.StatusBadRequest, "invalid gzip chunk")
				return
			}
			defer zr.Close()
			src = zr
		}
		data, err := io.ReadAll(io.LimitReader(src, chunkSize+1))
		if err != nil {
			writeDetail(w, http.StatusBadRequest, "unreadable chunk")
			return
		}
		if len(data) > chunkSize {
			writeDetail(w, http.StatusBadRequest, "chunk larger than chunkSize")
			return
		}
		sum := sha1.Sum(data) //nolint:gosec // see import
		if hex.EncodeToString(sum[:]) != sha {
			writeDetail(w, http.StatusBadRequest, "chunk sha1 mismatch")
			return
		}
		if _, err := h.deps.Blobs.PutIfAbsent(r.Context(), chunkKey(sha), bytes.NewReader(data)); err != nil {
			serverError(w)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// chunkExists reports whether a chunk is stored.
func (h *Handler) chunkExists(r *http.Request, sha string) (bool, error) {
	rc, err := h.deps.Blobs.Open(r.Context(), chunkKey(sha))
	if err == nil {
		_ = rc.Close()
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
