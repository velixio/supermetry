package sentryingest

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// ErrEncoding is an unsupported Content-Encoding.
var ErrEncoding = errors.New("unsupported content encoding")

const zstdMaxWindow = 32 << 20

// decodeBody reads the request body, undoing Content-Encoding. Both the
// compressed and the decompressed size are capped at max (ErrTooLarge), and
// decompression stops as soon as the cap is passed, so a bomb costs about max
// bytes of memory.
func decodeBody(r *http.Request, max int64) ([]byte, error) {
	raw, err := readLimited(r.Body, max)
	if err != nil {
		return nil, err
	}
	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "utf-8", "utf8", "identity":
		return raw, nil
	}
	var reader io.Reader
	switch encoding {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: gzip: %v", ErrMalformed, err)
		}
		defer zr.Close()
		reader = zr
	case "deflate":
		// HTTP deflate is zlib-wrapped, but some senders emit raw deflate.
		if len(raw) >= 2 && raw[0]&0x0f == 8 && (uint16(raw[0])<<8|uint16(raw[1]))%31 == 0 {
			zr, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, fmt.Errorf("%w: deflate: %v", ErrMalformed, err)
			}
			defer zr.Close()
			reader = zr
		} else {
			fr := flate.NewReader(bytes.NewReader(raw))
			defer fr.Close()
			reader = fr
		}
	case "br":
		reader = brotli.NewReader(bytes.NewReader(raw))
	case "zstd":
		zr, err := zstd.NewReader(bytes.NewReader(raw),
			zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(2*zstdMaxWindow), zstd.WithDecoderMaxWindow(zstdMaxWindow))
		if err != nil {
			return nil, fmt.Errorf("%w: zstd: %v", ErrMalformed, err)
		}
		defer zr.Close()
		reader = zr
	default:
		return nil, fmt.Errorf("%w: %q", ErrEncoding, encoding)
	}
	out, err := readLimited(reader, max)
	if err != nil {
		if errors.Is(err, ErrTooLarge) || errors.Is(err, zstd.ErrDecoderSizeExceeded) {
			return nil, ErrTooLarge
		}
		return nil, fmt.Errorf("%w: decompress: %v", ErrMalformed, err)
	}
	return out, nil
}
